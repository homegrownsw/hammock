package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/browserjs"
	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Agent scoping, enforced by the daemon. What a client says about itself
// (HMK_PANE_ID, its kind, which pane made a pane or asks for a grant) is
// easy to fake, so for local connections the daemon works it out from the
// connecting process: the pane it runs in (its ancestors up to a pane's
// shell) and whether it is an agent (an agent harness among those
// ancestors, or running in a pane an agent made). Agents get the policy
// `hmk mcp` applies: read panes in their own Group, control only panes
// they made (directly or through panes they made), and anything else only
// with the user's grant. There's no opt-out: these limits are guardrails
// against an agent wandering, not a boundary (an agent can run anything the
// user can); a real boundary is the agent harness's own sandbox.
// Everything outside Hammock's panes (the Mac app, the
// TUI in another terminal, launchd) and people's own commands in panes are
// unaffected.

// caller is who is at the other end of a local connection.
type caller struct {
	pane  string // the pane it runs in ("" outside Hammock's panes)
	agent bool   // an agent, or run by one
	// direct: found by its ancestors (a pane's shell among them), not by
	// the terminal it kept or a multiplexer it runs under (person.go).
	direct bool
	// scoped: not known to be an agent, but not placed as yours either (a
	// pane's own shell, a terminal: person.go), so scoped as one: in pane
	// (outsidePane if it's in none).
	scoped bool
}

// outsidePane is the pane, as far as agent scoping goes, of a local
// program outside Hammock's panes that isn't placed as yours (person.go):
// it controls the panes it opened, and asks for the rest, as an agent does.
const outsidePane = "outside"

func (cl *caller) isAgent() bool { return cl != nil && cl.pane != "" && cl.agent }

// peerPID returns the process id at the other end of a local connection,
// or 0 when it isn't one (or can't be told).
func peerPID(c net.Conn) int {
	for {
		switch v := c.(type) {
		case *peekedConn:
			c = v.Conn
		case *net.UnixConn:
			return sockPeerPID(v)
		default:
			return 0
		}
	}
}

// peerToken returns the audit token (macOS) of the process at the other
// end of a local connection, as it was when it connected: what a check of
// who it is (person.go) holds it to, so a program that connects and then
// execs another in its pid (the app, say) isn't taken for that program.
func peerToken(c net.Conn) codesign.Token {
	for {
		switch v := c.(type) {
		case *peekedConn:
			c = v.Conn
		case *net.UnixConn:
			var t codesign.Token
			if rc, err := v.SyscallConn(); err == nil {
				_ = rc.Control(func(fd uintptr) { t = codesign.PeerToken(int(fd)) })
			}
			return t
		default:
			return codesign.Token{}
		}
	}
}

// localPeer is the process at the other end of a local connection.
type localPeer struct {
	pid int
	tok codesign.Token
}

func localPeerOf(c net.Conn) localPeer { return localPeer{pid: peerPID(c), tok: peerToken(c)} }

// peerPIDKey carries a WebSocket connection's localPeer to its handler.
type peerPIDKey struct{}

// onceWindow is how long an "allow once" grant lets an agent repeat the
// action it asked for (wait_for_pane reads for up to 10 minutes; a browser
// click is several calls).
func onceWindow(action string) time.Duration {
	if action == "read" || action == "sessions" {
		return 11 * time.Minute
	}
	return 2 * time.Minute
}

// panePIDs maps each live terminal pane's process (its shell) to the pane.
func (d *Daemon) panePIDs() map[int]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := map[int]string{}
	for id, p := range d.panes {
		p.mu.Lock()
		if p.pid > 0 && !p.info.Exited {
			m[p.pid] = id
		} else if p.connecting && p.holderPID > 0 {
			// Still starting (spawnAsync): its program is already under
			// the holder, so an agent in it is known as one.
			m[p.holderPID] = id
		}
		p.mu.Unlock()
	}
	return m
}

// paneHolders maps each terminal pane's holder process to the pane.
func (d *Daemon) paneHolders() map[int]string {
	shells := d.panePIDs()
	m := map[int]string{}
	for pid, id := range shells {
		if h, err := procinfo.Parent(pid); err == nil && h > 1 && shells[h] == "" {
			m[h] = id
		}
	}
	d.mu.Lock()
	for id, p := range d.panes {
		p.mu.Lock()
		if p.holderPID > 0 {
			m[p.holderPID] = id
		}
		p.mu.Unlock()
	}
	d.mu.Unlock()
	return m
}

func isAgentProcess(pid int) bool { return agentName(pid) != "" }

// paneOfProcess finds the pane pid runs in: the first of its ancestors that
// is a pane's process, else (a process that left its parents behind) the
// pane whose terminal it still has, else the pane running the client of
// the terminal multiplexer it runs under. agent reports an agent harness
// between pid and the pane's process (inclusive), or, found by terminal,
// one running in the pane.
func (d *Daemon) paneOfProcess(pid int) (pane string, agent bool) {
	return d.paneOfProcessClaiming(pid, "")
}

// multiplexers run their programs under a server of their own, away from
// the pane their client runs in.
var multiplexers = map[string]bool{"tmux": true, "zellij": true, "screen": true, "herdr": true}

// paneOfProcessClaiming is paneOfProcess where the process says which pane
// it's in: under a multiplexer whose clients run in several panes, the
// claim picks among those (and only those).
func (d *Daemon) paneOfProcessClaiming(pid int, claimed string) (pane string, agent bool) {
	pane, agent, _ = d.locateProcess(pid, claimed)
	return pane, agent
}

// locateProcess is paneOfProcessClaiming, also saying whether the pane was
// found by pid's ancestors (direct) rather than by terminal or multiplexer.
func (d *Daemon) locateProcess(pid int, claimed string) (pane string, agent, direct bool) {
	if pid <= 0 {
		return "", false, false
	}
	panes := d.panePIDs()
	holders := d.paneHolders()
	muxes := map[string]bool{}
	cur := pid
	for i := 0; i < 64 && cur > 1; i++ {
		if isAgentProcess(cur) {
			agent = true
		}
		if id, ok := panes[cur]; ok {
			return id, agent, true
		}
		if id, ok := holders[cur]; ok && cur != pid {
			// Left its parents, and taken in by the pane's holder (on
			// Linux holders are subreapers: reap_linux.go): it's the
			// pane's, but not found through its shell, so not
			// positively you (an agent's, maybe, that has since gone).
			return id, agent, false
		}
		if name, err := procinfo.Name(cur); err == nil && multiplexers[name] {
			muxes[name] = true
		}
		next, err := procinfo.Parent(cur)
		if err != nil || next == cur {
			break
		}
		cur = next
	}
	if tty := procinfo.TTY(pid); tty >= 0 {
		for ppid, id := range panes {
			if procinfo.TTY(ppid) == tty {
				return id, agent || isAgentProcess(ppid) || findAgent(ppid) != "", false
			}
		}
	}
	if len(muxes) == 0 {
		return "", false, false
	}
	// Under a multiplexer's server: the panes running one of its clients.
	var found []string
	for ppid, id := range panes {
		if hasDescendant(ppid, func(c int) bool {
			name, err := procinfo.Name(c)
			return err == nil && muxes[name]
		}) {
			found = append(found, id)
		}
	}
	if claimed == "" {
		// What it was started with (a multiplexer passes HMK_PANE_ID on
		// from the client that started its server, or that it updates).
		for _, kv := range procinfo.Env(pid) {
			if v, ok := strings.CutPrefix(kv, "HMK_PANE_ID="); ok {
				claimed = v
			}
		}
	}
	switch {
	case len(found) == 1:
		return found[0], agent, false
	case claimed != "" && slices.Contains(found, claimed):
		return claimed, agent, false
	}
	return "", false, false
}

// hasDescendant reports whether one of pid's descendants (a few levels
// deep) matches.
func hasDescendant(pid int, match func(int) bool) bool {
	frontier := []int{pid}
	for depth := 0; depth < 4 && len(frontier) > 0; depth++ {
		var next []int
		for _, p := range frontier {
			for _, c := range procinfo.Children(p) {
				if match(c) {
					return true
				}
				next = append(next, c)
			}
		}
		frontier = next
	}
	return false
}

// identifyHook, when set (tests), stands in for working out a caller.
var identifyHook func(pid int) *caller

// identify works out who a local connection's process is.
func (d *Daemon) identify(pid int) *caller {
	if identifyHook != nil {
		return identifyHook(pid)
	}
	pane, agent, direct := d.locateProcess(pid, "")
	if pane == "" {
		return nil
	}
	cl := &caller{pane: pane, agent: agent, direct: direct}
	if !cl.agent {
		d.mu.Lock()
		cl.agent = d.agentMade[pane]
		d.mu.Unlock()
	}
	return cl
}

// loadAgentMade restores which panes agents made. Callers hold d.mu.
func (d *Daemon) loadAgentMade() {
	d.agentMade = map[string]bool{}
	var ids []string
	_ = json.Unmarshal([]byte(d.store.Setting("agent_panes")), &ids)
	for _, id := range ids {
		if d.panes[id] != nil {
			d.agentMade[id] = true
		}
	}
}

// markAgentMade records panes an agent made. Callers hold d.mu.
func (d *Daemon) markAgentMade(ids ...string) {
	for _, id := range ids {
		d.agentMade[id] = true
	}
	var keep []string
	for id := range d.agentMade {
		if d.panes[id] != nil {
			keep = append(keep, id)
		} else {
			delete(d.agentMade, id)
		}
	}
	slices.Sort(keep)
	b, _ := json.Marshal(keep)
	d.persist(d.store.SetSetting("agent_panes", string(b)))
}

func (d *Daemon) createdBy(id string) string {
	if p := d.panes[id]; p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.info.CreatedByPaneId
	}
	return ""
}

// owners is the caller's pane and, while each was made by an agent, the
// panes that made it: a command an agent ran in a pane of its own acts for
// that agent. Callers hold d.mu.
func (d *Daemon) owners(self string) map[string]bool {
	set := map[string]bool{self: true}
	for id, i := self, 0; i < 64 && d.agentMade[id]; i++ {
		id = d.createdBy(id)
		if id == "" || set[id] {
			break
		}
		set[id] = true
	}
	return set
}

// ownsPane reports whether target was made by one of owners, directly or
// transitively. Callers hold d.mu.
func (d *Daemon) ownsPane(owners map[string]bool, target string) bool {
	for id, i := target, 0; i < 64 && id != ""; i++ {
		if d.panes[id] == nil {
			return false
		}
		id = d.createdBy(id)
		if owners[id] {
			return true
		}
	}
	return false
}

// granted reports whether the user allowed requester to do action to
// target: always in its Group, or once, recently. Callers hold d.mu.
func (d *Daemon) granted(requester, action, target string) bool {
	if g := d.groupOfPane(requester); g != "" && d.remembered[grantKey(requester, action, g)] && g == d.groupOfPane(target) {
		return true
	}
	until, ok := d.onceGrants[requester+"|"+action+"|"+target]
	return ok && time.Now().Before(until)
}

// noteOnceGrant lets an allowed-once request go ahead. Callers hold d.mu.
func (d *Daemon) noteOnceGrant(req *pb.GrantRequest) {
	if d.onceGrants == nil {
		d.onceGrants = map[string]time.Time{}
	}
	now := time.Now()
	for k, t := range d.onceGrants {
		if now.After(t) {
			delete(d.onceGrants, k)
		}
	}
	d.onceGrants[req.RequesterPaneId+"|"+req.Action+"|"+req.TargetPaneId] = now.Add(onceWindow(req.Action))
}

var (
	errAgentScope   = errors.New("out of scope: agents can type into, interrupt or close only panes they opened, and reach Groups shared with you only with permission; this needs the user's permission")
	errAgentEditor  = errors.New("agents edit files on disk, not editor panes' buffers: write the file (the pane reloads it, or offers to)")
	errAgentOnly    = errors.New("not available to agents")
	errAgentGrant   = errors.New("agents can't answer permission requests")
	errNoPaneCaller = errors.New("hook events are only taken from inside Hammock's panes")
)

// agentCheck applies agent scoping to a local message before it's handled:
// an agent's own claims about who it is are replaced with what the daemon
// worked out, and what it asks for must be in its scope or granted.
func (c *conn) agentCheck(msg *pb.ClientMessage) error {
	c.scope()
	cl := c.caller
	if cl == nil || cl.pane == "" {
		return nil
	}
	d := c.d
	if cl.scoped {
		if personStrict(msg) {
			// Answering you, or clearing the word that a security
			// setting was loosened in the config file (configwatch.go):
			// that's for you to see and do.
			return errAnswerInApp
		}
		switch msg.Msg.(type) {
		case *pb.ClientMessage_ClearAttention, *pb.ClientMessage_ShutdownDaemon, *pb.ClientMessage_EndScreenSession,
			*pb.ClientMessage_ServerStatus, *pb.ClientMessage_SetColors:
			return nil // as personOnly has them: anything of yours could
		}
	}
	// A grant is the person's answer: never an agent's, nor its pane's.
	if m, ok := msg.Msg.(*pb.ClientMessage_RespondGrant); ok {
		d.mu.Lock()
		g := d.grants[m.RespondGrant.Id]
		remote := d.routes[m.RespondGrant.Id] != nil // another computer's agent asking
		d.mu.Unlock()
		if g != nil && (cl.agent || g.req.RequesterPaneId == cl.pane) || remote && cl.agent {
			return errAgentGrant
		}
		return nil
	}
	if !cl.agent {
		return nil
	}
	setCreator := func(spec **pb.PaneSpec) {
		if *spec == nil {
			*spec = &pb.PaneSpec{}
		}
		(*spec).CreatedByPaneId = cl.pane
	}
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Hello:
		m.Hello.FromPaneId = cl.pane
		if cl.pane == outsidePane {
			m.Hello.FromPaneId = ""
		}
		if m.Hello.ClientKind != "mcp" {
			m.Hello.ClientKind = "cli"
		}
	case *pb.ClientMessage_RequestGrant:
		m.RequestGrant.RequesterPaneId = cl.pane
	case *pb.ClientMessage_CreatePane:
		setCreator(&m.CreatePane.Spec)
	case *pb.ClientMessage_CreateTab:
		setCreator(&m.CreateTab.Pane)
	case *pb.ClientMessage_ReplacePane:
		setCreator(&m.ReplacePane.Spec)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	self := cl.pane
	group := d.groupOfPane(self)
	owners := d.owners(self)
	// Your own Groups (not ones shared with you): your agents read and
	// organise anything there; typing into, interrupting or closing a
	// pane they didn't open still asks you (control).
	mine := func(groupID string) bool { _, ok := d.groups[groupID]; return ok && groupID != "" }
	// Your other computers' panes (linked computers): agents read them
	// freely; anything more asks you, as for panes in your own Groups.
	// A pane that's gone (closed, or closed itself when its command ended
	// cleanly) says so, not "out of scope": there's nothing to ask about.
	gone := func(target string) error {
		if target != "" && d.panes[target] == nil && d.routes[target] == nil {
			return fmt.Errorf("no such pane %q: it has closed", target)
		}
		return nil
	}
	read := func(target string) error {
		if err := gone(target); err != nil {
			return err
		}
		if mine(d.groupOfPane(target)) || (group != "" && d.groupOfPane(target) == group) || d.onLinked(target) || d.granted(self, "read", target) {
			return nil
		}
		return errAgentScope
	}
	// A new Group, an agent or a session on another computer is yours to start.
	if cid := msgComputer(msg); cid != "" && cid != d.id.me.UserId {
		return errAgentScope
	}
	control := func(target, action string) error {
		if err := gone(target); err != nil {
			return err
		}
		if d.ownsPane(owners, target) || d.granted(self, action, target) {
			return nil
		}
		return errAgentScope
	}
	ownTab := func(id string) bool {
		t := d.tabs[id]
		return t != nil && (mine(t.GroupId) || group != "" && t.GroupId == group)
	}
	ownGroup := func(id string) bool { return mine(id) || group != "" && id == group }
	// A tab here, or on another computer (linked, or a Group shared with
	// you): those are checked pane by pane like any, never waved through
	// as "not found here".
	tabOf := func(id string) (*pb.Tab, bool) {
		if t := d.tabs[id]; t != nil {
			return t, true
		}
		if l := d.routes[id]; l != nil {
			return l.tabs[id], true
		}
		return nil, false
	}

	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Hello, *pb.ClientMessage_Subscribe, *pb.ClientMessage_DetachPane,
		*pb.ClientMessage_Presence, *pb.ClientMessage_RequestGrant, *pb.ClientMessage_ListAudit:
		return nil

	case *pb.ClientMessage_ReadPane:
		return read(m.ReadPane.PaneId)
	case *pb.ClientMessage_AttachPane:
		return read(m.AttachPane.PaneId)
	case *pb.ClientMessage_Focus:
		return read(m.Focus.PaneId)
	case *pb.ClientMessage_WatchAgent:
		return read(m.WatchAgent.PaneId)
	case *pb.ClientMessage_ReadAgentFile:
		return read(m.ReadAgentFile.PaneId)
	case *pb.ClientMessage_BrowserLogs:
		return read(m.BrowserLogs.PaneId)
	case *pb.ClientMessage_WatchLog:
		return read(m.WatchLog.PaneId)
	case *pb.ClientMessage_ReadLog:
		return read(m.ReadLog.PaneId)
	case *pb.ClientMessage_ClearLog:
		return control(m.ClearLog.PaneId, "send")
	case *pb.ClientMessage_ReadEditor:
		return read(m.ReadEditor.PaneId)
	case *pb.ClientMessage_EditorCommand:
		// Agents point you at things (select and scroll to them); they
		// change files on disk, never your buffer, nor save it.
		if m.EditorCommand.Action == pb.EditorCommand_ACTION_SELECT {
			return read(m.EditorCommand.PaneId)
		}
		return errAgentEditor
	case *pb.ClientMessage_EditorPush, *pb.ClientMessage_EditorSelect:
		return errAgentEditor
	case *pb.ClientMessage_ListFiles:
		if m.ListFiles.GroupId != "" {
			if ownGroup(m.ListFiles.GroupId) {
				return nil
			}
			return errAgentScope
		}
		return read(m.ListFiles.PaneId)

	case *pb.ClientMessage_CreatePane:
		t := m.CreatePane.SplitPaneId
		if ownGroup(d.groupOfPane(t)) || d.granted(self, "create", t) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_CreateTab:
		if ownGroup(m.CreateTab.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_CreateGroup, *pb.ClientMessage_MoveGroup:
		return nil
	case *pb.ClientMessage_StartAgent:
		if ownGroup(m.StartAgent.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_ListFolders:
		if m.ListFolders.GroupId == "" || ownGroup(m.ListFolders.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_CloseGroup:
		if !ownGroup(m.CloseGroup.GroupId) {
			return errAgentScope
		}
		// Its panes close with it: each must be one it opened, or allowed.
		if g := d.groups[m.CloseGroup.GroupId]; g != nil {
			for _, tid := range g.TabIds {
				if t := d.tabs[tid]; t != nil {
					for _, id := range tabPanes(t) {
						if err := control(id, "close"); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil

	// Scheduled tasks (schedulemsgs.go): saving or deleting one asks you
	// (the handler raises the grant, worded from the schedule), and only
	// this computer's; running, pausing and skipping one it made is free,
	// anyone else's asks (the handler again).
	case *pb.ClientMessage_SaveSchedule:
		s := m.SaveSchedule.Schedule
		if s != nil && s.Id != "" && d.sched.list[s.Id] == nil {
			return errAgentScope
		}
		// A new one only in a Group here: one in another computer's Group
		// would be saved there without asking you.
		if s != nil && s.Id == "" && s.GroupId != "" && d.groups[s.GroupId] == nil {
			return errAgentScope
		}
		return nil
	case *pb.ClientMessage_DeleteSchedule:
		if d.sched.list[m.DeleteSchedule.ScheduleId] == nil {
			return errAgentScope
		}
		return nil
	case *pb.ClientMessage_RunSchedule, *pb.ClientMessage_SetSchedulePaused, *pb.ClientMessage_SkipScheduleRun:
		return nil
	case *pb.ClientMessage_ShowOutput:
		if t := m.ShowOutput.IntoPaneId; t != "" {
			if err := control(t, "browse"); err != nil {
				return err
			}
		}
		if t := m.ShowOutput.PaneId; t != "" && t != self {
			return control(t, "create")
		}
		return nil

	case *pb.ClientMessage_Input:
		return control(m.Input.PaneId, "send")
	case *pb.ClientMessage_SignalPane:
		return control(m.SignalPane.PaneId, "send")
	case *pb.ClientMessage_Resize:
		return control(m.Resize.PaneId, "send")
	case *pb.ClientMessage_AgentAnswer:
		return control(m.AgentAnswer.PaneId, "send")
	case *pb.ClientMessage_AgentSend:
		return control(m.AgentSend.PaneId, "send")
	case *pb.ClientMessage_SwitchAgent:
		return control(m.SwitchAgent.PaneId, "send")
	case *pb.ClientMessage_BreakOutPane:
		// Moving a pane to a tab of its own leaves it running as it was.
		if ownGroup(d.groupOfPane(m.BreakOutPane.PaneId)) {
			return nil
		}
		return control(m.BreakOutPane.PaneId, "send")
	case *pb.ClientMessage_ClosePane:
		return control(m.ClosePane.PaneId, "close")
	case *pb.ClientMessage_ReplacePane:
		return control(m.ReplacePane.PaneId, "close")
	case *pb.ClientMessage_BrowserNavigate:
		return control(m.BrowserNavigate.PaneId, "browse")
	case *pb.ClientMessage_BrowserCall:
		// Reading a page (browser_snapshot, browser_wait_for) runs
		// JavaScript too: those scripts only (browserjs) wherever reading
		// is allowed; any other JavaScript acts in the page (your logged-in
		// one, maybe), so only in panes it may control.
		// (Only this computer's browser panes: browser calls aren't routed
		// to linked computers, nor taken from remote connections.)
		readOnly := !browserCallMutates(m.BrowserCall.Method)
		var checked string
		if m.BrowserCall.Method == "Runtime.evaluate" {
			checked, readOnly = browserjs.ReadOnlyParams(m.BrowserCall.ParamsJson)
		}
		if readOnly && read(m.BrowserCall.PaneId) == nil {
			if checked != "" {
				// What was checked is what runs: re-encoded, not the
				// bytes as sent (a key twice: Chromium may take the other).
				m.BrowserCall.ParamsJson = checked
			}
			return nil
		}
		return control(m.BrowserCall.PaneId, "browse")
	case *pb.ClientMessage_SimulatorCall:
		switch m.SimulatorCall.Op {
		case "devices":
			return nil
		case "screenshot":
			return read(m.SimulatorCall.PaneId)
		}
		return control(m.SimulatorCall.PaneId, "simulator")

	case *pb.ClientMessage_RenameTab:
		if ownTab(m.RenameTab.TabId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_SetLayout:
		if ownTab(m.SetLayout.TabId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_FloatPane:
		// Floats it opened itself; the tab's own arrangement (minimise or
		// restore them all) is yours.
		if m.FloatPane.Action == pb.FloatPane_ACTION_TOGGLE_ALL || m.FloatPane.PaneId == "" {
			return errAgentScope
		}
		if d.ownsPane(owners, m.FloatPane.PaneId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_MoveTab:
		if ownTab(m.MoveTab.TabId) && ownGroup(m.MoveTab.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_RenameGroup:
		if ownGroup(m.RenameGroup.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_SetGroupIcon:
		if ownGroup(m.SetGroupIcon.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_SetGroupFolder:
		if ownGroup(m.SetGroupFolder.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_SetGroupIconImage:
		if ownGroup(m.SetGroupIconImage.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_ListSessions, *pb.ClientMessage_SessionExcerpts:
		// Its project's sessions; others with permission (the handlers check:
		// a session's folder needs the index, which this lock mustn't wait on).
		return nil
	case *pb.ClientMessage_ReadGroupIcon:
		if ownGroup(m.ReadGroupIcon.GroupId) {
			return nil
		}
		return errAgentScope
	case *pb.ClientMessage_CloseTab:
		t, known := tabOf(m.CloseTab.TabId)
		if !known {
			return nil // the handler says there's no such tab
		}
		if t == nil {
			return errAgentScope
		}
		for _, id := range tabPanes(t) {
			if err := control(id, "close"); err != nil {
				return err
			}
		}
		return nil
	case *pb.ClientMessage_MergeTab:
		t, known := tabOf(m.MergeTab.TabId)
		if !known {
			return nil // the handler says there's no such tab
		}
		if t == nil {
			return errAgentScope
		}
		// Moving panes beside others leaves them running as they were.
		if ownTab(m.MergeTab.TabId) && ownGroup(d.groupOfPane(m.MergeTab.IntoPaneId)) {
			return nil
		}
		for _, id := range tabPanes(t) {
			if err := control(id, "send"); err != nil {
				return err
			}
		}
		return read(m.MergeTab.IntoPaneId)
	}
	// Administration, sharing, devices and the rest are for people: one
	// scoped as an agent (not positively you) asks you (personCheck).
	if cl.scoped {
		return nil
	}
	return errAgentOnly
}

// madeByAgent records that the panes an agent's request is making are an
// agent's, in the same d.mu section that adds them: a process in one that
// connects before the reply is sent (its shell running hmk at once) is
// known as the agent's, never taken for a pane of yours. Callers hold d.mu.
func (c *conn) madeByAgent(ids ...string) {
	if c.caller.isAgent() {
		c.d.markAgentMade(ids...)
	}
}
