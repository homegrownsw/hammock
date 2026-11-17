package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A simulator's clipboard goes both ways as it is: get_clipboard reads the
// device's pasteboard, set_clipboard writes it, and neither presses ⌘C or ⌘V.
func TestSimulatorClipboard(t *testing.T) {
	board := map[string]string{"SIM-1": "from the device"}
	get, set := simPasteboardGet, simPasteboardSet
	simPasteboardGet = func(udid string) (string, error) { return board[udid], nil }
	simPasteboardSet = func(udid, text string) error { board[udid] = text; return nil }
	defer func() { simPasteboardGet, simPasteboardSet = get, set }()

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Sim"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid,
		Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tab].FocusedPaneId]
	d.mu.Unlock()
	p.mu.Lock()
	p.info.Kind, p.info.DeviceUdid, p.info.DevicePlatform = pb.PaneKind_PANE_KIND_SIMULATOR, "SIM-1", "iOS"
	p.mu.Unlock()

	sim := func(op, args string) string {
		r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: p.id, Op: op, ArgsJson: args}}})
		return r.GetSimulatorResult().GetJson()
	}
	if got := sim("get_clipboard", ""); got != `{"text":"from the device"}` {
		t.Fatalf("get_clipboard: %s", got)
	}
	sim("set_clipboard", `{"text":"from the Mac"}`)
	if board["SIM-1"] != "from the Mac" {
		t.Fatalf("set_clipboard left %q", board["SIM-1"])
	}

	// An Apple TV has none to use (tvOS apps can't paste): it's refused.
	p.mu.Lock()
	p.info.DevicePlatform = "tvOS"
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: p.id, Op: "get_clipboard"}}}); err == nil || !strings.Contains(err.Error(), "Apple TV") {
		t.Fatalf("an Apple TV's clipboard: %v", err)
	}
}
