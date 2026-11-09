package daemon

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/agents"
	"github.com/alexjsp/hammock/internal/client"
	"github.com/alexjsp/hammock/internal/holder"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

var holderBin string

func TestMain(m *testing.M) {
	if os.Getenv("HMK_TEST_KEY_HELPER") != "" {
		os.Exit(fakeKeyHelper()) // the Hammock Key helper, as tests run it (hammockkey_test.go)
	}
	scrubPaneEnv()
	// Never the real ~/.ssh/config, nor the real Hammock Key.
	sshConfigPath = func() string { return "/nonexistent/hmk-test/ssh_config" }
	keyHelperPath = func() string { return "" }
	// Nor this Mac's own screen: locked while the suite runs, linked
	// computers would stop notifying (watchInput) and their tests fail.
	screenLocked = func() bool { return false }
	dir, err := os.MkdirTemp("", "hmk-bin")
	if err != nil {
		panic(err)
	}
	// Tests send hook events from outside any pane: take the pane they name.
	hookPane = func(_ *Daemon, _ net.Conn, claimed string) string { return claimed }
	// And are you (person.go), unless they say they're an agent: tests run
	// under all sorts of parents (an agent, CI), none of them a login.
	personHook = testPerson
	holderBin = filepath.Join(dir, "pane-holder")
	out, err := exec.Command("go", "build", "-o", holderBin, "../../cmd/pane-holder").CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("build pane-holder: %v\n%s", err, out))
	}
	code := m.Run()
	// Holders outlive their daemon by design; the tests' own would each
	// keep a pseudo-terminal until the Mac runs out of them.
	_ = exec.Command("pkill", "-f", holderBin).Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// scrubPaneEnv drops what a Hammock pane adds to the tests' environment:
// its SSH_AUTH_SOCK (the real daemon's agent, which test daemons would
// forward signing requests to, asking you to approve them with the real
// Hammock Key and filling its audit log), git's gpg.ssh.program
// (GIT_CONFIG_*, hmk-ssh-sign) and HMK_* (the real daemon's socket, its
// dir, the pane's id). It keeps HMK_TEST_*, which the tests set for their
// own helper processes, and HMK_SCREEN_LIVE, a person's opt-in.
func scrubPaneEnv() {
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "SSH_AUTH_SOCK", k == "GIT_CONFIG_COUNT", k == "GIT_CONFIG_PARAMETERS",
			strings.HasPrefix(k, "GIT_CONFIG_KEY_"), strings.HasPrefix(k, "GIT_CONFIG_VALUE_"):
		case strings.HasPrefix(k, "HMK_") && !strings.HasPrefix(k, "HMK_TEST_") && k != "HMK_SCREEN_LIVE":
		default:
			continue
		}
		os.Unsetenv(k)
	}
}

// Tests run in a Hammock pane mustn't reach the real daemon: what the
// pane adds goes, so a test daemon's agent has none of its own to forward
// signing to.
func TestScrubPaneEnv(t *testing.T) {
	for k, v := range map[string]string{
		"SSH_AUTH_SOCK": "/real/run/ssh-agent.sock", "GIT_CONFIG_COUNT": "1",
		"GIT_CONFIG_KEY_0": "gpg.ssh.program", "GIT_CONFIG_VALUE_0": "/real/hmk-ssh-sign",
		"GIT_CONFIG_PARAMETERS": "'a.b'='c'", "HMK_SOCKET": "/real/hmk.sock", "HMK_PANE_ID": "pn_x",
		"HMK_TEST_KEEP": "1", "HMK_SCREEN_LIVE": "1", "GIT_CONFIG_GLOBAL": "/keep",
	} {
		t.Setenv(k, v)
	}
	scrubPaneEnv()
	for _, k := range []string{"SSH_AUTH_SOCK", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_PARAMETERS", "HMK_SOCKET", "HMK_PANE_ID"} {
		if v, ok := os.LookupEnv(k); ok {
			t.Errorf("%s still set: %q", k, v)
		}
	}
	for _, k := range []string{"HMK_TEST_KEEP", "HMK_SCREEN_LIVE", "GIT_CONFIG_GLOBAL"} {
		if os.Getenv(k) == "" {
			t.Errorf("%s dropped", k)
		}
	}
	d := startDaemonTB(t, shortDirTB(t))
	if own := d.sshHub.own; own != "" {
		t.Fatalf("test daemon forwards to %q", own)
	}
}

// shortDir returns a temp dir short enough for Unix socket paths (104 bytes on macOS).
func shortDirB(tb testing.TB) string { return shortDirTB(tb) }

func startDaemonB(tb testing.TB, dir string) *Daemon { return startDaemonTB(tb, dir) }

func shortDir(t *testing.T) string { return shortDirTB(t) }

func startDaemon(t *testing.T, dir string) *Daemon { return startDaemonTB(t, dir) }

func shortDirTB(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hammock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func startDaemonTB(t testing.TB, dir string) *Daemon {
	t.Helper()
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := net.Dial("unix", SocketPath(dir)); err == nil {
			c.Close()
			return d
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func dial(t *testing.T, dir string) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, SocketPath(dir), "test", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func call(t *testing.T, c *client.Client, m *pb.ClientMessage) *pb.ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := c.Call(ctx, m)
	if err != nil {
		t.Fatalf("%T: %v", m.Msg, err)
	}
	return r
}

// waitEvent returns the first event matching fn, accumulating pane output into out.
func waitEvent(t *testing.T, c *client.Client, fn func(*pb.ServerMessage) bool) *pb.ServerMessage {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-c.Events:
			if !ok {
				t.Fatal("connection closed")
			}
			if fn(ev) {
				return ev
			}
		case <-timeout:
			t.Fatal("timed out waiting for event")
		}
	}
}

func waitOutput(t *testing.T, c *client.Client, paneID, want string) string {
	t.Helper()
	var buf strings.Builder
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		if s := ev.GetPaneSnapshot(); s != nil && s.PaneId == paneID {
			buf.Write(s.Data)
		}
		if o := ev.GetPaneOutput(); o != nil && o.PaneId == paneID {
			buf.Write(o.Data)
		}
		return strings.Contains(buf.String(), want)
	})
	return buf.String()
}

func TestEndToEndPaneLifecycleAndRestart(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Hammock"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Name: "main",
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", `stty -echo; echo "hello $HMK_PANE_ID"; exec cat`}, Cwd: "/tmp"},
	}}}))

	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitOutput(t, c, paneID, "hello "+paneID)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("ping\n")}}})
	waitOutput(t, c, paneID, "ping")

	text := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: paneID}}}).GetPaneText().Text
	if !strings.Contains(text, "hello "+paneID) || !strings.Contains(text, "ping") {
		t.Fatalf("pane text %q", text)
	}

	// Split: the new pane inherits the target's cwd.
	newPane := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
		SplitPaneId: paneID, Direction: pb.Split_DIRECTION_VERTICAL,
		Spec: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "pwd; exec cat"}},
	}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: newPane}}})
	waitOutput(t, c, newPane, "/tmp")

	// A permission hook raises attention.
	tdir := allowTranscripts(t)
	tpath := filepath.Join(tdir, "t.jsonl")
	os.WriteFile(tpath, nil, 0o600)
	tpath, _ = filepath.EvalSymlinks(tpath)
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"Notification","session_id":"abc-123","transcript_path":"` + tpath + `","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, a := range ev.GetState().GetAttention() {
			if a.PaneId == paneID && a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT {
				return true
			}
		}
		return false
	})
	// Answering it (typing into the pane) takes the agent back to working,
	// with no hook needed.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("1")}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && p.GetAgent().GetState() == pb.AgentState_AGENT_STATE_WORKING {
				return true
			}
		}
		return false
	})

	// Restart the daemon: holders keep running and output is preserved.
	c.Close()
	d.Shutdown()
	d2 := startDaemon(t, dir)
	defer d2.Shutdown()
	c2 := dial(t, dir)
	full := call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState()
	if len(full.Panes) != 2 {
		t.Fatalf("panes after restart: %d", len(full.Panes))
	}
	for _, p := range full.Panes {
		if p.Exited {
			t.Fatalf("pane %s exited across restart", p.Id)
		}
		if p.Id == paneID && p.GetAgent().GetTranscriptPath() != tpath {
			t.Fatalf("transcript path lost: %+v", p.Agent)
		}
		if p.Id == paneID && p.GetAgent().GetSessionId() != "abc-123" {
			t.Fatalf("agent lost: %+v", p.Agent)
		}
	}
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	out := waitOutput(t, c2, paneID, "ping")
	if !strings.Contains(out, "hello "+paneID) {
		t.Fatalf("snapshot after restart missing earlier output: %q", out)
	}
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("after-restart\n")}}})
	waitOutput(t, c2, paneID, "after-restart")

	// Closing the tab stops its holders.
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tabID}}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "run", "panes", paneID+".sock")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("holder socket still present after close")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func sendHook(t *testing.T, dir string, ev HookEvent) {
	t.Helper()
	conn, err := net.Dial("unix", HookSocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(ev); err != nil {
		t.Fatal(err)
	}
	reply, _ := io.ReadAll(conn)
	if strings.TrimSpace(string(reply)) != "ok" {
		t.Fatalf("hook reply %q", reply)
	}
}

func TestRawFrameTransport(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	nc, err := net.Dial("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	msg, _ := proto.Marshal(&pb.ClientMessage{Id: 7, Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{ProtocolVersion: 1, ClientKind: "test"}}})
	frame := append([]byte(frameMagic), 0, 0, 0, 0)
	binary.BigEndian.PutUint32(frame[len(frameMagic):], uint32(len(msg)))
	if _, err := nc.Write(append(frame, msg...)); err != nil {
		t.Fatal(err)
	}
	_ = nc.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hdr [4]byte
	if _, err := io.ReadFull(nc, hdr[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
	if _, err := io.ReadFull(nc, body); err != nil {
		t.Fatal(err)
	}
	reply := &pb.ServerMessage{}
	if err := proto.Unmarshal(body, reply); err != nil {
		t.Fatal(err)
	}
	if reply.ReplyTo != 7 || reply.GetHelloReply().GetProtocolVersion() != 1 {
		t.Fatalf("reply %v", reply)
	}
}

func TestSecondDaemonRefused(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	if _, err := New(Config{Dir: dir, HolderPath: holderBin}); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second daemon: %v", err)
	}
}

func TestSetLayout(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	first := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	second := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: first, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SetLayout{SetLayout: &pb.SetLayout{TabId: tid, Layout: leaf(first)}}}); err == nil {
		t.Fatal("accepted a layout missing a pane")
	}
	good := &pb.Layout{Node: &pb.Layout_Split{Split: &pb.Split{
		Direction: pb.Split_DIRECTION_HORIZONTAL, Children: []*pb.Layout{leaf(first), leaf(second)}, Weights: []float32{3, 1},
	}}}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetLayout{SetLayout: &pb.SetLayout{TabId: tid, Layout: good}}})
	d.mu.Lock()
	w := d.tabs[tid].Layout.GetSplit().Weights
	d.mu.Unlock()
	if len(w) != 2 || w[0] < 0.74 || w[0] > 0.76 {
		t.Fatalf("weights %v", w)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
}

func TestGrantFlow(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	sid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	agent := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	other := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: agent, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))

	ctx := context.Background()
	mcpConn, err := client.Dial(ctx, SocketPath(dir), "mcp", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer mcpConn.Close()
	go func() {
		for range mcpConn.Events {
		}
	}()
	ask := func() (*pb.GrantDecision, error) {
		r, err := mcpConn.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{
			RequesterPaneId: agent, TargetPaneId: other, Action: "send", Description: "type ls",
		}}})
		return r.GetGrantDecision(), err
	}

	// First request waits for the user, who chooses "always in this Group".
	done := make(chan *pb.GrantDecision, 1)
	go func() { g, _ := ask(); done <- g }()
	ev := waitEvent(t, ui, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetGrants()) > 0 })
	gid := ev.GetState().Grants[0].Id
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gid, Decision: pb.RespondGrant_DECISION_ALLOW_GROUP}}})
	select {
	case g := <-done:
		if g == nil || !g.Allowed {
			t.Fatalf("decision %v", g)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("grant reply never arrived")
	}
	// The same request is now remembered.
	if g, err := ask(); err != nil || !g.Allowed {
		t.Fatalf("remembered grant: %v %v", g, err)
	}
	d.mu.Lock()
	events, _ := d.store.RecentAudit(10)
	d.mu.Unlock()
	if len(events) < 3 {
		t.Fatalf("audit events %v", events)
	}
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
}

// TestRebootRestoresScrollback simulates a reboot: the daemon shuts down,
// the holder dies, and on restart the respawned pane shows its old screen.
func TestRebootRestoresScrollback(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "echo before-reboot-marker; exec cat"}},
	}}}))
	d.mu.Lock()
	pid := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pid}}})
	waitOutput(t, c, pid, "before-reboot-marker")
	c.Close()
	d.mu.Lock()
	token := d.panes[pid].token
	d.mu.Unlock()
	d.Shutdown() // saves scrollback

	// "Reboot": the holder goes away.
	hc, err := holder.Dial(filepath.Join(dir, "run", "panes", pid+".sock"), 0, token)
	if err != nil {
		t.Fatal(err)
	}
	_ = hc.CloseHolder()
	hc.Close()
	time.Sleep(200 * time.Millisecond)

	d2 := startDaemon(t, dir)
	defer d2.Shutdown()
	c2 := dial(t, dir)
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pid}}})
	out := waitOutput(t, c2, pid, "restored after restart")
	if !strings.Contains(out, "before-reboot-marker") {
		t.Fatalf("old screen not restored: %q", out)
	}
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
}

func TestMoveTab(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	newGroup := func(name string) string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: name}}}))
	}
	newTab := func(sid string) string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	}
	a, b := newGroup("a"), newGroup("b")
	t1, t2, t3 := newTab(a), newTab(a), newTab(a)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_MoveTab{MoveTab: &pb.MoveTab{TabId: t3, GroupId: a, Index: 0}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_MoveTab{MoveTab: &pb.MoveTab{TabId: t1, GroupId: b, Index: 5}}})
	d.mu.Lock()
	gotA, gotB, owner := slices.Clone(d.groups[a].TabIds), slices.Clone(d.groups[b].TabIds), d.tabs[t1].GroupId
	d.mu.Unlock()
	if !slices.Equal(gotA, []string{t3, t2}) || !slices.Equal(gotB, []string{t1}) || owner != b {
		t.Fatalf("a=%v b=%v owner=%s", gotA, gotB, owner)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: a}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: b}}})
}

func TestMergeTabAndBreakOut(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	newTab := func() string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	}
	t1, t2 := newTab(), newTab()
	d.mu.Lock()
	p1 := d.tabs[t1].FocusedPaneId
	p2 := d.tabs[t2].FocusedPaneId
	d.mu.Unlock()
	p3 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: p2, Direction: pb.Split_DIRECTION_VERTICAL, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))

	// t2 (p2 over p3) goes to the right of p1; t2 closes.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_MergeTab{MergeTab: &pb.MergeTab{TabId: t2, IntoPaneId: p1, Direction: pb.Split_DIRECTION_HORIZONTAL}}})
	d.mu.Lock()
	panes, gone, tabOf := layoutPanes(d.tabs[t1].Layout), d.tabs[t2] == nil, d.panes[p3].tabID()
	tabs := slices.Clone(d.groups[g].TabIds)
	d.mu.Unlock()
	if !slices.Equal(panes, []string{p1, p2, p3}) || !gone || tabOf != t1 || !slices.Equal(tabs, []string{t1}) {
		t.Fatalf("panes=%v gone=%v tabOf=%s tabs=%v", panes, gone, tabOf, tabs)
	}

	// p2 back out into a tab of its own, after t1.
	t4 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_BreakOutPane{BreakOutPane: &pb.BreakOutPane{PaneId: p2}}}))
	d.mu.Lock()
	panes, tabs, tabOf = layoutPanes(d.tabs[t1].Layout), slices.Clone(d.groups[g].TabIds), d.panes[p2].tabID()
	d.mu.Unlock()
	if !slices.Equal(panes, []string{p1, p3}) || !slices.Equal(tabs, []string{t1, t4}) || tabOf != t4 {
		t.Fatalf("panes=%v tabs=%v tabOf=%s", panes, tabs, tabOf)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}})
}

// TestStalledInputDoesNotWedgeDaemon pastes far more than a PTY buffers into
// a program that never reads stdin; the daemon must stay responsive and the
// pane must still close.
func TestStalledInputDoesNotWedgeDaemon(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/sleep", "60"}},
	}}}))
	d.mu.Lock()
	pid := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	ctx := context.Background()
	for i := 0; i < 64; i++ { // 4 MiB
		_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pid, Data: chunk}}})
	}
	start := time.Now()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: pid}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "still-alive"}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("daemon was unresponsive for %v", el)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "run", "panes", pid+".sock")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("holder for stalled pane did not exit")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Panes must not inherit whatever launched the daemon (here, an agent's
// session markers and another terminal's terminfo).
func TestPaneEnvIsAllowlisted(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("TERMINFO", "/Applications/Other.app/terminfo")
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir)

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Env"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c",
			`stty -echo; echo "leak=[$CLAUDE_CODE_CHILD_SESSION$TERMINFO] home=[$HOME] term=[$TERM_PROGRAM]"; exec cat`}},
	}}})
	var paneID string
	for _, p := range call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Panes {
		paneID = p.Id
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	out := waitOutput(t, c, paneID, "term=[")
	if !strings.Contains(out, "leak=[]") || !strings.Contains(out, "home=["+os.Getenv("HOME")+"]") || !strings.Contains(out, "term=[Hammock]") {
		t.Fatalf("pane env: %q", out)
	}
}

// Focusing a pane acknowledges its finished agent: the "seen" flag clears the
// checkmark and the "finished" item leaves Needs You.
func TestFocusAcknowledgesFinishedAgent(t *testing.T) {
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Agents"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	var paneID string
	for _, p := range call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Panes {
		paneID = p.Id
	}

	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit"}`)})
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"Stop"}`)})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, a := range ev.GetState().GetAttention() {
			if a.PaneId == paneID && a.State == pb.AgentState_AGENT_STATE_IDLE {
				return true
			}
		}
		return false
	})

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: paneID}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		st := ev.GetState()
		cleared := slices.Contains(st.GetClearedAttentionIds(), "at_"+paneID)
		for _, p := range st.GetPanes() {
			if p.Id == paneID && p.GetAgent().GetSeen() && cleared {
				return true
			}
		}
		return false
	})
}

func TestListAudit(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	d.audit("agent:pn_x", "send", "pn_y", "typed ls")
	log := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListAudit{ListAudit: &pb.ListAudit{Limit: 10}}}).GetAuditLog()
	if len(log.GetEvents()) == 0 || log.Events[0].Action != "send" || log.Events[0].Detail != "typed ls" {
		t.Fatalf("audit log %+v", log)
	}
}

// TestQuestionIsNotAPermission: Claude's question tool, and the
// "needs your permission" notification that follows it, raise a question
// (with its text) for Needs You, not a permission prompt.
func TestQuestionIsNotAPermission(t *testing.T) {
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir)
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Agents"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	var paneID string
	for _, p := range call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Panes {
		paneID = p.Id
	}
	asked := func(want string) {
		t.Helper()
		waitEvent(t, c, func(ev *pb.ServerMessage) bool {
			for _, a := range ev.GetState().GetAttention() {
				if a.PaneId == paneID && a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT && a.Question && a.Summary == want {
					return true
				}
			}
			return false
		})
	}
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","prompt":"ask me"}`)})
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Tea or coffee?","header":"Drink","multiSelect":false,"options":[{"label":"Tea"},{"label":"Coffee"}]}]}}`)})
	asked("Tea or coffee?")
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use AskUserQuestion"}`)})
	asked("Tea or coffee?")
}

func TestReplacePane(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tb := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	p1 := d.tabs[tb].FocusedPaneId
	d.mu.Unlock()
	p2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: p1, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	p3 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReplacePane{ReplacePane: &pb.ReplacePane{PaneId: p1, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	panes, gone := layoutPanes(d.tabs[tb].Layout), d.panes[p1] == nil
	d.mu.Unlock()
	if !slices.Equal(panes, []string{p3, p2}) || !gone {
		t.Fatalf("panes=%v old gone=%v", panes, gone)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}})
}

// TestPaneProcesses: a terminal reports what's running under its shell.
func TestPaneProcesses(t *testing.T) {
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/bash", "--norc"}}}}})
	var paneID string
	for _, p := range call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Panes {
		paneID = p.Id
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("sleep 30\r")}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && slices.Contains(p.Processes, "sleep") {
				return true
			}
		}
		return false
	})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte{3}}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && len(p.Processes) == 0 {
				return true
			}
		}
		return false
	})
}

// TestCloseFocusesPrevious: closing the focused pane focuses the one focused
// before it, not the first in the layout; a split focuses its new pane.
func TestCloseFocusesPrevious(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tb := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	a := d.tabs[tb].FocusedPaneId
	d.mu.Unlock()
	split := func(of string) string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: of, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	}
	focused := func() string {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.tabs[tb].FocusedPaneId
	}
	b := split(a)
	if f := focused(); f != b {
		t.Fatalf("after a split, focused %s, want the new %s", f, b)
	}
	cc := split(b) // layout a, b, c; focus went a → b → c
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: a}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: cc}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: cc}}})
	if f := focused(); f != a {
		t.Fatalf("after closing %s, focused %s, want %s (focused before it)", cc, f, a)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}})
}

// TestAgentRunningCommand: a shell the agent starts after going idle (a !
// command) shows as a running command; its shells are counted.
func TestAgentRunningCommand(t *testing.T) {
	// A program by the agent's name that runs a shell (macOS won't run a
	// renamed copy of a system shell).
	bin := t.TempDir()
	src := filepath.Join(bin, "main.go")
	sh, flag := "/bin/zsh", "-f" // (no zsh on most Linux)
	if _, err := os.Stat(sh); err != nil {
		sh, flag = "/bin/bash", "--norc"
	}
	if err := os.WriteFile(src, []byte("package main\n\nimport \"os/exec\"\n\nfunc main() { _ = exec.Command(\""+sh+"\", \""+flag+"\", \"-c\", \"sleep 30; true\").Run() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, "claude")
	if out, err := exec.Command("go", "build", "-o", fake, src).CombinedOutput(); err != nil {
		t.Skipf("can't build a stand-in agent: %v %s", err, out)
	}
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/bash", "--norc"}}}}})
	var paneID string
	for _, p := range call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Panes {
		paneID = p.Id
	}
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"Stop"}`)})
	time.Sleep(50 * time.Millisecond)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte(fake + "\r")}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && p.GetAgent().GetRunningCommand() && p.GetAgent().GetShells() == 1 {
				return true
			}
		}
		return false
	})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte{3}}}})
}

// A turn an API error ended sends StopFailure, not Stop: the pane shows
// errored (not still working) and needs you, with Claude's error line.
func TestStopFailureErrorsAgent(t *testing.T) {
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Agents"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	var paneID string
	for _, p := range call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Panes {
		paneID = p.Id
	}

	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","prompt":"go"}`)})
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"StopFailure","error":"unknown","last_assistant_message":"API Error: Connection lost mid-response. The response above may be incomplete."}`)})
	const want = "API Error: Connection lost mid-response. The response above may be incomplete."
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		st := ev.GetState()
		raised := false
		for _, a := range st.GetAttention() {
			raised = raised || a.PaneId == paneID && a.State == pb.AgentState_AGENT_STATE_ERRORED && a.Summary == want
		}
		for _, p := range st.GetPanes() {
			if p.Id == paneID && p.GetAgent().GetState() == pb.AgentState_AGENT_STATE_ERRORED && raised {
				return true
			}
		}
		return false
	})
}

func TestStopFailureSummary(t *testing.T) {
	for _, tc := range []struct {
		payload agents.Hook
		want    string
	}{
		{agents.Hook{Error: "rate_limit"}, "Hit a rate limit"},
		{agents.Hook{Error: "server_error", ErrorDetails: "500 Internal Server Error"}, "500 Internal Server Error"},
		{agents.Hook{Error: "unknown"}, "API error"},
	} {
		tc.payload.Event = "StopFailure"
		state, summary, ok := classify(tc.payload)
		if !ok || state != pb.AgentState_AGENT_STATE_ERRORED || summary != tc.want {
			t.Errorf("classify(StopFailure, %+v) = %v %q %v, want errored %q", tc.payload, state, summary, ok, tc.want)
		}
	}
}
