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
// Browser", helpers/browser): Chromium can't share its profiles between
// processes. Each Group has a profile of its own (cookies, logins, local
// storage, cache), so a page in one Group, or an agent driving it, never
// sees another Group's logins. The daemon owns which browsers exist, at what
// URL and in which Group; clients that can show them (the Mac app) talk to
// the host directly for pixels and input.
//
// The host outlives the daemon like pane holders do. On (re)connecting, the
// daemon recreates any browser the host doesn't have (after a reboot, at the
// stored URL), reopens any that's in another Group's profile, and closes any
// it no longer wants.

// BrowserSocketPath is the browser host's socket for a state dir.
func BrowserSocketPath(dir string) string { return filepath.Join(dir, "run", "browser-host.sock") }

// browserProfileRoot holds the browser host's profiles (Chromium's
// root_cache_path): each Group's in browserProfileDir.
func browserProfileRoot(dir string) string { return filepath.Join(dir, "browser-profile") }

// groupProfilePrefix starts each Group's profile's directory name: right in
// the root, since Chromium makes a profile nowhere else. The host names
// them the same way.
const groupProfilePrefix = "group-"

// browserProfileDir is a Group's profile (key: browserProfileKey).
func browserProfileDir(root, key string) string { return filepath.Join(root, groupProfilePrefix+key) }

// profilesMarker, in the root, says the old shared profile has been deleted.
const profilesMarker = ".group-profiles"

var profileKeyChars = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// browserProfileKey names a Group's browser profile: its id, when that's
// safe as a directory name (as newID makes them), else "", which the host
// keeps in memory only. The host checks it the same way.
func browserProfileKey(groupID string) string {
	if profileKeyChars.MatchString(groupID) {
		return groupID
	}
	return ""
}

// prepareBrowserProfiles readies the profiles before the host starts, while
// nothing has them open: the first time (no marker yet), it deletes the one
// profile every pane used to share (Default), so its logins don't linger on
// disk; every time, it deletes the profiles of Groups that are gone (one
// closed while the host wasn't running to do it). Anything else in root
// (Chromium's own Local State, and the Default it now keeps in memory) is
// left alone.
func prepareBrowserProfiles(root string, live map[string]bool) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	marker := filepath.Join(root, profilesMarker)
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		if err := os.RemoveAll(filepath.Join(root, "Default")); err != nil {
			return err
		}
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if key, ok := strings.CutPrefix(e.Name(), groupProfilePrefix); ok && e.IsDir() && !live[key] {
			errs = append(errs, os.RemoveAll(browserProfileDir(root, key)))
		}
	}
	return errors.Join(errs...)
}

// browserProfiles is the profile key of every Group here.
func (d *Daemon) browserProfiles() map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	live := map[string]bool{}
	for id := range d.reopenableGroups() {
		if k := browserProfileKey(id); k != "" {
			live[k] = true
		}
	}
	return live
}

// reopenableGroups is every Group here, and every closed one Reopen Closed
// can still bring back (an entry that remembers it). Callers hold d.mu.
func (d *Daemon) reopenableGroups() map[string]bool {
	ids := map[string]bool{}
	for id := range d.groups {
		ids[id] = true
	}
	for _, ct := range d.closedTabs {
		if ct.group != nil {
			ids[ct.groupID] = true
		}
	}
	return ids
}

// closedGroupProfile keeps a closed Group's browser profile (its logins)
// while Reopen Closed can bring the Group back, and drops it once it can't.
// Callers hold d.mu.
func (d *Daemon) closedGroupProfile(id string) {
	if d.closedProfs == nil {
		d.closedProfs = map[string]bool{}
	}
	d.closedProfs[id] = true
	d.dropGoneProfiles()
}

// dropGoneProfiles drops the browser profiles of closed Groups that are
// gone for good: their last entry fell off Reopen Closed (or was reopened
// elsewhere). One whose Group is back keeps it. Called whenever entries
// leave the list. Callers hold d.mu.
func (d *Daemon) dropGoneProfiles() {
	if len(d.closedProfs) == 0 {
		return
	}
	keep := d.reopenableGroups()
	for id := range d.closedProfs {
		if _, back := d.groups[id]; back {
			delete(d.closedProfs, id)
		} else if !keep[id] {
			delete(d.closedProfs, id)
			d.browsers.dropProfile(browserProfileKey(id))
		}
	}
}

// browserGroup is the profile key of the Group a browser pane is in.
// Callers hold d.mu.
func (d *Daemon) browserGroup(id string) string { return browserProfileKey(d.groupOfPane(id)) }

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
	h.media = &hostMedia{d: d, name: "browser", socket: func() string { return BrowserSocketPath(d.cfg.Dir) }, ensure: h.ensure, pages: true}
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

// create opens a browser in its Group's profile (group: browserGroup). For
// one the host has in another Group's profile, it reopens it in this one.
func (h *browserHub) create(id, url, group string) {
	h.ensure()
	h.send(map[string]any{"t": "create", "id": id, "url": h.hostURL(url), "group": group})
}

// dropProfile deletes a closed Group's browser profile, once the host has
// closed its browsers. With no host running, the next one's start does it
// (prepareBrowserProfiles).
func (h *browserHub) dropProfile(group string) {
	if group != "" {
		h.send(map[string]any{"t": "dropProfile", "group": group})
	}
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
	root := browserProfileRoot(dir)
	if err := prepareBrowserProfiles(root, h.d.browserProfiles()); err != nil {
		h.d.log.Error("browser profiles", "err", err)
	}
	// It talks only to hmkd and the Hammock app, by their code signatures
	// (helpers/common/peer.h).
	cmd := exec.Command(h.path, "--socket="+BrowserSocketPath(dir), "--profile="+root)
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
	T       string            `json:"t"`
	ID      string            `json:"id"`
	IDs     []string          `json:"ids"`
	Groups  map[string]string `json:"groups"` // hello: each browser's Group profile
	Built   int64             `json:"built"`
	Pid     int               `json:"pid"`
	URL     string            `json:"url"`
	Title   string            `json:"title"`
	Icon    string            `json:"icon"`
	Loading bool              `json:"loading"`
	Back    bool              `json:"back"`
	Fwd     bool              `json:"fwd"`
	Path    string            `json:"path"`
	Done    bool              `json:"done"`
	Failed  bool              `json:"failed"`
	Page    bool              `json:"page"` // popup: the page opened it (target=_blank, window.open), not the person's Open Link in New Tab
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
			h.sync(m.IDs, m.Groups)
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

// sync makes the host's browsers match the daemon's browser panes, each in
// its Group's profile (groups: the host's, by browser).
func (h *browserHub) sync(have []string, groups map[string]string) {
	d := h.d
	type browser struct{ url, group string }
	d.mu.Lock()
	want := map[string]browser{}
	for id, p := range d.panes {
		if info := p.snapshotInfo(); info.Kind == pb.PaneKind_PANE_KIND_BROWSER {
			want[id] = browser{info.Url, d.browserGroup(id)}
		}
	}
	d.mu.Unlock()
	for id, b := range want {
		if !slices.Contains(have, id) || groups[id] != b.group {
			h.create(id, b.url, b.group)
		}
	}
	for _, id := range have {
		if _, ok := want[id]; !ok {
			h.send(map[string]any{"t": "close", "id": id})
		}
	}
}

// regroupBrowsers reopens the browser panes among ids in the profile of the
// Group they're now in (their tab moved to another Group, or was merged into
// one of its tabs). Chromium can't move a live page between profiles, so the
// page loads again at its address, without what was on it (a form, where it
// was scrolled to). Callers hold d.mu.
func (d *Daemon) regroupBrowsers(ids []string) {
	for _, id := range ids {
		if p := d.panes[id]; p != nil && p.isBrowser() {
			d.browsers.create(id, p.snapshotInfo().Url, d.browserGroup(id))
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
	d.browsers.create(info.Id, u, browserProfileKey(t.GroupId))
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
