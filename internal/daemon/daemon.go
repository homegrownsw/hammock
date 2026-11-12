// Package daemon implements hmkd: the source of truth for Groups,
// Tabs and Panes, which supervises pane-holder processes, mirrors terminal
// state and serves clients over the v1 protocol.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/appbundle"
	"github.com/homegrownsw/hammock/internal/holder"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/state"
	"github.com/homegrownsw/hammock/internal/vt"
)

// ProductVersion is Hammock's release version, as programs in panes see
// it (TERM_PROGRAM_VERSION).
const ProductVersion = "0.1.0"

// Version is the daemon version reported to clients.
var Version = "0.1.0-dev"

// ProtocolVersion is the wire protocol version.
const ProtocolVersion = 1

// Config configures the daemon.
type Config struct {
	Dir        string // state directory (database, sockets)
	HolderPath string // pane-holder binary
	Log        *slog.Logger
	// Remote enables the Tailscale listener at startup (also persisted via
	// SetRemoteAccess). RemoteListen overrides the listen addresses (tests).
	Remote       bool
	RemoteListen []string
}

// Daemon is a running hmkd.
type Daemon struct {
	cfg   Config
	log   *slog.Logger
	store *state.Store
	// lock: exclusive flocks on run/daemon.lock (one daemon per dir) and the
	// state dir itself, which its holders require of the daemon attaching
	// (holder/statelock.go).
	lock  *dirLock
	icons groupIcons // Groups' pictures, from their folders (groupicons.go)
	// proofs: the Mac app's proofs that a connection is its own (appproof.go;
	// its own lock).
	proofs appProofs
	ln     net.Listener
	srv    *http.Server

	mu        sync.Mutex          // guards everything below; never acquired while holding a pane's mu
	phoneAt   time.Time           // you last did something in Hammock on a paired phone (push.go)
	pushSent  map[string][]string // what phones were notified of, by id: told when it's resolved (push.go)
	groups    map[string]*pb.Group
	tabs      map[string]*pb.Tab
	panes     map[string]*pane
	attention map[string]*pb.AttentionItem
	// focusOrder is each tab's panes in the order they were last focused
	// (most recent last), so closing the focused one hands focus back to
	// the one before it rather than the first in the layout. In memory only.
	focusOrder   map[string][]string
	closedTabs   []*closedTab    // Reopen Closed Tab (reopen.go)
	usage        *pb.UsageLimits // agents' usage limits (usage.go)
	theme        *pb.HostTheme   // this Mac's theme, for the iPhone (theme.go)
	cfgw         configWatch     // security settings in the config file (configwatch.go; its own lock)
	agentKinds   []*pb.AgentKind // the agents known, and installed here (agentkinds.go)
	version      uint64
	conns        map[*conn]struct{}
	agentHub     *agentHub
	sshHub       *sshHub        // panes' SSH agent (sshagent.go)
	hammockKey   *hammockKeyHub // the Hammock Key (hammockkey.go)
	browsers     *browserHub
	simulators   *simHub
	editors      *editorHub
	diffs        *diffHub
	screens      *screenHub
	started      time.Time // when this daemon started (the menu bar's uptime)
	media        *mediaHub
	sessions     *sessionFinder
	agentWatches map[string]*agentWatch // pane id → agent view watch
	shutdownOnce sync.Once
	done         chan struct{}
	stopped      bool
	endPanes     bool // set before Shutdown: end the pane holders too
	remote       remoteState
	bonjour      bonjourAds // this computer's Bonjour advertisements (bonjour.go)
	grants       map[string]*pendingGrant
	remembered   map[string]bool           // grantKey → allowed for the Group
	onceGrants   map[string]time.Time      // requester|action|target → an allowed-once grant, until (caller.go)
	agentMade    map[string]bool           // panes an agent made (caller.go)
	colors       atomic.Pointer[vt.Colors] // the user's terminal colours (see setColors)

	// Multiplayer (share.go: hosting; peer.go: joining others' Groups).
	id          identity
	people      map[string]*pb.Person   // user id → person, for members and requesters
	strangers   map[string]*pb.Person   // signed-in peers that are neither (yet), kept off disk
	letIn       map[string]bool         // user ids (keys) ever let into a Group here
	joins       map[string]*pendingJoin // join request id → the guest waiting on it
	typing      map[string]*time.Timer  // user id → clears their typing glow
	links       map[string]*peerLink    // host cert fingerprint → our link to that host
	remoteOrder map[string]int32        // where you've put Groups shared with you (order.go)
	routes      map[string]*peerLink    // a remote Group/tab/pane/attention id → its link
	comp        computerSet             // linked computers (computers.go)
	sched       schedState              // scheduled tasks (schedules.go)
}

// SocketPath returns the client socket path for a state dir.
func SocketPath(dir string) string { return filepath.Join(dir, "run", "hmkd.sock") }

// HookSocketPath returns the agent-hook socket path for a state dir.
func HookSocketPath(dir string) string { return filepath.Join(dir, "run", "hooks.sock") }

// DefaultDir is the default state directory: $HMK_DIR, else the one
// named by the enclosing app bundle (so "Hammock β" and Hammock keep
// separate daemons), else ~/Library/Application Support/Hammock on a Mac
// and $XDG_STATE_HOME/hammock (~/.local/state/hammock) elsewhere.
func DefaultDir() string {
	if d := os.Getenv("HMK_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS != "darwin" {
		if x := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(x) {
			return filepath.Join(x, "hammock")
		}
		return filepath.Join(home, ".local", "state", "hammock")
	}
	name := "Hammock"
	if n := bundleStateDir(); n != "" {
		name = n
	}
	return filepath.Join(home, "Library", "Application Support", name)
}

var (
	autoTabName   = regexp.MustCompile(`^Tab [0-9]+$`)
	autoGroupName = regexp.MustCompile(`^Group [0-9]+$`)
)

// bundleStateDir reads HammockStateDir from the Info.plist of the app this
// binary is bundled in (Contents/MacOS/x -> Contents/Info.plist).
func bundleStateDir() string { return bundleInfo("HammockStateDir") }

// BundleAgentLabel is the launchd label of the login item (LaunchAgent)
// bundled with the app this binary is in, or "" outside an app.
func BundleAgentLabel() string { return bundleInfo("HammockAgentLabel") }

// BundleDefaultDir is the state directory of the app this binary is in
// (ignoring $HMK_DIR), or "" outside an app.
func BundleDefaultDir() string {
	n := bundleStateDir()
	if n == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", n)
}

// bundleInfo reads a string key from the enclosing app's Info.plist.
func bundleInfo(key string) string { return appbundle.Info(key) }

// New loads state and reconnects to (or respawns) pane holders.
func New(cfg Config) (*Daemon, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	helperLog.Store(cfg.Log)
	if cfg.HolderPath != "" && !helperAllowed("--holder", cfg.HolderPath) {
		cfg.HolderPath = ""
	}
	if cfg.HolderPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cfg.HolderPath = filepath.Join(filepath.Dir(exe), "pane-holder")
		// In the app, the holder is a helper bundle named Hammock Pane, which
		// is what Activity Monitor lists for each pane and what tools that
		// name the terminal from the shell's parent (fastfetch) say.
		if h := filepath.Join(filepath.Dir(exe), "..", "Helpers", "Hammock Pane.app", "Contents", "MacOS", "Hammock Pane"); fileExists(h) {
			cfg.HolderPath = h
		}
	}
	if err := secureDir(cfg.Dir, true); err != nil {
		return nil, err
	}
	for _, sub := range []string{"run", "run/panes", "logs", "scrollback"} {
		if err := os.MkdirAll(filepath.Join(cfg.Dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	if err := secureDir(filepath.Join(cfg.Dir, "run"), false); err != nil {
		return nil, err
	}
	if err := secureDir(filepath.Join(cfg.Dir, "run", "panes"), false); err != nil {
		return nil, err // (a link to another state dir's: its holders' sockets)
	}
	lock, err := lockDir(cfg.Dir)
	if err != nil {
		return nil, err
	}
	st, err := state.Open(filepath.Join(cfg.Dir, "state.db"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	d := &Daemon{
		cfg: cfg, log: cfg.Log, store: st, lock: lock, started: time.Now(),
		groups: map[string]*pb.Group{}, tabs: map[string]*pb.Tab{},
		panes: map[string]*pane{}, attention: map[string]*pb.AttentionItem{}, focusOrder: map[string][]string{},
		conns:  map[*conn]struct{}{},
		grants: map[string]*pendingGrant{}, remembered: map[string]bool{},
		remote: remoteState{pairings: map[string]time.Time{}},
		done:   make(chan struct{}),
		people: map[string]*pb.Person{}, strangers: map[string]*pb.Person{}, letIn: map[string]bool{}, joins: map[string]*pendingJoin{}, typing: map[string]*time.Timer{},
		links: map[string]*peerLink{}, routes: map[string]*peerLink{},
	}
	if err := d.loadIdentity(); err != nil {
		st.Close()
		return nil, err
	}
	d.agentHub = newAgentHub(d)
	d.sshHub = newSSHHub(d)
	d.hammockKey = newHammockKeyHub(d)
	d.browsers = newBrowserHub(d)
	d.simulators = newSimHub(d)
	d.editors = newEditorHub(d)
	d.diffs = newDiffHub(d)
	d.screens = newScreenHub(d)
	d.media = newMediaHub(d)
	d.sessions = &sessionFinder{d: d}
	if err := d.restore(); err != nil {
		st.Close()
		return nil, err
	}
	d.loadAgentMade()
	d.mu.Lock()
	d.loadBootstrap()
	d.mu.Unlock()
	return d, nil
}

func (d *Daemon) restore() error {
	d.loadColors()
	// Hold the lock throughout: reconnected panes start reading at once and
	// may need d.mu (e.g. a pane whose process exited while we were down).
	d.mu.Lock()
	defer d.mu.Unlock()
	// Schedules first: a reconnected pane's exit (a scheduled command that
	// finished while we were down) needs its run, or would close as a
	// plain clean exit.
	d.loadSchedules()
	snap, err := d.store.Load()
	if err != nil {
		return err
	}
	defer d.loadComputers() // last: links and people first
	defer d.loadPeople()
	defer d.loadLinks()
	defer d.loadRemoteOrder() // runs first: links need it
	for _, s := range snap.Groups {
		// As with tabs: older daemons stored "Group 2"-style default names.
		if autoGroupName.MatchString(s.Name) {
			s.Name = ""
			d.persist(d.store.PutGroup(s))
		}
		d.groups[s.Id] = s
	}
	for _, t := range snap.Tabs {
		// Older daemons stored default names ("Tab 2"); unnamed tabs are now
		// numbered by position in the client, so the number stays current.
		if autoTabName.MatchString(t.Name) {
			t.Name = ""
			d.persist(d.store.PutTab(t))
		}
		d.tabs[t.Id] = t
	}
	var editorSaved map[string][]byte
	for _, info := range snap.Panes {
		p := newPane(d, info)
		d.panes[info.Id] = p
		if info.Kind == pb.PaneKind_PANE_KIND_EDITOR {
			if editorSaved == nil {
				editorSaved, _ = d.store.Records(editorRecord)
			}
			d.editors.restore(p, editorSaved)
			continue
		}
		if info.Kind == pb.PaneKind_PANE_KIND_DIFF {
			d.diffs.restore(p)
			continue
		}
		if info.Kind == pb.PaneKind_PANE_KIND_BROWSER {
			d.browsers.ensure() // reattaches to (or restarts) the browser host
			continue
		}
		if info.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
			d.simulators.ensure() // reopens it in the simulator host
			continue
		}
		if info.Log != nil {
			p.log = d.newLogPane(info.Id)
		}
		rt, ok := snap.Runtimes[info.Id]
		d.restoreHolder(p, info, rt, ok)
	}
	if editorSaved == nil {
		editorSaved, _ = d.store.Records(editorRecord)
	}
	d.editors.pruneRecords(editorSaved)
	for _, t := range d.tabs {
		if d.pruneFloats(t) {
			d.persist(d.store.PutTab(t))
		}
	}
	d.endStrayHolders()
	d.prunePaneFiles()
	d.restoreAttention()
	return nil
}

// prunePaneFiles removes what panes that are no more left behind (closed
// before their files went with them, or while the daemon couldn't remove
// them): holder logs, logs/holder-<pane id>.log, and saved screens,
// scrollback/<pane id>.vt with its .offset (and either's .tmp), and log
// panes' entries, panelogs/<pane id>.jsonl (and its .tmp). Only files named
// so, of panes not restored. Callers hold d.mu, with the restored
// panes in d.panes.
func (d *Daemon) prunePaneFiles() {
	prune := func(sub string, paneOf func(name string) string) {
		dir := filepath.Join(d.cfg.Dir, sub)
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if id := paneOf(e.Name()); e.Type().IsRegular() && validID(id, "pn") && d.panes[id] == nil {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	prune("logs", func(name string) string {
		id, ok := strings.CutPrefix(name, "holder-")
		if id, log := strings.CutSuffix(id, ".log"); ok && log {
			return id
		}
		return ""
	})
	prune("scrollback", func(name string) string {
		name, _ = strings.CutSuffix(name, ".tmp")
		name, _ = strings.CutSuffix(name, ".offset")
		if id, ok := strings.CutSuffix(name, ".vt"); ok {
			return id
		}
		return ""
	})
	prune("panelogs", func(name string) string {
		name, _ = strings.CutSuffix(name, ".tmp")
		if id, ok := strings.CutSuffix(name, ".jsonl"); ok {
			return id
		}
		return ""
	})
}

// restoreHolder reconnects a restored terminal pane to its holder, or
// starts its program again if it can't be. Callers hold d.mu.
func (d *Daemon) restoreHolder(p *pane, info *pb.Pane, rt state.PaneRuntime, ok bool) {
	if ok && !d.ownSocket(rt.Socket, info.Id) {
		// Another state dir's (this state copied, and hmkd started on
		// the copy), or a link to one: never reconnected to, nor ended,
		// nor replaced (endHolderAt goes by the holder's own --socket).
		d.log.Warn("not reconnecting: the holder's socket is gone, or a link, or not this state dir's", "pane", info.Id, "socket", rt.Socket)
		ok = false
	}
	if ok {
		err := p.reconnect(rt.Socket, rt.Token)
		if err == nil {
			d.log.Info("reconnected holder", "pane", info.Id)
			return
		}
		// Why matters: a holder that's alive but can't be reached gets
		// replaced, and its process with it.
		d.log.Warn("couldn't reconnect holder", "pane", info.Id, "socket", rt.Socket, "err", err)
	}
	// One still listening (it wouldn't take our token, or was started
	// just before a crash, with no runtime recorded) would otherwise run
	// on unseen once a new holder takes its socket, and later unlink it.
	if pid, err := endHolderAt(p.socket); err == nil {
		d.log.Info("ended the holder this pane couldn't reconnect to", "pane", info.Id, "pid", pid)
	}
	// Holder gone (reboot/logout): start a fresh process in the same place,
	// unless its program had already ended (a failed command, a kept
	// scheduled run): running that again would repeat it, unasked.
	p.loadScrollback()
	if info.Exited {
		p.showEnded()
		return
	}
	resume := d.agentHub.resumeCommand(info)
	if err := p.spawn(resume); err != nil {
		d.log.Error("respawn pane", "pane", info.Id, "err", err)
		if p.markStartFailed(err) { // on its screen, not only in its info
			d.startFailedLocked(p)
		}
	} else {
		d.log.Info("respawned pane", "pane", info.Id, "resume", resume != nil)
	}
}

// Serve listens on the client socket and blocks until Shutdown.
func (d *Daemon) Serve() error {
	sock := SocketPath(d.cfg.Dir)
	writePersonKey(d.cfg.Dir, d.log) // (Linux: Unraid's page, person.go)
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		ln.Close()
		return err
	}
	if screenSharing() {
		d.screens.ensure() // ready before anyone asks (it starts in about a second)
	}
	if err := d.agentHub.listen(HookSocketPath(d.cfg.Dir)); err != nil {
		ln.Close()
		return err
	}
	if err := d.sshHub.listen(SSHAgentSocketPath(d.cfg.Dir)); err != nil {
		d.log.Warn("SSH agent socket unavailable", "err", err) // panes still get it: ssh then finds no agent
	}
	go d.hammockKey.refresh()
	// One socket, two transports: WebSocket (HTTP upgrade) and raw
	// length-prefixed frames, told apart by the first bytes.
	httpLn := newSniffListener(ln, d.serveFrames)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1", d.handleWS)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second,
		// Who connected, for agent scoping (caller.go).
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, peerPIDKey{}, localPeerOf(c))
		}}
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		ln.Close()
		return nil
	}
	d.ln, d.srv = ln, srv
	d.mu.Unlock()
	if d.cfg.Remote || d.store.Setting("remote") == "true" {
		tailscaleNet() // asked before taking the lock: it can be slow
		d.mu.Lock()
		if err := d.startRemote(); err != nil {
			d.log.Warn("remote access unavailable", "err", err)
		}
		d.mu.Unlock()
	}
	go func() {
		hardware() // slow the first time: off the lock
		d.mu.Lock()
		d.refreshLinked()
		d.mu.Unlock()
	}()
	go d.watchInput()
	go d.runIntros() // introduced links (links.go)
	d.mu.Lock()
	d.deliverUnlinks() // requests kept from before
	d.mu.Unlock()
	go d.pollCwds()
	go d.watchGroupIcons()
	go d.watchUsage()
	go d.watchTheme()
	go d.watchConfig()
	go d.watchAgentKinds()
	go d.runSchedules()
	d.log.Info("listening", "socket", sock)
	err = srv.Serve(httpLn)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops serving and saves every pane's screen. Pane holders keep
// running. Safe to call more than once.
func (d *Daemon) Shutdown() { d.shutdownOnce.Do(d.shutdown) }

// Done is closed once Shutdown has finished.
func (d *Daemon) Done() <-chan struct{} { return d.done }

func (d *Daemon) shutdown() {
	defer close(d.done)
	defer d.sessions.close()
	d.mu.Lock()
	d.stopped = true
	srv := d.srv
	for c := range d.conns {
		c.cancel()
	}
	for _, l := range d.links {
		if sl := d.comp.links[l.tok.HostUserId]; l.linked && l.online && sl != nil {
			// Last in touch now (a request it delivers later can't have
			// been asked before this).
			sl.Record.LastSeenUnixMs = time.Now().UnixMilli()
			d.persist(d.store.PutRecord("complink", l.tok.HostUserId, sl))
		}
		l.close()
	}
	d.stopRemote()
	d.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	d.editors.close()
	d.diffs.close()
	d.agentHub.close()
	d.sshHub.close()
	d.hammockKey.close()
	d.mu.Lock()
	var ending []<-chan struct{}
	for _, p := range d.panes {
		p.saveScrollback(true)
		if d.endPanes {
			ending = append(ending, p.endHolder())
		} else {
			p.detachHolder()
		}
		if p.log != nil {
			p.log.close(false)
		}
	}
	d.mu.Unlock()
	if len(ending) > 0 {
		d.log.Info("ending pane holders", "count", len(ending))
		// Until each holder has been asked: the process may exit right after.
		timeout := time.After(2 * time.Second)
	wait:
		for _, sent := range ending {
			select {
			case <-sent:
			case <-timeout:
				break wait
			}
		}
	}
	d.store.Close()
	if d.lock != nil {
		d.lock.Close()
	}
}

// ---------- publishing ----------

// publish sends a state update to every subscribed connection. Callers hold d.mu.
func (d *Daemon) publish(u *pb.StateUpdate) {
	d.clearPushes(u)
	d.version++
	u.Version = d.version
	msg := &pb.ServerMessage{Msg: &pb.ServerMessage_State{State: u}}
	for c := range d.conns {
		switch {
		case !c.subscribed:
			// A guest that never subscribed sees no updates, but what it
			// watches is still bound by them (a tab moving out of its Group).
			if c.peer != "" && c.authed && movesThings(u) {
				d.revokeStreams(c)
			}
		case c.peer != "":
			// A guest sees only its Groups (share.go).
			if f := d.filterFor(c, u); f != nil {
				c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_State{State: f}})
				if len(f.DeletedGroupIds)+len(f.DeletedTabIds)+len(f.DeletedPaneIds) > 0 {
					d.revokeStreams(c) // something it saw moved out of reach
				}
			}
		default:
			c.send(msg)
		}
	}
}

// movesThings: u could take a pane out of a guest's reach (Groups, their
// members, tabs or panes changed or went).
func movesThings(u *pb.StateUpdate) bool {
	return len(u.Groups)+len(u.Tabs)+len(u.Panes)+len(u.DeletedGroupIds)+len(u.DeletedTabIds)+len(u.DeletedPaneIds) > 0
}

func (d *Daemon) fullState() *pb.StateUpdate {
	u := &pb.StateUpdate{Version: d.version, Full: true}
	for _, s := range d.groups {
		u.Groups = append(u.Groups, proto.CloneOf(s))
	}
	for _, t := range d.tabs {
		u.Tabs = append(u.Tabs, proto.CloneOf(t))
	}
	for _, p := range d.panes {
		u.Panes = append(u.Panes, p.snapshotInfo())
	}
	for _, a := range d.attention {
		u.Attention = append(u.Attention, proto.CloneOf(a))
	}
	for _, g := range d.grants {
		u.Grants = append(u.Grants, proto.CloneOf(g.req))
	}
	u.People = append(u.People, proto.CloneOf(d.id.me))
	for _, p := range d.people {
		u.People = append(u.People, proto.CloneOf(p))
	}
	for _, j := range d.joins {
		u.JoinRequests = append(u.JoinRequests, proto.CloneOf(j.req))
	}
	if d.usage != nil {
		u.Usage = proto.CloneOf(d.usage) // guests' filtered state leaves it out
	}
	if d.theme != nil {
		u.Theme = proto.CloneOf(d.theme) // (as usage)
	}
	for _, s := range d.sched.list {
		u.Schedules = append(u.Schedules, proto.CloneOf(s))
	}
	if d.agentKinds != nil {
		u.AgentKinds = cloneKinds(d.agentKinds)
	} else {
		u.AgentKinds = agentKinds([]string{"claude", "codex"}) // not asked yet: as clients took it before
	}
	u.HammockKey = proto.CloneOf(d.hammockKey.status) // (guests' and linked computers' filtered state leaves it out)
	for _, r := range d.hammockKey.reqs {
		u.HammockKeyRequests = append(u.HammockKeyRequests, proto.CloneOf(r.req))
	}
	d.remoteState(u) // Groups shared with you (peer.go), linked computers' schedules
	u.Computers = d.computersState(false)
	u.LinkRequests = d.pendingLinks()
	slices.SortFunc(u.Groups, func(a, b *pb.Group) int { return int(a.Order - b.Order) })
	return u
}

// ---------- model operations (callers hold d.mu) ----------

func (d *Daemon) createGroup(name string) *pb.Group {
	s := d.createGroupQuiet(name)
	d.publish(&pb.StateUpdate{Groups: []*pb.Group{proto.CloneOf(s)}})
	return s
}

// createGroupQuiet makes a Group without telling clients: the caller
// publishes it (a tab made in it does, with the Group), or drops it with
// dropQuietGroup. Callers hold d.mu.
func (d *Daemon) createGroupQuiet(name string) *pb.Group {
	s := &pb.Group{Id: newID("gp"), Name: name, Order: d.nextOrder()}
	d.groups[s.Id] = s
	d.persist(d.store.PutGroup(s))
	return s
}

// dropQuietGroup forgets a Group no client was told of. Callers hold d.mu.
func (d *Daemon) dropQuietGroup(id string) {
	delete(d.groups, id)
	d.persist(d.store.DeleteGroup(id))
}

func (d *Daemon) persist(err error) {
	if err != nil {
		d.log.Error("persist", "err", err)
	}
}

// checkTabID reports whether id can be a new tab's: empty (one is made
// up), or well formed and not in use. Callers hold d.mu.
func (d *Daemon) checkTabID(id string) error {
	if id == "" {
		return nil
	}
	if !validID(id, "tb") {
		return fmt.Errorf("bad tab id %q", id)
	}
	if _, used := d.tabs[id]; used {
		return fmt.Errorf("tab id %q is already used", id)
	}
	return nil
}

// createTab adds a tab to a Group with one pane. id is the client's choice
// (CreateTab.tab_id), or empty for a new one.
func (d *Daemon) createTab(groupID, name string, spec *pb.PaneSpec, id, after string) (*pb.Tab, error) {
	s, ok := d.groups[groupID]
	if !ok {
		return nil, fmt.Errorf("no such group %q", groupID)
	}
	if err := d.checkTabID(id); err != nil {
		return nil, err
	}
	if id == "" {
		id = newID("tb")
	}
	// An empty name means "Tab N" by position, worked out by each client.
	t := &pb.Tab{Id: id, GroupId: groupID, Name: name}
	p, err := d.startPane(t, spec)
	if err != nil {
		return nil, err
	}
	// A log tab is named for what it shows ("Console: Safari", "app.log").
	if t.Name == "" && p.info.Log != nil {
		t.Name = p.info.Log.Label
	}
	if t.Name == "" && p.info.Kind == pb.PaneKind_PANE_KIND_DIFF {
		t.Name = "Review"
	}
	t.Layout = leaf(p.id)
	t.FocusedPaneId = p.id
	d.tabs[t.Id] = t
	if i := slices.Index(s.TabIds, after); after != "" && i >= 0 {
		s.TabIds = slices.Insert(s.TabIds, i+1, t.Id)
	} else {
		s.TabIds = append(s.TabIds, t.Id)
	}
	if d.panes[s.LastFocusedPaneId] == nil && p.isTerminal() {
		// None yet, or it's closed since (the Group's tabs had all gone).
		s.LastFocusedPaneId = p.id // an unnamed Group is named after this pane's directory
	}
	if c := p.cwd(); c != "" && p.isTerminal() && (s.LastFocusedPaneId == p.id || s.Folder == "") {
		s.Folder = c
	}
	d.persist(d.store.PutTab(t))
	d.persist(d.store.PutGroup(s))
	d.publish(&pb.StateUpdate{
		Groups: []*pb.Group{proto.CloneOf(s)},
		Tabs:   []*pb.Tab{proto.CloneOf(t)},
		Panes:  []*pb.Pane{p.snapshotInfo()},
	})
	return t, nil
}

func (d *Daemon) splitPane(targetID string, dir pb.Split_Direction, spec *pb.PaneSpec, before bool) (*pane, error) {
	target, ok := d.panes[targetID]
	if !ok {
		return nil, fmt.Errorf("no such pane %q", targetID)
	}
	t := d.tabs[target.tabID()]
	if t == nil {
		return nil, fmt.Errorf("pane %q has no tab", targetID)
	}
	if dir == pb.Split_DIRECTION_UNSPECIFIED {
		dir = pb.Split_DIRECTION_HORIZONTAL
	}
	if spec == nil {
		spec = &pb.PaneSpec{}
	}
	if spec.Cwd == "" {
		spec.Cwd = target.cwd()
	}
	p, err := d.startPane(t, spec)
	if err != nil {
		return nil, err
	}
	if !splitLayout(t.Layout, targetID, p.id, dir, before) {
		p.kill()
		delete(d.panes, p.id)
		d.persist(d.store.DeletePane(p.id))
		p.discardFiles()
		return nil, fmt.Errorf("pane %q is not in its tab's layout", targetID)
	}
	d.noteFocus(t.Id, targetID)
	d.noteFocus(t.Id, p.id)
	t.FocusedPaneId = p.id
	d.persist(d.store.PutTab(t))
	d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}, Panes: []*pb.Pane{p.snapshotInfo()}})
	return p, nil
}

// startPane creates and spawns a pane for tab t (not yet placed in its layout).
func (d *Daemon) startPane(t *pb.Tab, spec *pb.PaneSpec) (*pane, error) {
	if spec == nil {
		spec = &pb.PaneSpec{}
	}
	if spec.Url != "" {
		return d.startBrowser(t, spec)
	}
	if spec.Simulator != "" {
		return d.startSimulator(t, spec), nil
	}
	if spec.Editor != nil {
		return d.startEditor(t, spec)
	}
	if spec.Diff != nil {
		return d.startDiff(t, spec)
	}
	cwd := spec.Cwd
	if cwd == "" {
		// Where the Group works: its last focused terminal's folder (kept
		// as it changes), or one set by hand, even once its tabs have gone.
		if s := d.groups[t.GroupId]; s != nil && s.Folder != "" && isDir(s.Folder) {
			cwd = s.Folder
		}
	}
	if cwd == "" {
		// A Group from before folders were kept.
		if s := d.groups[t.GroupId]; s != nil {
			if lp := d.panes[s.LastFocusedPaneId]; lp != nil {
				cwd = lp.cwd()
			} else if len(s.TabIds) > 0 {
				// Nothing focused yet: use the Group's first pane.
				if ft := d.tabs[s.TabIds[0]]; ft != nil {
					if ids := layoutPanes(ft.Layout); len(ids) > 0 {
						if fp := d.panes[ids[0]]; fp != nil {
							cwd = fp.cwd()
						}
					}
				}
			}
		}
	}
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	cols, rows := spec.Cols, spec.Rows
	if cols == 0 || rows == 0 {
		cols, rows = 120, 34
	}
	var logSrc *pb.LogSource
	if spec.Log != nil {
		c, label, err := logCommand(spec.Log)
		if err != nil {
			return nil, err
		}
		logSrc = proto.CloneOf(spec.Log)
		logSrc.Label = label
		spec = proto.CloneOf(spec)
		spec.Command = c
	}
	cmd := spec.Command
	if len(cmd) == 0 {
		cmd = []string{loginShell(), "-l"}
	}
	info := &pb.Pane{
		Id: newID("pn"), TabId: t.Id, Kind: pb.PaneKind_PANE_KIND_TERMINAL,
		Title: cmd[0], Cwd: cwd, Command: cmd, Cols: cols, Rows: rows,
		CreatedByPaneId: spec.CreatedByPaneId, Log: logSrc,
	}
	if logSrc != nil {
		info.Title = logSrc.Label
	}
	p := newPane(d, info)
	p.extraEnv = spec.Env
	if logSrc != nil {
		p.log = d.newLogPane(info.Id)
	}
	if err := p.spawnAsync(nil); err != nil {
		p.discardFiles()
		return nil, err
	}
	d.panes[info.Id] = p
	// A copy, under the pane's lock: its holder's reader is already running
	// and may be changing info (a title, exited) as this saves it.
	d.persist(d.store.PutPane(p.snapshotInfo()))
	return p, nil
}

func (d *Daemon) closePane(id string) error {
	d.log.Info("close pane", "id", id)
	p, ok := d.panes[id]
	if !ok {
		return fmt.Errorf("no such pane %q", id)
	}
	p.kill()
	delete(d.panes, id)
	d.persist(d.store.DeletePane(id))
	p.discardFiles()
	u := &pb.StateUpdate{DeletedPaneIds: []string{id}}
	if t := d.tabs[p.tabID()]; t != nil {
		if !removeFloat(t, id) {
			t.Layout = removeFromLayout(t.Layout, id)
			promoteFloat(t) // the layout's last pane: the front float takes its place
		}
		if t.Layout == nil {
			d.deleteTabLocked(t, u)
		} else {
			if t.FocusedPaneId == id {
				t.FocusedPaneId = d.previousFocus(t, id)
			}
			d.persist(d.store.PutTab(t))
			u.Tabs = append(u.Tabs, proto.CloneOf(t))
		}
	}
	d.clearAttentionForPane(id, u)
	d.publish(u)
	return nil
}

// deleteTabLocked removes an (already empty) tab and records it in u.
func (d *Daemon) deleteTabLocked(t *pb.Tab, u *pb.StateUpdate) {
	delete(d.tabs, t.Id)
	delete(d.focusOrder, t.Id)
	d.persist(d.store.DeleteTab(t.Id))
	u.DeletedTabIds = append(u.DeletedTabIds, t.Id)
	if s := d.groups[t.GroupId]; s != nil {
		s.TabIds = slices.DeleteFunc(s.TabIds, func(id string) bool { return id == t.Id })
		d.persist(d.store.PutGroup(s))
		u.Groups = append(u.Groups, proto.CloneOf(s))
	}
}

func (d *Daemon) closeTab(id string) error {
	d.log.Info("close tab", "id", id)
	t, ok := d.tabs[id]
	if !ok {
		return fmt.Errorf("no such tab %q", id)
	}
	u := &pb.StateUpdate{}
	for _, pid := range tabPanes(t) {
		if p := d.panes[pid]; p != nil {
			p.kill()
			delete(d.panes, pid)
			d.persist(d.store.DeletePane(pid))
			p.discardFiles()
			u.DeletedPaneIds = append(u.DeletedPaneIds, pid)
			d.clearAttentionForPane(pid, u)
		}
	}
	d.deleteTabLocked(t, u)
	d.publish(u)
	return nil
}

// closeGroup closes a Group and its tabs. remember: a person here closed
// it, so its tabs go on the Reopen Closed list, and reopening them brings
// the Group back (last tab first, so they reopen first to last). The list
// keeps maxClosedTabs, so a very big Group's first tabs fall off it.
func (d *Daemon) closeGroup(id string, remember bool) error {
	d.log.Info("close group", "id", id)
	s, ok := d.groups[id]
	if !ok {
		return fmt.Errorf("no such group %q", id)
	}
	if remember {
		for _, tid := range slices.Backward(slices.Clone(s.TabIds)) {
			d.rememberTab(tid, "")
		}
	}
	for _, tid := range slices.Clone(s.TabIds) {
		_ = d.closeTab(tid)
	}
	guests := map[string]bool{}
	for _, m := range s.Members {
		if guestRole(m.Role) {
			guests[m.UserId] = true
		}
	}
	delete(d.groups, id)
	d.persist(d.store.DeleteGroup(id))
	_ = os.Remove(d.storedPicture(id))
	u := &pb.StateUpdate{DeletedGroupIds: []string{id}}
	// A shared Group's link stops working, and those waiting on it are
	// turned away.
	if d.store.Setting(inviteKey(id)) != "" {
		d.persist(d.store.SetSetting(inviteKey(id), ""))
	}
	for jid, j := range d.joins {
		if j.req.GroupId == id {
			delete(d.joins, jid)
			u.ResolvedJoinIds = append(u.ResolvedJoinIds, jid)
			j.c.send(&pb.ServerMessage{ReplyTo: j.msgID, Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: "that Group was closed"}}})
			guests[j.req.UserId] = true
		}
	}
	d.publish(u)
	// Its guests go once they're in no Group here, as when removed.
	for c := range d.conns {
		if guests[c.peer] {
			d.hangUpIfOutside(c, false)
		}
	}
	return nil
}

// focusPane records pane focus for cwd inheritance.
func (d *Daemon) focusPane(id string) {
	p, ok := d.panes[id]
	if !ok {
		return
	}
	t := d.tabs[p.tabID()]
	if t == nil {
		return
	}
	d.noteFocus(t.Id, id)
	u := &pb.StateUpdate{}
	// A float focused comes to the front, off the shelf if it's there: a
	// pane revealed (a notification, Needs You) is never left out of sight.
	if raised := raiseFloat(t, id); t.FocusedPaneId != id || raised {
		t.FocusedPaneId = id
		d.persist(d.store.PutTab(t))
		u.Tabs = append(u.Tabs, proto.CloneOf(t))
	}
	// Browser panes have no directory: the Group keeps its terminal's (for
	// its name and new tabs' working directory).
	if s := d.groups[t.GroupId]; s != nil && s.LastFocusedPaneId != id && p.isTerminal() {
		s.LastFocusedPaneId = id
		if c := p.cwd(); c != "" {
			s.Folder = c
		}
		d.persist(d.store.PutGroup(s))
		u.Groups = append(u.Groups, proto.CloneOf(s))
	}
	// Looking at a finished agent acknowledges it: the checkmark and its
	// "finished" item in Needs You go away.
	p.mu.Lock()
	a := p.info.Agent
	ack := a != nil && !a.Seen && a.State == pb.AgentState_AGENT_STATE_IDLE
	if ack {
		a.Seen = true
	}
	p.mu.Unlock()
	if ack {
		info := p.snapshotInfo()
		d.persist(d.store.PutPane(info))
		u.Panes = append(u.Panes, info)
		if at := d.attention["at_"+id]; at != nil && at.State == pb.AgentState_AGENT_STATE_IDLE {
			d.clearAttentionForPane(id, u)
		}
	}
	if len(u.Tabs)+len(u.Groups)+len(u.Panes) > 0 {
		d.publish(u)
	}
}

// noteFocus records a pane as its tab's most recently focused. Callers hold d.mu.
func (d *Daemon) noteFocus(tabID, paneID string) {
	o := slices.DeleteFunc(d.focusOrder[tabID], func(id string) bool { return id == paneID })
	d.focusOrder[tabID] = append(o, paneID)
}

// previousFocus is the pane to focus when gone (the tab's focused pane)
// leaves: the most recently focused one still in the tab, else its first.
// Callers hold d.mu.
func (d *Daemon) previousFocus(t *pb.Tab, gone string) string {
	// Its layout's panes and the floats showing (not ones on the shelf).
	panes := layoutPanes(t.Layout)
	for _, f := range t.Floating {
		if !f.Minimised {
			panes = append(panes, f.PaneId)
		}
	}
	o := slices.DeleteFunc(d.focusOrder[t.Id], func(id string) bool { return id == gone || !slices.Contains(panes, id) })
	d.focusOrder[t.Id] = o
	if len(o) > 0 {
		return o[len(o)-1]
	}
	return panes[0]
}

// paneChanged persists and publishes a pane's updated info. Callers hold d.mu.
func (d *Daemon) paneChanged(p *pane) {
	info := p.snapshotInfo()
	d.persist(d.store.PutPane(info))
	u := &pb.StateUpdate{Panes: []*pb.Pane{info}}
	// A Group follows its last focused terminal's folder.
	if t := d.tabs[info.TabId]; t != nil && p.isTerminal() {
		if g := d.groups[t.GroupId]; g != nil && (g.LastFocusedPaneId == p.id || g.Folder == "") {
			d.noteGroupFolder(g, info.Cwd, u)
		}
	}
	d.publish(u)
}

// noteGroupFolder records folder as where g works, adding g to u if that
// changed it. Callers hold d.mu.
func (d *Daemon) noteGroupFolder(g *pb.Group, folder string, u *pb.StateUpdate) {
	if folder == "" || folder == g.Folder {
		return
	}
	g.Folder = folder
	d.persist(d.store.PutGroup(g))
	u.Groups = slices.DeleteFunc(u.Groups, func(o *pb.Group) bool { return o.Id == g.Id })
	u.Groups = append(u.Groups, proto.CloneOf(g))
}

// setGroupFolder sets where a Group's new tabs open (SetGroupFolder): an
// existing directory, by absolute path. Its terminals move it on again as
// you work in them. Callers hold d.mu.
func (d *Daemon) setGroupFolder(id, folder string) error {
	g, ok := d.groups[id]
	if !ok {
		return errors.New("no such group")
	}
	if !filepath.IsAbs(folder) {
		return errors.New("the folder must be an absolute path")
	}
	folder = filepath.Clean(folder)
	if !isDir(folder) {
		return fmt.Errorf("no folder at %s", folder)
	}
	u := &pb.StateUpdate{}
	d.noteGroupFolder(g, folder, u)
	d.publish(u)
	return nil
}

// pollCwds refreshes pane working directories from the shell process when
// the shell doesn't report OSC 7.
func (d *Daemon) pollCwds() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	ticks := 0
	for range t.C {
		ticks++
		d.mu.Lock()
		if d.stopped {
			d.mu.Unlock()
			return
		}
		for _, p := range d.panes {
			if c, r, g, s := p.refreshCwd(), p.refreshProcesses(), p.refreshProgram(), p.refreshSubagents(); c || r || g || s {
				d.paneChanged(p)
			}
		}
		d.detectAgents()
		d.refreshTabNames()
		if ticks%15 == 0 { // every 30s
			for _, p := range d.panes {
				go p.saveScrollback(false)
			}
		}
		d.mu.Unlock()
	}
}

// loginShell is the shell new panes run: $SHELL, else this user's own
// (from /etc/passwd: a daemon started by an init script may have no
// $SHELL), else zsh on a Mac and sh elsewhere.
func loginShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if s := passwdShell("/etc/passwd", os.Getuid()); s != "" {
		return s
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}

// passwdShell reads uid's shell from a passwd file: "" if it has none
// there, or one that only refuses logins (nologin, false).
func passwdShell(path string, uid int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 || f[2] != strconv.Itoa(uid) {
			continue
		}
		sh := strings.TrimSpace(f[6])
		if !filepath.IsAbs(sh) || strings.HasSuffix(sh, "/nologin") || strings.HasSuffix(sh, "/false") {
			return ""
		}
		if _, err := os.Stat(sh); err != nil {
			return ""
		}
		return sh
	}
	return ""
}

// secureDir makes sure dir (created if missing) is a directory only this
// user can write to: the daemon keeps its sockets, keys and state there, so
// one in a shared place (HMK_DIR=/tmp/...) made by someone else, or open to
// others, would hand them the daemon. Ours but too open is tightened to
// 0700; someone else's is refused. followLink allows dir itself to be a
// symlink (checked where it points), as a state dir moved elsewhere may be.
func secureDir(dir string, followLink bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		if !followLink {
			return fmt.Errorf("%s is a symlink; refusing to use it", dir)
		}
		if st, err = os.Stat(dir); err != nil {
			return err
		}
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user; refusing to use it", dir)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// ownSocket: socket is pane id's holder socket in this daemon's own state
// dir (run/panes/<id>.sock), where it starts them, and a socket there
// itself: not a link (to another state dir's holder) nor a second name of
// one. Compared as written, links unresolved: a copy whose run/panes links
// to the real one's (refused anyway: secureDir) isn't the real one.
func (d *Daemon) ownSocket(socket, id string) bool {
	abs := func(p string) string {
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return filepath.Clean(p)
	}
	if socket == "" || abs(socket) != abs(filepath.Join(d.cfg.Dir, "run", "panes", id+".sock")) {
		return false
	}
	st, err := os.Lstat(socket)
	if err != nil {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	return st.Mode()&os.ModeSocket != 0 && (!ok || sys.Nlink == 1)
}

// dirLock is a daemon's hold on its state dir: exclusive flocks on
// run/daemon.lock and on the dir itself, for as long as it runs.
type dirLock struct{ lock, dir *os.File }

func (l *dirLock) Close() error {
	if l.dir != nil {
		l.dir.Close()
	}
	return l.lock.Close()
}

// lockDir takes an exclusive lock on dir so only one daemon serves it (the
// app's launcher and a login item could otherwise race), and so its pane
// holders can tell it from another hmkd (holder/statelock.go): a flock on
// run/daemon.lock, a plain file of its own with the one name (not a link
// to another state dir's, which would lock that one's), then one on the
// dir.
// ErrAlreadyRunning: another hmkd has the state dir (nothing to do).
var ErrAlreadyRunning = errors.New("another hmkd is already running")

func lockDir(dir string) (*dirLock, error) {
	path := holder.LockPath(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("%w for %s", ErrAlreadyRunning, dir)
	}
	if err := holder.CheckLockFile(path, f); err != nil {
		f.Close()
		return nil, fmt.Errorf("refusing the state dir's lock: %w", err)
	}
	df, err := os.Open(dir)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := syscall.Flock(int(df.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Only this daemon takes it, once it has daemon.lock: something
		// else of yours is holding it, and the holders wouldn't take this
		// daemon without it.
		df.Close()
		f.Close()
		return nil, fmt.Errorf("something else holds a lock on %s (not a Hammock daemon: it has %s), so pane holders wouldn't take this one; quit it and start Hammock again: %w", dir, path, err)
	}
	// The pid, so the app can find (and if need be stop) the daemon.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	return &dirLock{lock: f, dir: df}, nil
}

// setLayout replaces a tab's split tree if it contains exactly the same
// panes. Callers hold d.mu.
func (d *Daemon) setLayout(tabID string, l *pb.Layout) error {
	t, ok := d.tabs[tabID]
	if !ok {
		return fmt.Errorf("no such tab %q", tabID)
	}
	have, want := layoutPanes(t.Layout), layoutPanes(l)
	slices.Sort(have)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		return errors.New("layout must contain exactly the tab's panes")
	}
	normaliseLayout(l)
	t.Layout = l
	d.persist(d.store.PutTab(t))
	d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}})
	return nil
}

// normaliseLayout fixes weights throughout a tree.
func normaliseLayout(l *pb.Layout) {
	if s := l.GetSplit(); s != nil {
		s.Weights = normalise(s.Weights, len(s.Children))
		for _, c := range s.Children {
			normaliseLayout(c)
		}
	}
}

// moveTab reorders a tab within its Group or moves it to another. Callers hold d.mu.
func (d *Daemon) moveTab(tabID, groupID string, index int) error {
	t, ok := d.tabs[tabID]
	if !ok {
		return fmt.Errorf("no such tab %q", tabID)
	}
	if groupID == "" {
		groupID = t.GroupId
	}
	dst, ok := d.groups[groupID]
	if !ok {
		return fmt.Errorf("no such group %q", groupID)
	}
	u := &pb.StateUpdate{}
	if src := d.groups[t.GroupId]; src != nil {
		src.TabIds = slices.DeleteFunc(src.TabIds, func(id string) bool { return id == tabID })
		if src != dst {
			// The Group it leaves is no longer named after, or starts agents
			// in, a pane that's gone to another.
			if slices.Contains(tabPanes(t), src.LastFocusedPaneId) {
				src.LastFocusedPaneId = d.groupTerminalLocked(src)
				if lp := d.panes[src.LastFocusedPaneId]; lp != nil && lp.cwd() != "" {
					src.Folder = lp.cwd()
				}
			}
			d.persist(d.store.PutGroup(src))
			u.Groups = append(u.Groups, proto.CloneOf(src))
		}
	}
	index = max(0, min(index, len(dst.TabIds)))
	dst.TabIds = slices.Insert(dst.TabIds, index, tabID)
	t.GroupId = groupID
	d.persist(d.store.PutGroup(dst))
	d.persist(d.store.PutTab(t))
	u.Groups = append(u.Groups, proto.CloneOf(dst))
	u.Tabs = append(u.Tabs, proto.CloneOf(t))
	d.publish(u)
	return nil
}

// groupTerminalLocked is a terminal pane in g to stand for it, preferring
// the focused one of its first tab that has one, or "" if it has none.
// Callers hold d.mu.
func (d *Daemon) groupTerminalLocked(g *pb.Group) string {
	for _, tid := range g.TabIds {
		t := d.tabs[tid]
		if t == nil {
			continue
		}
		if p := d.panes[t.FocusedPaneId]; p != nil && p.isTerminal() {
			return p.id
		}
		for _, id := range tabPanes(t) {
			if p := d.panes[id]; p != nil && p.isTerminal() {
				return p.id
			}
		}
	}
	return ""
}

// mergeTab moves every pane of tabID, keeping their layout, next to
// intoPaneID in its tab, then removes the emptied tab. Callers hold d.mu.
func (d *Daemon) mergeTab(tabID, intoPaneID string, dir pb.Split_Direction) error {
	src, ok := d.tabs[tabID]
	if !ok {
		return fmt.Errorf("no such tab %q", tabID)
	}
	target, ok := d.panes[intoPaneID]
	if !ok {
		return fmt.Errorf("no such pane %q", intoPaneID)
	}
	dst := d.tabs[target.tabID()]
	if dst == nil {
		return fmt.Errorf("pane %q has no tab", intoPaneID)
	}
	if dst == src {
		return errors.New("a tab can't be merged into itself")
	}
	if dir == pb.Split_DIRECTION_UNSPECIFIED {
		dir = pb.Split_DIRECTION_HORIZONTAL
	}
	moved := proto.CloneOf(src.Layout)
	if !insertLayout(dst.Layout, intoPaneID, moved, dir, false) {
		return fmt.Errorf("pane %q is not in its tab's layout", intoPaneID)
	}
	normaliseLayout(dst.Layout)
	// Its floats float over the tab it joins.
	dst.Floating = append(dst.Floating, src.Floating...)
	u := &pb.StateUpdate{}
	for _, id := range tabPanes(src) {
		if p := d.panes[id]; p != nil {
			p.setTab(dst.Id)
			info := p.snapshotInfo()
			d.persist(d.store.PutPane(info))
			u.Panes = append(u.Panes, info)
		}
	}
	if g := d.groups[src.GroupId]; g != nil && src.GroupId != dst.GroupId && slices.Contains(tabPanes(src), g.LastFocusedPaneId) {
		g.LastFocusedPaneId = ""
	}
	dst.FocusedPaneId = src.FocusedPaneId
	d.persist(d.store.PutTab(dst))
	u.Tabs = append(u.Tabs, proto.CloneOf(dst))
	d.deleteTabLocked(src, u)
	d.publish(u)
	return nil
}

// replacePane turns a pane into another made from spec, in its place in the
// layout; the old pane closes. Callers hold d.mu.
func (d *Daemon) replacePane(id string, spec *pb.PaneSpec) (*pane, error) {
	old, ok := d.panes[id]
	if !ok {
		return nil, fmt.Errorf("no such pane %q", id)
	}
	t := d.tabs[old.tabID()]
	if t == nil {
		return nil, fmt.Errorf("pane %q has no tab", id)
	}
	if spec == nil {
		spec = &pb.PaneSpec{}
	}
	if spec.Cwd == "" && old.isTerminal() {
		spec.Cwd = old.cwd()
	}
	p, err := d.startPane(t, spec)
	if err != nil {
		return nil, err
	}
	if i := floatIndex(t, id); i >= 0 {
		t.Floating[i].PaneId = p.id
	} else {
		replaceLeaf(t.Layout, id, p.id)
	}
	if t.FocusedPaneId == id {
		t.FocusedPaneId = p.id
	}
	d.log.Info("replace pane", "id", id, "with", p.id)
	old.kill()
	delete(d.panes, id)
	d.persist(d.store.DeletePane(id))
	old.discardFiles() // a log pane's log too, which once ran on unseen
	d.persist(d.store.PutTab(t))
	u := &pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}, Panes: []*pb.Pane{p.snapshotInfo()}, DeletedPaneIds: []string{id}}
	d.clearAttentionForPane(id, u)
	d.publish(u)
	return p, nil
}

// breakOutPane moves a pane out of its tab into a new tab just after it.
// Callers hold d.mu.
func (d *Daemon) breakOutPane(paneID string) (*pb.Tab, error) {
	p, ok := d.panes[paneID]
	if !ok {
		return nil, fmt.Errorf("no such pane %q", paneID)
	}
	src := d.tabs[p.tabID()]
	if src == nil {
		return nil, fmt.Errorf("pane %q has no tab", paneID)
	}
	floating := floatIndex(src, paneID) >= 0
	if !floating && len(layoutPanes(src.Layout)) < 2 {
		return nil, errors.New("the pane is already its tab's only pane in its layout")
	}
	g := d.groups[src.GroupId]
	if g == nil {
		return nil, fmt.Errorf("no such group %q", src.GroupId)
	}
	if floating {
		removeFloat(src, paneID)
	} else {
		src.Layout = removeFromLayout(src.Layout, paneID)
	}
	if src.FocusedPaneId == paneID {
		src.FocusedPaneId = d.previousFocus(src, paneID)
	}
	t := &pb.Tab{Id: newID("tb"), GroupId: src.GroupId, Layout: leaf(paneID), FocusedPaneId: paneID}
	d.tabs[t.Id] = t
	i := slices.Index(g.TabIds, src.Id)
	g.TabIds = slices.Insert(g.TabIds, i+1, t.Id)
	p.setTab(t.Id)
	info := p.snapshotInfo()
	d.persist(d.store.PutPane(info))
	d.persist(d.store.PutTab(src))
	d.persist(d.store.PutTab(t))
	d.persist(d.store.PutGroup(g))
	d.publish(&pb.StateUpdate{
		Groups: []*pb.Group{proto.CloneOf(g)},
		Tabs:   []*pb.Tab{proto.CloneOf(src), proto.CloneOf(t)},
		Panes:  []*pb.Pane{info},
	})
	return t, nil
}

// paneChangedIfLive publishes a pane change unless the pane has been closed
// meanwhile (which would otherwise resurrect it in the store). Callers hold d.mu.
func (d *Daemon) paneChangedIfLive(p *pane) {
	if d.panes[p.id] == p {
		d.paneChanged(p)
	}
}

// setColors records the client's terminal colours and gives them to every
// pane's mirror, which answers colour queries. Callers hold d.mu.
func (d *Daemon) setColors(m *pb.SetColors) {
	c := vt.Colors{Foreground: m.Foreground, Background: m.Background, Cursor: m.Cursor, Palette: m.Palette, Dark: m.Dark}
	if old := d.colors.Load(); old != nil && reflect.DeepEqual(*old, c) {
		return
	}
	d.colors.Store(&c)
	if b, err := json.Marshal(c); err == nil {
		d.persist(d.store.SetSetting("colors", string(b)))
	}
	for _, p := range d.panes {
		p.mu.Lock()
		if p.mirror != nil {
			p.mirror.SetColors(c)
		}
		p.mu.Unlock()
	}
}

// loadColors restores the last client's colours, so queries are answered
// right after a daemon restart, before any client connects.
func (d *Daemon) loadColors() {
	var c vt.Colors
	if s := d.store.Setting("colors"); s != "" && json.Unmarshal([]byte(s), &c) == nil {
		d.colors.Store(&c)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
