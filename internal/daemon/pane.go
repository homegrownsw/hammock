package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	"github.com/homegrownsw/hammock/internal/holder"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/state"
	"github.com/homegrownsw/hammock/internal/vt"
)

// pane is the daemon's runtime for one terminal pane.
type pane struct {
	d        *Daemon
	id       string
	extraEnv map[string]string

	mu     sync.Mutex
	info   *pb.Pane
	socket string
	token  []byte // the holder's client secret (nil: an older holder without one)
	hc     *holder.Client
	mirror vt.Mirror
	osc    vt.OSC
	offset uint64 // next byte offset expected from the holder
	// Kitty images sent as a file path, inlined for clients that can't
	// open it (vt.KittyFiles); what clients get is then longer than what
	// the holder sent, so their offsets count what they've been sent.
	kitty     vt.KittyFiles
	outOffset uint64
	attached  map[*conn]struct{}
	sizes     map[*conn][2]uint32
	driver    *conn
	pid       int
	gen       int // bumps on every holder (re)connection; stale readers exit
	closed    bool
	hookAt    time.Time      // last hook event; heuristics stand down while fresh
	asking    *pb.AgentBlock // a question from a PreToolUse hook, until answered (often not yet in the transcript)
	// The agent's turn was started by a background task reporting progress
	// (a Monitor's event), not by you or by the task ending: its finish
	// isn't news (agents.go).
	progressTurn bool
	// When the pane's agent process began: background shells from before
	// it (a resumed session's) are gone.
	agentStart time.Time
	ops        chan func(*holder.Client) // queued writes to hc, drained off-lock
	connecting bool                      // a holder is starting (spawnAsync): ops waits for it, hc is nil
	holderPID  int                       // a starting holder's process (its program runs under it), until pid is known
	handover   bool                      // closed for the next daemon to take over (detachHolder): holders keep running
	cellW      int                       // the last cell size a client gave (setCellSize)
	cellH      int
	seed       []byte    // saved screen to replay into the next mirror (reboot restore)
	resume     []byte    // saved screen to rebuild the mirror from on reconnect…
	resumeAt   uint64    // …and the holder offset it was saved at
	savedAt    time.Time // when the scrollback snapshot was last written

	// awaitSize, set before a new pane spawns, has its holder hold the
	// program back until a client says the pane's size (or this long);
	// awaitingSize until one has, whose first resize then goes to the
	// holder even if it's the size it started at: it's the go-ahead.
	awaitSize    time.Duration
	awaitingSize bool

	// saveMu orders writing the saved screen against removing it: a save
	// that took its snapshot before the pane closed mustn't write the
	// files back after closePane removed them. discarded (under it): gone.
	saveMu    sync.Mutex
	discarded bool
	lastOut   time.Time
	log       *logPane // set for a log pane: its parsed entries
	// The --allowedTools rule for the tool its agent last asked to use
	// (Always Allow for This Schedule, schedules.go).
	pendingTool string
	askedFor    string    // Claude's own words for its permission prompt ("…permission to use Bash")
	askedRule   string    // pendingTool as it was when that prompt came (later tool calls don't change it)
	typedBy     paneInput // who typed into it lately (inputsource.go)
}

func newPane(d *Daemon, info *pb.Pane) *pane {
	return &pane{
		d: d, id: info.Id, info: info,
		socket:   filepath.Join(d.cfg.Dir, "run", "panes", info.Id+".sock"),
		attached: map[*conn]struct{}{}, sizes: map[*conn][2]uint32{},
	}
}

func (p *pane) snapshotInfo() *pb.Pane {
	p.mu.Lock()
	defer p.mu.Unlock()
	return proto.CloneOf(p.info)
}

func (p *pane) tabID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.TabId
}

func (p *pane) setTab(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.info.TabId = id
}

func (p *pane) cwd() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Cwd
}

// keepEnv is what panes inherit from the daemon. Everything else is dropped,
// so whatever launched the daemon (a terminal, an agent's shell) doesn't leak
// into panes: the login shell builds the rest from the user's dotfiles, as in
// a terminal launched from the Dock.
var keepEnv = map[string]bool{
	"HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "PATH": true, "TMPDIR": true,
	"LANG": true, "SSH_AUTH_SOCK": true, "__CF_USER_TEXT_ENCODING": true,
}

// baseEnv is the daemon's environment filtered by keepEnv.
func baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if keepEnv[k] || strings.HasPrefix(k, "LC_") {
			env = append(env, kv)
		}
	}
	return env
}

// env builds the child environment.
func (p *pane) env() []string {
	env := append(baseEnv(),
		"TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=Hammock",
		// Claude Code only draws images (Kitty graphics) and uses synchronized
		// output in terminals it knows by name; Hammock supports both.
		"CLAUDE_CODE_FORCE_TERMINAL_IMAGES=1", "CLAUDE_CODE_FORCE_SYNC_OUTPUT=1",
		"TERM_PROGRAM_VERSION="+ProductVersion,
		"HMK_PANE_ID="+p.id,
		"HMK_SOCKET="+SocketPath(p.d.cfg.Dir),
		"HMK_HOOK_SOCKET="+HookSocketPath(p.d.cfg.Dir),
	)
	// A β's (or a scratch) config, so hmk in its panes changes that one.
	if dir := hammockconfig.Dir(); dir != hammockconfig.DefaultDir() {
		env = append(env, "HMK_CONFIG_DIR="+dir)
	}
	env = append(env, p.d.sshHub.paneEnv()...) // (after the daemon's own: the last one counts)
	for k, v := range p.extraEnv {
		env = append(env, k+"="+v)
	}
	return env
}

// spawn starts a pane-holder for this pane and connects to it, waiting for
// it to answer. If command is nil the pane's recorded command is used.
func (p *pane) spawn(command []string) error {
	h, err := p.startHolder(command)
	if err != nil {
		return err
	}
	return p.connectHolder(h)
}

// spawnAsync starts a pane-holder like spawn, but connects to it in the
// background, so callers holding the daemon lock don't wait for it (normally
// ~20 ms, up to holderConnectTimeout). Until it answers, writes (input,
// resizes) queue for it and clients that attach wait for its first snapshot;
// if it never does, the pane shows as failed.
func (p *pane) spawnAsync(command []string) error {
	h, err := p.startHolder(command)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.connecting = true
	p.ops = make(chan func(*holder.Client), holderOpsQueue) // drained once linked (linkLocked)
	p.holderPID = h.proc.Pid
	p.mu.Unlock()
	go func() {
		if err := p.connectHolder(h); err != nil {
			p.connectFailed(err)
		}
	}()
	return nil
}

// holderConnectTimeout is how long a new holder has to start listening.
var holderConnectTimeout = 5 * time.Second

// holderOpsQueue bounds the writes queued for a holder.
const holderOpsQueue = 4096

// startedHolder is a pane-holder startHolder started, for connectHolder.
type startedHolder struct {
	proc  *os.Process
	token []byte
	// exited is closed when the process ends, with its state in state: a
	// holder that ends before it listens has failed, and needn't be waited
	// for.
	exited chan struct{}
	state  *os.ProcessState
	log    string // its log file (its stderr), to say why
	logAt  int64  // where this holder's lines start in it (it's appended to)
}

// startHolder starts the pane-holder process and hands it its secret.
func (p *pane) startHolder(command []string) (*startedHolder, error) {
	p.mu.Lock()
	info := p.info
	if command == nil {
		command = info.Command
	}
	cols, rows, cwd := info.Cols, info.Rows, info.Cwd
	await := p.awaitSize
	p.awaitingSize = await > 0
	p.mu.Unlock()
	command = viaLoginShell(command, loginShell())

	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		cwd, _ = os.UserHomeDir()
	}
	// A fresh secret for the holder, handed over on a pipe (fd 3) so it's in
	// neither its arguments nor the environment its program inherits.
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	token := []byte(hex.EncodeToString(secret))
	tr, tw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer tr.Close()
	args := []string{
		"--socket", p.socket, "--dir", cwd, "--token-fd", "3",
		"--daemon", daemonExe(),
		"--cols", fmt.Sprint(cols), "--rows", fmt.Sprint(rows),
	}
	if await > 0 {
		args = append(args, "--await-size", await.String())
	}
	if dir, err := filepath.Abs(p.d.cfg.Dir); err == nil {
		// Only this daemon, holding its state dir's locks, may attach: not
		// another hmkd started on a copy of the state (holder/statelock.go).
		args = append(args, "--state", dir)
	}
	args = append(args, "--")
	args = append(args, command...)
	logPath := p.holderLogPath()
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	var logAt int64
	if err == nil {
		defer logf.Close()
		if st, err := logf.Stat(); err == nil {
			logAt = st.Size() // earlier holders' lines (the pane's before a restart) aren't this one's
		}
	} else {
		logf = nil
	}
	proc, err := spawnHolder(p.d.cfg.HolderPath, args, p.env(), cwd, logf, tr)
	if err != nil {
		tw.Close()
		return nil, fmt.Errorf("start holder: %w", err)
	}
	h := &startedHolder{proc: proc, token: token, exited: make(chan struct{}), log: logPath, logAt: logAt}
	go func() { // reap if the holder exits while we're its parent
		h.state, _ = proc.Wait()
		close(h.exited)
	}()
	_, _ = tw.Write(token)
	tw.Close()
	return h, nil
}

// connectHolder waits for a holder started by startHolder to listen, then
// links the pane to it.
func (p *pane) connectHolder(h *startedHolder) error {
	proc, token := h.proc, h.token
	deadline := time.Now().Add(holderConnectTimeout)
	for {
		// Only once the socket is the new holder's (dialHolder's rule): until
		// it has replaced what was at its path (a link, put there by
		// whatever copied this state), a dial could reach another holder.
		hc, err := p.dialHolder(p.socket, 0, token)
		if err == nil {
			// Recorded first, so a daemon taking over (detachHolder) while
			// this runs finds the holder to reconnect to.
			p.d.persist(p.d.store.PutRuntime(state.PaneRuntime{PaneID: p.id, Socket: p.socket, Token: string(token)}))
			if p.log != nil {
				p.log.restarted() // its output starts again at 0
			}
			p.mu.Lock()
			if p.closed {
				// Closed while it started (spawnAsync, so without the
				// daemon lock): end it, unless it's being handed over.
				handover := p.handover
				p.mu.Unlock()
				if !handover {
					_ = hc.CloseHolder()
					p.d.persist(p.d.store.DeleteRuntime(p.id))
				}
				hc.Close()
				return nil
			}
			p.token = token
			p.info.Exited, p.info.ExitStatus = false, 0
			p.info.StartError = ""
			gen := p.attachHolderLocked(hc)
			p.mu.Unlock()
			go p.read(hc, gen)
			return nil
		}
		select {
		case <-h.exited:
			if h.state == nil {
				break // couldn't wait for it: don't take that for its end
			}
			// Ended before we reached it (it listens, then exits if its
			// program can't start: a command that isn't found): say how,
			// and what it logged, rather than that its socket isn't there.
			removeStaleSocket(p.socket)
			why := "the pane-holder ended before the terminal started (" + h.state.String() + ")"
			if l := lastLogLine(h.log, h.logAt); l != "" {
				why += ": " + l
			}
			return errors.New(why)
		default:
		}
		if time.Now().After(deadline) {
			_ = proc.Kill() // don't leave an unreachable holder behind
			removeStaleSocket(p.socket)
			return fmt.Errorf("the pane-holder didn't answer within %v: %w", holderConnectTimeout, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// lastLogLine is the last line a holder wrote to its log (its stderr) at or
// after offset from, shortened, or "" if it wrote none.
func lastLogLine(path string, from int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	st, err := f.Stat()
	if err != nil || st.Size() <= from {
		return ""
	}
	_, _ = f.Seek(max(from, st.Size()-int64(len(buf))), 0)
	n, _ := f.Read(buf)
	lines := strings.Split(strings.TrimSpace(string(buf[:n])), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if r := []rune(l); len(r) > 300 {
		l = string(r[:300]) + "…"
	}
	return l
}

// connectFailed marks a pane whose holder never answered (spawnAsync) as
// failed, saying why on its screen.
func (p *pane) connectFailed(err error) {
	p.d.log.Error("pane's holder didn't start", "pane", p.id, "err", err)
	if !p.markStartFailed(err) {
		return
	}
	p.d.mu.Lock()
	p.d.startFailedLocked(p)
	p.d.mu.Unlock()
}

// markStartFailed marks a pane whose terminal couldn't be started as ended,
// saying why in red on its screen (under what it showed before, if it's
// being started again). It reports false for a pane already closed.
func (p *pane) markStartFailed(err error) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.connecting = false
	p.ops = nil // nothing will drain it
	p.info.Exited, p.info.ExitStatus = true, -1
	p.info.StartError = err.Error()
	p.mirror = vt.New(int(p.info.Cols), int(p.info.Rows))
	if c := p.d.colors.Load(); c != nil {
		p.mirror.SetColors(*c)
	}
	if len(p.seed) > 0 {
		p.mirror.Write(p.seed)
		p.mirror.Write([]byte("\r\n"))
		p.seed = nil
	}
	p.mirror.Write([]byte("\x1b[0;31mHammock couldn't start this pane's terminal: " + err.Error() + "\x1b[0m\r\n"))
	p.sendSnapshotsLocked()
	return true
}

// startFailedLocked tells the rest of the daemon a pane's terminal couldn't
// start (markStartFailed), as if its program had failed (read's exit
// handling): a scheduled run in it ends, and so does its agent. Callers
// hold d.mu.
func (d *Daemon) startFailedLocked(p *pane) {
	if d.panes[p.id] == p {
		d.scheduleCommandExited(p, -1)
		d.paneChanged(p)
		d.agentHub.paneExited(p, -1)
	}
}

// sendSnapshotsLocked sends the screen to every attached client (one that
// attached before there was a screen). Callers hold p.mu.
func (p *pane) sendSnapshotsLocked() {
	for c := range p.attached {
		c.send(p.snapshotMsgLocked())
	}
}

func (p *pane) snapshotMsgLocked() *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_PaneSnapshot{PaneSnapshot: &pb.PaneSnapshot{
		PaneId: p.id, Data: p.mirror.Snapshot(), Offset: p.offset,
		Cols: p.info.Cols, Rows: p.info.Rows,
	}}}
}

// dialHolder dials the pane's holder at socket again (a daemon restarted,
// or the connection dropped), only where this daemon starts them: its own
// run/panes, the socket itself, not a link (ownSocket). The holder decides
// whether it takes this daemon (holder/statelock.go); a socket put there by
// something else is still a residual: it would see this pane's input, as
// anything of yours that changes your shell's startup files could.
func (p *pane) dialHolder(socket string, from uint64, token []byte) (*holder.Client, error) {
	if !p.d.ownSocket(socket, p.id) {
		return nil, fmt.Errorf("%s: not this pane's holder socket in this state dir, or not a socket", socket)
	}
	return holder.Dial(socket, from, token)
}

// reconnect attaches to an already-running holder (after a daemon restart).
// token is its secret ("" for a holder started before holders had one).
func (p *pane) reconnect(socket, token string) error {
	p.socket = socket
	p.mu.Lock()
	p.token = nil
	if token != "" {
		p.token = []byte(token)
	}
	p.mu.Unlock()
	// Rebuild the mirror from the snapshot saved at shutdown and stream only
	// what came after it: the holder's buffer alone loses everything older
	// than it, including modes a program set long ago (bracketed paste, the
	// kitty keyboard), which clients then get wrong.
	snap, from, saved := p.savedSnapshot()
	hc, err := p.dialHolder(socket, from, p.token)
	if err != nil {
		return err // gone: the caller starts a new process (and seeds it)
	}
	p.mu.Lock()
	// An offset past what this holder has written is from an earlier process.
	if saved && from <= hc.Info.RingEnd {
		p.resume, p.resumeAt = snap, from
	}
	// An exit while the daemon was down isn't copied here: the holder
	// replays it, and read handles it as it would a live one (a scheduled
	// run ends, a clean exit closes the pane). One from before, already
	// in info, read skips.
	p.info.Cols, p.info.Rows = uint32(hc.Info.Cols), uint32(hc.Info.Rows)
	p.mu.Unlock()
	p.attachHolder(hc)
	return nil
}

func (p *pane) attachHolder(hc *holder.Client) {
	p.mu.Lock()
	gen := p.attachHolderLocked(hc)
	p.mu.Unlock()
	go p.read(hc, gen)
}

// attachHolderLocked builds the pane's mirror for hc and links it; the
// caller starts read with the generation returned. Callers hold p.mu.
func (p *pane) attachHolderLocked(hc *holder.Client) int {
	wasConnecting := p.connecting
	if p.mirror != nil {
		p.mirror.Close()
	}
	p.pid = int(hc.Info.PID)
	p.mirror = vt.New(int(hc.Info.Cols), int(hc.Info.Rows))
	if c := p.d.colors.Load(); c != nil {
		p.mirror.SetColors(*c)
	}
	if p.cellW > 0 && p.cellH > 0 {
		p.mirror.SetCellSize(p.cellW, p.cellH) // told before there was a mirror
	}
	if len(p.seed) > 0 {
		// Scrollback saved before a reboot: replay it ahead of the new process.
		p.mirror.Write(p.seed)
		p.mirror.Write([]byte("\r\n\x1b[0;2m── restored after restart ──\x1b[0m\r\n"))
		p.seed = nil
	}
	p.osc = vt.OSC{}
	p.kitty = vt.KittyFiles{}
	p.offset = hc.Info.RingStart
	if len(p.resume) > 0 {
		// The same process, reconnected: continue from the saved screen.
		p.mirror.Write(p.resume)
		p.offset = max(p.resumeAt, hc.Info.RingStart)
		p.resume = nil
	}
	gen := p.linkLocked(hc)
	if wasConnecting {
		// Resized while it started: the resize is queued for the holder.
		if p.info.Cols != uint32(hc.Info.Cols) || p.info.Rows != uint32(hc.Info.Rows) {
			p.mirror.Resize(int(p.info.Cols), int(p.info.Rows))
		}
		p.sendSnapshotsLocked() // clients that attached while it started
	}
	return gen
}

// linkLocked makes hc the pane's holder connection. All writes to it go
// through a queue drained by its own goroutine, so no lock is ever held
// across holder I/O (a program that stops reading stdin can't wedge the
// daemon). Returns the new generation. Callers hold p.mu.
func (p *pane) linkLocked(hc *holder.Client) int {
	ops := p.ops
	if p.connecting && ops != nil {
		// Writes queued while it started go to it first.
		p.connecting = false
	} else {
		if ops != nil {
			close(ops) // the old writer closes the old connection
		}
		ops = make(chan func(*holder.Client), holderOpsQueue)
	}
	p.hc, p.ops = hc, ops
	go func() {
		for op := range ops {
			op(hc)
		}
		hc.Close()
	}()
	if p.mirror != nil {
		p.mirror.SetResponder(func(b []byte) { p.postLocked(func(hc *holder.Client) { _ = hc.Input(b) }) })
	}
	p.gen++
	return p.gen
}

// postLocked queues a write to the holder without blocking; it reports false
// if there is no holder or the queue is full. Callers hold p.mu.
func (p *pane) postLocked(op func(*holder.Client)) bool {
	if p.ops == nil {
		return false
	}
	select {
	case p.ops <- op:
		return true
	default:
		return false
	}
}

// unlinkLocked drops the holder connection after queued writes. Callers hold p.mu.
func (p *pane) unlinkLocked() {
	if p.ops != nil {
		close(p.ops)
		p.ops, p.hc = nil, nil
	}
	p.connecting = false
}

func (p *pane) read(hc *holder.Client, gen int) {
	for {
		ev, err := hc.Next()
		if err != nil {
			p.mu.Lock()
			closing := p.closed || p.gen != gen
			p.mu.Unlock()
			if !closing {
				p.d.log.Warn("lost the pane's holder; reconnecting", "pane", p.id, "err", err)
			}
			p.holderLost(gen)
			return
		}
		if ev.Exited {
			p.mu.Lock()
			// A reconnect replays the exit; only act on it once.
			stale := p.gen != gen || p.closed || p.info.Exited
			if !stale {
				p.info.Exited, p.info.ExitStatus = true, ev.ExitStatus
			}
			p.mu.Unlock()
			if !stale {
				p.d.mu.Lock()
				if p.d.panes[p.id] == p {
					keep := p.d.scheduleCommandExited(p, ev.ExitStatus) // a scheduled command's run may stay to be read
					if ev.ExitStatus == 0 && p.log == nil && !keep {
						// Clean exit: close the pane, as terminals do. Failures
						// stay visible so the output can be read, and so do
						// logs (a command that printed its log and finished).
						_ = p.d.closePane(p.id)
					} else {
						p.d.paneChanged(p)
						p.d.agentHub.paneExited(p, ev.ExitStatus)
					}
				}
				p.d.mu.Unlock()
			}
			continue
		}
		p.mu.Lock()
		if p.gen != gen || p.closed {
			p.mu.Unlock()
			return
		}
		data := ev.Output
		if ev.Offset < p.offset {
			skip := p.offset - ev.Offset
			if skip >= uint64(len(data)) {
				p.mu.Unlock()
				continue
			}
			data, ev.Offset = data[skip:], p.offset
		}
		if p.log != nil {
			p.log.write(data, ev.Offset)
		}
		shown := p.kitty.Rewrite(data)
		p.mirror.Write(shown)
		p.osc.Write(data)
		p.offset = ev.Offset + uint64(len(data))
		p.lastOut = time.Now()
		if len(shown) > 0 {
			for c := range p.attached {
				c.sendOutput(p, p.outOffset, shown)
			}
			p.outOffset += uint64(len(shown))
		}
		changed := false
		if p.osc.Changed {
			p.osc.Changed = false
			if p.osc.Title != "" && p.osc.Title != p.info.Title {
				// A spinner frame isn't news: kept, but clients hear of it
				// with the next real change.
				changed = changed || !spinnerFrame(p.info.Title, p.osc.Title)
				p.info.Title = p.osc.Title
			}
			if p.osc.Cwd != "" && p.osc.Cwd != p.info.Cwd && isDir(p.osc.Cwd) {
				p.info.Cwd, changed = p.osc.Cwd, true
			}
		}
		p.mu.Unlock()
		if changed {
			p.d.mu.Lock()
			p.d.paneChangedIfLive(p)
			p.d.mu.Unlock()
		}
	}
}

// holderLost handles the holder connection dropping unexpectedly.
func (p *pane) holderLost(gen int) {
	p.mu.Lock()
	if p.gen != gen || p.closed {
		p.mu.Unlock()
		return
	}
	p.unlinkLocked() // closes the dead connection
	socket, from, token := p.socket, p.offset, p.token
	p.mu.Unlock()
	for i := 0; i < 20; i++ {
		hc, err := p.dialHolder(socket, from, token)
		if err == nil {
			p.mu.Lock()
			if p.closed || p.gen != gen {
				// Closed (or relinked) while we were reconnecting: make sure
				// the holder doesn't outlive the pane.
				p.mu.Unlock()
				if p.closed {
					_ = hc.CloseHolder()
				}
				hc.Close()
				return
			}
			g := p.linkLocked(hc)
			p.mu.Unlock()
			go p.read(hc, g)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.d.log.Warn("the pane's holder is gone", "pane", p.id, "socket", socket)
	p.mu.Lock()
	was := p.info.Exited
	p.info.Exited, p.info.ExitStatus = true, -1
	p.mu.Unlock()
	p.d.mu.Lock()
	if p.d.panes[p.id] == p && !was {
		// As if its program had failed: a scheduled run in it ends, and
		// so does its agent.
		p.d.scheduleCommandExited(p, -1)
		p.d.agentHub.paneExited(p, -1)
	}
	p.d.paneChangedIfLive(p)
	p.d.mu.Unlock()
}

// attach starts streaming to c: a snapshot, then live output.
func (p *pane) attach(c *conn) {
	p.mu.Lock()
	if p.info.Kind == pb.PaneKind_PANE_KIND_EDITOR {
		p.mu.Unlock()
		p.d.editors.attach(c, p.id)
		return
	}
	if p.info.Kind == pb.PaneKind_PANE_KIND_DIFF {
		p.mu.Unlock()
		p.d.diffs.attach(c, p.id) // watched while it's showing
		return
	}
	defer p.mu.Unlock()
	if p.mirror == nil {
		if p.connecting {
			p.attached[c] = struct{}{} // its snapshot comes once the holder answers
		}
		return
	}
	c.send(p.snapshotMsgLocked())
	p.attached[c] = struct{}{}
}

func (p *pane) detach(c *conn) {
	p.mu.Lock()
	if p.info.Kind == pb.PaneKind_PANE_KIND_EDITOR {
		p.mu.Unlock()
		p.d.editors.detach(c, p.id)
		return
	}
	if p.info.Kind == pb.PaneKind_PANE_KIND_DIFF {
		p.mu.Unlock()
		p.d.diffs.detach(c, p.id)
		return
	}
	defer p.mu.Unlock()
	delete(p.attached, c)
	delete(p.sizes, c)
	if p.driver == c {
		p.driver = nil
	}
}

// input writes to the PTY and makes c the active driver. Reports whether the
// pane's size changed as a result.
func (p *pane) input(c *conn, data []byte) (resized bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ops == nil || p.info.Exited { // no holder, nor one starting
		return false, errors.New("pane is not running")
	}
	resized = p.setDriverLocked(c)
	p.typedBy.noteLocked(inputSourceOf(c), time.Now())
	data = append([]byte(nil), data...)
	if !p.postLocked(func(hc *holder.Client) { _ = hc.Input(data) }) {
		return resized, errors.New("pane input queue is full")
	}
	return resized, nil
}

// focus makes c the active driver.
func (p *pane) focus(c *conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.setDriverLocked(c)
}

func (p *pane) setDriverLocked(c *conn) bool {
	if c == nil || p.driver == c {
		return false
	}
	p.driver = c
	if sz, ok := p.sizes[c]; ok {
		return p.applySizeLocked(sz[0], sz[1])
	}
	return false
}

// resize records c's viewport size and applies it if c is the active driver
// (or there is none). Reports whether the pane's size changed.
func (p *pane) resize(c *conn, cols, rows uint32) bool {
	if cols == 0 || rows == 0 {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c != nil {
		p.sizes[c] = [2]uint32{cols, rows}
		if p.driver != nil && p.driver != c {
			return false
		}
		p.driver = c
	}
	changed := p.applySizeLocked(cols, rows)
	if c != nil && p.awaitingSize {
		p.awaitingSize = false
		if !changed && !p.info.Exited {
			// Started at the right size: its holder still waits to hear it.
			p.postLocked(func(hc *holder.Client) { _ = hc.Resize(uint16(cols), uint16(rows)) })
		}
	}
	return changed
}

func (p *pane) applySizeLocked(cols, rows uint32) bool {
	if cols == p.info.Cols && rows == p.info.Rows {
		return false
	}
	p.info.Cols, p.info.Rows = cols, rows
	if p.mirror != nil {
		p.mirror.Resize(int(cols), int(rows))
	}
	if !p.info.Exited {
		p.postLocked(func(hc *holder.Client) { _ = hc.Resize(uint16(cols), uint16(rows)) })
	}
	return true
}

// setCellSize records a client's cell size in pixels, which programs see in
// size queries and Kitty graphics use (the last client to say wins).
func (p *pane) setCellSize(w, h int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cellW, p.cellH = w, h
	if p.mirror != nil {
		p.mirror.SetCellSize(w, h)
	}
}

func (p *pane) signal(sig syscall.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ops == nil {
		return errors.New("pane is not running")
	}
	p.postLocked(func(hc *holder.Client) { _ = hc.Signal(sig) })
	return nil
}

func (p *pane) text(scrollback bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mirror == nil {
		return ""
	}
	return p.mirror.Text(scrollback)
}

// bottomText is the screen's last n rows without faint text (vt.Mirror).
func (p *pane) bottomText(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mirror == nil {
		return ""
	}
	return p.mirror.BottomText(n)
}

// kill hangs up the child and stops the holder.
func (p *pane) kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.info.Kind == pb.PaneKind_PANE_KIND_BROWSER {
		p.d.browsers.close(p.id)
		go p.d.media.paneClosed(p.id)
		return
	}
	if p.info.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
		p.d.simulators.close(p.id, p.info.DeviceUdid)
		go p.d.media.paneClosed(p.id)
		return
	}
	if p.info.Kind == pb.PaneKind_PANE_KIND_EDITOR {
		p.d.editors.paneClosed(p.id)
		return
	}
	if p.info.Kind == pb.PaneKind_PANE_KIND_DIFF {
		p.d.diffs.paneClosed(p.id)
		return
	}
	p.postLocked(func(hc *holder.Client) { _ = hc.CloseHolder() })
	p.unlinkLocked()
	if p.mirror != nil {
		p.mirror.Close()
		p.mirror = nil
	}
	for c := range p.attached {
		delete(p.attached, c)
	}
}

// detachHolder drops the holder connection, leaving the holder running.
func (p *pane) detachHolder() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed, p.handover = true, true
	p.unlinkLocked()
}

// endHolder asks the holder to hang up its child and exit, then drops the
// connection; the pane itself stays, for the next daemon to start afresh.
// The returned channel closes once the holder has let go of its socket (so a
// new daemon starting at once can't reconnect to it as it goes), or there
// was no holder to ask; callers bound the wait.
func (p *pane) endHolder() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	done := make(chan struct{})
	if p.info.Kind == pb.PaneKind_PANE_KIND_BROWSER || p.info.Kind == pb.PaneKind_PANE_KIND_SIMULATOR || p.info.Kind == pb.PaneKind_PANE_KIND_EDITOR ||
		p.info.Kind == pb.PaneKind_PANE_KIND_DIFF {
		close(done) // no holder: their hosts go with the daemon (an editor's buffer is kept)
		return done
	}
	socket := p.socket
	// One still starting has no connection to take the request yet (nor
	// will it: connectHolder ends it once it answers).
	ended := !p.connecting && p.postLocked(func(hc *holder.Client) {
		_ = hc.CloseHolder()
		// It unlinks the socket before closing its clients.
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(socket); errors.Is(err, os.ErrNotExist) {
				break
			}
		}
		close(done)
	})
	if !ended {
		// Not connected (still starting) or its queue is full (stuck): end
		// it by its socket instead.
		go func() { _, _ = endHolderAt(socket); close(done) }()
	}
	p.unlinkLocked()
	return done
}

// refreshCwd polls the shell's cwd when the shell doesn't report OSC 7.
// Reports whether it changed.
func (p *pane) refreshCwd() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.osc.Cwd != "" || p.pid == 0 || p.info.Exited {
		return false
	}
	cwd, err := procinfo.Cwd(p.pid)
	if err != nil || cwd == p.info.Cwd {
		return false
	}
	p.info.Cwd = cwd
	return true
}

// refreshSubagents counts the agent's subagents still at work: their records
// (<session>/subagents/agent-<id>.meta.json) not finished, and their
// transcripts written lately (a killed session leaves records that never
// finish). Reports whether it changed.
func (p *pane) refreshSubagents() bool {
	p.mu.Lock()
	a := p.info.Agent
	path := ""
	if a != nil && !p.info.Exited {
		path = a.TranscriptPath
	}
	p.mu.Unlock()
	n := 0
	if path != "" {
		n = runningSubagents(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents"))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if a := p.info.Agent; a != nil && a.Subagents != int32(n) {
		a.Subagents = int32(n)
		return true
	}
	return false
}

// subagentQuiet: a subagent that hasn't written for this long isn't running.
const subagentQuiet = 5 * time.Minute

func runningSubagents(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		var m subagentMeta
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || json.Unmarshal(b, &m) != nil || m.Finished {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, strings.TrimSuffix(name, ".meta.json")+".jsonl"))
		if err == nil && time.Since(st.ModTime()) < subagentQuiet {
			n++
		}
	}
	return n
}

// shells are the programs whose children are "what's running" in a pane.
var shells = map[string]bool{"zsh": true, "bash": true, "fish": true, "sh": true, "dash": true, "ksh": true, "tcsh": true, "csh": true, "nu": true, "elvish": true, "xonsh": true}

// viaLoginShell runs a pane's command (hmk tab new -- CMD, create_pane)
// through the person's login shell, as its agents, scheduled tasks and log
// panes are: a daemon started at login (launchd) has only the system's PATH
// (/usr/bin:/bin:…), so a command from Homebrew, mise or ~/.local/bin isn't
// found otherwise, and the pane fails to start (#43). The shell execs it, so
// it's still the pane's process, with its arguments untouched. A command
// that's a shell already (a pane's own, or an agent's or log's wrapper) runs
// as it is, and so does any command under a shell that can't pass argv on
// (csh and the like).
func viaLoginShell(cmd []string, shell string) []string {
	if len(cmd) == 0 || shells[strings.TrimPrefix(filepath.Base(cmd[0]), "-")] {
		return cmd
	}
	var script string
	switch filepath.Base(shell) {
	case "zsh", "bash", "sh", "dash", "ksh":
		script = `exec "$@"`
	case "fish":
		script = `exec $argv`
	default:
		return cmd
	}
	// $0 (the name it reports errors as) is the command's own; fish has no
	// $0, so it takes every argument as $argv.
	out := []string{shell, "-l", "-c", script}
	if filepath.Base(shell) != "fish" {
		out = append(out, cmd[0])
	}
	return append(out, cmd...)
}

// refreshProcesses records what's running in a terminal besides its shell
// (by name), so clients can warn before closing it: the shell's children,
// or the pane's own command when it isn't a shell. Reports whether it changed.
func (p *pane) refreshProcesses() bool {
	if !p.isTerminal() {
		return false
	}
	p.mu.Lock()
	pid, exited, cmd := p.pid, p.info.Exited, p.info.Command
	p.mu.Unlock()
	var names []string
	// An agent's own shells (its commands, ! commands, background tasks):
	// how many, and when the newest began. MCP servers and the like aren't shells.
	agentShells, newest, agentStart := 0, time.Time{}, time.Time{}
	if !exited && pid != 0 {
		if len(cmd) > 0 && !shells[strings.TrimPrefix(filepath.Base(cmd[0]), "-")] {
			names = []string{filepath.Base(cmd[0])}
		} else {
			for _, c := range procinfo.Children(pid) {
				name, err := procinfo.Name(c)
				if err != nil {
					continue
				}
				// An agent by its harness's name (Claude Code's binary is
				// named after its version).
				h := agentName(c)
				if h != "" {
					name = h
				}
				names = append(names, name)
				if h == "" {
					continue
				}
				if st, err := procinfo.StartTime(c); err == nil {
					agentStart = st
				}
				for _, cc := range procinfo.Children(c) {
					if n, err := procinfo.Name(cc); err == nil && shells[strings.TrimPrefix(n, "-")] {
						agentShells++
						if st, err := procinfo.StartTime(cc); err == nil && st.After(newest) {
							newest = st
						}
					}
				}
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.agentStart = agentStart
	changed := false
	if !slices.Equal(names, p.info.Processes) {
		p.info.Processes = names
		changed = true
	}
	if a := p.info.Agent; a != nil {
		// A shell begun after the agent went idle is a command you ran (!).
		running := a.State == pb.AgentState_AGENT_STATE_IDLE && agentShells > 0 && newest.UnixMilli() > a.StateSinceUnixMs
		if a.Shells != int32(agentShells) || a.RunningCommand != running {
			a.Shells, a.RunningCommand = int32(agentShells), running
			changed = true
		}
	}
	return changed
}

// holderLogPath is the file the pane's holders write their stderr to.
func (p *pane) holderLogPath() string {
	return filepath.Join(p.d.cfg.Dir, "logs", "holder-"+p.id+".log")
}

func (p *pane) snapshotPath() string {
	return filepath.Join(p.d.cfg.Dir, "scrollback", p.id+".vt")
}

// discardFiles stops a pane closed for good (killed, and out of d.panes)
// keeping anything: a log pane's log, its saved screen and its holders'
// log. Reopen Closed starts a new pane, needing none of them.
func (p *pane) discardFiles() {
	if p.log != nil {
		p.log.close(true)
	}
	_ = os.Remove(p.holderLogPath())
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	p.discarded = true
	for _, ext := range []string{"", ".tmp", ".offset", ".offset.tmp"} {
		_ = os.Remove(p.snapshotPath() + ext)
	}
}

// saveScrollback writes the mirror's VT snapshot to disk if there has been
// output since the last save, so a reboot can restore the screen.
func (p *pane) saveScrollback(force bool) {
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	if p.discarded {
		return
	}
	p.mu.Lock()
	if p.mirror == nil || p.closed && !force || (!force && !p.lastOut.After(p.savedAt)) {
		p.mu.Unlock()
		return
	}
	data := p.mirror.Snapshot()
	off := p.offset
	p.savedAt = time.Now()
	p.mu.Unlock()
	path := p.snapshotPath()
	if replaceFile(path, data) == nil {
		// The holder offset the snapshot reflects, for resuming the stream,
		// and the snapshot's checksum: a crash between the two renames
		// leaves an offset that isn't this screen's, which savedSnapshot
		// then doesn't use (it would replay output the screen has).
		_ = replaceFile(path+".offset", fmt.Appendf(nil, "%d %08x", off, crc32.ChecksumIEEE(data)))
	}
}

// replaceFile writes data to path by way of path.tmp, so path has either
// its old contents or all of the new.
func replaceFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// savedSnapshot is the last saved screen and the holder offset it was taken
// at (written together by saveScrollback). An offset written for another
// screen (a crash between writing them) isn't used; one with no checksum
// (an older daemon's) is.
func (p *pane) savedSnapshot() ([]byte, uint64, bool) {
	path := p.snapshotPath()
	ob, err := os.ReadFile(path + ".offset")
	if err != nil {
		return nil, 0, false
	}
	offs, sum, checked := strings.Cut(strings.TrimSpace(string(ob)), " ")
	off, err := strconv.ParseUint(offs, 10, 64)
	if err != nil {
		return nil, 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil, 0, false
	}
	if checked && sum != fmt.Sprintf("%08x", crc32.ChecksumIEEE(b)) {
		return nil, 0, false
	}
	return b, off, true
}

// showEnded gives a pane whose program had ended before a reboot its saved
// screen back, with no process: nothing is run again.
func (p *pane) showEnded() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mirror = vt.New(int(p.info.Cols), int(p.info.Rows))
	if c := p.d.colors.Load(); c != nil {
		p.mirror.SetColors(*c)
	}
	if len(p.seed) > 0 {
		p.mirror.Write(p.seed)
		p.seed = nil
	}
	p.mirror.Write([]byte("\r\n\x1b[0;2m── ended before restart ──\x1b[0m\r\n"))
	p.sendSnapshotsLocked()
}

// loadScrollback queues a saved snapshot to seed the next mirror (a new
// process); its offset belonged to the old one.
func (p *pane) loadScrollback() {
	_ = os.Remove(p.snapshotPath() + ".offset")
	if b, err := os.ReadFile(p.snapshotPath()); err == nil {
		p.mu.Lock()
		p.seed = b
		p.mu.Unlock()
	}
}

// isDir reports whether path is an existing directory (an OSC 7 cwd is taken
// only if it is one).
func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// daemonExe is this daemon's executable, for its holders to know it by
// (pane-holder --daemon): they take no other program, token or not.
var daemonExe = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
})
