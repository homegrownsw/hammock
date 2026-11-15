package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/emulator"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
)

// Android Emulators in simulator panes. A pane whose device is an AVD
// ("avd:NAME") has a session here: it attaches to the AVD's emulator, or
// boots it with no window, then streams its screen's frames into a file the
// simulator host maps and shows (and encodes for remote viewers) as it does
// a simulator's screen. Input goes from here to the emulator by gRPC
// (internal/emulator), not through the host.

type emuHub struct {
	d        *Daemon
	mu       sync.Mutex
	sessions map[string]*emuSession // by pane
	// AVDs Hammock booted: shut down a while after their last pane closes,
	// as simulators are. Emulators already running are left alone.
	started  map[string]bool
	shutdown map[string]*time.Timer
}

func newEmuHub(d *Daemon) *emuHub {
	return &emuHub{d: d, sessions: map[string]*emuSession{}, started: map[string]bool{}, shutdown: map[string]*time.Timer{}}
}

// emuSession drives one pane's emulator.
type emuSession struct {
	hub      *emuHub
	id, avd  string
	ctx      context.Context
	cancel   context.CancelFunc
	takeover chan struct{}

	mu       sync.Mutex
	info     emulator.AVD
	client   *emulator.Client
	running  emulator.Running
	booted   bool
	w, h     int     // the last frame, in pixels
	turn     float32 // degrees the device is turned
	hinge    float64 // a foldable's hinge, as last set
	framePth string
}

// sdk is the Android SDK (android-sdk in the config file, else the usual places).
func emulatorSDK() string {
	v, _ := hammockconfig.Value("android-sdk")
	return emulator.SDKDir(v)
}

// open starts a pane's session (again, if it had one).
func (h *emuHub) open(id, udid string) {
	avd := emulator.AVDName(udid)
	h.mu.Lock()
	if old := h.sessions[id]; old != nil {
		old.cancel()
	}
	if t := h.shutdown[avd]; t != nil {
		t.Stop() // shown again: keep it running
		delete(h.shutdown, avd)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &emuSession{hub: h, id: id, avd: avd, ctx: ctx, cancel: cancel, takeover: make(chan struct{}, 1),
		framePth: filepath.Join(h.d.cfg.Dir, "run", "emu-"+id+".rgba")}
	h.sessions[id] = s
	h.mu.Unlock()
	go s.run()
}

// close ends a pane's session; an emulator Hammock booted shuts down a
// while after its last pane closes.
func (h *emuHub) close(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	if s == nil {
		return
	}
	s.cancel()
	delete(h.sessions, id)
	if h.started[s.avd] && simulatorAutoShutdown() && !h.inUseLocked(s.avd) {
		if t := h.shutdown[s.avd]; t != nil {
			t.Stop()
		}
		avd := s.avd
		h.shutdown[avd] = time.AfterFunc(simulatorShutdownDelay, func() { h.shutDownIfUnused(avd) })
	}
}

func (h *emuHub) inUseLocked(avd string) bool {
	for _, s := range h.sessions {
		if s.avd == avd {
			return true
		}
	}
	return false
}

func (h *emuHub) shutDownIfUnused(avd string) {
	h.mu.Lock()
	inUse := h.inUseLocked(avd)
	delete(h.shutdown, avd)
	if !inUse {
		delete(h.started, avd)
	}
	h.mu.Unlock()
	if !inUse {
		h.shutDown(avd)
	}
}

// shutDown stops an AVD's emulator now.
func (h *emuHub) shutDown(avd string) {
	if r, ok := emulator.RunningAVD(emulator.DiscoveryDir(), avd); ok {
		h.d.log.Info("shutting emulator down", "avd", avd)
		emulator.Stop(emulatorSDK(), r)
	}
}

func (h *emuHub) session(id string) *emuSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[id]
}

// announceAll tells a (re)started simulator host about every emulator pane.
func (h *emuHub) announceAll() {
	h.mu.Lock()
	ss := make([]*emuSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	for _, s := range ss {
		s.announce()
		s.mu.Lock()
		w, hh := s.w, s.h
		s.mu.Unlock()
		if w > 0 {
			s.hub.d.simulators.send(map[string]any{"t": "emuFrame", "id": s.id, "w": w, "h": hh}) // the file still holds it
		}
	}
}

// note says why the pane shows nothing (empty: nothing's wrong).
func (s *emuSession) note(text string, elsewhere bool) {
	d := s.hub.d
	d.mu.Lock()
	p := d.panes[s.id]
	d.mu.Unlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	changed := p.info.DeviceNote != text || p.info.DeviceElsewhere != elsewhere
	p.info.DeviceNote, p.info.DeviceElsewhere = text, elsewhere
	p.mu.Unlock()
	if changed {
		d.mu.Lock()
		d.paneChangedIfLive(p)
		d.mu.Unlock()
	}
}

// scale is the screen's density in pixels per dp (points, as simulator
// panes measure screens): 2.625 for a 420 dpi phone.
func (s *emuSession) scale() float64 {
	if s.info.Density > 0 {
		return float64(s.info.Density) / 160
	}
	return 2
}

// announce tells the host the pane's state (and its frames' file).
func (s *emuSession) announce() {
	s.mu.Lock()
	m := map[string]any{"t": "emuOpen", "id": s.id, "udid": emulator.Prefix + s.avd, "name": cmp.Or(s.info.Display, s.avd),
		"platform": s.info.Platform(), "scale": s.scale(), "booted": s.booted, "path": s.framePth,
		"foldable": s.info.Foldable, "hinge": s.hinge}
	s.mu.Unlock()
	s.hub.d.simulators.ensure()
	s.hub.d.simulators.send(m)
}

func (s *emuSession) wait(d time.Duration) bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// run attaches to (or boots) the AVD's emulator and shows its screen until
// the pane closes, coming back when the emulator stops and starts again.
func (s *emuSession) run() {
	log := s.hub.d.log.With("component", "emulator", "avd", s.avd, "pane", s.id)
	var info emulator.AVD
	for _, a := range emulator.ListAVDs(emulator.AVDDir()) {
		if a.Name == s.avd {
			info = a
		}
	}
	if info.Name == "" {
		s.note("There's no Android Virtual Device named "+s.avd, false)
		return
	}
	s.mu.Lock()
	s.info = info
	if info.Foldable {
		s.hinge = 180 // it boots open
	}
	s.mu.Unlock()
	// Room for a frame either way up.
	side := cmp.Or(max(info.Width, info.Height), 4096)
	fr, err := emulator.NewFrames(s.framePth, side, side)
	if err != nil {
		s.note("Can't show the emulator: "+err.Error(), false)
		return
	}
	defer fr.Close()
	s.announce()
	for s.ctx.Err() == nil {
		r, err := s.attach(log)
		if err != nil {
			if s.ctx.Err() == nil {
				log.Warn("emulator", "err", err)
			}
			if !s.wait(3 * time.Second) {
				return
			}
			continue
		}
		s.note("", false)
		c, err := emulator.Dial(r)
		if err != nil {
			s.note(err.Error(), false)
			s.wait(3 * time.Second)
			continue
		}
		s.mu.Lock()
		s.client, s.running = c, r
		s.mu.Unlock()
		s.waitBooted(c)
		// The frames, until the stream ends (the emulator stopped).
		fr.W, fr.H = side, side
		err = c.StreamFrames(s.ctx, fr, func(w, h int, seq uint32) {
			s.mu.Lock()
			s.w, s.h = w, h
			s.mu.Unlock()
			s.hub.d.simulators.send(map[string]any{"t": "emuFrame", "id": s.id, "w": w, "h": h})
		})
		s.mu.Lock()
		s.client, s.booted = nil, false
		s.mu.Unlock()
		c.Close()
		if s.ctx.Err() != nil {
			return
		}
		log.Info("emulator stream ended", "err", err)
		s.announce()
		if !s.wait(2 * time.Second) {
			return
		}
	}
}

// attach finds the AVD's emulator, booting it if none runs (Hammock's own,
// first time round). One running where Hammock can't drive it (Android
// Studio's) waits for the pane's take over, or for it to stop.
func (s *emuSession) attach(log interface{ Info(string, ...any) }) (emulator.Running, error) {
	dir := emulator.DiscoveryDir()
	for {
		r, ok := emulator.RunningAVD(dir, s.avd)
		if ok && r.Reachable() {
			return r, nil
		}
		if ok {
			s.note("Running in Android Studio", true)
			select {
			case <-s.ctx.Done():
				return r, s.ctx.Err()
			case <-s.takeover:
				s.note("Taking it over from Android Studio…", false)
				emulator.Stop(emulatorSDK(), r)
				for i := 0; i < 40; i++ { // until it's gone
					if _, still := emulator.RunningAVD(dir, s.avd); !still || !s.wait(250*time.Millisecond) {
						break
					}
				}
			case <-time.After(3 * time.Second):
			}
			continue
		}
		return s.boot(log)
	}
}

func (s *emuSession) boot(log interface{ Info(string, ...any) }) (emulator.Running, error) {
	s.note("", false)
	logPath := filepath.Join(s.hub.d.cfg.Dir, "logs", "emulator-"+s.avd+".log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return emulator.Running{}, err
	}
	defer f.Close()
	if _, err := emulator.Boot(emulatorSDK(), s.avd, f); err != nil {
		s.note(err.Error(), false)
		return emulator.Running{}, err
	}
	log.Info("booting emulator")
	s.hub.mu.Lock()
	s.hub.started[s.avd] = true
	s.hub.mu.Unlock()
	r, err := emulator.WaitRunning(emulator.DiscoveryDir(), s.avd, 2*time.Minute)
	if err != nil {
		s.note("The emulator didn't start (see "+logPath+")", false)
	}
	return r, err
}

// waitBooted waits for Android to finish booting, telling the host when it has.
func (s *emuSession) waitBooted(c *emulator.Client) {
	for s.ctx.Err() == nil {
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		st, err := c.Status(ctx)
		cancel()
		if err == nil && st.Booted {
			s.mu.Lock()
			s.booted = true
			s.mu.Unlock()
			s.announce()
			return
		}
		s.wait(time.Second)
	}
}

// requestTakeover asks the session to stop Android Studio's instance and
// boot its own.
func (s *emuSession) requestTakeover() {
	select {
	case s.takeover <- struct{}{}:
	default:
	}
}

// ---- input ----

var errNotReady = errors.New("the emulator isn't running yet")

func (s *emuSession) conn() (*emulator.Client, float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil, 0, errNotReady
	}
	return s.client, s.scale(), nil
}

// px turns points (dp, as screenshots and taps measure the screen) into the
// screen's pixels.
func px(v, scale float64) int { return int(math.Round(v * scale)) }

// androidButtons are the hardware and navigation buttons, by sim_press_button's
// names, as the emulator's key names.
var androidButtons = map[string]string{
	"back": "GoBack", "home": "GoHome", "recents": "AppSwitch", "power": "Power", "lock": "Power",
	"volume_up": "AudioVolumeUp", "volume_down": "AudioVolumeDown",
}

// androidRemote is an Android TV remote's buttons, by sim_remote's names.
var androidRemote = map[string]string{
	"up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight", "select": "Enter",
	"menu": "GoBack", "home": "GoHome", "play_pause": "MediaPlayPause", "volume_up": "AudioVolumeUp",
	"volume_down": "AudioVolumeDown", "mute": "AudioVolumeMute",
}

// usageKeys are the HID keyboard usages the Mac sends for keys without
// text, as the emulator's key names (Escape is Android's Back).
var usageKeys = map[int]string{40: "Enter", 41: "GoBack", 42: "Backspace", 43: "Tab", 76: "Delete", 79: "ArrowRight", 80: "ArrowLeft",
	81: "ArrowDown", 82: "ArrowUp"}

// emulatorTurns are the orientations sim_rotate names, in degrees.
var emulatorTurns = map[string]float32{"portrait": 0, "landscape-left": 90, "upside-down": 180, "landscape-right": 270}

// emulatorCall runs a simulator op on an emulator pane. Done reports whether
// the op is finished (else it's the host's: a screenshot).
func (d *Daemon) emulatorCall(s *emuSession, op string, args simArgs, size [2]float64) (reply any, done bool, err error) {
	if op == "screenshot" {
		return nil, false, nil // the host has its surface
	}
	if op == "takeover" {
		s.requestTakeover()
		return map[string]any{"ok": true}, true, nil
	}
	c, scale, err := s.conn()
	if err != nil {
		return nil, true, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	touch := func(phase string, x, y float64) error {
		pressure := 1
		if phase == "up" {
			pressure = 0
		}
		return c.Touches(ctx, emulator.Touch{ID: 1, X: px(x, scale), Y: px(y, scale), Pressure: pressure})
	}
	key := func(name string, phase emulator.KeyPhase) error { return c.Key(ctx, name, phase) }
	switch op {
	case "touch":
		return nil, true, touch(args.Phase, args.X, args.Y)
	case "tap":
		if err := touch("down", args.X, args.Y); err != nil {
			return nil, true, err
		}
		time.Sleep(60 * time.Millisecond)
		err = touch("up", args.X, args.Y)
	case "swipe":
		ms := cmp.Or(args.Ms, 300)
		steps := max(ms/16, 4)
		if err := touch("down", args.X, args.Y); err != nil {
			return nil, true, err
		}
		for i := 1; i <= steps; i++ {
			f := float64(i) / float64(steps)
			time.Sleep(time.Duration(ms/steps) * time.Millisecond)
			_ = touch("move", args.X+(args.X2-args.X)*f, args.Y+(args.Y2-args.Y)*f)
		}
		err = touch("up", args.X2, args.Y2)
	case "pinch", "gesture":
		paths := args.Paths
		if op == "pinch" {
			if paths = pinchPaths(args.X, args.Y, args.Scale, args.Ms); paths == nil {
				return nil, true, errors.New("pinch: scale must be above 0 and not 1 (above 1 zooms in)")
			}
		}
		err = playFingers(ctx, c, paths, scale)
	case "text":
		err = c.Text(ctx, args.Text)
	case "key":
		name, ok := usageKeys[args.Usage]
		if !ok {
			return nil, true, fmt.Errorf("key: usage %d isn't one an emulator takes here", args.Usage)
		}
		err = key(name, emulator.KeyPress)
	case "copy":
		text, err := c.Clipboard(ctx)
		return map[string]any{"text": text}, true, err
	case "paste":
		if err := c.SetClipboard(ctx, args.Text); err != nil {
			return nil, true, err
		}
		err = key("Paste", emulator.KeyPress)
	case "button":
		name, ok := androidButtons[args.Name]
		if !ok {
			return nil, true, fmt.Errorf("unknown button %q (back, home, recents, power, volume_up, volume_down)", args.Name)
		}
		err = key(name, emulator.KeyPress)
	case "remote":
		if args.Phase != "" {
			name, ok := androidRemote[args.Name]
			if !ok {
				return nil, true, fmt.Errorf("an Android TV remote has no %q button", args.Name)
			}
			return nil, true, key(name, map[string]emulator.KeyPhase{"down": emulator.KeyDown, "up": emulator.KeyUp}[args.Phase])
		}
		presses, perr := remotePresses(args)
		if perr != nil {
			return nil, true, perr
		}
		for i, p := range presses {
			name, ok := androidRemote[p]
			if !ok {
				return nil, true, fmt.Errorf("an Android TV remote has no %q button", p)
			}
			if i > 0 {
				time.Sleep(150 * time.Millisecond)
			}
			if err := key(name, emulator.KeyDown); err != nil {
				return nil, true, err
			}
			time.Sleep(time.Duration(cmp.Or(args.HoldMs, 60)) * time.Millisecond)
			if err := key(name, emulator.KeyUp); err != nil {
				return nil, true, err
			}
		}
	case "rotate":
		s.mu.Lock()
		turn := s.turn
		s.mu.Unlock()
		switch args.Value {
		case "left":
			turn = float32(math.Mod(float64(turn)+90, 360))
		case "right":
			turn = float32(math.Mod(float64(turn)+270, 360))
		default:
			t, ok := emulatorTurns[args.Value]
			if !ok {
				return nil, true, errors.New("orientation: portrait, upside-down, landscape-left, landscape-right, left or right")
			}
			turn = t
		}
		if err := c.Rotate(ctx, turn); err != nil {
			return nil, true, err
		}
		s.mu.Lock()
		s.turn = turn
		s.mu.Unlock()
		time.Sleep(600 * time.Millisecond) // it turns, then redraws
	case "fold":
		s.mu.Lock()
		foldable := s.info.Foldable
		s.mu.Unlock()
		if !foldable {
			return nil, true, errors.New("this emulator doesn't fold")
		}
		angle, ok := map[string]float64{"open": 180, "closed": 0, "close": 0, "half": 90}[args.Value]
		if args.Angle != nil {
			angle, ok = *args.Angle, true
		}
		if !ok || angle < 0 || angle > 180 {
			return nil, true, errors.New("fold: open, closed, half, or an angle from 0 (closed) to 180 (flat)")
		}
		if err := c.Hinge(ctx, float32(angle)); err != nil {
			return nil, true, err
		}
		s.mu.Lock()
		s.hinge = angle
		s.mu.Unlock()
		s.announce() // the pane's fold button follows it
		time.Sleep(time.Second)
	default:
		return nil, true, fmt.Errorf("an emulator can't %s", op)
	}
	if err != nil {
		return nil, true, err
	}
	time.Sleep(150 * time.Millisecond) // let it react before a screenshot follows
	return map[string]any{"ok": true}, true, nil
}

// playFingers plays fingers' paths (points, ms from the start) as touches.
func playFingers(ctx context.Context, c *emulator.Client, paths [][]struct{ X, Y, T float64 }, scale float64) error {
	if len(paths) == 0 {
		return errors.New("gesture: no paths")
	}
	// Every moment any finger has a sample, in order.
	var times []float64
	for _, p := range paths {
		for _, s := range p {
			times = append(times, s.T)
		}
	}
	slices.Sort(times)
	times = slices.Compact(times)
	at := func(p []struct{ X, Y, T float64 }, t float64) (float64, float64) {
		for i := 1; i < len(p); i++ {
			if t <= p[i].T {
				a, b := p[i-1], p[i]
				f := 0.0
				if b.T > a.T {
					f = (t - a.T) / (b.T - a.T)
				}
				return a.X + (b.X-a.X)*f, a.Y + (b.Y-a.Y)*f
			}
		}
		return p[len(p)-1].X, p[len(p)-1].Y
	}
	start := time.Now()
	for i, t := range times {
		time.Sleep(time.Until(start.Add(time.Duration(t * float64(time.Millisecond)))))
		var ts []emulator.Touch
		for f, p := range paths {
			if len(p) == 0 {
				continue
			}
			x, y := at(p, t)
			ts = append(ts, emulator.Touch{ID: f + 1, X: px(x, scale), Y: px(y, scale), Pressure: 1})
		}
		if i == len(times)-1 {
			for j := range ts {
				ts[j].Pressure = 0 // all lifted
			}
		}
		if err := c.Touches(ctx, ts...); err != nil {
			return err
		}
	}
	return nil
}

// emulatorDevices lists the AVDs, as the simulator host lists simulators.
func emulatorDevices() []map[string]any {
	running := map[string]bool{}
	for _, r := range emulator.ListRunning(emulator.DiscoveryDir()) {
		running[r.AVD] = true
	}
	var out []map[string]any
	for _, a := range emulator.ListAVDs(emulator.AVDDir()) {
		runtime := "Android"
		if api, ok := strings.CutPrefix(a.API, "android-"); ok {
			if n, err := strconv.Atoi(api); err == nil {
				runtime = fmt.Sprintf("Android API %d", n)
			}
		}
		out = append(out, map[string]any{"udid": emulator.Prefix + a.Name, "name": a.Display, "runtime": runtime,
			"booted": running[a.Name], "platform": a.Platform()})
	}
	return out
}

// emulatorPane is the session for a pane, if it shows an emulator.
func (d *Daemon) emulatorPane(p *pane) *emuSession {
	if !emulator.IsAVD(p.snapshotInfo().DeviceUdid) {
		return nil
	}
	return d.emulators.session(p.id)
}

// isEmulatorPane reports whether a pane spec or pane shows an AVD.
func isEmulatorDevice(udid string) bool { return emulator.IsAVD(udid) }

// emulatorAVD is the AVD a device id names.
func emulatorAVD(udid string) string { return emulator.AVDName(udid) }
