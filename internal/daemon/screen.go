package daemon

// Screen sharing (ADR-0014): your paired phones and linked computers see
// and control this Mac's displays. One screen host per daemon ("Hammock
// Screen", helpers/screen) captures a display while someone watches it and
// posts their input; video goes through the media hub like a pane's
// (media.go), as session "screen:<display id>". Never guests or agents:
// checkGuest refuses it outright, agentCheck has no case for it, and
// OpenMedia of a screen is checked again here.

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// ScreenSocketPath is the screen host's socket for a state dir.
func ScreenSocketPath(dir string) string { return filepath.Join(dir, "run", "screen-host.sock") }

// screenSharing: Settings ▸ Devices ▸ Screen Sharing (screen-sharing in
// ~/.config/hammock/config), off unless turned on.
func screenSharing() bool { return hammockconfig.Bool("screen-sharing", false) }

// screenPaneID is the media hub's name for a display.
func screenPaneID(display uint32) string { return "screen:" + strconv.FormatUint(uint64(display), 10) }

type screenHub struct {
	d     *Daemon
	path  string
	start sync.Once
	kick  chan struct{}

	mu    sync.Mutex
	c     net.Conn
	seq   int
	calls map[int]chan screenMsg

	media *hostMedia

	// secret proves to the hosts this daemon starts that it's the one that
	// started them (screenauth.go); one per daemon run, never on disk.
	secret []byte

	shapes map[uint32]*pb.ScreenCursor // pointer shapes the host has sent (image, hot spot, size), by id
}

type screenMsg struct {
	T       string `json:"t"`
	Seq     int    `json:"seq"`
	Pid     int    `json:"pid"`
	Refused bool   `json:"refused"` // a hello from a host that won't take this daemon
	// hello: what to answer (screenauth.go) before the host serves anything.
	Challenge []byte `json:"challenge"` // base64 in the JSON
	Built     int64  `json:"built"`
	Screen    bool   `json:"screen"` // Screen Recording allowed
	Input     bool   `json:"input"`  // Accessibility allowed
	Error     string `json:"error"`
	// cursor and cursorShape (the pointer: where, and its shapes).
	Display  uint32  `json:"display"`
	IsMain   bool    `json:"main"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	Visible  bool    `json:"visible"`
	Locked   bool    `json:"locked"`
	JPEG     []byte  `json:"jpeg"` // snapshotResult (base64 in the JSON)
	Taken    int64   `json:"taken"`
	Asleep   bool    `json:"asleep"`
	Shape    uint32  `json:"shape"`
	ID       uint32  `json:"id"`
	PNG      []byte  `json:"png"` // base64 in the JSON
	HotX     float32 `json:"hotX"`
	HotY     float32 `json:"hotY"`
	W        float32 `json:"w"`
	H        float32 `json:"h"`
	Displays []struct {
		ID    uint32  `json:"id"`
		Name  string  `json:"name"`
		W     uint32  `json:"w"`
		H     uint32  `json:"h"`
		Scale float64 `json:"scale"`
		Main  bool    `json:"main"`
	} `json:"displays"`
}

func newScreenHub(d *Daemon) *screenHub {
	h := &screenHub{d: d, path: screenHostPath(), kick: make(chan struct{}, 1), calls: map[int]chan screenMsg{}, shapes: map[uint32]*pb.ScreenCursor{}}
	h.secret = make([]byte, 32)
	_, _ = rand.Read(h.secret)
	h.media = &hostMedia{d: d, name: "screen", socket: func() string { return ScreenSocketPath(d.cfg.Dir) }, ensure: h.ensure,
		greet: func(c net.Conn, r *bufio.Reader) error { _, err := h.greet(c, r); return err }}
	return h
}

// screenHostPath finds the host: $HMK_SCREEN_HOST, else the helper bundled
// in the app (Contents/Helpers), else one next to hmkd (bin/).
func screenHostPath() string {
	if p := helperEnv("HMK_SCREEN_HOST"); p != "" { // (vetted: helperpath.go)
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	const app = "Hammock Screen.app/Contents/MacOS/Hammock Screen"
	for _, p := range []string{filepath.Join(filepath.Dir(exe), "..", "Helpers", app), filepath.Join(filepath.Dir(exe), app)} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func (h *screenHub) ensure() {
	h.start.Do(func() { go h.run() })
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

func (h *screenHub) send(m map[string]any) error {
	b, _ := json.Marshal(m)
	b = append(b, '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.c == nil {
		return errors.New("the screen sharing helper isn't running")
	}
	_ = h.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := h.c.Write(b); err != nil {
		h.c.Close()
		return err
	}
	return nil
}

// call sends a request and waits for its answer.
func (h *screenHub) call(m map[string]any) (screenMsg, error) {
	h.ensure()
	// The first call may come before the host is up (it's started, or an
	// outdated one replaced, on demand: about a second).
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		h.mu.Lock()
		up := h.c != nil
		h.mu.Unlock()
		if up {
			break
		}
	}
	h.mu.Lock()
	h.seq++
	seq := h.seq
	ch := make(chan screenMsg, 1)
	h.calls[seq] = ch
	h.mu.Unlock()
	m["seq"] = seq
	if err := h.send(m); err != nil {
		h.mu.Lock()
		delete(h.calls, seq)
		h.mu.Unlock()
		return screenMsg{}, err
	}
	select {
	case r := <-ch:
		if r.Error != "" {
			return r, errors.New(r.Error)
		}
		return r, nil
	case <-time.After(5 * time.Second):
		h.mu.Lock()
		delete(h.calls, seq)
		h.mu.Unlock()
		return screenMsg{}, errors.New("the screen sharing helper didn't answer")
	}
}

func (h *screenHub) run() {
	log := h.d.log.With("component", "screen")
	for {
		if h.path == "" {
			log.Warn("no screen host found; screen sharing can't run")
			<-h.kick
			continue
		}
		c, err := net.DialTimeout("unix", ScreenSocketPath(h.d.cfg.Dir), time.Second)
		if err != nil {
			if err = h.spawn(); err == nil {
				for i := 0; i < 50 && c == nil; i++ {
					time.Sleep(100 * time.Millisecond)
					c, err = net.DialTimeout("unix", ScreenSocketPath(h.d.cfg.Dir), time.Second)
				}
			}
		}
		if c == nil {
			log.Error("screen host unavailable", "err", err)
			select {
			case <-h.kick:
			case <-time.After(10 * time.Second):
			}
			continue
		}
		// Ready once it greets us as this build's host: an older one is
		// told to quit first, so no call goes to a host on its way out.
		r := bufio.NewReaderSize(c, 64*1024)
		if !h.current(c, r) {
			c.Close()
			time.Sleep(200 * time.Millisecond)
			continue
		}
		h.mu.Lock()
		h.c = c
		h.mu.Unlock()
		log.Info("screen host ready")
		h.read(r)
		h.mu.Lock()
		h.c = nil
		for seq, ch := range h.calls {
			ch <- screenMsg{Error: "the screen sharing helper stopped"}
			delete(h.calls, seq)
		}
		h.mu.Unlock()
		c.Close()
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *screenHub) spawn() error {
	dir := h.d.cfg.Dir
	logf, err := os.OpenFile(filepath.Join(dir, "logs", "screen-host.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	// Through Launch Services (open), not as our child: macOS then asks for
	// and remembers Screen Recording and Accessibility for Hammock Screen
	// itself, not for whatever started the daemon.
	app := strings.TrimSuffix(h.path, "/Contents/MacOS/Hammock Screen")
	// It serves only this daemon, which it learns over Mach (the rendezvous
	// named here) and checks on every connection (screenauth.go), never by
	// what its command line says: nothing else of yours gets its
	// permissions.
	name, err := h.rendezvousFor(ScreenSocketPath(dir))
	if err != nil {
		return err
	}
	cmd := exec.Command("/usr/bin/open", "-g", "-j", "-n", "-a", app, "--args", "--socket="+ScreenSocketPath(dir), "--rendezvous="+name, "--log="+logf.Name())
	if !strings.HasSuffix(app, ".app") {
		cmd = exec.Command(h.path, "--socket="+ScreenSocketPath(dir), "--rendezvous="+name)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	cmd.Env = baseEnv()
	if err := cmd.Start(); err != nil {
		return err
	}
	h.d.log.Info("started screen host", "path", h.path)
	go cmd.Wait()
	return nil
}

// current greets the host (screenauth.go): whether it's this daemon's and
// this build's. One that won't take this daemon (another daemon started
// it, or another build) is stopped, and an outdated one told to quit; run
// then starts this build's.
func (h *screenHub) current(c net.Conn, r *bufio.Reader) bool {
	m, err := h.greet(c, r)
	var refused errScreenHostRefused
	if errors.As(err, &refused) {
		h.d.log.Info("replacing a screen host that refused this daemon", "pid", refused.pid)
		if refused.pid > 0 {
			_ = syscall.Kill(refused.pid, syscall.SIGTERM)
		}
		time.Sleep(300 * time.Millisecond)
		return false
	}
	if err != nil {
		return false
	}
	if st, err := os.Stat(h.path); err == nil && m.Built != 0 && st.ModTime().Unix() != m.Built {
		h.d.log.Info("replacing outdated screen host", "pid", m.Pid)
		_ = c.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = c.Write([]byte(`{"t":"quit"}` + "\n"))
		return false
	}
	return true
}

func (h *screenHub) read(r *bufio.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m screenMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.T {
		case "cursorShape":
			h.mu.Lock()
			h.shapes[m.ID] = &pb.ScreenCursor{ShapeId: m.ID, Png: m.PNG, HotX: m.HotX, HotY: m.HotY, Width: m.W, Height: m.H}
			h.mu.Unlock()
		case "screenState":
			h.d.media.screenState(m.Locked)
		case "cursor":
			h.mu.Lock()
			shape := h.shapes[m.Shape]
			h.mu.Unlock()
			h.d.media.screenCursor(m, shape)
		case "displaysResult", "requestResult", "snapshotResult":
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

// screenAllowed says whether c may share this computer's screen: your
// paired phones and linked computers (and this Mac's own apps), with Screen
// Sharing on. Never a guest, never an agent.
func (d *Daemon) screenAllowed(c *conn) error {
	d.mu.Lock()
	peer, agent := c.peer, c.isAgent() || c.caller.isAgent()
	linked := peer != "" && d.isLinked(peer)
	d.mu.Unlock()
	if agent {
		return errAgentScope
	}
	if peer != "" && !linked {
		return errors.New("only your own devices can share this Mac's screen")
	}
	if !screenSharing() {
		return errors.New("Screen Sharing is off on this Mac (Settings ▸ Devices)")
	}
	return nil
}

// screenSnapshot answers ScreenSnapshot: a display, small, for one who may
// watch it.
func (d *Daemon) screenSnapshot(c *conn, m *pb.ScreenSnapshot) (*pb.ServerMessage, error) {
	if err := d.screenAllowed(c); err != nil {
		return nil, err
	}
	r, err := d.screens.call(map[string]any{"t": "snapshot", "display": m.DisplayId, "maxWidth": min(max(m.MaxWidth, 160), 2048)})
	if err != nil {
		return nil, err
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_ScreenSnapshotReply{ScreenSnapshotReply: &pb.ScreenSnapshotReply{
		Jpeg: r.JPEG, Width: uint32(r.W), Height: uint32(r.H), TakenUnixMs: r.Taken, Asleep: r.Asleep,
	}}}, nil
}

// listDisplays answers ListDisplays: what can be shared, and why not.
func (d *Daemon) listDisplays(c *conn) (*pb.ServerMessage, error) {
	out := &pb.Displays{Enabled: screenSharing()}
	reply := &pb.ServerMessage{Msg: &pb.ServerMessage_Displays{Displays: out}}
	if err := d.screenAllowed(c); err != nil {
		if !out.Enabled {
			out.Unavailable = err.Error()
			return reply, nil
		}
		return nil, err
	}
	start := time.Now()
	r, err := d.screens.call(map[string]any{"t": "displays"})
	if took := time.Since(start); took > 500*time.Millisecond || err != nil {
		d.log.Info("listed displays", "took_ms", took.Milliseconds(), "err", err)
	}
	if err != nil {
		out.Unavailable = err.Error()
		return reply, nil
	}
	out.ScreenRecording, out.Accessibility = r.Screen, r.Input
	for _, x := range r.Displays {
		out.Displays = append(out.Displays, &pb.Display{Id: x.ID, Name: x.Name, Width: x.W, Height: x.H, Scale: x.Scale, Main: x.Main})
	}
	switch {
	case !r.Screen:
		out.Unavailable = "Hammock Screen Sharing needs Screen Recording (Settings ▸ Devices ▸ Screen Sharing)"
	case !r.Input:
		out.Unavailable = "Hammock Screen Sharing needs Accessibility to take clicks and typing (Settings ▸ Devices ▸ Screen Sharing)"
	}
	return reply, nil
}

// screenInput posts a viewer's input: only from a connection watching that
// display (so nobody controls a screen they can't see).
func (d *Daemon) screenInput(c *conn, m *pb.ScreenInput) error {
	if err := d.screenAllowed(c); err != nil {
		return err
	}
	display := m.DisplayId
	watching := d.media.matching(func(s *mediaSession) bool {
		return s.owner == c && strings.HasPrefix(s.paneID, "screen:") && (display == 0 || s.paneID == screenPaneID(display))
	})
	if len(watching) == 0 {
		return errors.New("open the screen before sending it input")
	}
	if display == 0 {
		display = screenDisplayOf(watching[0].paneID) // still 0 for the main display: the host knows which
	}
	for _, s := range watching {
		s.mu.Lock()
		s.inputAt = time.Now()
		s.mu.Unlock()
	}
	var args map[string]any
	if m.ArgsJson != "" {
		if err := json.Unmarshal([]byte(m.ArgsJson), &args); err != nil {
			return fmt.Errorf("bad input: %w", err)
		}
	}
	return d.screens.send(map[string]any{"t": "input", "display": display, "op": m.Op, "args": args})
}

func screenDisplayOf(paneID string) uint32 {
	n, _ := strconv.ParseUint(strings.TrimPrefix(paneID, "screen:"), 10, 32)
	return uint32(n)
}

// requestScreenAccess asks macOS for Hammock Screen's permissions (Settings,
// on this Mac): what — "screen" or "input".
func (d *Daemon) requestScreenAccess(what string) (screen, input bool, err error) {
	r, err := d.screens.call(map[string]any{"t": "request", "what": what})
	return r.Screen, r.Input, err
}
