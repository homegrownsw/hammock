package daemon

// Looking under a design note's pin (pins.go): a page's element, an app's
// accessibility element (a simulator's XCTest runner, an Android Emulator's
// uiautomator), and the screen. Everything found here is the page's or the
// app's, so untrusted: each field is made one line and capped here, whatever
// the page's own script did, before it goes on a board or to an agent.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/homegrownsw/hammock/internal/browserjs"
	"github.com/homegrownsw/hammock/internal/emulator"
	"github.com/homegrownsw/hammock/internal/imaging"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// pinFound is a lookup's result.
type pinFound struct {
	what   string      // in a few words, for the board
	rect   *pb.PinRect // in fractions of the screen, clamped to it
	detail string      // the lines the agent gets
	// Where the page was scrolled to ("" not a page): pins dropped at
	// different scrolls are on different screenshots, however alike.
	scroll string
}

// pinLine is untrusted text made safe to quote on a line of its own: line
// breaks of every kind, other control characters and bidi overrides become
// spaces or go, runs of space are one, and it's cut to n runes.
func pinLine(s string, n int) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == '‎' || r == '‏' || r == '؜' || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩'):
			continue // bidi controls: what's shown needn't be what's there
		case unicode.IsSpace(r) || unicode.IsControl(r) || r == ' ' || r == ' ':
			if !space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return clip(strings.TrimSpace(b.String()), n)
}

// clampRect keeps a rectangle (fractions) on the screen; nil when nothing's left.
func clampRect(r *pb.PinRect) *pb.PinRect {
	if r == nil || math.IsNaN(r.X+r.Y+r.W+r.H) || math.IsInf(r.X+r.Y+r.W+r.H, 0) {
		return nil
	}
	x0, y0 := min(max(r.X, 0), 1), min(max(r.Y, 0), 1)
	x1, y1 := min(max(r.X+r.W, 0), 1), min(max(r.Y+r.H, 0), 1)
	if x1 <= x0 || y1 <= y0 {
		return nil
	}
	return &pb.PinRect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// pinLookup looks under a point (fractions of what the pane shows), giving
// up after timeout.
func (d *Daemon) pinLookup(info *pb.Pane, x, y float64, timeout time.Duration) (pinFound, error) {
	if info.Kind == pb.PaneKind_PANE_KIND_BROWSER {
		return d.pinLookupPage(info.Id, x, y, timeout)
	}
	if isEmulatorDevice(info.DeviceUdid) {
		return d.pinLookupEmulator(info.Id, x, y, timeout)
	}
	return d.pinLookupSimulator(info.Id, x, y, timeout)
}

// pageElement is what browserjs.ElementAt finds.
type pageElement struct {
	URL      string  `json:"url"`
	FrameURL string  `json:"frameUrl"`
	W        float64 `json:"w"`
	H        float64 `json:"h"`
	SX       float64 `json:"sx"`
	SY       float64 `json:"sy"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	El       *pageEl `json:"el"`
}

type pageEl struct {
	Tag      string `json:"tag"`
	What     string `json:"what"`
	Selector string `json:"selector"`
	Text     string `json:"text"`
	HTML     string `json:"html"`
	Role     string `json:"role"`
	Label    string `json:"label"`
	Rect     struct {
		X, Y, W, H float64
	} `json:"rect"`
	Styles map[string]string `json:"styles"`
}

// pinLookupPage runs browserjs.ElementAt in a world of its own (an isolated
// world of the page's main frame), so the page's scripts can't change what
// it calls; the DOM it reads is still the page's.
func (d *Daemon) pinLookupPage(paneID string, x, y float64, timeout time.Duration) (pinFound, error) {
	deadline := time.Now().Add(timeout)
	left := func() time.Duration { return max(time.Until(deadline), 50*time.Millisecond) }
	raw, err := d.browsers.call(paneID, "Page.getFrameTree", nil, left())
	if err != nil {
		return pinFound{}, err
	}
	var tree struct {
		FrameTree struct {
			Frame struct {
				ID string `json:"id"`
			} `json:"frame"`
		} `json:"frameTree"`
	}
	if json.Unmarshal(raw, &tree) != nil || tree.FrameTree.Frame.ID == "" {
		return pinFound{}, errors.New("the page has no frame to look in")
	}
	params, _ := json.Marshal(map[string]any{"frameId": tree.FrameTree.Frame.ID, "worldName": "hammock-pins"})
	if raw, err = d.browsers.call(paneID, "Page.createIsolatedWorld", params, left()); err != nil {
		return pinFound{}, err
	}
	var world struct {
		ID int `json:"executionContextId"`
	}
	if json.Unmarshal(raw, &world) != nil || world.ID == 0 {
		return pinFound{}, errors.New("the page wouldn't make a world to look in")
	}
	params, _ = json.Marshal(map[string]any{"expression": browserjs.ElementAt(x, y), "returnByValue": true, "contextId": world.ID})
	if raw, err = d.browsers.call(paneID, "Runtime.evaluate", params, left()); err != nil {
		return pinFound{}, err
	}
	var r struct {
		Result struct {
			Value pageElement `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return pinFound{}, err
	}
	if r.Exception != nil {
		return pinFound{}, errors.New("the page wouldn't say: " + pinLine(r.Exception.Text, 120))
	}
	return pageFound(r.Result.Value), nil
}

// pageFound describes what ElementAt found, for the board and the agent.
func pageFound(v pageElement) pinFound {
	var b strings.Builder
	num := func(f float64) float64 {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0
		}
		return math.Round(f)
	}
	fmt.Fprintf(&b, "Point: (%.0f, %.0f) in the viewport, CSS px (viewport %.0f×%.0f", num(v.X), num(v.Y), num(v.W), num(v.H))
	if v.SX != 0 || v.SY != 0 {
		fmt.Fprintf(&b, ", scrolled to %.0f, %.0f", num(v.SX), num(v.SY))
	}
	b.WriteString(")\n")
	if u := pinLine(v.URL, 300); u != "" {
		fmt.Fprintf(&b, "Page: %s\n", u)
	}
	if v.FrameURL != "" {
		fmt.Fprintf(&b, "In a frame showing: %s\n", pinLine(v.FrameURL, 300))
	}
	f := pinFound{scroll: fmt.Sprintf("%.0f,%.0f", num(v.SX), num(v.SY))}
	e := v.El
	if e == nil {
		b.WriteString("Element: none (the page's background)")
		f.detail = b.String()
		return f
	}
	f.what = pinLine(e.What, 100)
	if v.W > 0 && v.H > 0 {
		f.rect = clampRect(&pb.PinRect{X: e.Rect.X / v.W, Y: e.Rect.Y / v.H, W: e.Rect.W / v.W, H: e.Rect.H / v.H})
	}
	fmt.Fprintf(&b, "Element: %s\n", f.what)
	fmt.Fprintf(&b, "Selector: %s\n", pinLine(e.Selector, 300))
	if r := pinLine(e.Role, 40); r != "" {
		fmt.Fprintf(&b, "Role: %s\n", r)
	}
	fmt.Fprintf(&b, "Box: x %.0f, y %.0f, %.0f×%.0f\n", num(e.Rect.X), num(e.Rect.Y), num(e.Rect.W), num(e.Rect.H))
	if len(e.Styles) > 0 {
		keys := make([]string, 0, len(e.Styles))
		for k := range e.Styles {
			if slices.Contains(styleNames, k) { // only the ones asked for
				keys = append(keys, k)
			}
		}
		slices.SortFunc(keys, func(a, b string) int { return cmp.Compare(styleOrder(a), styleOrder(b)) })
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + pinLine(e.Styles[k], 120)
		}
		if len(parts) > 0 {
			fmt.Fprintf(&b, "Styles: %s\n", clip(strings.Join(parts, "; "), 1200))
		}
	}
	if h := pinLine(e.HTML, 700); h != "" {
		fmt.Fprintf(&b, "HTML: %s", h)
	}
	f.detail = strings.TrimRight(b.String(), "\n")
	return f
}

// styleNames are the styles ElementAt reads, in its order.
var styleNames = []string{"display", "position", "width", "height", "margin", "padding", "color", "background-color", "background-image",
	"font-family", "font-size", "font-weight", "line-height", "letter-spacing", "text-align", "text-transform", "border", "border-radius",
	"box-shadow", "opacity", "gap", "flex-direction", "justify-content", "align-items", "grid-template-columns", "z-index", "overflow"}

func styleOrder(name string) int {
	if i := slices.Index(styleNames, name); i >= 0 {
		return i
	}
	return len(styleNames)
}

// simElement is what the simulator's runner finds under a point
// (helpers/simulator/runner/Tests/Runner.m, describe).
type simElement struct {
	App        string   `json:"app"`
	Type       string   `json:"type"`
	Label      string   `json:"label"`
	Identifier string   `json:"identifier"`
	Value      string   `json:"value"`
	Title      string   `json:"title"`
	Hint       string   `json:"placeholder"`
	Enabled    *bool    `json:"enabled"`
	Selected   bool     `json:"selected"`
	Path       []string `json:"path"`
	Frame      struct {
		X, Y, W, H float64
	} `json:"frame"`
	Screen struct {
		W, H float64
	} `json:"screen"`
}

func (d *Daemon) pinLookupSimulator(paneID string, x, y float64, timeout time.Duration) (pinFound, error) {
	h := d.simulators
	h.mu.Lock()
	size := h.size[paneID]
	h.mu.Unlock()
	r, err := h.ask(map[string]any{"t": "describe", "id": paneID, "x": x, "y": y}, timeout)
	if err != nil {
		return pinFound{}, err
	}
	var e *simElement
	if len(r.Element) > 0 && string(r.Element) != "null" {
		e = &simElement{}
		if err := json.Unmarshal(r.Element, e); err != nil {
			return pinFound{}, err
		}
	}
	return simFound(e, x*size[0], y*size[1], size), nil
}

// simFound describes what the runner found at (px, py), in points, on a
// screen size (points) as shown.
func simFound(e *simElement, px, py float64, size [2]float64) pinFound {
	var b strings.Builder
	fmt.Fprintf(&b, "Point: (%.0f, %.0f) in points, as sim_tap takes them", px, py)
	if size[0] > 0 {
		fmt.Fprintf(&b, " (the screen is %.0f×%.0f)", size[0], size[1])
	}
	b.WriteString("\n")
	f := pinFound{}
	if e == nil {
		b.WriteString("Element: none found there")
		f.detail = b.String()
		return f
	}
	if a := pinLine(e.App, 120); a != "" {
		fmt.Fprintf(&b, "App: %s\n", a)
	}
	typ := pinLine(e.Type, 40)
	name := pinLine(cmpOr(e.Label, e.Title, e.Value, e.Hint, e.Identifier), 40)
	f.what = typ
	if name != "" {
		f.what += " “" + name + "”"
	}
	fmt.Fprintf(&b, "Element: %s\n", cmpOr(typ, "element"))
	for _, kv := range [][2]string{{"Label", e.Label}, {"Identifier", e.Identifier}, {"Value", e.Value}, {"Title", e.Title}, {"Placeholder", e.Hint}} {
		if v := pinLine(kv[1], 200); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], v)
		}
	}
	if e.Enabled != nil && !*e.Enabled {
		b.WriteString("Disabled\n")
	}
	if e.Selected {
		b.WriteString("Selected\n")
	}
	fmt.Fprintf(&b, "Frame: x %.0f, y %.0f, %.0f×%.0f (points)", e.Frame.X, e.Frame.Y, e.Frame.W, e.Frame.H)
	if len(e.Path) > 0 {
		path := make([]string, 0, 4)
		for _, p := range e.Path[max(len(e.Path)-4, 0):] {
			path = append(path, pinLine(p, 40))
		}
		fmt.Fprintf(&b, "\nIn: %s", strings.Join(path, " › "))
	}
	sw, sh := cmp.Or(e.Screen.W, size[0]), cmp.Or(e.Screen.H, size[1])
	if sw > 0 && sh > 0 {
		f.rect = clampRect(&pb.PinRect{X: e.Frame.X / sw, Y: e.Frame.Y / sh, W: e.Frame.W / sw, H: e.Frame.H / sh})
	}
	f.detail = b.String()
	return f
}

// uiNode is a node of uiautomator's dump.
type uiNode struct {
	Class    string   `xml:"class,attr"`
	Text     string   `xml:"text,attr"`
	ID       string   `xml:"resource-id,attr"`
	Desc     string   `xml:"content-desc,attr"`
	Package  string   `xml:"package,attr"`
	Bounds   string   `xml:"bounds,attr"`
	Enabled  string   `xml:"enabled,attr"`
	Selected string   `xml:"selected,attr"`
	Nodes    []uiNode `xml:"node"`
}

var boundsRe = regexp.MustCompile(`^\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]$`)

func (n uiNode) rect() (x0, y0, x1, y1 float64, ok bool) {
	m := boundsRe.FindStringSubmatch(n.Bounds)
	if m == nil {
		return 0, 0, 0, 0, false
	}
	v := [4]float64{}
	for i := range v {
		v[i], _ = strconv.ParseFloat(m[i+1], 64)
	}
	return v[0], v[1], v[2], v[3], v[2] > v[0] && v[3] > v[1]
}

// uiAt is the smallest node containing (x, y), in pixels, and the classes
// of the nodes it's in.
func uiAt(nodes []uiNode, x, y float64, path []string) (*uiNode, []string) {
	var best *uiNode
	var bestPath []string
	bestArea := math.Inf(1)
	for i := range nodes {
		n := &nodes[i]
		x0, y0, x1, y1, ok := n.rect()
		if !ok || x < x0 || x >= x1 || y < y0 || y >= y1 {
			continue
		}
		if a := (x1 - x0) * (y1 - y0); a <= bestArea {
			best, bestArea, bestPath = n, a, path
		}
		if c, cp := uiAt(n.Nodes, x, y, append(slices.Clone(path), shortClass(n.Class))); c != nil {
			cx0, cy0, cx1, cy1, _ := c.rect()
			if a := (cx1 - cx0) * (cy1 - cy0); a <= bestArea {
				best, bestArea, bestPath = c, a, cp
			}
		}
	}
	return best, bestPath
}

func shortClass(c string) string { return pinLine(c[strings.LastIndex(c, ".")+1:], 40) }

// emulatorUIDump is uiautomator's dump of an emulator's screen. A
// variable, for tests.
var emulatorUIDump = func(ctx context.Context, serial string) ([]byte, error) {
	return exec.CommandContext(ctx, emulator.ADB(emulatorSDK()), "-s", serial, "exec-out", "uiautomator", "dump", "/dev/tty").Output()
}

// uiDumping keeps one uiautomator dump at a time per emulator: two at once
// write over each other's file on the device.
var uiDumping sync.Map // serial → *sync.Mutex

func (d *Daemon) pinLookupEmulator(paneID string, x, y float64, timeout time.Duration) (pinFound, error) {
	s := d.emulators.session(paneID)
	if s == nil {
		return pinFound{}, errNotReady
	}
	s.mu.Lock()
	serial, w, h, scale := s.running.ADBSerial(), float64(s.w), float64(s.h), s.scale()
	ok := s.client != nil && s.running.Serial != 0
	s.mu.Unlock()
	if !ok || w == 0 || h == 0 {
		return pinFound{}, errNotReady
	}
	mu, _ := uiDumping.LoadOrStore(serial, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := emulatorUIDump(ctx, serial)
	if err != nil {
		return pinFound{}, fmt.Errorf("uiautomator: %w", err)
	}
	return uiFound(out, x, y, w, h, scale)
}

// uiFound describes the node of a uiautomator dump at (x, y), fractions of
// a screen w×h pixels at scale pixels per dp.
func uiFound(dump []byte, x, y, w, h, scale float64) (pinFound, error) {
	start, end := bytes.Index(dump, []byte("<hierarchy")), bytes.LastIndex(dump, []byte("</hierarchy>"))
	if start < 0 || end < start {
		return pinFound{}, errors.New("uiautomator gave no hierarchy")
	}
	var root struct {
		Nodes []uiNode `xml:"node"`
	}
	if err := xml.Unmarshal(dump[start:end+len("</hierarchy>")], &root); err != nil {
		return pinFound{}, err
	}
	px, py := x*w, y*h
	var b strings.Builder
	fmt.Fprintf(&b, "Point: (%.0f, %.0f) in dp, as sim_tap takes them (the screen is %.0f×%.0f)\n", px/scale, py/scale, w/scale, h/scale)
	n, path := uiAt(root.Nodes, px, py, nil)
	f := pinFound{}
	if n == nil {
		b.WriteString("Element: none found there")
		f.detail = b.String()
		return f, nil
	}
	if p := pinLine(n.Package, 120); p != "" {
		fmt.Fprintf(&b, "App: %s\n", p)
	}
	f.what = shortClass(n.Class)
	if name := pinLine(cmpOr(n.Text, n.Desc, n.ID), 40); name != "" {
		f.what += " “" + name + "”"
	}
	fmt.Fprintf(&b, "Element: %s\n", pinLine(n.Class, 120))
	for _, kv := range [][2]string{{"Text", n.Text}, {"Content description", n.Desc}, {"Resource id", n.ID}} {
		if v := pinLine(kv[1], 200); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], v)
		}
	}
	if n.Enabled == "false" {
		b.WriteString("Disabled\n")
	}
	if n.Selected == "true" {
		b.WriteString("Selected\n")
	}
	x0, y0, x1, y1, _ := n.rect()
	fmt.Fprintf(&b, "Frame: x %.0f, y %.0f, %.0f×%.0f (dp)", x0/scale, y0/scale, (x1-x0)/scale, (y1-y0)/scale)
	if len(path) > 0 {
		fmt.Fprintf(&b, "\nIn: %s", strings.Join(path[max(len(path)-4, 0):], " › "))
	}
	f.rect = clampRect(&pb.PinRect{X: x0 / w, Y: y0 / h, W: (x1 - x0) / w, H: (y1 - y0) / h})
	f.detail = b.String()
	return f, nil
}

// pinScreenshot is what a pane shows now, as a PNG no wider than
// pinShotWidest: a page's viewport, a simulator's screen (in points). Kept
// as it's encoded: a decoded Retina page is tens of megabytes.
func (d *Daemon) pinScreenshot(info *pb.Pane) ([]byte, error) {
	var data []byte
	if info.Kind == pb.PaneKind_PANE_KIND_BROWSER {
		raw, err := d.browsers.call(info.Id, "Page.captureScreenshot", json.RawMessage(`{"format":"png"}`), 10*time.Second)
		if err != nil {
			return nil, err
		}
		var shot struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(raw, &shot); err != nil {
			return nil, err
		}
		if data, err = base64.StdEncoding.DecodeString(shot.Data); err != nil {
			return nil, err
		}
	} else {
		r, err := d.simulators.ask(map[string]any{"t": "shot", "id": info.Id}, 15*time.Second)
		if err != nil {
			return nil, err
		}
		if data, err = base64.StdEncoding.DecodeString(r.PNG); err != nil {
			return nil, err
		}
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= pinShotWidest {
		return data, nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var small image.Image = imaging.Downscale(img, float64(cfg.Width)/pinShotWidest)
	return imaging.EncodePNG(small)
}
