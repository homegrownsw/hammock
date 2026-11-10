package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/state"
)

// testAgent serves an in-memory SSH agent holding one new key, as a
// computer's own agent; it returns the socket and the key.
func testAgent(t *testing.T, comment string) (string, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv, Comment: comment}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(shortDir(t), "agent.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _ = agent.ServeAgent(kr, c) }()
		}
	}()
	signer, _ := ssh.NewSignerFromKey(priv)
	return path, signer.PublicKey()
}

// sshConfig points the daemons at a scratch config file.
func sshConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "config"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HMK_CONFIG_DIR", dir)
}

// A pane on A, typed into from B (you're at B), gets B's keys alongside
// A's own: listed by ssh-add in the pane, signed with over the link; an
// agent there asks you first, and you answer from B.
func TestSSHAgentForwarding(t *testing.T) {
	sshConfig(t, "")
	a, b := startComputer(t), startComputer(t)
	aSock, _ := testAgent(t, "a-key")
	bSock, bKey := testAgent(t, "b-key")
	a.d.mu.Lock()
	a.d.sshHub.own = aSock
	a.d.mu.Unlock()
	b.d.mu.Lock()
	b.d.sshHub.own = bSock
	b.d.mu.Unlock()

	// You're at B (as far as B can tell: it checks before lending a key).
	b.d.mu.Lock()
	b.d.comp.self.LastInputUnixMs = time.Now().UnixMilli()
	b.d.mu.Unlock()

	link(t, a, b)
	waitComputers(t, a, "a knows b", hasComputer(b.uid, true))
	waitComputers(t, b, "b knows a", hasComputer(a.uid, true))

	group := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On A"}}}))
	waitGroup(t, b, "a's group on b", func(g *pb.Group) bool { return g.Id == group })
	if _, err := exec.LookPath("ssh-add"); err != nil {
		t.Skip("no ssh-add")
	}
	tab := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "read x; ssh-add -l; exec cat"}}}}}))
	a.d.mu.Lock()
	paneID := layoutPanes(a.d.tabs[tab].Layout)[0]
	a.d.mu.Unlock()
	// Typed from B: the pane's keys are B's while you're there.
	deadline := time.Now().Add(15 * time.Second)
	for {
		b.d.mu.Lock()
		_, routed := b.d.routes[paneID]
		b.d.mu.Unlock()
		if routed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a's pane never reached b")
		}
		time.Sleep(20 * time.Millisecond)
	}
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("go\n")}}})
	var text string
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		a.d.mu.Lock()
		text = a.d.panes[paneID].text(false)
		a.d.mu.Unlock()
		if strings.Contains(text, "a-key") && strings.Contains(text, "b-key (from ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ssh-add -l in the pane:\n%s", text)
		}
	}

	// Signing with B's key goes to B (as ssh in that pane would).
	sign := func(isAgent bool) (*ssh.Signature, error) {
		conn := &sshAgentConn{h: a.d.sshHub, pane: paneID, agent: isAgent, remote: map[string]remoteKey{}}
		if _, err := conn.List(); err != nil {
			return nil, err
		}
		return conn.Sign(bKey, []byte("hello"))
	}
	sig, err := sign(false)
	if err != nil {
		t.Fatal(err)
	}
	if err := bKey.Verify([]byte("hello"), sig); err != nil {
		t.Fatalf("signature from b: %v", err)
	}

	// An agent in the pane asks first: the request shows on B, where you
	// are, and your answer there lets it through.
	done := make(chan error, 1)
	go func() {
		_, err := sign(true)
		done <- err
	}()
	var grant string
	waitEvent(t, b.c, func(ev *pb.ServerMessage) bool {
		for _, g := range ev.GetState().GetGrants() {
			if g.Action == "ssh" && g.RequesterPaneId == paneID {
				grant = g.Id
				return true
			}
		}
		return false
	})
	// An agent at B can't answer it for you.
	if err := (&conn{d: b.d, caller: &caller{pane: "pn_b_agent", agent: true}}).agentCheck(&pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: grant}}}); err != errAgentGrant {
		t.Fatalf("an agent at b answering a's grant: %v", err)
	}
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: grant, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent's signature: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent's signature never came")
	}
	// Denied: nothing is signed.
	a.d.mu.Lock()
	a.d.onceGrants = nil
	a.d.mu.Unlock()
	go func() {
		_, err := sign(true)
		done <- err
	}()
	waitEvent(t, b.c, func(ev *pb.ServerMessage) bool {
		for _, g := range ev.GetState().GetGrants() {
			if g.Action == "ssh" && g.Id != grant {
				grant = g.Id
				return true
			}
		}
		return false
	})
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: grant, Decision: pb.RespondGrant_DECISION_DENY}}})
	if err := <-done; err == nil {
		t.Fatal("signed though denied")
	}

	// Both computers' audit logs say so.
	a.d.mu.Lock()
	aLog, _ := a.d.store.RecentAudit(50)
	a.d.mu.Unlock()
	b.d.mu.Lock()
	bLog, _ := b.d.store.RecentAudit(50)
	b.d.mu.Unlock()
	has := func(evs []state.AuditEvent, action string) bool {
		for _, e := range evs {
			if e.Action == action {
				return true
			}
		}
		return false
	}
	if !has(aLog, "ssh-sign") || !has(bLog, "ssh-lent") {
		t.Fatalf("audit: a %v, b %v", aLog, bLog)
	}

	// A pane an agent opened acts for it: ssh-keygen signing there with
	// B's key (through the socket, as git would) asks first, and denied,
	// signs nothing.
	pub := filepath.Join(shortDir(t), "b.pub")
	if err := os.WriteFile(pub, ssh.MarshalAuthorizedKey(bKey), 0o600); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(filepath.Dir(pub), "data")
	_ = os.WriteFile(data, []byte("hello"), 0o600)
	tab2 := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "read x; ssh-keygen -Y sign -f " + pub + " -n test " + data + " && echo SIGNED-OK; echo DONE; exec cat"}}}}}))
	a.d.mu.Lock()
	agentPane := layoutPanes(a.d.tabs[tab2].Layout)[0]
	a.d.markAgentMade(agentPane)
	a.d.onceGrants = nil
	a.d.mu.Unlock()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		b.d.mu.Lock()
		_, routed := b.d.routes[agentPane]
		b.d.mu.Unlock()
		if routed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent's pane never reached b")
		}
	}
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: agentPane, Data: []byte("go\n")}}})
	waitEvent(t, b.c, func(ev *pb.ServerMessage) bool {
		for _, g := range ev.GetState().GetGrants() {
			if g.Action == "ssh" && g.RequesterPaneId == agentPane {
				grant = g.Id
				return true
			}
		}
		return false
	})
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: grant, Decision: pb.RespondGrant_DECISION_DENY}}})
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		a.d.mu.Lock()
		text = a.d.panes[agentPane].text(false)
		a.d.mu.Unlock()
		if strings.Contains(text, "DONE") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ssh-keygen in the agent's pane:\n%s", text)
		}
	}
	if strings.Contains(text, "SIGNED-OK") {
		t.Fatalf("signed in an agent's pane though denied:\n%s", text)
	}

	// B lends nothing once you're away from it.
	b.d.mu.Lock()
	b.d.comp.self.LastInputUnixMs = time.Now().Add(-time.Hour).UnixMilli()
	b.d.mu.Unlock()
	if hardware().os == "macos" {
		if _, err := a.d.sshCall(b.uid, &pb.SshAgentCall{KeyBlob: bKey.Marshal(), Data: []byte("x")}, 5*time.Second); err == nil {
			t.Fatal("b signed while you're away from it")
		}
	}

	// Off (both daemons read this process's config): nothing is lent, and
	// A's pane keeps its own keys.
	sshConfig(t, "ssh-agent-forwarding = false\n")
	oc, err := net.Dial("unix", aSock)
	if err != nil {
		t.Fatal(err)
	}
	defer oc.Close()
	conn := &sshAgentConn{h: a.d.sshHub, pane: paneID, own: agent.NewClient(oc), remote: map[string]remoteKey{}}
	keys, err := conn.List()
	if err != nil || len(keys) != 1 || keys[0].Comment != "a-key" {
		t.Fatalf("off: %v %v", keys, err)
	}
	if _, err := a.d.sshCall(b.uid, &pb.SshAgentCall{List: true}, 5*time.Second); err == nil {
		t.Fatal("b lent its keys with forwarding off")
	}
}

// Where you are: the computer that typed into the pane lately, else the
// one used last, else nowhere.
func TestSSHKeySource(t *testing.T) {
	sshConfig(t, "")
	d := startComputer(t).d
	d.mu.Lock()
	defer d.mu.Unlock()
	fakeLinked(t, d)
	now := time.Now()
	rec := &pb.Computer{UserId: "us_studio", Name: "Studio", Notifies: true}
	d.comp.links["us_studio"] = &pb.StoredLink{Record: rec}
	h := d.sshHub

	rec.LastInputUnixMs = now.UnixMilli()
	d.comp.self.LastInputUnixMs = now.Add(-time.Minute).UnixMilli()
	if got := h.source("pn_x"); got != "us_studio" {
		t.Fatalf("used last: %q", got)
	}
	d.comp.self.LastInputUnixMs = now.Add(time.Second).UnixMilli()
	if got := h.source("pn_x"); got != "" {
		t.Fatalf("here, used last: %q", got)
	}
	// Typed into from Studio: Studio, though this one was used since.
	h.typed["pn_x"] = typedBy{uid: "us_studio", at: now}
	if got := h.source("pn_x"); got != "us_studio" {
		t.Fatalf("typed from studio: %q", got)
	}
	// Typed into here: here.
	h.typed["pn_x"] = typedBy{uid: d.id.me.UserId, at: now}
	rec.LastInputUnixMs = now.Add(time.Hour).UnixMilli()
	if got := h.source("pn_x"); got != "" {
		t.Fatalf("typed here: %q", got)
	}
	// Nobody anywhere lately: nowhere.
	delete(h.typed, "pn_x")
	rec.LastInputUnixMs = now.Add(-time.Hour).UnixMilli()
	d.comp.self.LastInputUnixMs = now.Add(-time.Hour).UnixMilli()
	if got := h.source("pn_x"); got != "" {
		t.Fatalf("away: %q", got)
	}
	// An app that's closed there doesn't count; nor a refused link.
	rec.LastInputUnixMs, rec.Notifies = now.UnixMilli(), false
	if got := h.source("pn_x"); got != "" {
		t.Fatalf("closed app: %q", got)
	}
	rec.Notifies = true
	d.comp.links["us_studio"].Refused = true
	if got := h.source("pn_x"); got != "" {
		t.Fatalf("refused: %q", got)
	}

	// A guest typing never counts; nor an agent or a phone here.
	h.typed = map[string]typedBy{}
	h.noteTyped(&conn{d: d, peer: "us_guest"}, "pn_g")
	h.noteTyped(&conn{d: d, remote: true}, "pn_p")
	h.noteTyped(&conn{d: d, caller: &caller{pane: "pn_y", agent: true}}, "pn_a")
	if len(h.typed) != 0 {
		t.Fatalf("typed: %v", h.typed)
	}
}

// Only a linked computer's daemon may ask for this computer's keys: not a
// guest's, a phone, an agent or a local client.
func TestSSHAgentCallRefused(t *testing.T) {
	sshConfig(t, "")
	tc := startComputer(t)
	d := tc.d
	msg := &pb.ClientMessage{Msg: &pb.ClientMessage_SshAgentCall{SshAgentCall: &pb.SshAgentCall{List: true}}}
	if _, err := (&conn{d: d, peer: "us_guest"}).peerCheck(msg); err == nil {
		t.Error("a guest's daemon may ask")
	}
	if _, err, done := (&conn{d: d, remote: true, authed: true}).handleRemoteAuth(msg); err == nil || !done {
		t.Error("a phone may ask")
	}
	if err := (&conn{d: d, caller: &caller{pane: "pn_y", agent: true}}).agentCheck(msg); err == nil {
		t.Error("an agent may ask")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if r, err := tc.c.Call(ctx, msg); err == nil && r.GetError() == nil {
		t.Errorf("a local client may ask: %v", r)
	}
	// Linked: the owner check lets it through (the handler asks the agent).
	d.mu.Lock()
	_, err := d.ownerCheck(&conn{d: d, peer: "us_other"}, msg)
	d.mu.Unlock()
	if err != nil {
		t.Errorf("linked: %v", err)
	}
	// Panes get Hammock's socket either way (your own agent and the Hammock Key
	// are there); off, it only stops keys coming from elsewhere.
	if env := d.sshHub.paneEnv(); len(env) == 0 || !strings.HasSuffix(env[0], "ssh-agent.sock") {
		t.Errorf("env on: %v", env)
	}
	sshConfig(t, "ssh-agent-forwarding = false\n")
	if env := d.sshHub.paneEnv(); len(env) == 0 || !strings.HasSuffix(env[0], "ssh-agent.sock") {
		t.Errorf("env off: %v", env)
	}
}

// At another of your computers (typing into a pane here from there), this
// Mac is away for that pane, though it was touched a minute ago: a key that
// asks here (1Password, jj's op-ssh-sign) would wait on a prompt nobody
// sees, so its twin from where you are signs, or the Hammock Key.
func TestSSHAwayAtAnotherComputer(t *testing.T) {
	sshConfig(t, "")
	d := startComputer(t).d
	oldLocked, oldIdle := screenLocked, idleTime
	t.Cleanup(func() { screenLocked, idleTime = oldLocked, oldIdle })
	screenLocked = func() bool { return false }
	idleTime = func() time.Duration { return time.Minute }
	d.mu.Lock()
	fakeLinked(t, d)
	now := time.Now()
	rec := &pb.Computer{UserId: "us_studio", Name: "Studio", Notifies: true, LastInputUnixMs: now.UnixMilli()}
	d.comp.links["us_studio"] = &pb.StoredLink{Record: rec}
	d.comp.self.LastInputUnixMs = now.Add(-time.Minute).UnixMilli()
	h := d.sshHub
	h.typed["pn_x"] = typedBy{uid: "us_studio", at: now}
	d.mu.Unlock()
	if !h.awayFrom("pn_x") {
		t.Fatal("typed from studio: not away")
	}
	d.mu.Lock()
	h.typed["pn_x"] = typedBy{uid: d.id.me.UserId, at: now}
	d.mu.Unlock()
	if h.awayFrom("pn_x") {
		t.Fatal("typed here: away")
	}
	if h.awayFrom("") {
		t.Fatal("outside a pane, idle a minute: away")
	}
}
