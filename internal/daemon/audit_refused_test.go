package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/state"
)

// auditRows is every audit row, newest first.
func auditRows(t *testing.T, d *Daemon) []state.AuditEvent {
	t.Helper()
	evs, err := d.store.RecentAudit(100000)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// newRows is the rows written since before (a count of rows then).
func newRows(t *testing.T, d *Daemon, before int) []state.AuditEvent {
	t.Helper()
	evs := auditRows(t, d)
	return evs[:len(evs)-before]
}

// mutations are messages that are audited when they're done, for a
// connection to try.
func mutations(gid, paneID, tabID, folder string) []*pb.ClientMessage {
	return []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: paneID}}},
		{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tabID}}},
		{Msg: &pb.ClientMessage_SignalPane{SignalPane: &pb.SignalPane{PaneId: paneID, Signal: 2}}},
		{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}},
		{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{GroupId: gid, Harness: "claude", Cwd: folder}}},
		{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: gid, Folder: folder}}},
		{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("x")}}},
	}
}

// A remote connection that hasn't signed in writes nothing to the audit
// log, whatever it sends and whoever its Hello says it is: each message is
// refused, and none of them is recorded (as done or otherwise).
func TestUnauthenticatedRemoteNotAudited(t *testing.T) {
	host, dir := startHost(t)
	ui := dial(t, dir)
	gid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Web"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	host.mu.Lock()
	paneID := host.tabs[tab].FocusedPaneId
	fp := host.remote.fingerprint
	host.mu.Unlock()
	before := len(auditRows(t, host))

	huge := "/tmp/" + strings.Repeat("x", 500000)
	for _, who := range []*pb.Hello{
		{ProtocolVersion: 1, ClientKind: "ios", ClientName: ""}, // "Your iPhone"
		{ProtocolVersion: 1, ClientKind: "user", ClientName: "You"},
		{ProtocolVersion: 1, ClientKind: "mcp", ClientName: "x", FromPaneId: paneID}, // an agent of yours
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cl, err := client.DialRemote(ctx, host.RemoteAddrs()[0], fp, who.ClientKind, who.ClientName)
		if err != nil {
			t.Fatal(err)
		}
		call(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: who}})
		for _, m := range mutations(gid, paneID, tab, huge) {
			if _, err := cl.Call(ctx, m); err == nil || !strings.Contains(err.Error(), "not authenticated") {
				t.Fatalf("%T before signing in: %v, want not authenticated", m.Msg, err)
			}
		}
		cl.Close()
		cancel()
	}
	if rows := newRows(t, host, before); len(rows) != 0 {
		t.Fatalf("%d rows audited from connections that never signed in, the first %q %q (%s)", len(rows), rows[0].Who, rows[0].Summary, rows[0].Action)
	}
	host.mu.Lock()
	alive := host.panes[paneID] != nil
	host.mu.Unlock()
	if !alive {
		t.Fatal("the pane was closed")
	}
}

// A stranger (signed in with a key anyone can make, a member of nothing)
// counts as not signed in: what it's refused isn't recorded either.
func TestStrangerRefusedNotAudited(t *testing.T) {
	host, dir := startHost(t)
	ui := dial(t, dir)
	gid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Web"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	host.mu.Lock()
	paneID := host.tabs[tab].FocusedPaneId
	host.mu.Unlock()
	stranger, _ := rawPeer(t, host)
	before := len(auditRows(t, host))
	for _, m := range mutations(gid, paneID, tab, "/tmp/"+strings.Repeat("x", 100000)) {
		if _, err := try(stranger, m); err == nil {
			t.Fatalf("%T from a stranger was done", m.Msg)
		}
	}
	if rows := newRows(t, host, before); len(rows) != 0 {
		t.Fatalf("%d rows audited from a stranger, the first %q %q", len(rows), rows[0].Who, rows[0].Summary)
	}
}

// A guest's message that's refused is recorded as a refused attempt, in
// words that say so, never as done; a connection's refused attempts are
// recorded only so many a minute, the rest counted in one row.
func TestGuestRefusedAuditedAsRefused(t *testing.T) {
	old := refusedAuditEvery
	refusedAuditEvery = 3 * time.Second
	defer func() { refusedAuditEvery = old }()
	host, _, peer, uid, gid, tab, paneID := sharedSetup(t, pb.Role_ROLE_VIEWER)
	before := len(auditRows(t, host))

	if _, err := try(peer, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}); err == nil {
		t.Fatal("a viewer opened a tab")
	}
	if _, err := try(peer, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tab}}}); err == nil {
		t.Fatal("a viewer closed a tab")
	}
	rows := newRows(t, host, before)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
	closeRow, tabRow := rows[0], rows[1]
	if tabRow.Action != state.RefusedPrefix+"create-tab" || tabRow.Who != "Guest" ||
		!strings.HasPrefix(tabRow.Summary, "tried to open a tab in Shared (refused: ") || tabRow.Kind != "guest" {
		t.Errorf("refused tab: %s %q %q (%s)", tabRow.Action, tabRow.Who, tabRow.Summary, tabRow.Kind)
	}
	if closeRow.Action != state.RefusedPrefix+"close-tab" || !strings.HasPrefix(closeRow.Summary, "tried to close the tab Shared") ||
		!strings.Contains(closeRow.Summary, "(refused: you can view this Group, not drive it)") {
		t.Errorf("refused close: %s %q", closeRow.Action, closeRow.Summary)
	}
	host.mu.Lock()
	open := host.tabs[tab] != nil
	host.mu.Unlock()
	if !open {
		t.Fatal("the tab was closed")
	}

	// A flood: only refusedAuditBudget a minute (here, a moment), and one
	// row for the rest once it's up.
	for range 40 {
		_, _ = try(peer, &pb.ClientMessage{Msg: &pb.ClientMessage_SignalPane{SignalPane: &pb.SignalPane{PaneId: paneID, Signal: 2}}})
	}
	count := func() (refused int, more string) {
		for _, e := range newRows(t, host, before) {
			if strings.HasPrefix(e.Action, state.RefusedPrefix) {
				refused++
			}
			if e.Action == state.RefusedPrefix+"more" {
				more = e.Who + " " + e.Summary
			}
		}
		return
	}
	if n, _ := count(); n != refusedAuditBudget {
		t.Fatalf("%d refused attempts recorded, want %d", n, refusedAuditBudget)
	}
	waitFor(t, "the row for the rest", func() bool { _, more := count(); return more != "" })
	if n, more := count(); n != refusedAuditBudget+1 || more != "Guest had 32 more requests refused, not recorded one by one" {
		t.Fatalf("%d rows, %q", n, more)
	}
	for _, e := range newRows(t, host, before) {
		if !strings.HasPrefix(e.Action, state.RefusedPrefix) {
			t.Errorf("recorded as done: %s %q %q", e.Action, e.Who, e.Summary)
		}
	}
	_ = uid
}

// An agent's message that's done is audited as before; one that's refused
// (out of its scope, or about a pane that isn't there) is recorded as
// refused, and a close that didn't happen isn't written as done.
func TestAgentRefusedAuditedAsRefused(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dialKind(t, dir, "mac")
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Web"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Name: "api", Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	yours := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	asPerson(t, nobody)
	asPlain(t, inPaneShell)
	before := len(auditRows(t, d))

	// Scoped as an agent (a program outside its pane): your pane isn't its
	// to type into.
	agent := dialAs(t, dir, &caller{pane: yours}, "cli")
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: yours, Data: []byte("x")}}}); err == nil || err.Error() != errAgentScope.Error() {
		t.Fatalf("typing into your pane: %v", err)
	}
	// The MCP server: a close of a pane that isn't there, and a tab it opens.
	mcp, err := client.Dial(context.Background(), SocketPath(dir), "mcp", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer mcp.Close()
	go func() {
		for range mcp.Events {
		}
	}()
	if _, err := try(mcp, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: "pn_nothere"}}}); err == nil {
		t.Fatal("closed a pane that isn't there")
	}
	call(t, mcp, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})

	rows := newRows(t, d, before)
	byAction := map[string]state.AuditEvent{}
	for _, e := range rows {
		byAction[e.Action] = e
	}
	if e, ok := byAction[state.RefusedPrefix+"input"]; !ok || !strings.HasPrefix(e.Summary, "tried to type in Web ▸ api") || !strings.Contains(e.Summary, "(refused: out of scope") || e.Kind != "agent" {
		t.Errorf("refused input: %+v", e)
	}
	if e, ok := byAction[state.RefusedPrefix+"close-pane"]; !ok || !strings.HasPrefix(e.Summary, "tried to close a pane that's closed (refused: no such pane") {
		t.Errorf("refused close: %+v", e)
	}
	if e, ok := byAction["create-tab"]; !ok || e.Summary != "opened a tab in Web" {
		t.Errorf("a tab opened: %+v", e)
	}
	for _, a := range []string{"input", "close-pane"} {
		if _, ok := byAction[a]; ok {
			t.Errorf("%s recorded as done", a)
		}
	}
}

// A paired phone is named in the log by the name it paired with, never by
// what its Hello says, before signing in or after.
func TestDeviceAuditedAsItsPairedName(t *testing.T) {
	host, dir := startHost(t)
	ui := dial(t, dir)
	gid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Web"}}}))
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	host.mu.Lock()
	if host.remote.pairings == nil {
		host.remote.pairings = map[string]time.Time{}
	}
	host.remote.pairings[hex.EncodeToString(secret)] = time.Now().Add(time.Minute)
	fp := host.remote.fingerprint
	host.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, err := client.DialRemote(ctx, host.RemoteAddrs()[0], fp, "ios", "")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	call(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{ProtocolVersion: 1, ClientKind: "user", ClientName: "You"}}})
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	call(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: secret, PublicKey: pub, DeviceName: "Sam's iPhone", Platform: "ios"}}})
	call(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{ProtocolVersion: 1, ClientKind: "mcp", ClientName: "You", FromPaneId: "pn_x"}}})
	before := len(auditRows(t, host))
	call(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	_, _ = try(cl, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: "pn_nothere"}}})
	rows := newRows(t, host, before)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
	for _, e := range rows {
		if e.Who != "Sam's iPhone" || e.Kind != "device" {
			t.Errorf("%s by %q (%s), want the phone's paired name", e.Action, e.Who, e.Kind)
		}
	}
	if rows[1].Summary != "opened a tab in Web" || !strings.HasPrefix(rows[0].Summary, "tried to close") {
		t.Errorf("rows: %q; %q", rows[1].Summary, rows[0].Summary)
	}
}

// A request that waits for you (approval.go) is recorded once it's handled,
// as what happened then; one you deny (or that times out) isn't recorded as
// done, nor as refused (the decision has its own row).
func TestDeferredRequestAuditedOnce(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dialKind(t, dir, "mac")
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	asPerson(t, nobody)
	asPlain(t, inPaneShell)
	outside := dialAs(t, dir, nil, "cli") // scoped as an agent: an upload asks you
	upload := &pb.ClientMessage{Msg: &pb.ClientMessage_UploadFile{UploadFile: &pb.UploadFile{PaneId: "pn_nothere", Name: "a.txt", Data: []byte("x")}}}
	answer := func(id string, dec pb.RespondGrant_Decision) *pb.ClientMessage {
		return &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: id, Decision: dec}}}
	}
	uploads := func() (n int) {
		for _, e := range auditRows(t, d) {
			if strings.HasSuffix(e.Action, "upload") {
				n++
			}
		}
		return n
	}

	got := make(chan error, 1)
	go func() { _, err := try(outside, upload); got <- err }()
	req := personGrant(t, d, nil)
	call(t, ui, answer(req.Id, pb.RespondGrant_DECISION_DENY))
	if err := <-got; err == nil || err.Error() != errDenied.Error() {
		t.Fatalf("denied: %v", err)
	}
	if n := uploads(); n != 0 {
		t.Fatalf("a denied upload: %d rows", n)
	}

	go func() { _, err := try(outside, upload); got <- err }()
	req2 := personGrant(t, d, map[string]bool{req.Id: true})
	call(t, ui, answer(req2.Id, pb.RespondGrant_DECISION_ALLOW_ONCE))
	<-got // (allowed, then refused: there's no such pane)
	if n := uploads(); n != 1 {
		t.Fatalf("an allowed upload: %d rows, want 1", n)
	}
	for _, e := range auditRows(t, d) {
		if e.Action == "upload" {
			t.Fatalf("an upload that failed recorded as done: %q", e.Summary)
		}
	}
}

// However many refused attempts there are, they can't push out what was
// done: they're kept to a cap of their own.
func TestRefusedFloodKeepsHistory(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	defer d.mu.Unlock()
	d.audit("user", "paired", "dv_1", "ios")
	msg := &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: "gr_x", Folder: "/" + strings.Repeat("x", 100000)}}}
	for range 600 { // 6,000 attempts from 600 connections, each at its budget
		c := &conn{d: d, kind: "mcp"}
		for range refusedAuditBudget {
			c.auditRefused(msg, errAgentScope)
		}
	}
	evs := auditRows(t, d)
	var real, refused int
	for _, e := range evs {
		if strings.HasPrefix(e.Action, state.RefusedPrefix) {
			refused++
			if len(e.Target) > state.AuditTargetMax || len(e.Summary) > state.AuditSummaryMax {
				t.Fatalf("a row of %d and %d bytes", len(e.Target), len(e.Summary))
			}
		} else if e.Action == "paired" {
			real++
		}
	}
	if real != 1 {
		t.Fatal("a flood of refused attempts pushed out the history")
	}
	if refused > 5000+500 {
		t.Fatalf("%d refused attempts kept", refused)
	}
}

// Refused attempts, and a device naming itself as it pairs, in words.
func TestRefusedInWords(t *testing.T) {
	d := &Daemon{panes: map[string]*pane{}, tabs: map[string]*pb.Tab{}, groups: map[string]*pb.Group{}, people: map[string]*pb.Person{}}
	d.groups["gp_1"] = &pb.Group{Id: "gp_1", Name: "Web", TabIds: []string{"tb_1"}}
	d.tabs["tb_1"] = &pb.Tab{Id: "tb_1", GroupId: "gp_1", Name: "api", Layout: leaf("pn_1")}
	d.panes["pn_1"] = newPane(d, &pb.Pane{Id: "pn_1", TabId: "tb_1"})
	d.people["us_1"] = &pb.Person{Name: "Duncan"}
	for _, c := range []struct{ actor, action, target, detail, who, summary string }{
		{"guest:us_1", "refused:close-pane", "pn_1", "you can view this Group, not drive it", "Duncan", "tried to close Web ▸ api (refused: you can view this Group, not drive it)"},
		{"guest:us_1", "refused:create-tab", "gp_1", "x", "Duncan", "tried to open a tab in Web (refused: x)"},
		{"guest:us_1", "refused:group-folder", "gp_1 → /Users/me/Developer/app", "x", "Duncan", "tried to change the folder of Web to app (refused: x)"},
		{"guest:us_1", "refused:start-agent", "claude in /Users/me/Developer/app", "x", "Duncan", "tried to start Claude in app (refused: x)"},
		{"guest:us_1", "refused:arrange-pane", "tb_1", "x", "Duncan", "tried to arrange the panes of Web ▸ api (refused: x)"},
		{"guest:us_1", "refused:more", "", "12", "Duncan", "had 12 more requests refused, not recorded one by one"},
		{"device:Sam's iPhone", "input", "pn_1", "", "Sam's iPhone", "typed in Web ▸ api"},
		{"remote:Your iPhone", "pair-rejected", "", "", "A device calling itself “Your iPhone”", "tried to pair with a wrong or expired code"},
	} {
		who, summary, _ := d.describeAudit(c.actor, c.action, c.target, c.detail)
		if who != c.who || summary != c.summary {
			t.Errorf("%s %s: %q %q", c.actor, c.action, who, summary)
		}
	}
}
