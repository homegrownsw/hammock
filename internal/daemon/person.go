package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexjsp/hammock/internal/procinfo"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Person-only actions. Agent scoping (caller.go) limits what a process it
// can place as an agent may do; but a process it can't place at all used to
// be taken for you, so an agent got your rights by leaving its pane: a
// double fork (reparented to launchd), `launchctl submit`, a tmux server, or
// a background job left running after it exits. Same-user processes can't
// be fenced off from each other, but the cheap ways out are closed: what
// only you may do (answer an agent's request, pair a phone, share, link,
// set up remote access or SSH, save a schedule without asking, …) is taken
// only from a process positively known to be you:
//
//   - the Hammock app, by its code signature (or, signed ad hoc, by being
//     the program of the app this daemon is in);
//   - a program whose ancestors reach a pane's shell, with no agent between
//     (found by its parents, not by the terminal it kept or a multiplexer);
//   - a program outside Hammock whose ancestors reach a login session (a
//     terminal app launched as an app, a console's login, sshd from
//     another computer, Unraid's web UI)
//     without passing an agent, a pane or Hammock itself.
//
// Anything else (detached, found only by its terminal or multiplexer, or
// not placed at all) is scoped as an agent (scope), and told to do these
// in the app or a pane's own shell.

var errPersonOnly = errors.New("only you can do that: do it in the Hammock app, or run it in a Hammock pane's own shell or in a terminal of your own (not from a background job, launchd, or a terminal multiplexer)")

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

// personCheck refuses a person-only message from a local process that
// isn't positively you. Agents' messages were checked by agentCheck (and
// anything not positively you is scoped as an agent: scope).
func (c *conn) personCheck(msg *pb.ClientMessage) error {
	c.scope()
	if c.remote || c.caller.isAgent() || !personOnly(msg) || c.personOK {
		return nil
	}
	c.d.log.Info("refused a person-only request from a process it can't tell is you", "pid", c.pid, "msg", msgName(msg))
	return errPersonOnly
}

// scope works out, before a local connection's first message is handled,
// whether it's positively you. Anything else not already known as an agent
// is scoped as one (agentCheck): a program that left its pane, a launchd
// job, an agent outside Hammock, something under a multiplexer. It may
// still read your Groups and open panes, and controls those it opened;
// typing into, closing or driving any other pane asks you, as an agent's
// request does. Read loop only.
func (c *conn) scope() {
	c.personOnce.Do(func() {
		if c.remote || c.caller.isAgent() {
			return
		}
		if c.d.positivelyPerson(c.caller, c.pid) {
			c.personOK = true
			return
		}
		cl := c.d.scopedCaller(c.caller, c.pid)
		c.d.mu.Lock()
		c.caller = cl // (read by other connections under d.mu)
		c.d.mu.Unlock()
		c.d.log.Info("scoped a local client it can't tell is you as an agent", "pid", c.pid, "pane", cl.pane)
	})
}

// scopedCaller is who a local process that isn't positively you is, for
// agent scoping: an agent in the pane it was found in (by terminal,
// multiplexer, or the holder that took it in), or, on macOS, in the pane
// whose holder is responsible for it (however it left its parents); else
// outsidePane.
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

// personHook, when set (tests), stands in for positivelyPerson.
var personHook func(cl *caller, pid int) bool

// positivelyPerson reports whether a local connection's process (cl as
// identify placed it, pid) is known to be you.
func (d *Daemon) positivelyPerson(cl *caller, pid int) bool {
	if personHook != nil {
		return personHook(cl, pid)
	}
	if cl != nil {
		// In a pane: found by its own ancestors, and no agent's.
		return cl.pane != "" && cl.direct && !cl.agent
	}
	if pid <= 0 {
		return false
	}
	if pid == os.Getpid() {
		return true // hmkd's own process (a test running it): nothing else has its pid
	}
	if isHammockApp(pid) {
		return true
	}
	stop := d.hammockPIDs()
	// On macOS a program started in a pane stays the responsibility of
	// that pane's holder however it leaves its parents (a double fork,
	// nohup, setsid): it's one of the panes', not yours.
	if r := responsibleFor(pid); r > 0 && r != pid && stop[r] {
		return false
	}
	return walkToLogin(pid, stop, realTree)
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

// procTree is what walkToLogin reads of processes (tests fake it).
type procTree struct {
	parent func(pid int) (int, error)
	agent  func(pid int) bool
	// anchor reports whether pid starts a login session for caller; child
	// is the process below it on the way up (the one sshd started).
	anchor func(pid, child, caller int) bool
}

var realTree = procTree{parent: procinfo.Parent, agent: isAgentProcess, anchor: loginAnchor}

// walkToLogin reports whether pid's ancestors include a login session,
// with no agent, pane or Hammock process anywhere among them: one above
// the session counts too (a build tool inside an app bundle, run by an
// agent, is no terminal).
func walkToLogin(pid int, stop map[int]bool, t procTree) bool {
	anchored := false
	child, cur := 0, pid
	for i := 0; ; i++ {
		if i == 64 {
			return false
		}
		if cur <= 1 {
			return anchored
		}
		if stop[cur] || t.agent(cur) {
			return false
		}
		if !anchored && cur != pid && t.anchor(cur, child, pid) {
			anchored = true
		}
		next, err := t.parent(cur)
		if err != nil || next == cur {
			return false
		}
		child, cur = cur, next
	}
}

// sshFromElsewhere reports whether the session sshd started as child came
// from another computer: one from this one (ssh localhost) is a program of
// yours getting a login session, not you. The address is the one sshd put
// in child's environment when it started it, which the command can't
// change.
func sshFromElsewhere(child int) bool {
	if child <= 0 {
		return false
	}
	var from string
	for _, kv := range procinfo.Env(child) {
		if v, ok := strings.CutPrefix(kv, "SSH_CONNECTION="); ok {
			from, _, _ = strings.Cut(v, " ")
		}
	}
	ip := net.ParseIP(from)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return false
		}
	}
	return true
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
