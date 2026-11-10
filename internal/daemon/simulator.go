package daemon

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Simulator panes run in one simulator host per daemon ("Hammock Simulator",
// helpers/simulator), as browser panes do in the browser host: the daemon
// owns which panes show which device; the Mac app talks to the host directly
// for the picture and touches; agents' screenshots and input go through
// here. The host outlives the daemon, and the devices outlive both.

// SimulatorSocketPath is the simulator host's socket for a state dir.
func SimulatorSocketPath(dir string) string { return filepath.Join(dir, "run", "simulator-host.sock") }

type simHub struct {
	d     *Daemon
	path  string
	start sync.Once
	kick  chan struct{}

	mu    sync.Mutex
	c     net.Conn
	seq   int
	calls map[int]chan simMsg
	size  map[string][2]float64 // pane -> screen size in points

	media *hostMedia // video (hostmedia.go)
	// Devices Hammock booted (a pane saw them off, then on): shut down a
	// while after their last pane closes, unless one opens again first.
	// Devices already running (Xcode's, yours) are left alone.
	seenOff  map[string]bool
	started  map[string]bool
	shutdown map[string]*time.Timer
}

// simulatorShutdownDelay: long enough that closing and reopening a pane (or
// a quick switch) doesn't cost a cold boot.
var simulatorShutdownDelay = time.Minute

type simMsg struct {
	T        string            `json:"t"`
	ID       string            `json:"id"`
	IDs      []string          `json:"ids"`
	Built    int64             `json:"built"`
	Pid      int               `json:"pid"`
	Seq      int               `json:"seq"`
	UDID     string            `json:"udid"`
	Name     string            `json:"name"`
	Booted   bool              `json:"booted"`
	W        float64           `json:"w"`
	H        float64           `json:"h"`
	PNG      string            `json:"png"`
	Error    string            `json:"error"`
	Devices  []json.RawMessage `json:"devices"`
	Chrome   string            `json:"chrome"`
	Mask     string            `json:"mask"`
	Foldable bool              `json:"foldable"`
	Hinge    float64           `json:"hinge"`
	Text     string            `json:"text"`
}

func newSimHub(d *Daemon) *simHub {
	h := &simHub{d: d, path: simulatorHostPath(), kick: make(chan struct{}, 1), calls: map[int]chan simMsg{}, size: map[string][2]float64{},
		seenOff: map[string]bool{}, started: map[string]bool{}, shutdown: map[string]*time.Timer{}}
	h.media = &hostMedia{d: d, name: "simulator", socket: func() string { return SimulatorSocketPath(d.cfg.Dir) }, ensure: h.ensure}
	return h
}

// simulatorHostPath finds the host: $HMK_SIMULATOR_HOST, else the helper
// bundled in the app (Contents/Helpers), else one next to hmkd (bin/).
func simulatorHostPath() string {
	if p := helperEnv("HMK_SIMULATOR_HOST"); p != "" { // (vetted: helperpath.go)
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	const app = "Hammock Simulator.app/Contents/MacOS/Hammock Simulator"
	for _, p := range []string{filepath.Join(filepath.Dir(exe), "..", "Helpers", app), filepath.Join(filepath.Dir(exe), app)} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func (h *simHub) ensure() {
	h.start.Do(func() { go h.run() })
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

func (h *simHub) send(m map[string]any) {
	b, _ := json.Marshal(m)
	b = append(b, '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.c == nil {
		return // panes are reopened on connect
	}
	_ = h.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := h.c.Write(b); err != nil {
		h.c.Close()
	}
}

func (h *simHub) open(id, udid string) {
	h.ensure()
	h.mu.Lock()
	if t := h.shutdown[udid]; t != nil {
		t.Stop() // shown again: keep it running
		delete(h.shutdown, udid)
	}
	h.mu.Unlock()
	h.send(map[string]any{"t": "open", "id": id, "udid": udid})
}

func (h *simHub) close(id, udid string) {
	h.mu.Lock()
	delete(h.size, id)
	if udid != "" && h.started[udid] && simulatorAutoShutdown() {
		if t := h.shutdown[udid]; t != nil {
			t.Stop()
		}
		h.shutdown[udid] = time.AfterFunc(simulatorShutdownDelay, func() { h.shutDownIfUnused(udid) })
	}
	h.mu.Unlock()
	h.send(map[string]any{"t": "close", "id": id})
}

// booted records a device's state as a pane sees it: off, then on, means
// Hammock booted it.
func (h *simHub) booted(udid string, on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !on {
		h.seenOff[udid] = true
	} else if h.seenOff[udid] {
		h.started[udid] = true
	}
}

// shutDownIfUnused shuts a device Hammock booted down once no pane shows it.
func (h *simHub) shutDownIfUnused(udid string) {
	h.d.mu.Lock()
	for _, p := range h.d.panes {
		if p.isSimulator() && p.snapshotInfo().DeviceUdid == udid {
			h.d.mu.Unlock()
			return
		}
	}
	h.d.mu.Unlock()
	h.shutDown(udid)
}

// shutDown shuts a device down now.
func (h *simHub) shutDown(udid string) {
	h.mu.Lock()
	if t := h.shutdown[udid]; t != nil {
		t.Stop()
	}
	delete(h.shutdown, udid)
	delete(h.started, udid)
	delete(h.seenOff, udid)
	h.mu.Unlock()
	h.d.log.Info("shutting simulator down", "udid", udid)
	if out, err := exec.Command("xcrun", "simctl", "shutdown", udid).CombinedOutput(); err != nil {
		h.d.log.Warn("simulator shutdown", "udid", udid, "err", err, "out", strings.TrimSpace(string(out)))
	}
}

// simulatorAutoShutdown: simulators Hammock booted shut down after their last
// pane closes, unless the config file says simulator-shutdown = false.
func simulatorAutoShutdown() bool { return hammockconfig.Bool("simulator-shutdown", true) }

func (h *simHub) run() {
	log := h.d.log.With("component", "simulator")
	for {
		if h.path == "" {
			log.Warn("no simulator host found; simulator panes can't run")
			<-h.kick
			continue
		}
		c, err := net.DialTimeout("unix", SimulatorSocketPath(h.d.cfg.Dir), time.Second)
		if err != nil {
			if err = h.spawn(); err == nil {
				for i := 0; i < 50 && c == nil; i++ {
					time.Sleep(100 * time.Millisecond)
					c, err = net.DialTimeout("unix", SimulatorSocketPath(h.d.cfg.Dir), time.Second)
				}
			}
		}
		if c == nil {
			log.Error("simulator host unavailable", "err", err)
			select {
			case <-h.kick:
			case <-time.After(10 * time.Second):
			}
			continue
		}
		h.mu.Lock()
		h.c = c
		h.mu.Unlock()
		h.read(c)
		h.mu.Lock()
		h.c = nil
		for seq, ch := range h.calls {
			ch <- simMsg{Error: "the simulator host stopped"}
			delete(h.calls, seq)
		}
		h.mu.Unlock()
		c.Close()
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *simHub) spawn() error {
	dir := h.d.cfg.Dir
	logf, err := os.OpenFile(filepath.Join(dir, "logs", "simulator-host.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	// It talks only to hmkd and the Hammock app, by their code signatures
	// (helpers/common/peer.h).
	cmd := exec.Command(h.path, "--socket="+SimulatorSocketPath(dir))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = baseEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives the daemon
	if err := cmd.Start(); err != nil {
		return err
	}
	h.d.log.Info("started simulator host", "pid", cmd.Process.Pid)
	go cmd.Wait()
	return nil
}

func (h *simHub) read(c net.Conn) {
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var m simMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.T {
		case "hello":
			// A host from an older build: replace it.
			if st, err := os.Stat(h.path); err == nil && m.Built != 0 && st.ModTime().Unix() != m.Built {
				h.d.log.Info("replacing outdated simulator host", "pid", m.Pid)
				h.send(map[string]any{"t": "quit"})
				continue
			}
			h.sync(m.IDs)
		case "state":
			h.mu.Lock()
			if m.W > 0 {
				h.size[m.ID] = [2]float64{m.W, m.H}
			}
			h.mu.Unlock()
			h.d.simulatorState(m)
		case "shotResult", "devicesResult", "copyResult", "runnerResult":
			h.mu.Lock()
			ch := h.calls[m.Seq]
			delete(h.calls, m.Seq)
			h.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

// sync opens every simulator pane in the host and closes any it no longer wants.
func (h *simHub) sync(have []string) {
	d := h.d
	d.mu.Lock()
	want := map[string]string{}
	for id, p := range d.panes {
		if info := p.snapshotInfo(); info.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
			want[id] = info.DeviceUdid
		}
	}
	d.mu.Unlock()
	for id, udid := range want {
		h.send(map[string]any{"t": "open", "id": id, "udid": udid})
	}
	for _, id := range have {
		if _, ok := want[id]; !ok {
			h.send(map[string]any{"t": "close", "id": id})
		}
	}
}

// ask sends m with a fresh seq and waits for its answer.
func (h *simHub) ask(m map[string]any, timeout time.Duration) (simMsg, error) {
	h.ensure()
	for i := 0; i < 50; i++ {
		h.mu.Lock()
		up := h.c != nil
		h.mu.Unlock()
		if up {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	ch := make(chan simMsg, 1)
	h.mu.Lock()
	if h.c == nil {
		h.mu.Unlock()
		return simMsg{}, errors.New("the simulator host isn't running (is Xcode installed?)")
	}
	h.seq++
	m["seq"] = h.seq
	h.calls[h.seq] = ch
	seq := h.seq
	h.mu.Unlock()
	h.send(m)
	select {
	case r := <-ch:
		if r.Error != "" {
			return r, errors.New(r.Error)
		}
		return r, nil
	case <-time.After(timeout):
		h.mu.Lock()
		delete(h.calls, seq)
		h.mu.Unlock()
		return simMsg{}, errors.New("no answer from the simulator host")
	}
}

// simulatorState records a pane's device name and whether it's booted.
func (d *Daemon) simulatorState(m simMsg) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.panes[m.ID]
	if p == nil {
		return
	}
	p.mu.Lock()
	info := p.info
	if info.Kind != pb.PaneKind_PANE_KIND_SIMULATOR {
		p.mu.Unlock()
		return
	}
	before := proto.CloneOf(info)
	if m.Name != "" {
		info.Title = m.Name
	}
	info.DeviceBooted = m.Booted
	info.DeviceChrome, info.DeviceMask = m.Chrome, m.Mask
	info.DeviceFoldable, info.DeviceHinge = m.Foldable, m.Hinge
	changed := !proto.Equal(before, info)
	udid := info.DeviceUdid
	p.mu.Unlock()
	if udid != "" {
		d.simulators.booted(udid, m.Booted)
	}
	if changed {
		d.paneChangedIfLive(p)
	}
}

// startSimulator creates a simulator pane for tab t (not yet placed in its layout).
// Callers hold d.mu.
func (d *Daemon) startSimulator(t *pb.Tab, spec *pb.PaneSpec) *pane {
	info := &pb.Pane{
		Id: newID("pn"), TabId: t.Id, Kind: pb.PaneKind_PANE_KIND_SIMULATOR,
		DeviceUdid: spec.Simulator, Title: "Simulator", CreatedByPaneId: spec.CreatedByPaneId,
	}
	p := newPane(d, info)
	d.panes[info.Id] = p
	d.persist(d.store.PutPane(info))
	d.simulators.open(info.Id, spec.Simulator)
	return p
}

func (p *pane) isSimulator() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Kind == pb.PaneKind_PANE_KIND_SIMULATOR
}

// isTerminal reports whether the pane runs a program in a holder (not a
// browser or simulator).
func (p *pane) isTerminal() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Kind == pb.PaneKind_PANE_KIND_TERMINAL || p.info.Kind == pb.PaneKind_PANE_KIND_UNSPECIFIED
}

// simulatorCall runs an agent's (or the CLI's) simulator operation off the
// read loop and replies when it's done.
func (d *Daemon) simulatorCall(c *conn, reqID uint64, m *pb.SimulatorCall) error {
	var args struct {
		X, Y, X2, Y2 float64
		Ms           int
		Text, Name   string
		Phase        string   // touch: down, move or up
		Usage        int      // key: a HID keyboard usage
		Shift, Cmd   bool     // key: held with it
		Format       string   // screenshot: "jpeg" for a smaller picture (the phone's view)
		Pixels       bool     // screenshot: in the device's pixels, not points (for people saving one)
		Value        string   // rotate: portrait, upside-down, landscape-left, landscape-right, left or right
		Angle        *float64 // fold: the hinge in degrees (0 closed, 180 flat)
		Scale        float64
		Paths        [][]struct{ X, Y, T float64 } // gesture: each finger's path, in points and ms
	}
	// Your paired phones and guests may watch and touch a simulator; the
	// rest (the device list, opening one) stays on this Mac.
	if c.remote && !slices.Contains([]string{"screenshot", "tap", "swipe", "touch", "text", "key", "copy", "button", "rotate", "pinch", "gesture", "fold"}, m.Op) {
		return errors.New("only available on this Mac")
	}
	if m.ArgsJson != "" {
		if err := json.Unmarshal([]byte(m.ArgsJson), &args); err != nil {
			return fmt.Errorf("args_json: %w", err)
		}
	}
	h := d.simulators
	reply := func(j any, png []byte) {
		b, _ := json.Marshal(j)
		c.send(&pb.ServerMessage{ReplyTo: reqID, Msg: &pb.ServerMessage_SimulatorResult{SimulatorResult: &pb.SimulatorResult{Json: string(b), Png: png}}})
	}
	if m.Op == "devices" {
		go func() {
			r, err := h.ask(map[string]any{"t": "devices"}, 10*time.Second)
			if err != nil {
				c.replyErr(reqID, err)
				return
			}
			reply(r.Devices, nil)
		}()
		return errDeferred
	}
	p, err := c.pane(m.PaneId)
	if err != nil {
		return err
	}
	if !p.isSimulator() {
		return errors.New("not a simulator pane")
	}
	d.mu.Lock()
	peer := c.peer
	d.mu.Unlock()
	if (c.isAgent() || peer != "") && m.Op != "screenshot" && !(m.Op == "touch" && args.Phase != "down") {
		d.mu.Lock()
		d.audit(c.auditActor(), "simulator "+m.Op, p.id, "")
		d.mu.Unlock()
	}
	h.mu.Lock()
	size := h.size[p.id]
	h.mu.Unlock()
	ratio := func(x, y float64) (float64, float64) {
		if size[0] == 0 {
			return x, y
		}
		return x / size[0], y / size[1]
	}
	touch := func(phase string, x, y float64) {
		rx, ry := ratio(x, y)
		h.send(map[string]any{"t": "touch", "id": p.id, "phase": phase, "x": rx, "y": ry})
	}
	if m.Op == "touch" {
		// Inline, so a drag's down, moves and up stay in order.
		if !slices.Contains([]string{"down", "move", "up"}, args.Phase) {
			return fmt.Errorf("unknown touch phase %q (down, move, up)", args.Phase)
		}
		touch(args.Phase, args.X, args.Y)
		return nil
	}
	go func() {
		switch m.Op {
		case "shutdown":
			// Close the pane and shut its device down now (not after a while).
			udid := p.snapshotInfo().DeviceUdid
			d.mu.Lock()
			err := d.closePane(p.id)
			d.mu.Unlock()
			if err != nil {
				c.replyErr(reqID, err)
				return
			}
			h.shutDown(udid)
			reply(map[string]any{"ok": true}, nil)
			return
		case "screenshot":
			r, err := h.ask(map[string]any{"t": "shot", "id": p.id, "pixels": args.Pixels}, 15*time.Second)
			if err != nil {
				c.replyErr(reqID, err)
				return
			}
			png, err := base64.StdEncoding.DecodeString(r.PNG)
			if err != nil {
				c.replyErr(reqID, err)
				return
			}
			if args.Format == "jpeg" {
				if j, err := toJPEG(png); err == nil {
					png = j
				}
			}
			reply(map[string]any{"width": r.W, "height": r.H, "format": cmp.Or(args.Format, "png")}, png)
			return
		case "tap":
			touch("down", args.X, args.Y)
			time.Sleep(60 * time.Millisecond)
			touch("up", args.X, args.Y)
		case "swipe":
			ms := args.Ms
			if ms <= 0 {
				ms = 300
			}
			steps := max(ms/16, 4)
			touch("down", args.X, args.Y)
			for i := 1; i <= steps; i++ {
				f := float64(i) / float64(steps)
				time.Sleep(time.Duration(ms/steps) * time.Millisecond)
				touch("move", args.X+(args.X2-args.X)*f, args.Y+(args.Y2-args.Y)*f)
			}
			touch("up", args.X2, args.Y2)
		case "text":
			h.send(map[string]any{"t": "text", "id": p.id, "text": args.Text})
		case "key":
			if args.Usage < 4 || args.Usage > 0xE7 {
				c.replyErr(reqID, fmt.Errorf("key: usage %d isn't a keyboard key", args.Usage))
				return
			}
			h.send(map[string]any{"t": "key", "id": p.id, "usage": args.Usage, "shift": args.Shift, "cmd": args.Cmd})
		case "copy":
			// ⌘C on the device, then its pasteboard.
			r, err := h.ask(map[string]any{"t": "copy", "id": p.id}, 10*time.Second)
			if err != nil {
				c.replyErr(reqID, err)
				return
			}
			reply(map[string]any{"text": r.Text}, nil)
			return
		case "fold":
			// A foldable's hinge: open (180), closed (0), half (90) or degrees.
			angle, ok := map[string]float64{"open": 180, "closed": 0, "close": 0, "half": 90}[args.Value]
			if args.Angle != nil {
				angle, ok = *args.Angle, true
			}
			if !ok || angle < 0 || angle > 180 {
				c.replyErr(reqID, errors.New("fold: open, closed, half, or an angle from 0 (closed) to 180 (flat)"))
				return
			}
			ms := args.Ms
			if ms <= 0 {
				ms = 600
			}
			if _, err := h.ask(map[string]any{"t": "hinge", "id": p.id, "angle": angle, "ms": ms}, 30*time.Second); err != nil {
				c.replyErr(reqID, err)
				return
			}
			time.Sleep(time.Second) // the device swaps screens as it settles
		case "rotate", "pinch", "gesture":
			// Through the device's XCTest runner (started on first use).
			msg := map[string]any{"t": "rotate", "id": p.id, "value": args.Value}
			if m.Op != "rotate" {
				paths := args.Paths
				if m.Op == "pinch" {
					paths = pinchPaths(args.X, args.Y, args.Scale, args.Ms)
					if paths == nil {
						c.replyErr(reqID, errors.New("pinch: scale must be above 0 and not 1 (above 1 zooms in)"))
						return
					}
				}
				if len(paths) == 0 {
					c.replyErr(reqID, errors.New("gesture: no paths"))
					return
				}
				out := make([][]map[string]float64, len(paths))
				for i, path := range paths {
					for _, s := range path {
						rx, ry := ratio(s.X, s.Y)
						out[i] = append(out[i], map[string]float64{"x": rx, "y": ry, "t": s.T})
					}
				}
				msg = map[string]any{"t": "gesture", "id": p.id, "paths": out}
			}
			if _, err := h.ask(msg, 4*time.Minute); err != nil {
				c.replyErr(reqID, err)
				return
			}
			time.Sleep(400 * time.Millisecond) // a rotation animates
		case "button":
			if !slices.Contains([]string{"home", "lock", "side", "siri", "keyboard"}, args.Name) {
				c.replyErr(reqID, fmt.Errorf("unknown button %q (home, lock, side, siri, keyboard)", args.Name))
				return
			}
			h.send(map[string]any{"t": "button", "id": p.id, "name": args.Name})
		default:
			c.replyErr(reqID, fmt.Errorf("unknown simulator op %q", m.Op))
			return
		}
		time.Sleep(150 * time.Millisecond) // let the device react before a screenshot follows
		reply(map[string]any{"ok": true}, nil)
	}()
	return errDeferred
}

// pinchPaths: two fingers either side of (x, y), moving apart (scale > 1:
// zoom in) or together, over ms.
func pinchPaths(x, y, scale float64, ms int) [][]struct{ X, Y, T float64 } {
	if scale <= 0 || scale == 1 {
		return nil
	}
	if ms <= 0 {
		ms = 400
	}
	from := 40.0 // half the fingers' distance, in points
	if scale < 1 {
		from /= scale
	}
	to := from * scale
	const steps = 12
	paths := make([][]struct{ X, Y, T float64 }, 2)
	for i := 0; i <= steps; i++ {
		f := float64(i) / steps
		r := from + (to-from)*f
		t := float64(ms) * f
		paths[0] = append(paths[0], struct{ X, Y, T float64 }{x - r*0.7, y - r*0.7, t})
		paths[1] = append(paths[1], struct{ X, Y, T float64 }{x + r*0.7, y + r*0.7, t})
	}
	return paths
}

// toJPEG re-encodes a screenshot: a fraction of the PNG's size, for
// watching a simulator from the phone.
func toJPEG(png []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 70}); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
