package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	"github.com/alexjsp/hammock/internal/procinfo"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// testPerson is personHook as tests have it: anyone but an agent.
func testPerson(cl *caller, _ int) bool { return cl == nil || !cl.agent }

// asPerson makes positivelyPerson (the app) answer is for the rest of the test.
func asPerson(t *testing.T, is func(cl *caller, pid int) bool) {
	personHook = is
	t.Cleanup(func() { personHook = testPerson })
}

// asPlain makes plainCaller answer is for the rest of the test.
func asPlain(t *testing.T, is func(cl *caller, pid int) bool) {
	plainHook = is
	t.Cleanup(func() { plainHook = nil })
}

// inPaneShell is plainCaller's rule for callers in panes; nobody outside
// one is placed (the test's own process might have a terminal).
func inPaneShell(cl *caller, _ int) bool { return cl != nil && cl.direct && !cl.agent }

func nobody(*caller, int) bool { return false }

// dialKind connects a client of kind (the app's is "mac").
func dialKind(t *testing.T, dir, kind string) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, SocketPath(dir), kind, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// personGrant waits for a request asking you, and returns it.
func personGrant(t *testing.T, d *Daemon, not map[string]bool) *pb.GrantRequest {
	t.Helper()
	var req *pb.GrantRequest
	waitFor(t, "a request asking you", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		for id, g := range d.grants {
			if g.person && !not[id] {
				req = proto.CloneOf(g.req)
			}
		}
		return req != nil
	})
	return req
}

// A process the daemon can't positively tell is you (detached from its
// pane, found only by the terminal it kept or under a multiplexer) is
// scoped as an agent, and what only you may do asks you: it waits, and
// goes ahead once you allow it in the app (or on a phone). It never
// answers a request itself, its own least of all.
func TestPersonOnlyAsksForUnplacedCallers(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dialKind(t, dir, "mac") // the app (testPerson, before asPerson below)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	tab2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	agentPane, pane := d.tabs[tab].FocusedPaneId, d.tabs[tab2].FocusedPaneId
	d.mu.Unlock()

	// Nobody else is the app; a pane's shell is yours for scoping.
	asPerson(t, nobody)
	asPlain(t, inPaneShell)
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
	// The agent itself: refused outright, not asked about.
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}); err == nil || err.Error() != errAgentOnly.Error() {
		t.Fatalf("an agent printing a pairing code: %v, want refused", err)
	}

	answer := func(id string, dec pb.RespondGrant_Decision) *pb.ClientMessage {
		return &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: id, Decision: dec}}}
	}
	detached := dialAs(t, dir, nil, "mac")               // reparented to launchd: no pane
	viaTTY := dialAs(t, dir, &caller{pane: pane}, "cli") // found by its terminal, or a multiplexer
	for _, c := range []*client.Client{detached, viaTTY} {
		// Answering is never asked about: refused.
		if _, err := try(c, answer(grant, pb.RespondGrant_DECISION_ALLOW_ONCE)); err == nil || err.Error() != errAnswerInApp.Error() {
			t.Errorf("answering the agent: %v, want refused", err)
		}
		// Scoped as an agent: your pane isn't its to type into.
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("x")}}}); err == nil || err.Error() != errAgentScope.Error() {
			t.Errorf("typing into your pane: %v, want out of scope", err)
		}
		// But it still reads your Groups.
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: pane}}}); err != nil {
			t.Errorf("reading your pane: %v", err)
		}
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ServerStatus{ServerStatus: &pb.ServerStatus{}}}); err != nil {
			t.Errorf("server status: %v", err)
		}
	}
	d.mu.Lock()
	open := d.grants[grant] != nil
	d.mu.Unlock()
	if !open {
		t.Fatal("an unplaced caller answered the agent's request")
	}

	// A pairing code, from the terminal-found caller: it asks you.
	type result struct {
		r   *pb.ServerMessage
		err error
	}
	ask := func(c *client.Client, m *pb.ClientMessage) chan result {
		ch := make(chan result, 1)
		go func() { r, err := try(c, m); ch <- result{r, err} }()
		return ch
	}
	var told *pb.ApprovalPending
	viaTTY.OnApproval = func(ap *pb.ApprovalPending) { told = ap }
	got := ask(viaTTY, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}})
	req := personGrant(t, d, map[string]bool{grant: true})
	if !strings.HasPrefix(req.Headline, "hmk wants to get a pairing code") || !strings.Contains(req.Origin, "not its own shell") || req.Action != "person" {
		t.Errorf("asked %q, from %q (%s)", req.Headline, req.Origin, req.Action)
	}
	// Neither it nor any other program that isn't the app answers it.
	for _, c := range []*client.Client{viaTTY, detached} {
		if _, err := try(c, answer(req.Id, pb.RespondGrant_DECISION_ALLOW_ONCE)); err == nil {
			t.Fatal("a program that isn't the app answered a request asking you")
		}
	}
	select {
	case r := <-got:
		t.Fatalf("answered before you did: %v %v", r.r, r.err)
	case <-time.After(100 * time.Millisecond):
	}
	// You allow it in the app: it's done, and its reply is the pairing.
	call(t, ui, answer(req.Id, pb.RespondGrant_DECISION_ALLOW_ONCE))
	r := <-got
	if r.err != nil || r.r.GetPairing() == nil {
		t.Fatalf("after you allowed it: %v %v", r.r, r.err)
	}
	if told == nil || told.GrantId != req.Id {
		t.Errorf("not told it waits for you: %v", told)
	}

	// Denied: it fails, and isn't done.
	got = ask(detached, &pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{Enabled: true}}})
	req2 := personGrant(t, d, map[string]bool{grant: true, req.Id: true})
	if !strings.HasPrefix(req2.Headline, "hmk wants to turn on remote access") {
		t.Errorf("asked %q", req2.Headline)
	}
	call(t, ui, answer(req2.Id, pb.RespondGrant_DECISION_DENY))
	if r := <-got; r.err == nil || r.err.Error() != errDenied.Error() {
		t.Fatalf("denied: %v", r.err)
	}
	d.mu.Lock()
	on := d.remote.on
	d.mu.Unlock()
	if on {
		t.Fatal("remote access turned on though you said no")
	}

	// Your pane's own shell: yours for typing into panes, but what only
	// you may do asks you all the same.
	shell := dialAs(t, dir, &caller{pane: pane, direct: true}, "cli")
	if _, err := try(shell, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: agentPane, Data: []byte("x")}}}); err != nil {
		t.Errorf("your shell typing into a pane: %v", err)
	}
	if _, err := try(shell, &pb.ClientMessage{Msg: &pb.ClientMessage_ListDevices{ListDevices: &pb.ListDevices{}}}); err != nil {
		t.Errorf("your shell listing devices: %v", err)
	}
	if _, err := try(shell, answer(grant, pb.RespondGrant_DECISION_ALLOW_ONCE)); err == nil || err.Error() != errAnswerInApp.Error() {
		t.Errorf("your shell answering the agent: %v, want refused", err)
	}
	got = ask(shell, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}})
	req3 := personGrant(t, d, map[string]bool{grant: true, req.Id: true, req2.Id: true})
	if !strings.HasPrefix(req3.Origin, "From the shell in ") || req3.RequesterPaneId != pane {
		t.Errorf("from %q (%s)", req3.Origin, req3.RequesterPaneId)
	}
	// Gone before you answer: the request goes with it.
	shell.Close()
	<-got
	waitFor(t, "the request withdrawn", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.grants[req3.Id] == nil
	})

	// Unanswered: it times out, denied.
	grantTimeout = 300 * time.Millisecond
	defer func() { grantTimeout = 2 * time.Minute }()
	if _, err := try(viaTTY, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: g}}}); err == nil || err.Error() != errDenied.Error() {
		t.Fatalf("unanswered: %v", err)
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

// A program in a terminal outside Hammock is yours for scoping only with
// nothing of an agent's, a pane's or Hammock's anywhere above it.
func TestNoAgentAbove(t *testing.T) {
	tree := map[int]int{}
	agents := map[int]bool{}
	pt := procTree{
		parent: func(pid int) (int, error) {
			if p, ok := tree[pid]; ok {
				return p, nil
			}
			return 0, errors.New("gone")
		},
		agent: func(pid int) bool { return agents[pid] },
	}
	// A terminal: hmk 10 → zsh 11 → login 12 → Terminal 13 → launchd.
	tree[10], tree[11], tree[12], tree[13] = 11, 12, 13, 1
	if !noAgentAbove(10, nil, pt) {
		t.Error("hmk in Terminal has an agent above it")
	}
	// An agent in that terminal: hmk 30 → claude 31 → zsh 11 ….
	tree[30], tree[31] = 31, 11
	agents[31] = true
	if noAgentAbove(30, nil, pt) {
		t.Error("an agent's command in a terminal")
	}
	// Under one of this daemon's panes.
	tree[50], tree[51] = 51, 13
	if noAgentAbove(50, map[int]bool{51: true}, pt) {
		t.Error("a pane's process")
	}
	// Its parent gone: not known.
	tree[60] = 61
	if noAgentAbove(60, nil, pt) {
		t.Error("a process whose parent can't be read")
	}
}

// A program that isn't positively you and isn't in a pane (an agent's
// double fork, a launchd job) is scoped as an agent: it can't type into
// your pane, nor get your rights by opening a pane and acting from inside
// it (`hmk tab new -- hmk …`): that pane is an agent's.
func TestUnplacedCallerIsScopedAsAgent(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	yours := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	asPerson(t, nobody)
	asPlain(t, inPaneShell)

	outside := dialAs(t, dir, nil, "cli")
	if _, err := try(outside, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: yours, Data: []byte("x")}}}); err == nil || err.Error() != errAgentScope.Error() {
		t.Fatalf("typing into your pane: %v, want out of scope", err)
	}
	// Its own tab: made by it, an agent's, and its to type into.
	r, err := try(outside, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, CreatedByPaneId: yours}}}})
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	own := d.tabs[client.CreatedID(r)].FocusedPaneId
	made, by := d.agentMade[own], d.createdBy(own)
	d.mu.Unlock()
	if !made || by != outsidePane {
		t.Fatalf("its pane: agent-made %v, made by %q", made, by)
	}
	if _, err := try(outside, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: own, Data: []byte("x")}}}); err != nil {
		t.Fatalf("typing into its own pane: %v", err)
	}
	// Its pane's shell running hmk, as identify finds it: an agent.
	inside := dialAs(t, dir, &caller{pane: own, agent: made, direct: true}, "cli")
	if _, err := try(inside, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}); err == nil {
		t.Fatal("a pane it opened got a pairing code")
	}
	if _, err := try(inside, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: yours, Data: []byte("x")}}}); err == nil || err.Error() != errAgentScope.Error() {
		t.Fatalf("typing into your pane from its own: %v, want out of scope", err)
	}
	// Asking goes to you, in Hammock's words.
	go try(outside, &pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{Action: "send", TargetPaneId: yours}}})
	var head string
	waitFor(t, "the request", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, gr := range d.grants {
			head = gr.req.Headline
			if gr.req.RequesterPaneId != outsidePane {
				t.Errorf("asked as %q", gr.req.RequesterPaneId)
			}
		}
		return head != ""
	})
	if !strings.HasPrefix(head, outsideWho) {
		t.Errorf("asked %q", head)
	}
	// What anything of yours could do anyway, it still can.
	if _, err := try(outside, &pb.ClientMessage{Msg: &pb.ClientMessage_ServerStatus{ServerStatus: &pb.ServerStatus{}}}); err != nil {
		t.Errorf("server status: %v", err)
	}
}

// Before anything could ask you (no phone paired, no computer linked, the
// app not open), a program with a terminal that isn't an agent's does what
// only you may directly; the first phone paired after is told so; and once
// anything is paired, it asks again (unpairing it is asked about too).
func TestPersonOnlyBootstrap(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir) // not the app: kind "test"
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	pane := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	asPerson(t, nobody)
	asPlain(t, inPaneShell)
	tty := true
	hasTTY = func(int) bool { return tty }
	defer func() { hasTTY = func(pid int) bool { return procinfo.TTY(pid) >= 0 } }()
	pairing := &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}

	// An agent: refused, window or not.
	agent := dialAs(t, dir, &caller{pane: pane, agent: true}, "cli")
	if _, err := try(agent, pairing); err == nil || err.Error() != errAgentOnly.Error() {
		t.Fatalf("an agent: %v", err)
	}
	// Without a terminal: nothing could ask you, so it's refused.
	tty = false
	shell := dialAs(t, dir, &caller{pane: pane, direct: true}, "cli")
	if _, err := try(shell, pairing); err == nil || err.Error() != errNobodyToAsk.Error() {
		t.Fatalf("no terminal: %v", err)
	}
	// One scoped as an agent (detached): refused too, terminal or not.
	tty = true
	detached := dialAs(t, dir, nil, "cli")
	if _, err := try(detached, pairing); err == nil || err.Error() != errNobodyToAsk.Error() {
		t.Fatalf("detached: %v", err)
	}
	// In a terminal: done at once.
	r, err := try(shell, pairing)
	if err != nil || r.GetPairing() == nil {
		t.Fatalf("in a terminal, nothing paired: %v %v", r, err)
	}
	// The first phone paired is told.
	d.mu.Lock()
	if err := d.store.PutDevice(state.Device{ID: "dv_x", Name: "Phone", PublicKey: make([]byte, 32), Paired: time.Now(), LastSeen: time.Now()}); err != nil {
		d.mu.Unlock()
		t.Fatal(err)
	}
	d.afterBootstrap("Paired Phone", "dv_x", "")
	item := d.attention[bootstrapAttentionPrefix+"bootstrap"]
	d.mu.Unlock()
	if item == nil || !strings.Contains(item.Summary, "Paired Phone from a terminal") || !strings.Contains(item.Summary, "From the shell in") {
		t.Fatalf("told: %v", item)
	}
	// Only you clear that.
	if _, err := try(shell, &pb.ClientMessage{Msg: &pb.ClientMessage_ClearAttention{ClearAttention: &pb.ClearAttention{Id: item.Id}}}); err == nil || err.Error() != errAnswerInApp.Error() {
		t.Fatalf("clearing it from a terminal: %v", err)
	}
	// Paired: it asks you now, unpairing included.
	for _, m := range []*pb.ClientMessage{pairing, {Msg: &pb.ClientMessage_RevokeDevice{RevokeDevice: &pb.RevokeDevice{DeviceId: "dv_x"}}}} {
		go try(shell, m)
		req := personGrant(t, d, nil)
		if m.GetRevokeDevice() != nil && !strings.Contains(req.Headline, "unpair Phone") {
			t.Errorf("asked %q", req.Headline)
		}
		d.mu.Lock()
		d.resolveGrant(req.Id, pb.RespondGrant_DECISION_DENY, "")
		d.mu.Unlock()
	}
	if devs, _ := d.store.Devices(); len(devs) != 1 {
		t.Fatal("unpaired without asking")
	}
}

// With the app open, nothing paired, a terminal asks it: there's someone
// to ask.
func TestPersonOnlyAsksTheAppBeforeAnythingIsPaired(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	app := dialKind(t, dir, "mac")
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	asPerson(t, nobody)
	asPlain(t, func(*caller, int) bool { return true })
	hasTTY = func(int) bool { return true }
	defer func() { hasTTY = func(pid int) bool { return procinfo.TTY(pid) >= 0 } }()
	term := dialAs(t, dir, nil, "cli")
	done := make(chan error, 1)
	go func() {
		_, err := try(term, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}})
		done <- err
	}()
	req := personGrant(t, d, nil)
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: req.Id, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Once the app has connected, the bootstrap window has shut for good: with
// the app quit and nothing paired, a terminal asks (and waits for the app)
// instead of doing it directly, after a restart too.
func TestBootstrapShutsOnceTheAppConnects(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	app := dialKind(t, dir, "mac") // the app (testPerson)
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	d.mu.Lock()
	closed := d.bootstrapClosed()
	d.mu.Unlock()
	if !closed {
		t.Fatal("the app connected, and the window is still open")
	}
	app.Close()
	waitFor(t, "the app gone", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.nobodyToAsk()
	})
	d.Shutdown()
	d = startDaemon(t, dir)
	defer d.Shutdown()

	asPerson(t, nobody)
	asPlain(t, func(*caller, int) bool { return true })
	hasTTY = func(int) bool { return true }
	defer func() { hasTTY = func(pid int) bool { return procinfo.TTY(pid) >= 0 } }()
	// A detached program with a terminal of its own: it asks, and waits.
	term := dialAs(t, dir, nil, "cli")
	done := make(chan error, 1)
	go func() {
		_, err := try(term, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}})
		done <- err
	}()
	req := personGrant(t, d, nil)
	select {
	case err := <-done:
		t.Fatalf("answered before anyone allowed it: %v", err)
	default:
	}
	// The app opens later and says no.
	asPerson(t, testPerson)
	app = dialKind(t, dir, "mac")
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: req.Id, Decision: pb.RespondGrant_DECISION_DENY}}})
	if err := <-done; err == nil || err.Error() != errDenied.Error() {
		t.Fatalf("denied: %v", err)
	}
	d.mu.Lock()
	by := d.remote.bootstrapBy
	d.mu.Unlock()
	if by != "" {
		t.Fatalf("noted as a bootstrap: %q", by)
	}
}

// The word that a terminal did what only you may survives a restart, and
// the phone paired as it was said can't clear it: the app can, or a phone
// paired before it.
func TestBootstrapWarningKeptAndOnlyYoursToClear(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	asPerson(t, nobody)
	asPlain(t, func(*caller, int) bool { return true })
	hasTTY = func(int) bool { return true }
	defer func() { hasTTY = func(pid int) bool { return procinfo.TTY(pid) >= 0 } }()
	term := dialAs(t, dir, nil, "cli")
	if r, err := try(term, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}); err != nil || r.GetPairing() == nil {
		t.Fatalf("bootstrap: %v %v", r, err)
	}
	// Killed before anything is paired: where it came from is kept.
	d.Shutdown()
	d = startDaemon(t, dir)
	defer func() { d.Shutdown() }()
	d.mu.Lock()
	if d.remote.bootstrapBy == "" {
		d.mu.Unlock()
		t.Fatal("forgot the bootstrap across a restart")
	}
	old := time.Now().Add(-time.Hour)
	if err := d.store.PutDevice(state.Device{ID: "dv_new", Name: "Phone", PublicKey: make([]byte, 32), Paired: time.Now(), LastSeen: time.Now()}); err != nil {
		d.mu.Unlock()
		t.Fatal(err)
	}
	d.afterBootstrap("Paired Phone", "dv_new", "")
	if err := d.store.PutDevice(state.Device{ID: "dv_old", Name: "Old", PublicKey: make([]byte, 32), Paired: old, LastSeen: old}); err != nil {
		d.mu.Unlock()
		t.Fatal(err)
	}
	if err := d.store.PutDevice(state.Device{ID: "dv_later", Name: "Later", PublicKey: make([]byte, 32), Paired: time.Now().Add(time.Second), LastSeen: time.Now()}); err != nil {
		d.mu.Unlock()
		t.Fatal(err)
	}
	d.mu.Unlock()
	d.Shutdown()
	d = startDaemon(t, dir)
	id := bootstrapAttentionPrefix + "bootstrap"
	d.mu.Lock()
	item := d.attention[id]
	closed := d.bootstrapClosed()
	may := func(c *conn) bool { return d.mayClearBootstrap(c) }
	newPhone, oldPhone, later := may(&conn{d: d, remote: true, deviceID: "dv_new"}), may(&conn{d: d, remote: true, deviceID: "dv_old"}), may(&conn{d: d, remote: true, deviceID: "dv_later"})
	computer := may(&conn{d: d, peer: "u_x"})
	d.mu.Unlock()
	if item == nil || !strings.Contains(item.Summary, "Paired Phone from a terminal") || !closed {
		t.Fatalf("after a restart: %v closed %v", item, closed)
	}
	if newPhone || later || computer || !oldPhone {
		t.Fatalf("may clear: the new phone %v, one paired since %v, a computer %v, one paired before %v", newPhone, later, computer, oldPhone)
	}
	// Not from a terminal; from the app, for good.
	if _, err := try(term2(t, dir), &pb.ClientMessage{Msg: &pb.ClientMessage_ClearAttention{ClearAttention: &pb.ClearAttention{Id: id}}}); err == nil {
		t.Fatal("cleared from a terminal")
	}
	asPerson(t, testPerson)
	app := dialKind(t, dir, "mac")
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_ClearAttention{ClearAttention: &pb.ClearAttention{Id: id}}})
	d.Shutdown()
	d = startDaemon(t, dir)
	d.mu.Lock()
	item = d.attention[id]
	d.mu.Unlock()
	if item != nil {
		t.Fatal("cleared, and back after a restart")
	}
}

// term2 is a terminal caller that isn't the app.
func term2(t *testing.T, dir string) *client.Client {
	asPerson(t, nobody)
	return dialAs(t, dir, nil, "cli")
}

// A person-only request from a pane something other than you typed into
// lately says so: a program of yours typing `hmk pair --print` into your
// shell's pane doesn't read as you.
func TestApprovalSaysWhoTypedIntoThePane(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	app := dialKind(t, dir, "mac") // the app (testPerson, before asPerson below)
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	tab2 := client.CreatedID(call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	pane, other := d.tabs[tab].FocusedPaneId, d.tabs[tab2].FocusedPaneId
	p := d.panes[pane]
	d.mu.Unlock()
	asPerson(t, nobody)
	asPlain(t, inPaneShell)
	hasTTY = func(int) bool { return false }
	defer func() { hasTTY = func(pid int) bool { return procinfo.TTY(pid) >= 0 } }()
	pairing := &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}
	shell := dialAs(t, dir, &caller{pane: pane, direct: true}, "cli")
	ask := func() *pb.GrantRequest {
		t.Helper()
		go try(shell, pairing)
		req := personGrant(t, d, nil)
		d.mu.Lock()
		d.resolveGrant(req.Id, pb.RespondGrant_DECISION_DENY, "")
		d.mu.Unlock()
		return req
	}
	// You typed there, in the app: as before.
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("x")}}})
	if req := ask(); strings.Contains(req.Origin, "after typing") || !strings.HasPrefix(req.Headline, "hmk wants to get a pairing code: anyone who has it can pair a device") {
		t.Fatalf("after you typed: %q / %q", req.Origin, req.Headline)
	}
	// A program saying it's the TUI typed there: not you, whatever it says.
	tui := dialAs(t, dir, &caller{pane: other, direct: true}, "tui")
	if _, err := try(tui, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("hmk pair --print\n")}}}); err != nil {
		t.Fatal(err)
	}
	if req := ask(); !strings.Contains(req.Origin, ", after typing from a program in") || !strings.Contains(req.Origin, "hmk tui") {
		t.Fatalf("after a caller saying it's the TUI typed: %q", req.Origin)
	}
	d.mu.Lock()
	who, _, kind := d.describeAudit("tui:hmk", "input", pane, "")
	d.mu.Unlock()
	if who != "hmk tui" || kind == "you" {
		t.Fatalf("the audit says %q, as %q", who, kind)
	}
	// A program in another pane typed there.
	prog := dialAs(t, dir, &caller{pane: other, direct: true}, "cli")
	if _, err := try(prog, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("hmk pair --print\n")}}}); err != nil {
		t.Fatal(err)
	}
	req := ask()
	if !strings.Contains(req.Origin, "From the shell in") || !strings.Contains(req.Origin, ", after typing from a program in") {
		t.Fatalf("after a program typed: %q", req.Origin)
	}
	// Your keystroke since doesn't hide it while it's recent; once it's
	// old and you've typed since, it's gone; not if you haven't.
	now := time.Now()
	p.mu.Lock()
	p.typedBy.youAt = now
	p.mu.Unlock()
	if s := p.recentInputFrom(now); s.kind != "program" {
		t.Fatalf("recent, you typed since: %+v", s)
	}
	if s := p.recentInputFrom(now.Add(time.Minute)); s.kind != "" {
		t.Fatalf("old, you typed since: %+v", s)
	}
	p.mu.Lock()
	p.typedBy.youAt = now.Add(-time.Hour)
	p.mu.Unlock()
	if s := p.recentInputFrom(now.Add(time.Hour)); s.kind != "program" {
		t.Fatalf("old, you haven't typed since: %+v", s)
	}
}

// A local client that isn't the app by its signature can't say it's the
// app: notifications, requests and pairing codes go by it.
func TestOnlyTheAppIsKindMac(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	asPerson(t, nobody)
	asPlain(t, func(*caller, int) bool { return true })
	fake := dialAs(t, dir, nil, "mac")
	call(t, fake, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	d.mu.Lock()
	open := d.appIsOpen()
	d.mu.Unlock()
	if open {
		t.Fatal("a plain caller saying it's the app counts as the app")
	}
	asPerson(t, testPerson)
	app := dialKind(t, dir, "mac")
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	d.mu.Lock()
	open = d.appIsOpen()
	d.mu.Unlock()
	if !open {
		t.Fatal("the app doesn't count")
	}
}

// A caller that isn't an agent can't name an agent's pane as the one it's
// in, or as the one that made a new pane (which would make it the
// agent's): it's the pane it's really in, or none.
func TestPlainCallersPaneClaimsAreTheirOwn(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	tab2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	agentPane, shellPane := d.tabs[tab].FocusedPaneId, d.tabs[tab2].FocusedPaneId
	d.mu.Unlock()
	asPerson(t, nobody)
	asPlain(t, func(*caller, int) bool { return true })
	made := func(c *client.Client) string {
		t.Helper()
		id := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
			Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, CreatedByPaneId: agentPane}}}}))
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.panes[d.tabs[id].FocusedPaneId].snapshotInfo().CreatedByPaneId
	}
	t.Setenv("HMK_PANE_ID", agentPane) // (what the clients' Hello claims)
	term := dialAs(t, dir, nil, "cli") // a terminal outside Hammock
	if by := made(term); by != "" {
		t.Errorf("from a terminal: made by %q", by)
	}
	shell := dialAs(t, dir, &caller{pane: shellPane, direct: true}, "cli")
	if by := made(shell); by != shellPane {
		t.Errorf("from a pane's shell: made by %q, want its own", by)
	}
	d.mu.Lock()
	var froms []string
	for c := range d.conns {
		if c.kind == "cli" {
			froms = append(froms, c.fromPane)
		}
	}
	d.mu.Unlock()
	for _, f := range froms {
		if f == agentPane {
			t.Errorf("a caller's pane is the agent's it named")
		}
	}
}
