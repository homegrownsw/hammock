package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
)

func TestActivityInWords(t *testing.T) {
	d := &Daemon{panes: map[string]*pane{}, tabs: map[string]*pb.Tab{}, groups: map[string]*pb.Group{}, people: map[string]*pb.Person{}}
	d.groups["gp_1"] = &pb.Group{Id: "gp_1", Name: "Web", TabIds: []string{"tb_1"}}
	d.tabs["tb_1"] = &pb.Tab{Id: "tb_1", GroupId: "gp_1", Name: "api", Layout: leaf("pn_1")}
	d.panes["pn_1"] = newPane(d, &pb.Pane{Id: "pn_1", TabId: "tb_1", Agent: &pb.Agent{Harness: "claude"}})
	d.people["us_1"] = &pb.Person{Name: "Duncan"}

	for _, c := range []struct{ actor, action, target, detail, who, summary string }{
		{"ios:iPhone", "input", "pn_1", "", "iPhone", "typed in Web ▸ api"},
		{"agent:pn_1", "browser Runtime.evaluate", "pn_1", "", "Claude in Web ▸ api", "ran JavaScript in Web ▸ api"},
		{"guest:us_1", "close-tab", "tb_1", "", "Duncan", "closed the tab Web ▸ api"},
		{"user", "grant-allowed", "pn_1", "Claude in Web ▸ api wants to read Web ▸ api", "You", "allowed: read Web ▸ api"},
		{"cli:hmk", "input", "pn_gone", "", "hmk (command line)", "typed in a pane that's closed"},
		{"remote", "auth-failed", "", "", "A device", "failed to sign in"},
	} {
		who, summary, _ := d.describeAudit(c.actor, c.action, c.target, c.detail)
		if who != c.who || summary != c.summary {
			t.Errorf("%s %s: %q %q", c.actor, c.action, who, summary)
		}
	}

	// Typing in the same pane, a minute apart, is one event; then another.
	now := time.Now()
	ev := func(ago time.Duration, actor string) state.AuditEvent {
		return state.AuditEvent{Time: now.Add(-ago), Actor: actor, Action: "input", Target: "pn_1"}
	}
	out := d.activity([]state.AuditEvent{ev(0, "ios:iPhone"), ev(time.Minute, "ios:iPhone"), ev(2*time.Minute, "ios:iPhone"), ev(3*time.Minute, "cli:hmk"), ev(time.Hour, "ios:iPhone")})
	if len(out) != 3 || out[0].Count != 3 || out[0].FirstUnixMs != now.Add(-2*time.Minute).UnixMilli() || out[2].Count != 1 {
		var s []string
		for _, e := range out {
			s = append(s, e.Who+" "+e.Summary)
		}
		t.Errorf("collapsed into %d: %s", len(out), strings.Join(s, "; "))
	}
}

// A close is described before it happens: the tab's name, not "(closed)".
func TestActivityNamesWhatsClosed(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	gid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Web"}}}))
	tid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Name: "api", Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	mcp, err := client.Dial(context.Background(), SocketPath(dir), "mcp", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer mcp.Close()
	go func() {
		for range mcp.Events {
		}
	}()
	call(t, mcp, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
	events, err := d.store.RecentAudit(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Action != "close-tab" || !strings.Contains(events[0].Summary, "Web ▸ api") {
		t.Fatalf("events %+v", events)
	}
}
