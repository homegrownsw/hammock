package daemon

import (
	"errors"
	"testing"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// testPerson is personHook as tests have it: anyone but an agent.
func testPerson(cl *caller, _ int) bool { return cl == nil || !cl.agent }

// asPerson makes positivelyPerson answer is for the rest of the test.
func asPerson(t *testing.T, is func(cl *caller, pid int) bool) {
	personHook = is
	t.Cleanup(func() { personHook = testPerson })
}

// A process the daemon can't positively tell is you (detached from its
// pane, found only by the terminal it kept or under a multiplexer) can't
// do what only you may, though it keeps everything else.
func TestPersonOnlyRefusedToUnplacedCallers(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	tab2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	agentPane, pane := d.tabs[tab].FocusedPaneId, d.tabs[tab2].FocusedPaneId
	d.mu.Unlock()

	// The real rules for callers in panes; nobody outside one is placed.
	asPerson(t, func(cl *caller, pid int) bool {
		if cl != nil {
			return cl.direct && !cl.agent
		}
		return false
	})
	// An agent asks for something only you may allow.
	agent := dialAs(t, dir, &caller{pane: agentPane, agent: true}, "cli")
	go try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{
		Action: "send", TargetPaneId: pane, Description: "x",
	}}})
	var grant string
	waitFor(t, "the request", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		for id := range d.grants {
			grant = id
		}
		return grant != ""
	})

	personOnly := []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: grant, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}},
		{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}},
		{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{Enabled: true}}},
		{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: g}}},
		{Msg: &pb.ClientMessage_CreateLinkInvite{CreateLinkInvite: &pb.CreateLinkInvite{}}},
		{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{Name: "x"}}}},
		{Msg: &pb.ClientMessage_SshSetup{SshSetup: &pb.SshSetup{}}},
	}
	detached := dialAs(t, dir, nil, "mac")               // reparented to launchd: no pane
	viaTTY := dialAs(t, dir, &caller{pane: pane}, "cli") // found by its terminal, or a multiplexer
	for _, c := range []*client.Client{detached, viaTTY} {
		for _, m := range personOnly {
			if _, err := try(c, m); err == nil || !errors.Is(err, errPersonOnly) && err.Error() != errPersonOnly.Error() {
				t.Errorf("%s: %v, want refused as person-only", msgName(m), err)
			}
		}
		// Everything else as before.
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("x")}}}); err != nil {
			t.Errorf("typing into a pane: %v", err)
		}
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ServerStatus{ServerStatus: &pb.ServerStatus{}}}); err != nil && err.Error() == errPersonOnly.Error() {
			t.Errorf("server status refused: %v", err)
		}
	}
	d.mu.Lock()
	open := d.grants[grant] != nil
	d.mu.Unlock()
	if !open {
		t.Fatal("an unplaced caller answered the agent's request")
	}

	// Found by its own ancestors in its pane, it's you.
	direct := dialAs(t, dir, &caller{pane: pane, direct: true}, "cli")
	if _, err := try(direct, personOnly[0]); err != nil {
		t.Fatalf("you, in your pane's shell: %v", err)
	}
	if _, err := try(direct, personOnly[1]); err != nil {
		t.Fatalf("you, printing a pairing code: %v", err)
	}
}

// Every message agentCheck has no rule for is refused to agents, and is
// person-only (bar the few anything of yours could do anyway): the two
// lists stay in step.
func TestPersonOnlyMatchesAgentRules(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := &conn{d: d, caller: &caller{pane: "nowhere", agent: true}}
	fields := (&pb.ClientMessage{}).ProtoReflect().Descriptor().Oneofs().ByName("msg").Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		msg := &pb.ClientMessage{}
		r := msg.ProtoReflect()
		r.Set(fd, protoreflect.ValueOfMessage(r.NewField(fd).Message()))
		err := c.agentCheck(msg)
		if refused := err == errAgentOnly; refused == agentReachable(msg) {
			t.Errorf("%s: agentCheck says %v, agentReachable %v", fd.Name(), err, agentReachable(msg))
		}
		if !agentReachable(msg) && !personOnly(msg) {
			switch msg.Msg.(type) {
			case *pb.ClientMessage_ShutdownDaemon, *pb.ClientMessage_EndScreenSession, *pb.ClientMessage_ServerStatus,
				*pb.ClientMessage_ClearAttention, *pb.ClientMessage_SetColors:
			default:
				t.Errorf("%s: refused to agents but not person-only", fd.Name())
			}
		}
	}
}

// What counts as a login session: a terminal's, with nothing of Hammock's
// or an agent's anywhere above the caller.
func TestWalkToLogin(t *testing.T) {
	// pid → parent; names for what each is.
	tree := map[int]int{}
	agents := map[int]bool{}
	anchors := map[int]bool{}
	pt := procTree{
		parent: func(pid int) (int, error) {
			if p, ok := tree[pid]; ok {
				return p, nil
			}
			return 0, errors.New("gone")
		},
		agent:  func(pid int) bool { return agents[pid] },
		anchor: func(pid, _, _ int) bool { return anchors[pid] },
	}
	// A terminal: hmk 10 → zsh 11 → login 12 → Terminal 13 → launchd.
	tree[10], tree[11], tree[12], tree[13] = 11, 12, 13, 1
	anchors[12], anchors[13] = true, true
	if !walkToLogin(10, nil, pt) {
		t.Error("hmk in Terminal isn't you")
	}
	// Detached: hmk 20 → launchd.
	tree[20] = 1
	if walkToLogin(20, nil, pt) {
		t.Error("a detached process is you")
	}
	// An agent in that terminal: hmk 30 → claude 31 → zsh 11 ….
	tree[30], tree[31] = 31, 11
	agents[31] = true
	if walkToLogin(30, nil, pt) {
		t.Error("an agent's command in a terminal is you")
	}
	// A tool inside an app bundle (an anchor by its looks), run by an
	// agent: what's above it counts too.
	tree[40], tree[41] = 41, 31
	anchors[41] = true
	if walkToLogin(40, nil, pt) {
		t.Error("an agent's build tool is you")
	}
	// Under one of this daemon's panes.
	tree[50], tree[51] = 51, 13
	if walkToLogin(50, map[int]bool{51: true}, pt) {
		t.Error("a pane's process is you")
	}
	// The caller itself is never its own login.
	tree[60] = 1
	anchors[60] = true
	if walkToLogin(60, nil, pt) {
		t.Error("an anchor-looking caller with nothing above is you")
	}
}
