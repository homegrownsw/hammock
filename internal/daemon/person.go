package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/homegrownsw/hammock/internal/codesign"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Person-only actions. What only you may do (answer an agent's request,
// pair a phone, share, link, set up remote access or SSH, save a schedule
// without asking, …) is taken directly only from a process positively
// known to be you:
//
//   - the Hammock app (or its menu bar item), by its code signature (or,
//     signed ad hoc, by being the program of the app this daemon is in),
//     proven with a Mach message from the process at the other end of the
//     connection (appproof.go), never by the socket's peer alone;
//   - on Unraid, its plugin page, by a key only it reads (person_linux.go;
//     nominal: everything there runs as root).
//
// Where a process runs (its ancestors, its terminal, an ssh session) is no
// proof that it's you: a program of yours (an agent) chooses its own
// ancestors, environment and terminal (`launchctl submit`, a pty of its
// own, `setsid`, systemd-run). So from anything else, hmk in a pane's
// shell or a terminal included, these ask you (approval.go): you allow
// them in the app or on your phone, once.
//
// Until something could answer (no phone paired, no computer linked, the
// app never yet open), a program with a terminal that isn't an agent's
// does them directly (bootstrap.go), and the first phone or computer
// paired or linked after that is told so in Needs You.
//
// Agents are refused them outright (agentCheck). For everything else
// agent scoping applies (caller.go): a pane's own shell, and a program in
// a terminal with no agent or pane above it, are taken as yours; anything
// else (detached from its pane, found only by its terminal or
// multiplexer, a launchd job) is scoped as an agent (scope).

var (
	errPersonOnly  = errors.New("only you can do that: do it in the Hammock app (or ask from a terminal and allow it there or on your phone)")
	errAnswerInApp = errors.New("only you can answer that, in the Hammock app or on your paired phone")
	errNobodyToAsk = errors.New("only you can do that, and nothing can ask you yet (the Hammock app isn't open, and no phone is paired): open the app, or run it in a terminal of your own")
)

// personOnly reports whether msg is only for a person, from this computer.
func personOnly(msg *pb.ClientMessage) bool {
	switch msg.Msg.(type) {
	case *pb.ClientMessage_RespondGrant, *pb.ClientMessage_SaveSchedule, *pb.ClientMessage_DeleteSchedule:
		// A person's answer, and a schedule saved without asking (an agent's asks).
		return true
	case *pb.ClientMessage_ShutdownDaemon, *pb.ClientMessage_EndScreenSession, *pb.ClientMessage_ServerStatus,
		*pb.ClientMessage_ClearAttention, *pb.ClientMessage_SetColors:
		// Nothing anyone of yours couldn't do anyway (stopping hmkd is a
		// kill away; ending a screen session only takes access away), and
		// what scripts ask for (Unraid's rc.hammock stops the daemon).
		return false
	}
	return !agentReachable(msg)
}

// plainAllowed reports whether msg, refused to agents, is still yours to
// send without asking from a pane's shell or a terminal (not the app): it
// does no more than what those may anyway (typing into a browser pane as
// into any pane, reopening a tab, reading or resuming a session whose
// files are yours to read, listing devices and displays, a stream's
// acks), so asking you each time would only teach you to say yes.
func plainAllowed(msg *pb.ClientMessage) bool {
	switch msg.Msg.(type) {
	case *pb.ClientMessage_BrowserInput, *pb.ClientMessage_BrowserMenuChoice, *pb.ClientMessage_ReopenClosed,
		*pb.ClientMessage_ResumeSession, *pb.ClientMessage_ReadSession, *pb.ClientMessage_ListDevices,
		*pb.ClientMessage_DiscoverComputers, *pb.ClientMessage_SetComputerTint, *pb.ClientMessage_ListDisplays,
		*pb.ClientMessage_MediaAck, *pb.ClientMessage_CloseMedia, *pb.ClientMessage_SetMediaView:
		return true
	}
	return false
}

// personStrict reports whether msg is an answer only you give, in the app
// or on a paired phone: answering a request (an agent's, or one of these),
// a Hammock Key request, the app's word that it wrote the config file,
// clearing a security warning. Asking you about one would let whatever
// asks answer for you.
func personStrict(msg *pb.ClientMessage) bool {
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_RespondGrant, *pb.ClientMessage_RespondHammockKey, *pb.ClientMessage_ConfigWritten:
		return true
	case *pb.ClientMessage_ClearAttention:
		return strings.HasPrefix(m.ClearAttention.Id, configAttentionPrefix) || strings.HasPrefix(m.ClearAttention.Id, bootstrapAttentionPrefix)
	}
	return false
}

// agentReachable reports whether agentCheck has a rule for msg: everything
// else it refuses agents outright (errAgentOnly).
func agentReachable(msg *pb.ClientMessage) bool {
	switch msg.Msg.(type) {
	case *pb.ClientMessage_Hello, *pb.ClientMessage_Subscribe, *pb.ClientMessage_DetachPane,
		*pb.ClientMessage_Presence, *pb.ClientMessage_RequestGrant, *pb.ClientMessage_RespondGrant, *pb.ClientMessage_ListAudit,
		*pb.ClientMessage_ReadPane, *pb.ClientMessage_AttachPane, *pb.ClientMessage_Focus,
		*pb.ClientMessage_WatchAgent, *pb.ClientMessage_ReadAgentFile, *pb.ClientMessage_BrowserLogs,
		*pb.ClientMessage_WatchLog, *pb.ClientMessage_ReadLog, *pb.ClientMessage_ClearLog,
		*pb.ClientMessage_ReadEditor, *pb.ClientMessage_EditorCommand, *pb.ClientMessage_EditorPush,
		*pb.ClientMessage_EditorSelect, *pb.ClientMessage_ListFiles,
		*pb.ClientMessage_ReadDiff, *pb.ClientMessage_ReadDiffFile, *pb.ClientMessage_DiffCommand,
		*pb.ClientMessage_CreatePane, *pb.ClientMessage_CreateTab, *pb.ClientMessage_CreateGroup,
		*pb.ClientMessage_MoveGroup, *pb.ClientMessage_StartAgent, *pb.ClientMessage_ListFolders,
		*pb.ClientMessage_CloseGroup, *pb.ClientMessage_SaveSchedule, *pb.ClientMessage_DeleteSchedule,
		*pb.ClientMessage_RunSchedule, *pb.ClientMessage_SetSchedulePaused, *pb.ClientMessage_SkipScheduleRun,
		*pb.ClientMessage_ShowOutput, *pb.ClientMessage_Input, *pb.ClientMessage_SignalPane,
		*pb.ClientMessage_Resize, *pb.ClientMessage_AgentAnswer, *pb.ClientMessage_AgentSend,
		*pb.ClientMessage_SwitchAgent, *pb.ClientMessage_BreakOutPane, *pb.ClientMessage_ClosePane,
		*pb.ClientMessage_ReplacePane, *pb.ClientMessage_BrowserNavigate, *pb.ClientMessage_BrowserCall,
		*pb.ClientMessage_SimulatorCall, *pb.ClientMessage_RenameTab, *pb.ClientMessage_SetLayout,
		*pb.ClientMessage_FloatPane, *pb.ClientMessage_MoveTab, *pb.ClientMessage_RenameGroup,
		*pb.ClientMessage_SetGroupIcon, *pb.ClientMessage_SetGroupFolder, *pb.ClientMessage_SetGroupIconImage,
		*pb.ClientMessage_ListSessions, *pb.ClientMessage_SessionExcerpts, *pb.ClientMessage_ReadGroupIcon,
		*pb.ClientMessage_CloseTab, *pb.ClientMessage_MergeTab:
		return true
	}
	return false
}

// personCheck lets a person-only message through from the app, and asks
// you about one from anything else (approval.go: it waits for your answer,
// errDeferred, and is handled once you allow it). Answers (personStrict)
// are never asked about: refused. Before anything could ask you, a
// terminal of yours does them directly (bootstrap.go). Agents' messages
// were checked by agentCheck: an agent is refused these outright; a caller
// scoped as one (not positively you) asks, as anyone does.
func (c *conn) personCheck(msg *pb.ClientMessage) error {
	c.scope()
	cl := c.caller
	if c.remote || c.personOK.Load() || cl.isAgent() && (!cl.scoped || agentReachable(msg)) {
		return nil
	}
	if plainAllowed(msg) {
		if !cl.isAgent() {
			return nil
		}
		switch msg.Msg.(type) {
		case *pb.ClientMessage_ListDevices:
			// A read of nothing secret (devices' names, remote access's
			// networks): what state.db and the config file already
			// say to anything running as you. A script's `hmk status` (cron,
			// launchd, ssh without a terminal: scoped) gets them.
			return nil
		}
		// The rest do something (typing into a browser pane, resuming a
		// session, looking for computers): from a scoped caller, refused
		// rather than asked, as not worth a question each time.
		return errPersonOnly
	}
	strict := personStrict(msg)
	if !strict && !personOnly(msg) {
		return nil
	}
	if c.bootstrap(msg) {
		return nil
	}
	if strict {
		c.d.log.Info("refused a person-only answer from a process that isn't the app", "pid", c.pid, "msg", msgName(msg))
		return errAnswerInApp
	}
	c.d.mu.Lock()
	open := c.d.bootstrapOpen()
	c.d.mu.Unlock()
	if open {
		return errNobodyToAsk
	}
	// (With the app closed and nothing paired, once the window has shut:
	// it waits for you to open the app and allow it, or times out.)
	return c.askPerson(msg)
}

// scope works out, before a local connection's first message is handled,
// who it is: the app (personOK), yours as far as agent scoping goes (a
// pane's own shell, a terminal with nothing of an agent's above it), or
// else scoped as an agent (agentCheck): a program that left its pane, a
// launchd job, an agent outside Hammock, something under a multiplexer. It
// may still read your Groups and open panes, and controls those it
// opened; typing into, closing or driving any other pane asks you, as an
// agent's request does. Read loop only.
func (c *conn) scope() {
	c.personOnce.Do(func() {
		if c.remote || c.caller.isAgent() {
			return
		}
		if c.d.positivelyPerson(c.caller, c.pid, c.proven) {
			c.personOK.Store(true)
			return
		}
		if c.d.plainCaller(c.caller, c.pid, c.tok) {
			return
		}
		cl := c.d.scopedCaller(c.caller, c.pid)
		c.d.mu.Lock()
		c.caller = cl // (read by other connections under d.mu)
		c.d.mu.Unlock()
		// Which program, and what started it: a pid alone is gone by the time
		// anyone reads the log. Once per program and parent: something that
		// polls (every few seconds, for hours) would otherwise fill the log.
		prog, parent := processPath(c.pid), ""
		if pp, err := procinfo.Parent(c.pid); err == nil {
			parent = processPath(pp)
		}
		if _, seen := scopedLogged.LoadOrStore(prog+"\x00"+parent, true); seen {
			c.d.log.Debug("scoped a local client it can't place as yours as an agent", "pid", c.pid, "pane", cl.pane, "program", prog, "parent", parent)
			return
		}
		c.d.log.Info("scoped a local client it can't place as yours as an agent (once per program)", "pid", c.pid, "pane", cl.pane, "program", prog, "parent", parent)
	})
}

// scopedLogged holds the programs (and their parents) scope has logged.
var scopedLogged sync.Map

// processPath is pid's program, or "?" when it can't be read.
func processPath(pid int) string {
	if p, err := procinfo.Path(pid); err == nil && p != "" {
		return p
	}
	if n, err := procinfo.Name(pid); err == nil && n != "" {
		return n
	}
	return "?"
}

// scopedCaller is who a local process that isn't yours is, for agent
// scoping: an agent in the pane it was found in (by terminal, multiplexer,
// or the holder that took it in), or, on macOS, in the pane whose holder
// is responsible for it (however it left its parents); else outsidePane.
func (d *Daemon) scopedCaller(cl *caller, pid int) *caller {
	if cl != nil && cl.pane != "" {
		return &caller{pane: cl.pane, agent: true, scoped: true}
	}
	if r := responsibleFor(pid); r > 0 && r != pid {
		if id := d.paneHolders()[r]; id != "" {
			return &caller{pane: id, agent: true, scoped: true}
		}
		if id := d.panePIDs()[r]; id != "" {
			return &caller{pane: id, agent: true, scoped: true}
		}
	}
	return &caller{pane: outsidePane, agent: true, scoped: true}
}

// msgName is a message's kind, for the log.
func msgName(msg *pb.ClientMessage) string {
	if msg.Msg == nil {
		return "none"
	}
	return strings.TrimPrefix(fmt.Sprintf("%T", msg.Msg), "*hammockv1.ClientMessage_")
}

// personHook, when set (tests), stands in for positivelyPerson; plainHook
// for plainCaller. selfIsPerson: whether hmkd's own process is you (tests
// of the app's proof, whose stand-in app is the test's own process, turn it
// off).
var (
	selfIsPerson = true

	personHook func(cl *caller, pid int) bool
	plainHook  func(cl *caller, pid int) bool
)

// positivelyPerson reports whether a local connection's process (cl as
// identify placed it, pid) is the Hammock app, or Unraid's plugin page.
// The app, only by its proof (appproof.go: proven, a Mach message from the
// app's own process, the one at the other end of the connection): never by
// the socket's peer, which a program of yours can make look like the app by
// exec'ing a suspended copy of it in its own pid.
func (d *Daemon) positivelyPerson(cl *caller, pid int, proven bool) bool {
	if personHook != nil {
		return personHook(cl, pid)
	}
	if cl != nil || pid <= 0 {
		return false // (the app is in no pane)
	}
	if pid == os.Getpid() && selfIsPerson {
		// hmkd's own process (a test running it, in-process): a socket's
		// peer is this pid only if this process used that end itself, and
		// hmkd takes no descriptors from anything (no SCM_RIGHTS).
		return true
	}
	return proven || personPage(pid)
}

// plainCaller reports whether a local process that isn't the app is yours
// as far as agent scoping goes (never for what only you may do): a pane's
// own shell's (found by its ancestors, no agent between), or one in a
// terminal outside Hammock with no agent, pane or Hammock process above it
// and, on macOS, no pane's holder responsible for it. A guardrail, not a
// proof: a program of yours can give itself a terminal.
func (d *Daemon) plainCaller(cl *caller, pid int, tok codesign.Token) bool {
	if plainHook != nil {
		return plainHook(cl, pid)
	}
	if tok.Valid() && !tok.Live() {
		return false
	}
	if cl != nil {
		return cl.pane != "" && cl.direct && !cl.agent
	}
	if pid <= 0 || procinfo.TTY(pid) < 0 {
		return false
	}
	stop := d.hammockPIDs()
	if r := responsibleFor(pid); r > 0 && r != pid && stop[r] {
		return false // started in a pane, however it left it
	}
	return noAgentAbove(pid, stop, realTree)
}

// hammockPIDs are this daemon, its panes' holders and their shells.
func (d *Daemon) hammockPIDs() map[int]bool {
	stop := map[int]bool{os.Getpid(): true}
	for p := range d.panePIDs() {
		stop[p] = true
		if h, err := procinfo.Parent(p); err == nil && h > 1 {
			stop[h] = true // its holder
		}
	}
	d.mu.Lock()
	for _, p := range d.panes {
		p.mu.Lock()
		if p.holderPID > 0 {
			stop[p.holderPID] = true
		}
		p.mu.Unlock()
	}
	d.mu.Unlock()
	return stop
}

// procTree is what noAgentAbove reads of processes (tests fake it).
type procTree struct {
	parent func(pid int) (int, error)
	agent  func(pid int) bool
}

var realTree = procTree{parent: procinfo.Parent, agent: isAgentProcess}

// noAgentAbove reports whether pid and its ancestors include no agent, no
// pane and no Hammock process, all the way up.
func noAgentAbove(pid int, stop map[int]bool, t procTree) bool {
	cur := pid
	for i := 0; i < 64; i++ {
		if cur <= 1 {
			return true
		}
		if stop[cur] || t.agent(cur) {
			return false
		}
		next, err := t.parent(cur)
		if err != nil || next == cur {
			return false
		}
		cur = next
	}
	return false
}

// daemonInstall is the app this daemon runs from (…/Hammock.app), or ""
// outside one.
func daemonInstall() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if i := strings.Index(exe, ".app/"); i >= 0 {
		return exe[:i+len(".app")]
	}
	return ""
}
