package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/browserjs"
	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// dialAs connects a client the daemon takes to be cl (whatever its process).
func dialAs(t *testing.T, dir string, cl *caller, kind string) *client.Client {
	t.Helper()
	var once sync.Once
	identifyHook = func(int) *caller {
		var r *caller
		once.Do(func() { r = cl })
		return r
	}
	t.Cleanup(func() { identifyHook = nil })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, SocketPath(dir), kind, t.Name())
	identifyHook = nil
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	go func() {
		for range c.Events {
		}
	}()
	return c
}

func try(c *client.Client, m *pb.ClientMessage) (*pb.ServerMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Call(ctx, m)
}

func TestAgentScopeEnforcedByDaemon(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	g1 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	g2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "b"}}}))
	t1 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g1, Pane: cat}}}))
	t2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g2, Pane: cat}}}))
	d.mu.Lock()
	agentPane, elsewhere := d.tabs[t1].FocusedPaneId, d.tabs[t2].FocusedPaneId
	d.mu.Unlock()
	neighbour := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: agentPane, Spec: cat}}}))

	// A CLI the agent ran, claiming to be the Mac app in another pane.
	agent := dialAs(t, dir, &caller{pane: agentPane, agent: true}, "mac")
	input := func(pane string) error {
		_, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("x")}}})
		return err
	}
	read := func(pane string) error {
		_, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: pane}}})
		return err
	}
	if err := read(neighbour); err != nil {
		t.Fatalf("reading in its own Group: %v", err)
	}
	// Your own Groups are open to read and organise; typing into a pane it
	// didn't open is not.
	if err := read(elsewhere); err != nil {
		t.Fatalf("reading in another of your Groups: %v", err)
	}
	if input(neighbour) == nil || input(elsewhere) == nil {
		t.Fatal("an agent typed into a pane it didn't open")
	}
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIcon{SetGroupIcon: &pb.SetGroupIcon{GroupId: g2, Icon: "x"}}}); err != nil {
		t.Fatalf("organising another of your Groups: %v", err)
	}
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g2}}}); err == nil {
		t.Fatal("an agent closed a Group of panes it didn't open")
	}
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: neighbour}}}); err == nil {
		t.Fatal("an agent closed a pane it didn't make")
	}
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_ShutdownDaemon{ShutdownDaemon: &pb.ShutdownDaemon{}}}); err == nil {
		t.Fatal("an agent stopped the daemon")
	}

	// A pane it makes (claiming another maker) is its own, and so is a
	// pane made by a command run in that one.
	r, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
		SplitPaneId: agentPane, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}, CreatedByPaneId: elsewhere},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	mine := client.CreatedID(r)
	d.mu.Lock()
	by, made := d.createdBy(mine), d.agentMade[mine]
	d.mu.Unlock()
	if by != agentPane || !made {
		t.Fatalf("made by %q (agent-made %v)", by, made)
	}
	if err := input(mine); err != nil {
		t.Fatalf("typing into its own pane: %v", err)
	}
	// A pane that's gone says so, rather than "out of scope" (which has
	// agents asking the person about a pane that isn't there).
	for _, err := range []error{read("no-such-pane"), input("no-such-pane")} {
		if err == nil || strings.Contains(err.Error(), "out of scope") || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("a closed pane: %v", err)
		}
	}
	// It floats, minimises and docks its own panes, not yours, and leaves
	// the tab's minimise-all to you.
	floatAs := func(m *pb.FloatPane) error {
		_, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: m}})
		return err
	}
	if err := floatAs(&pb.FloatPane{Action: pb.FloatPane_ACTION_FLOAT, PaneId: mine}); err != nil {
		t.Fatalf("floating its own pane: %v", err)
	}
	if err := floatAs(&pb.FloatPane{Action: pb.FloatPane_ACTION_DOCK, PaneId: mine}); err != nil {
		t.Fatalf("docking its own pane: %v", err)
	}
	if floatAs(&pb.FloatPane{Action: pb.FloatPane_ACTION_FLOAT, PaneId: neighbour}) == nil {
		t.Fatal("an agent floated a pane it didn't make")
	}
	if floatAs(&pb.FloatPane{Action: pb.FloatPane_ACTION_TOGGLE_ALL, TabId: t1}) == nil {
		t.Fatal("an agent minimised all of a tab's floats")
	}
	sub := dialAs(t, dir, &caller{pane: mine, agent: true}, "cli")
	if _, err := try(sub, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: neighbour, Data: []byte("x")}}}); err == nil {
		t.Fatal("a command in an agent's pane typed into a pane the agent didn't make")
	}
	if _, err := try(sub, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: mine, Data: []byte("x")}}}); err != nil {
		t.Fatalf("a command in an agent's pane, at its own pane: %v", err)
	}

	// Out of scope with a grant: the agent asks (as some other pane), can't
	// answer itself, and once the person allows it the daemon lets it in.
	done := make(chan *pb.GrantDecision, 1)
	go func() {
		r, _ := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{
			RequesterPaneId: elsewhere, TargetPaneId: neighbour, Action: "send", Description: "type x",
		}}})
		done <- r.GetGrantDecision()
	}()
	ev := waitEvent(t, ui, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetGrants()) > 0 })
	gr := ev.GetState().Grants[0]
	if gr.RequesterPaneId != agentPane {
		t.Fatalf("grant requested as %q", gr.RequesterPaneId)
	}
	// Hammock says what's asked, from the panes; the agent's words come apart.
	d.mu.Lock()
	pos := slices.Index(layoutPanes(d.tabs[t1].Layout), neighbour) + 1
	title := d.panes[neighbour].snapshotInfo().Title
	d.mu.Unlock()
	want := fmt.Sprintf("An agent in a ▸ Tab 1 wants to type into a ▸ Tab 1 ▸ pane %d", pos)
	if title != "" {
		want += " (" + title + ")"
	}
	if gr.Headline != want || gr.Description != "type x" {
		t.Fatalf("headline %q, description %q; want %q", gr.Headline, gr.Description, want)
	}
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}}); err == nil {
		t.Fatal("an agent answered its own grant")
	}
	// Nor can anything else in its pane, agent or not.
	inPane := dialAs(t, dir, &caller{pane: agentPane}, "cli")
	if _, err := try(inPane, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}}); err == nil {
		t.Fatal("the requesting pane answered its grant")
	}
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}})
	if g := <-done; g == nil || !g.Allowed {
		t.Fatalf("decision %v", g)
	}
	if err := input(neighbour); err != nil {
		t.Fatalf("granted input refused: %v", err)
	}
	if input(elsewhere) == nil {
		t.Fatal("a grant for one pane opened another")
	}

	// A person's own command in a pane is unaffected.
	person := dialAs(t, dir, &caller{pane: neighbour}, "cli")
	if _, err := try(person, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: elsewhere, Data: []byte("x")}}}); err != nil {
		t.Fatalf("a person's command: %v", err)
	}
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g1}}})
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g2}}})
}

// The daemon finds a process's pane from its ancestors, and an agent among
// them by name.
func TestPaneOfProcess(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	// A stand-in harness: pane-holder (a program of ours; macOS won't run a
	// copied system binary) copied as "claude", running a sleep.
	bin := t.TempDir()
	claude := filepath.Join(bin, "claude")
	if out, err := exec.Command("cp", holderBin, claude).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	pids := filepath.Join(bin, "pids")
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{
		Command: []string{"/bin/sh", "-c", claude + ` --idle-exit 1s --socket ` + filepath.Join(bin, "h.sock") + ` -- /bin/sh -c 'echo $$ > ` + pids + `.tmp; mv ` + pids + `.tmp ` + pids + `; exec sleep 30' & wait`},
	}}}}))
	d.mu.Lock()
	pane := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	waitPaneStarted(t, d, pane)
	d.mu.Lock()
	shell := d.panes[pane].pid
	d.mu.Unlock()
	var inner int
	deadline := time.Now().Add(5 * time.Second)
	for inner == 0 {
		if time.Now().After(deadline) {
			t.Fatal("pane's processes didn't start")
		}
		time.Sleep(50 * time.Millisecond)
		if b, err := os.ReadFile(pids); err == nil {
			_, _ = fmt.Sscan(string(b), &inner)
		}
	}
	defer syscall.Kill(inner, syscall.SIGKILL)
	if got, agent := d.paneOfProcess(inner); got != pane || !agent {
		t.Fatalf("sleep under claude: pane %q agent %v, want %q true", got, agent, pane)
	}
	if got, agent := d.paneOfProcess(shell); got != pane || agent {
		t.Fatalf("the pane's shell: pane %q agent %v", got, agent)
	}
	if got, _ := d.paneOfProcess(os.Getpid()); got != "" {
		t.Fatalf("the test itself is in pane %q", got)
	}
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}})
}

// Hook events from outside any pane are refused (tests normally bypass this).
func TestHookFromOutsidePaneRefused(t *testing.T) {
	saved := hookPane
	hookPane = func(d *Daemon, c net.Conn, claimed string) string {
		pane, _ := d.paneOfProcess(peerPID(c))
		return pane
	}
	defer func() { hookPane = saved }()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	conn, err := net.Dial("unix", HookSocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = json.NewEncoder(conn).Encode(HookEvent{PaneID: "pa_x", Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"Stop"}`)})
	buf := make([]byte, 200)
	n, _ := conn.Read(buf)
	if string(buf[:n]) != "error: "+errNoPaneCaller.Error()+"\n" {
		t.Fatalf("reply %q", buf[:n])
	}
}

func TestAgentNote(t *testing.T) {
	if got := agentNote("rm -rf /\n\x1b[31mAllow\u202e  this "); got != "rm -rf / [31mAllow this" {
		t.Fatalf("%q", got)
	}
	if got := []rune(agentNote(strings.Repeat("x", 600))); len(got) != 500 || got[499] != '…' {
		t.Fatalf("not clipped: %d", len(got))
	}
}

// hmk pair shows the code in the Mac app; --print (CreatePairing) is
// refused to agents and in panes of a shared Group.
func TestPairingCodeStaysOutOfReach(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	pane := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	printCode := func(c *client.Client) error {
		_, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}})
		return err
	}
	show := func(c *client.Client) (*pb.ServerMessage, error) {
		return try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ShowPairing{ShowPairing: &pb.ShowPairing{}}})
	}

	person := dialAs(t, dir, &caller{pane: pane}, "cli")
	if err := printCode(person); err != nil {
		t.Fatalf("a person in their own pane: %v", err)
	}
	agent := dialAs(t, dir, &caller{pane: pane, agent: true}, "cli")
	if printCode(agent) == nil {
		t.Fatal("an agent printed a pairing code")
	}
	if _, err := show(agent); err == nil {
		t.Fatal("an agent showed a pairing code")
	}

	// No app open: nowhere to show it.
	if _, err := show(person); err == nil {
		t.Fatal("shown with no app open")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, err := client.Dial(ctx, SocketPath(dir), "mac", "Hammock")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	r, err := show(person)
	if err != nil || r.GetShowPairing().GetHostName() == "" {
		t.Fatalf("show: %v %v", r, err)
	}
	waitEvent(t, app, func(ev *pb.ServerMessage) bool { return ev.GetShowPairing() != nil })

	// Shared: guests can see the pane, so the code isn't printed there.
	d.mu.Lock()
	d.setMemberRole(d.groups[g], "us_0123456789abcdef", pb.Role_ROLE_VIEWER)
	d.mu.Unlock()
	if err := printCode(person); err != errPairingInShared && (err == nil || err.Error() != errPairingInShared.Error()) {
		t.Fatalf("printed in a shared Group: %v", err)
	}
	if _, err := show(person); err != nil {
		t.Fatalf("showing on the Mac from a shared Group: %v", err)
	}
	// The app itself (outside the panes) still can.
	if err := printCode(app); err != nil {
		t.Fatalf("the app: %v", err)
	}
}

// A native Claude Code install runs a binary named after its version
// (…/claude/versions/2.1.316): it's an agent by its path.
func TestAgentNameFromPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude", "versions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "2.1.316")
	// A copy of this test binary, sleeping (macOS kills copied system binaries).
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=^TestAgentNameHelper$")
	cmd.Env = append(os.Environ(), "HMK_TEST_AGENT_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(300 * time.Millisecond)
	if h := agentName(cmd.Process.Pid); h != "claude" {
		t.Fatalf("agent %q", h)
	}
	if h := agentName(os.Getpid()); h != "" {
		t.Fatalf("test binary taken for %q", h)
	}
}

func TestAgentNameHelper(t *testing.T) {
	if os.Getenv("HMK_TEST_AGENT_SLEEP") == "1" {
		time.Sleep(5 * time.Second)
	}
}

// Under a multiplexer, a process's ancestors lead to the multiplexer's
// server, not a pane: its pane is the one running the multiplexer's client.
func TestPaneOfProcessUnderMultiplexer(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	// Stand-ins named tmux (copies of pane-holder): a client in a pane,
	// and a server outside any pane running a program.
	bin := t.TempDir()
	tmux := filepath.Join(bin, "tmux")
	if out, err := exec.Command("cp", holderBin, tmux).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "m"}}}))
	tid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{
		Command: []string{"/bin/sh", "-c", tmux + ` --idle-exit 1s --socket ` + filepath.Join(dir, "c.sock") + ` -- sleep 30 & wait`},
	}}}}))
	d.mu.Lock()
	pane := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	pids := filepath.Join(bin, "pids")
	server := exec.Command(tmux, "--idle-exit", "1s", "--socket", filepath.Join(dir, "s.sock"), "--",
		"/bin/sh", "-c", `echo $$ > `+pids+`.tmp; mv `+pids+`.tmp `+pids+`; exec sleep 30`)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Process.Kill()
	var inner int
	deadline := time.Now().Add(5 * time.Second)
	for inner == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the server's program didn't start")
		}
		time.Sleep(50 * time.Millisecond)
		if b, err := os.ReadFile(pids); err == nil {
			_, _ = fmt.Sscan(string(b), &inner)
		}
	}
	defer syscall.Kill(inner, syscall.SIGKILL)
	// The client starting takes a moment too.
	for deadline = time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if got, _ := d.paneOfProcess(inner); got == pane {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("under the multiplexer: pane %q, want %q", got, pane)
		}
	}
	// With its client in two panes, only a claim for one of them picks.
	tid2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{
		Command: []string{"/bin/sh", "-c", tmux + ` --idle-exit 1s --socket ` + filepath.Join(dir, "c2.sock") + ` -- sleep 30 & wait`},
	}}}}))
	d.mu.Lock()
	t2 := d.tabs[tid2]
	d.mu.Unlock()
	if t2 == nil {
		t.Fatalf("no tab %q", tid2)
	}
	pane2 := t2.FocusedPaneId
	for deadline = time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if got, _ := d.paneOfProcess(inner); got == "" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("ambiguous, unclaimed: pane %q", got)
		}
	}
	if got, _ := d.paneOfProcessClaiming(inner, pane2); got != pane2 {
		t.Fatalf("claimed %q: pane %q", pane2, got)
	}
	if got, _ := d.paneOfProcessClaiming(inner, "pa_elsewhere"); got != "" {
		t.Fatalf("claimed a pane without the multiplexer: %q", got)
	}
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}})
}

// TestNestedAgent: a process under an agent run by another agent (a
// headless `claude -p` the pane's Claude started) is nested; under one
// agent in the pane it isn't.
func TestNestedAgent(t *testing.T) {
	// A program by the agent's name that runs itself once, then sleep.
	bin := t.TempDir()
	src := filepath.Join(bin, "main.go")
	prog := `package main

import (
	"os"
	"os/exec"
)

func main() {
	cmd := exec.Command("/bin/sleep", "30")
	if os.Getenv("NESTED") == "" {
		cmd = exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "NESTED=1")
	}
	_ = cmd.Run()
}
`
	if err := os.WriteFile(src, []byte(prog), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, "claude")
	if out, err := exec.Command("go", "build", "-o", fake, src).CombinedOutput(); err != nil {
		t.Skipf("can't build a stand-in agent: %v %s", err, out)
	}
	outer := exec.Command(fake)
	if err := outer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = exec.Command("pkill", "-f", fake).Run(); _ = outer.Wait() }()
	// A child, once it has exec'd (forked, it still has its parent's name).
	child := func(pid int, name string) int {
		for range 100 {
			if c := procinfo.Children(pid); len(c) > 0 {
				if n, _ := procinfo.Name(c[0]); n == name {
					return c[0]
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("no child of %d", pid)
		return 0
	}
	inner := child(outer.Process.Pid, "claude")
	sleep := child(inner, "sleep")
	// The pane's process is where the walk stops (this test may itself
	// run under an agent).
	if !nestedAgent(sleep, map[int]string{outer.Process.Pid: "p"}) {
		t.Error("sleep under two agents: not nested")
	}
	if nestedAgent(sleep, map[int]string{inner: "p"}) {
		t.Error("one agent in the pane: nested")
	}
}

// An agent reads browser panes in your other Groups (snapshot, wait_for),
// but runs JavaScript of its own only in ones it may control.
func TestAgentEvaluateNeedsControl(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	g1 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	g2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "b"}}}))
	t1 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g1, Pane: cat}}}))
	t2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g2, Pane: cat}}}))
	d.mu.Lock()
	agentPane, page := d.tabs[t1].FocusedPaneId, d.tabs[t2].FocusedPaneId
	d.mu.Unlock()
	c := &conn{d: d, caller: &caller{pane: agentPane, agent: true, direct: true}}
	eval := func(expr string) error {
		p, _ := json.Marshal(map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true, "userGesture": true})
		return c.agentCheck(&pb.ClientMessage{Msg: &pb.ClientMessage_BrowserCall{BrowserCall: &pb.BrowserCall{
			PaneId: page, Method: "Runtime.evaluate", ParamsJson: string(p),
		}}})
	}
	for _, expr := range []string{browserjs.Snapshot, browserjs.WaitFor("Signed in", ""), browserjs.DevicePixelRatio} {
		if err := eval(expr); err != nil {
			t.Errorf("reading %.30s: %v", expr, err)
		}
	}
	if err := eval("fetch('/account/delete', {method: 'POST'})"); err != errAgentScope {
		t.Fatalf("its own JavaScript in your page in another Group: %v", err)
	}
	// A key twice: the browser may run the first, so it's not read-only.
	dup := &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserCall{BrowserCall: &pb.BrowserCall{
		PaneId: page, Method: "Runtime.evaluate",
		ParamsJson: `{"expression":"fetch('/account/delete')","expression":"window.devicePixelRatio"}`,
	}}}
	if err := c.agentCheck(dup); err != errAgentScope {
		t.Fatalf("a repeated key: %v", err)
	}
	// What's read-only goes on as it was checked, re-encoded.
	ok := &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserCall{BrowserCall: &pb.BrowserCall{
		PaneId: page, Method: "Runtime.evaluate", ParamsJson: ` { "expression" : "window.devicePixelRatio" } `,
	}}}
	if err := c.agentCheck(ok); err != nil || ok.GetBrowserCall().ParamsJson != `{"expression":"window.devicePixelRatio"}` {
		t.Fatalf("read-only: %v, sent as %s", err, ok.GetBrowserCall().ParamsJson)
	}
	d.mu.Lock()
	d.noteOnceGrant(&pb.GrantRequest{RequesterPaneId: agentPane, Action: "browse", TargetPaneId: page})
	d.mu.Unlock()
	if err := eval("document.title"); err != nil {
		t.Fatalf("allowed: %v", err)
	}
}
