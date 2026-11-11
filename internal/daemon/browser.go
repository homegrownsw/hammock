package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Browser panes run in one browser host process per daemon ("Hammock
// Browser", helpers/browser): Chromium can't share a profile between
// processes, and one profile means one set of logins for every pane. The
// daemon owns which browsers exist and at what URL; clients that can show
// them (the Mac app) talk to the host directly for pixels and input.
//
// The host outlives the daemon like pane holders do. On (re)connecting, the
// daemon recreates any browser the host doesn't have (after a reboot, at the
// stored URL) and closes any it no longer wants.

// BrowserSocketPath is the browser host's socket for a state dir.
func BrowserSocketPath(dir string) string { return filepath.Join(dir, "run", "browser-host.sock") }

type browserHub struct {
	d     *Daemon
	path  string // the host executable
	start sync.Once
	kick  chan struct{}

	mu     sync.Mutex
	c      net.Conn
	seq    int
	calls  map[int]chan cdpReply // DevTools calls in flight, by seq
	logs   map[string]*pageLog   // console and network activity, by pane
	inputs map[string]*conn      // who last gave a pane remote input (gets its menus)
	menus  map[int]*remoteMenu   // menus shown to remote viewers, by the host's seq

	media    *hostMedia     // video (hostmedia.go)
	devtools devtoolsBridge // DevTools panes' frontends (devtools.go)
}

func newBrowserHub(d *Daemon) *browserHub {
	h := &browserHub{d: d, path: browserHostPath(), kick: make(chan struct{}, 1)}
	h.devtools.h = h
	h.media = &hostMedia{d: d, name: "browser", socket: func() string { return BrowserSocketPath(d.cfg.Dir) }, ensure: h.ensure}
	return h
}

// browserHostPath finds the host: $HMK_BROWSER_HOST, else the helper
// bundled in the app (Contents/Helpers), else one next to hmkd (bin/).
func browserHostPath() string {
	if p := helperEnv("HMK_BROWSER_HOST"); p != "" { // (vetted: helperpath.go)
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	const app = "Hammock Browser.app/Contents/MacOS/Hammock Browser"
	for _, p := range []string{filepath.Join(filepath.Dir(exe), "..", "Helpers", app), filepath.Join(filepath.Dir(exe), app)} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// ensure starts the connection loop and wakes it.
func (h *browserHub) ensure() {
	h.start.Do(func() { go h.run() })
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

func (h *browserHub) send(m map[string]any) {
	b, _ := json.Marshal(m)
	b = append(b, '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.c == nil {
		return // sent again on connect
	}
	_ = h.c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := h.c.Write(b); err != nil {
		h.c.Close()
	}
}

func (h *browserHub) create(id, url string) {
	h.ensure()
	h.send(map[string]any{"t": "create", "id": id, "url": h.hostURL(url)})
}

func (h *browserHub) close(id string) {
	h.forget(id)
	h.send(map[string]any{"t": "close", "id": id})
	go h.d.closeOrphanDevTools() // after whoever's closing it has let go
}

func (h *browserHub) navigate(id, action, url string) {
	h.ensure()
	if action == "load" {
		h.send(map[string]any{"t": "nav", "id": id, "url": url})
		return
	}
	h.send(map[string]any{"t": action, "id": id})
}

func (h *browserHub) run() {
	log := h.d.log.With("component", "browser")
	for {
		if !h.d.hasBrowsers() {
			<-h.kick
			continue
		}
		if h.path == "" {
			log.Warn("no browser host found; browser panes can't run")
			<-h.kick
			continue
		}
		c, err := h.dial()
		if err != nil {
			if err = h.spawn(); err == nil {
				for i := 0; i < 100 && c == nil; i++ {
					time.Sleep(100 * time.Millisecond)
					c, err = h.dial()
				}
			}
		}
		if c == nil {
			log.Error("browser host unavailable", "err", err)
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
		h.mu.Unlock()
		h.failCalls()
		c.Close()
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *browserHub) dial() (net.Conn, error) {
	return net.DialTimeout("unix", BrowserSocketPath(h.d.cfg.Dir), time.Second)
}

func (h *browserHub) spawn() error {
	dir := h.d.cfg.Dir
	logf, err := os.OpenFile(filepath.Join(dir, "logs", "browser-host.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	// It talks only to hmkd and the Hammock app, by their code signatures
	// (helpers/common/peer.h).
	cmd := exec.Command(h.path, "--socket="+BrowserSocketPath(dir), "--profile="+filepath.Join(dir, "browser-profile"))
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = baseEnv()
	// Its own session, so it outlives the daemon (as pane holders do).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	h.d.log.Info("started browser host", "pid", cmd.Process.Pid)
	go cmd.Wait()
	return nil
}

type hostMsg struct {
	T       string   `json:"t"`
	ID      string   `json:"id"`
	IDs     []string `json:"ids"`
	Built   int64    `json:"built"`
	Pid     int      `json:"pid"`
	URL     string   `json:"url"`
	Title   string   `json:"title"`
	Icon    string   `json:"icon"`
	Loading bool     `json:"loading"`
	Back    bool     `json:"back"`
	Fwd     bool     `json:"fwd"`
	Path    string   `json:"path"`
	Done    bool     `json:"done"`
	Failed  bool     `json:"failed"`
	Page    bool     `json:"page"` // popup: the page opened it (target=_blank, window.open), not the person's Open Link in New Tab
	// DevTools
	Seq    int             `json:"seq"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Msg    string          `json:"msg"` // a DevTools message for a DevTools pane's frontend (dtOut)
	// Menus (menu, context)
	Items []hostMenuItem `json:"items"`
	Index int            `json:"index"`
	Kind  string         `json:"kind"`
	Text  string         `json:"text"`
	Fx    float64        `json:"fx"`
	Fy    float64        `json:"fy"`
	Fw    float64        `json:"fw"`
	Fh    float64        `json:"fh"`
}

func (h *browserHub) read(c net.Conn) {
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m hostMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.T {
		case "hello":
			// A host from an older build (the app was updated): replace it.
			if st, err := os.Stat(h.path); err == nil && m.Built != 0 && st.ModTime().Unix() != m.Built {
				h.d.log.Info("replacing outdated browser host", "pid", m.Pid)
				h.send(map[string]any{"t": "quit"})
				// Older hosts could miss a quit; don't wait on one forever.
				go func(pid int) {
					time.Sleep(5 * time.Second)
					if pid > 0 && syscall.Kill(pid, 0) == nil {
						_ = syscall.Kill(pid, syscall.SIGTERM)
					}
				}(m.Pid)
				continue
			}
			h.send(map[string]any{"t": "cdpEvents"})
			h.sync(m.IDs)
		case "cdpResult":
			h.reply(m)
		case "cdpEvent":
			h.event(m)
		case "dtOut":
			h.devtools.deliver(m.ID, m.Msg)
		case "menu", "context":
			h.menu(m)
		case "clipboard":
			h.clipboard(m)
		case "state":
			h.d.browserState(m)
		case "popup":
			h.d.browserPopup(m.ID, m.URL, m.Page)
		case "download":
			h.d.log.Info("download", "pane", m.ID, "path", m.Path, "done", m.Done, "failed", m.Failed)
		}
	}
}

// sync makes the host's browsers match the daemon's browser panes.
func (h *browserHub) sync(have []string) {
	d := h.d
	d.mu.Lock()
	want := map[string]string{}
	for id, p := range d.panes {
		if info := p.snapshotInfo(); info.Kind == pb.PaneKind_PANE_KIND_BROWSER {
			want[id] = info.Url
		}
	}
	d.mu.Unlock()
	for id, u := range want {
		if !slices.Contains(have, id) {
			h.send(map[string]any{"t": "create", "id": id, "url": h.hostURL(u)})
		}
	}
	for _, id := range have {
		if _, ok := want[id]; !ok {
			h.send(map[string]any{"t": "close", "id": id})
		}
	}
}

// hasBrowser says whether id is a browser pane.
func (d *Daemon) hasBrowser(id string) bool {
	d.mu.Lock()
	p := d.panes[id]
	d.mu.Unlock()
	return p != nil && p.isBrowser()
}

func (d *Daemon) hasBrowsers() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.panes {
		if p.isBrowser() {
			return true
		}
	}
	return false
}

func (p *pane) isBrowser() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Kind == pb.PaneKind_PANE_KIND_BROWSER
}

// DevToolsURL is a browser pane showing another's DevTools: the browser
// host opens Chromium's DevTools for that page in it.
const DevToolsURL = "hmk-devtools:"

// startBrowser creates a browser pane for tab t (not yet placed in its layout).
// Callers hold d.mu.
func (d *Daemon) startBrowser(t *pb.Tab, spec *pb.PaneSpec) (*pane, error) {
	u := BrowserURL(spec.Url)
	if target, ok := strings.CutPrefix(u, DevToolsURL); ok {
		// Only for a page in the same Group: DevTools reaches all of it.
		// (Who may open it is agentCheck's: as for controlling the page.)
		tp := d.panes[target]
		if tp == nil || !tp.isBrowser() || d.groupOfPane(target) != t.GroupId {
			return nil, errors.New("DevTools opens for a browser pane in the same Group")
		}
		if d.devtoolsPage(target) != "" {
			return nil, errors.New("DevTools opens for a web page, not for another DevTools")
		}
	}
	info := &pb.Pane{
		Id: newID("pn"), TabId: t.Id, Kind: pb.PaneKind_PANE_KIND_BROWSER,
		Url: u, Title: u, Loading: true, CreatedByPaneId: spec.CreatedByPaneId,
	}
	p := newPane(d, info)
	d.panes[info.Id] = p
	d.persist(d.store.PutPane(info))
	d.browsers.create(info.Id, u)
	return p, nil
}

// browserState records a page's URL, title and loading state.
func (d *Daemon) browserState(m hostMsg) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.panes[m.ID]
	if p == nil {
		return
	}
	p.mu.Lock()
	info := p.info
	if info.Kind != pb.PaneKind_PANE_KIND_BROWSER {
		p.mu.Unlock()
		return
	}
	before := proto.CloneOf(info)
	// A DevTools pane keeps its own address (its page's DevTools), not the
	// frontend's, so it comes back as one. Any other pane never takes a
	// DevTools address from its page (one it went to, an error page for
	// it): it would come back as DevTools for that page, opened by whoever
	// drove the page there.
	devtools := strings.HasPrefix(info.Url, DevToolsURL)
	if m.URL != "" && !devtools && !isDevToolsAddress(m.URL) {
		info.Url = m.URL
	}
	info.Title = m.Title
	if devtools {
		info.Title = devtoolsTitle(m.Title) // never the frontend's address
	} else if info.Title == "" || isDevToolsAddress(info.Title) {
		info.Title = info.Url
	}
	info.Loading, info.CanGoBack, info.CanGoForward, info.FaviconUrl = m.Loading, m.Back, m.Fwd, m.Icon
	changed := !proto.Equal(before, info)
	p.mu.Unlock()
	if changed {
		d.paneChangedIfLive(p)
	}
}

// browserPopup opens a link the page wanted in a new window (target=_blank,
// window.open, "Open Link in New Tab") as a browser tab after the page's own,
// or, for a new-tab link the person clicked on this Mac (page), in their own
// browser unless browser-new-tab = hammock.
func (d *Daemon) browserPopup(fromID, u string, page bool) {
	if isDevToolsAddress(u) {
		// DevTools opens from Show DevTools (or hmk devtools), never from a
		// page's link: a page (or an agent driving it) could open DevTools
		// for another page in its Group.
		d.log.Warn("ignored a page's DevTools link", "pane", fromID)
		return
	}
	if page && u != "" && openNewTabsOutside(u) && d.hasBrowser(fromID) {
		// open(1) takes a moment: not on the host's reader.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := exec.CommandContext(ctx, "open", u).Run(); err != nil {
				d.log.Error("open popup in browser", "err", err)
				d.browserPopup(fromID, u, false) // a tab, rather than nothing
			}
		}()
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.panes[fromID]
	if p == nil || u == "" {
		return
	}
	t := d.tabs[p.tabID()]
	if t == nil {
		return
	}
	nt, err := d.createTab(t.GroupId, "", &pb.PaneSpec{Url: u, CreatedByPaneId: fromID}, "", "")
	if err != nil {
		d.log.Error("open popup", "err", err)
		return
	}
	if s := d.groups[t.GroupId]; s != nil {
		_ = d.moveTab(nt.Id, s.Id, slices.Index(s.TabIds, t.Id)+1)
	}
}

// openNewTabsOutside says whether a link a page opens in a new tab goes to
// the person's own browser (browser-new-tab = external, the default) rather
// than a Hammock browser tab. Only web links: anything else (about:, data:,
// blob:, the host's own pages) has no meaning outside this browser.
func openNewTabsOutside(u string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if v, _ := hammockconfig.Value("browser-new-tab"); strings.TrimSpace(v) == "hammock" {
		return false
	}
	pu, err := url.Parse(u)
	return err == nil && (pu.Scheme == "http" || pu.Scheme == "https") && pu.Host != ""
}

var (
	hasScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
	hostPort  = regexp.MustCompile(`^[a-zA-Z0-9.-]+:[0-9]+(/.*)?$`)
)

// BrowserURL turns what someone typed into a URL: as-is with a scheme,
// file:// for a path, http:// for local hosts, https:// for anything else
// that looks like a host, and otherwise a web search.
func BrowserURL(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "about:blank"
	case hasScheme.MatchString(s):
		return s
	case strings.HasPrefix(s, "about:"), strings.HasPrefix(s, "data:"), strings.HasPrefix(s, "mailto:"), strings.HasPrefix(s, DevToolsURL):
		return s
	case strings.HasPrefix(s, "/"), strings.HasPrefix(s, "~/"):
		if strings.HasPrefix(s, "~/") {
			home, _ := os.UserHomeDir()
			s = filepath.Join(home, s[2:])
		}
		return (&url.URL{Scheme: "file", Path: s}).String()
	case strings.ContainsAny(s, " \t"):
	case hostPort.MatchString(s), s == "localhost", strings.HasPrefix(s, "localhost/"),
		strings.HasPrefix(s, "127.0.0.1"), strings.HasSuffix(strings.SplitN(s, "/", 2)[0], ".local"),
		strings.HasSuffix(strings.SplitN(s, "/", 2)[0], ".test"):
		return "http://" + s
	case strings.Contains(strings.SplitN(s, "/", 2)[0], "."):
		return "https://" + s
	}
	return "https://www.google.com/search?q=" + url.QueryEscape(s)
}
