package daemon

import (
	"encoding/json"
	"testing"

	"github.com/homegrownsw/hammock/internal/browserjs"
	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestBrowserAction(t *testing.T) {
	size := func() (float64, float64) { return 800, 600 }
	ev := func(m map[string]any) json.RawMessage { b, _ := json.Marshal(m); return b }
	evalOf := func(expr string) json.RawMessage {
		return ev(map[string]any{"expression": expr, "returnByValue": true})
	}
	for _, tc := range []struct {
		name, method string
		params       json.RawMessage
		kind, label  string
		x, y         float32
	}{
		{"move", "Input.dispatchMouseEvent", ev(map[string]any{"type": "mouseMoved", "x": 200, "y": 150}), "move", "", 0.25, 0.25},
		{"press", "Input.dispatchMouseEvent", ev(map[string]any{"type": "mousePressed", "x": 400, "y": 600}), "click", "", 0.5, 1},
		{"off the page", "Input.dispatchMouseEvent", ev(map[string]any{"type": "mousePressed", "x": 900, "y": -5}), "click", "", 1, 0},
		{"wheel", "Input.dispatchMouseEvent", ev(map[string]any{"type": "mouseWheel", "x": 80, "y": 60}), "scroll", "", 0.1, 0.1},
		{"text", "Input.insertText", ev(map[string]any{"text": "Sam"}), "type", "", -1, -1},
		{"letter", "Input.dispatchKeyEvent", ev(map[string]any{"type": "keyDown", "key": "a", "text": "a"}), "type", "", -1, -1},
		{"enter", "Input.dispatchKeyEvent", ev(map[string]any{"type": "keyDown", "key": "Enter", "text": "\r"}), "key", "Enter", -1, -1},
		{"arrow", "Input.dispatchKeyEvent", ev(map[string]any{"type": "rawKeyDown", "key": "ArrowDown"}), "key", "ArrowDown", -1, -1},
		{"screenshot", "Page.captureScreenshot", nil, "look", "", -1, -1},
		{"snapshot", "Runtime.evaluate", evalOf(browserjs.Snapshot), "look", "", -1, -1},
		{"script", "Runtime.evaluate", evalOf("document.forms[0].submit()"), "act", "", -1, -1},
		{"navigate", "Page.navigate", ev(map[string]any{"url": "http://localhost:5173"}), "act", "", -1, -1},
	} {
		a := browserAction(tc.method, tc.params, size)
		if a == nil || a.Kind != tc.kind || a.Label != tc.label || a.X != tc.x || a.Y != tc.y {
			t.Errorf("%s: got %v, want %s %q at %v, %v", tc.name, a, tc.kind, tc.label, tc.x, tc.y)
		}
	}
	for _, typ := range []string{"mouseReleased"} {
		if a := browserAction("Input.dispatchMouseEvent", ev(map[string]any{"type": typ}), size); a != nil {
			t.Errorf("%s: shown as %v", typ, a)
		}
	}
	if a := browserAction("Input.dispatchKeyEvent", ev(map[string]any{"type": "keyUp", "key": "Enter"}), size); a != nil {
		t.Errorf("key up: shown as %v", a)
	}
	// The page's size unknown (it didn't answer): a click without a place.
	a := browserAction("Input.dispatchMouseEvent", ev(map[string]any{"type": "mousePressed", "x": 4, "y": 4}), func() (float64, float64) { return 0, 0 })
	if a == nil || a.Kind != "click" || a.X != -1 {
		t.Errorf("no size: %v", a)
	}
}

func TestSimulatorAction(t *testing.T) {
	size := [2]float64{400, 800}
	a := simulatorAction("tap", simArgs{X: 100, Y: 200}, size)
	if a.Kind != "tap" || a.X != 0.25 || a.Y != 0.25 {
		t.Errorf("tap: %v", a)
	}
	a = simulatorAction("swipe", simArgs{X: 200, Y: 700, X2: 200, Y2: 100}, size)
	if a.Kind != "swipe" || a.Y != 0.875 || a.Y2 != 0.125 || a.Ms != 300 {
		t.Errorf("swipe: %v", a)
	}
	g := simArgs{Paths: [][]struct{ X, Y, T float64 }{{{X: 0, Y: 0, T: 0}, {X: 400, Y: 800, T: 500}}}}
	a = simulatorAction("gesture", g, size)
	if a.Kind != "swipe" || a.X2 != 1 || a.Y2 != 1 || a.Ms != 500 {
		t.Errorf("gesture: %v", a)
	}
	a = simulatorAction("pinch", simArgs{X: 200, Y: 400, Scale: 2, Ms: 400}, size)
	if a.Kind != "pinch" || a.X != 0.5 || a.Scale != 2 {
		t.Errorf("pinch: %v", a)
	}
	if a = simulatorAction("button", simArgs{Name: "home"}, size); a.Kind != "key" || a.Label != "home" {
		t.Errorf("button: %v", a)
	}
	if a = simulatorAction("screenshot", simArgs{}, size); a.Kind != "look" {
		t.Errorf("screenshot: %v", a)
	}
	if a = simulatorAction("tap", simArgs{X: 1, Y: 1}, [2]float64{}); a.X != -1 {
		t.Errorf("no size yet: %v", a)
	}
	if a = simulatorAction("devices", simArgs{}, size); a != nil {
		t.Errorf("devices: %v", a)
	}
}

// The cursor waits to glide only when it goes somewhere new.
func TestPresenceMoved(t *testing.T) {
	var p agentPresence
	at := func(kind string, x, y float32) *pb.AgentAction {
		return &pb.AgentAction{PaneId: "p", Kind: kind, X: x, Y: y}
	}
	if !p.moved(at("move", 0.5, 0.5)) {
		t.Fatal("first move: no glide")
	}
	if p.moved(at("click", 0.5, 0.5)) {
		t.Fatal("click where it moved to: a glide")
	}
	if !p.moved(at("click", 0.2, 0.5)) {
		t.Fatal("click somewhere new: no glide")
	}
	if p.moved(at("type", -1, -1)) || p.moved(at("look", 0.9, 0.9)) {
		t.Fatal("typing or looking: a glide")
	}
	if !p.moved(&pb.AgentAction{PaneId: "p", Kind: "touch", Label: "down", X: 0.9, Y: 0.9}) {
		t.Fatal("touch down somewhere new: no glide")
	}
	if p.moved(&pb.AgentAction{PaneId: "p", Kind: "touch", Label: "move", X: 0.1, Y: 0.1}) {
		t.Fatal("a drag's move: a glide")
	}
}

// Taken over, a pane refuses agents' actions but not their looking, until
// it's handed back; and only browser and simulator panes can be.
func TestTakeOver(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	t1 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: cat}}}))
	t2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: cat}}}))
	d.mu.Lock()
	agentPane, page := d.tabs[t1].FocusedPaneId, d.tabs[t2].FocusedPaneId
	d.mu.Unlock()

	hold := func(held bool) *pb.ServerMessage {
		return call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_HoldPane{HoldPane: &pb.HoldPane{PaneId: page, Held: held}}})
	}
	d.mu.Lock()
	err := d.holdPane(&pb.HoldPane{PaneId: page, Held: true})
	d.mu.Unlock()
	if err == nil {
		t.Fatal("a terminal taken over")
	}
	d.mu.Lock()
	p := d.panes[page]
	p.mu.Lock()
	p.info.Kind = pb.PaneKind_PANE_KIND_BROWSER
	p.mu.Unlock()
	d.mu.Unlock()
	if r := hold(true); r.GetError() != nil {
		t.Fatalf("taking over: %v", r.GetError())
	}
	if !p.snapshotInfo().AgentHeld {
		t.Fatal("not marked taken over")
	}

	c := &conn{d: d, caller: &caller{pane: agentPane, agent: true, direct: true}}
	click := &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserCall{BrowserCall: &pb.BrowserCall{
		PaneId: page, Method: "Input.dispatchMouseEvent", ParamsJson: `{"type":"mousePressed","x":1,"y":1}`,
	}}}
	if err := c.agentCheck(click); err != errTakenOver {
		t.Fatalf("clicking a pane taken over: %v", err)
	}
	nav := &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserNavigate{BrowserNavigate: &pb.BrowserNavigate{PaneId: page, Action: "reload"}}}
	if err := c.agentCheck(nav); err != errTakenOver {
		t.Fatalf("reloading a pane taken over: %v", err)
	}
	shot := &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserCall{BrowserCall: &pb.BrowserCall{PaneId: page, Method: "Page.captureScreenshot"}}}
	if err := c.agentCheck(shot); err != nil {
		t.Fatalf("looking at a pane taken over: %v", err)
	}
	// Agents can't take it back themselves.
	if err := c.agentCheck(&pb.ClientMessage{Msg: &pb.ClientMessage_HoldPane{HoldPane: &pb.HoldPane{PaneId: page}}}); err == nil {
		t.Fatal("an agent handed a pane back to itself")
	}

	if r := hold(false); r.GetError() != nil {
		t.Fatalf("handing back: %v", r.GetError())
	}
	if err := c.agentCheck(click); err == errTakenOver {
		t.Fatal("still taken over once handed back")
	}
}
