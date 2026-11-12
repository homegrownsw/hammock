package daemon

// Agent presence: when an agent acts on a browser or simulator pane, this
// Mac's apps show it on the pane (a cursor or finger where it acts, a glow
// and a badge), as AgentAction events. The daemon sees every such call
// (browserCall, simulatorCall), so it sends them, and holds an agent's
// pointer back for as long as the cursor takes to glide to a new place,
// so the cursor gets there before the page or app reacts. The config
// file's agent-presence = false turns it off (no events, no waiting).
//
// The badge's Take over (HoldPane) stops agents acting on the pane until
// the person hands it back: agentCheck refuses them (errTakenOver), though
// they may still look.

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/browserjs"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// agentGlide is how long an agent's pointer waits for the cursor to get to
// a new place: the apps' glide (AgentPresenceOverlay.glide on the Mac, 0.22 s)
// and a little more, for the event to reach them.
const agentGlide = 300 * time.Millisecond

var errTakenOver = errors.New("the person at this Mac took this pane over: you can still look at it (screenshots, snapshots), but ask them to hand it back before acting on it")

func agentPresenceOn() bool { return hammockconfig.Bool("agent-presence", true) }

// agentPresence is what the daemon remembers to show agents acting.
type agentPresence struct {
	mu       sync.Mutex
	at       map[string][2]float32 // pane -> where its agent's pointer last was
	viewport map[string]viewport   // browser pane -> its page's size, briefly
}

type viewport struct {
	w, h float64
	at   time.Time
}

// moved records where an action put the pane's pointer, reporting whether
// that's somewhere new (so the cursor glides there).
func (p *agentPresence) moved(a *pb.AgentAction) bool {
	if a.X < 0 {
		return false
	}
	switch a.Kind {
	case "move", "click", "tap", "swipe", "pinch", "touch", "scroll":
	default:
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.at == nil {
		p.at = map[string][2]float32{}
	}
	last, seen := p.at[a.PaneId]
	end := [2]float32{a.X, a.Y}
	if a.Kind == "swipe" {
		end = [2]float32{a.X2, a.Y2}
	}
	p.at[a.PaneId] = end
	if a.Kind == "touch" && a.Label != "down" {
		return false // a drag already under way: no waiting between its moves
	}
	const near = 0.004
	return !seen || abs32(last[0]-a.X) > near || abs32(last[1]-a.Y) > near
}

func (p *agentPresence) forget(paneID string) {
	p.mu.Lock()
	delete(p.at, paneID)
	delete(p.viewport, paneID)
	p.mu.Unlock()
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

// presenceAgent is the agent's own pane, for the badge.
func (c *conn) presenceAgent() (string, bool) {
	if cl := c.caller; cl != nil && cl.agent {
		return cl.pane, true
	}
	if c.isAgent() {
		return c.fromPane, true
	}
	return "", false
}

// showAction sends an agent's action to this Mac's apps and waits while
// the cursor glides to it, if it went somewhere new.
func (d *Daemon) showAction(a *pb.AgentAction) {
	if a == nil {
		return
	}
	glide := d.presence.moved(a)
	d.mu.Lock()
	d.toLocal(&pb.ServerMessage{Msg: &pb.ServerMessage_AgentAction{AgentAction: a}})
	d.mu.Unlock()
	if glide {
		time.Sleep(agentGlide)
	}
}

// browserPresence shows an agent's DevTools call on a browser pane. It
// runs before the call, off the read loop (it may wait).
func (d *Daemon) browserPresence(c *conn, paneID, method string, params json.RawMessage) {
	agent, ok := c.presenceAgent()
	if !ok || !agentPresenceOn() {
		return
	}
	a := browserAction(method, params, func() (float64, float64) { return d.pageSize(paneID) })
	if a == nil {
		return
	}
	a.PaneId, a.AgentPaneId = paneID, agent
	d.showAction(a)
}

// browserAction is what a DevTools call looks like to the person, nil for
// nothing worth showing. size gives the page's size in CSS pixels, which
// pointer events are in (only asked for those).
func browserAction(method string, params json.RawMessage, size func() (float64, float64)) *pb.AgentAction {
	a := &pb.AgentAction{X: -1, Y: -1, X2: -1, Y2: -1}
	switch method {
	case "Input.dispatchMouseEvent":
		var ev struct {
			Type string  `json:"type"`
			X    float64 `json:"x"`
			Y    float64 `json:"y"`
		}
		_ = json.Unmarshal(params, &ev)
		switch ev.Type {
		case "mouseMoved":
			a.Kind = "move"
		case "mousePressed":
			a.Kind = "click"
		case "mouseWheel":
			a.Kind = "scroll"
		default:
			return nil // the release: its press showed it
		}
		if w, h := size(); w > 0 && h > 0 {
			a.X, a.Y = clamp01(ev.X/w), clamp01(ev.Y/h)
		}
	case "Input.insertText", "Input.imeSetComposition":
		a.Kind = "type"
	case "Input.dispatchKeyEvent":
		var ev struct {
			Type string `json:"type"`
			Key  string `json:"key"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(params, &ev)
		if ev.Type != "keyDown" && ev.Type != "rawKeyDown" {
			return nil
		}
		a.Kind = "key"
		if len([]rune(ev.Key)) == 1 && ev.Text != "" {
			a.Kind = "type" // a letter, typed one at a time
		} else {
			a.Label = ev.Key
		}
	case "Page.captureScreenshot", "Accessibility.getFullAXTree", "DOM.getDocument", "Page.getLayoutMetrics":
		a.Kind = "look"
	case "Runtime.evaluate":
		// The MCP tools read the page with scripts (browser_snapshot,
		// browser_wait_for, finding what to click): those are looking.
		if _, readOnly := browserjs.ReadOnlyParams(string(params)); readOnly {
			a.Kind = "look"
		} else {
			a.Kind = "act"
		}
	default:
		a.Kind = "act"
	}
	return a
}

func clamp01(f float64) float32 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	}
	return float32(f)
}

// pageSize is a browser pane's page in CSS pixels, asked of the page (and
// remembered for a second, as a click's move, press and release come together).
func (d *Daemon) pageSize(paneID string) (float64, float64) {
	p := &d.presence
	p.mu.Lock()
	v, ok := p.viewport[paneID]
	p.mu.Unlock()
	if ok && time.Since(v.at) < time.Second {
		return v.w, v.h
	}
	params, _ := json.Marshal(map[string]any{"expression": "[innerWidth, innerHeight]", "returnByValue": true})
	r, err := d.browsers.call(paneID, "Runtime.evaluate", params, time.Second)
	if err != nil {
		return 0, 0
	}
	var res struct {
		Result struct {
			Value []float64 `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(r, &res) != nil || len(res.Result.Value) != 2 {
		return 0, 0
	}
	v = viewport{res.Result.Value[0], res.Result.Value[1], time.Now()}
	p.mu.Lock()
	if p.viewport == nil {
		p.viewport = map[string]viewport{}
	}
	p.viewport[paneID] = v
	p.mu.Unlock()
	return v.w, v.h
}

// simulatorAction is what a simulator call looks like to the person, nil
// for nothing worth showing. size is the screen in points (zero when not
// yet known: then there's no position).
func simulatorAction(op string, args simArgs, size [2]float64) *pb.AgentAction {
	a := &pb.AgentAction{Kind: op, X: -1, Y: -1, X2: -1, Y2: -1}
	at := func(x, y float64) (float32, float32) {
		if size[0] <= 0 || size[1] <= 0 {
			return -1, -1
		}
		return clamp01(x / size[0]), clamp01(y / size[1])
	}
	switch op {
	case "tap":
		a.X, a.Y = at(args.X, args.Y)
	case "swipe":
		a.X, a.Y = at(args.X, args.Y)
		a.X2, a.Y2 = at(args.X2, args.Y2)
		a.Ms = uint32(args.Ms)
		if args.Ms <= 0 {
			a.Ms = 300 // as simulatorCall swipes
		}
	case "touch":
		a.X, a.Y = at(args.X, args.Y)
		a.Label = args.Phase
	case "pinch":
		a.X, a.Y = at(args.X, args.Y)
		a.Scale = float32(args.Scale)
		a.Ms = uint32(max(args.Ms, 0))
	case "gesture":
		// Shown as a swipe of its first finger.
		if len(args.Paths) == 0 || len(args.Paths[0]) == 0 {
			return nil
		}
		f := args.Paths[0]
		a.Kind = "swipe"
		a.X, a.Y = at(f[0].X, f[0].Y)
		a.X2, a.Y2 = at(f[len(f)-1].X, f[len(f)-1].Y)
		a.Ms = uint32(max(f[len(f)-1].T-f[0].T, 0))
	case "text":
		a.Kind = "type"
	case "key":
		a.Kind = "key"
	case "button":
		a.Kind, a.Label = "key", args.Name
	case "screenshot", "copy":
		a.Kind = "look"
	case "rotate", "fold":
		a.Kind = "act"
	default:
		return nil
	}
	return a
}

// simulatorPresence shows an agent's simulator call. It may wait (the
// glide), so it runs where the call can.
func (d *Daemon) simulatorPresence(c *conn, paneID, op string, args simArgs, size [2]float64) {
	agent, ok := c.presenceAgent()
	if !ok || !agentPresenceOn() {
		return
	}
	a := simulatorAction(op, args, size)
	if a == nil {
		return
	}
	a.PaneId, a.AgentPaneId = paneID, agent
	d.showAction(a)
}

// paneHeld reports whether the person took a pane over from agents.
// Callers hold d.mu.
func (d *Daemon) paneHeld(id string) bool {
	p := d.panes[id]
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.AgentHeld
}

// holdPane takes a browser or simulator pane over from agents, or hands
// it back. Callers hold d.mu.
func (d *Daemon) holdPane(m *pb.HoldPane) error {
	p := d.panes[m.PaneId]
	if p == nil {
		return errors.New("no such pane")
	}
	p.mu.Lock()
	kind := p.info.Kind
	ok := kind == pb.PaneKind_PANE_KIND_BROWSER || kind == pb.PaneKind_PANE_KIND_SIMULATOR
	changed := ok && p.info.AgentHeld != m.Held
	if ok {
		p.info.AgentHeld = m.Held
	}
	p.mu.Unlock()
	if !ok {
		return errors.New("only browser and simulator panes can be taken over")
	}
	if changed {
		d.paneChanged(p)
	}
	return nil
}
