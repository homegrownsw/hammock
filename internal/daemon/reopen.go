package daemon

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/holder"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/thread"
)

// Reopen Closed (⇧⌘T): the tabs and panes you closed, newest last, as what
// it takes to open them again: a tab's split, each pane's folder, page,
// device or log, and an agent's session (resumed, not started afresh). A
// pane goes back where it was in its tab (its tab gone, a tab of its own).
// Kept in memory only.
//
// With linked computers: a tab or pane a person here closed on another
// computer goes on this list as a marker (on, ref): in its place in your
// order, naming the entry that computer keeps of it (what it takes to
// reopen it, noted as closed by this one: by). The marker goes on only once
// the close succeeded, and comes off only once the reopen did; an entry
// that's gone there (reopened, restarted, pushed out) is dropped and the
// next one tried. Entries a linked computer made here are reopened only
// from it: Reopen Closed here skips them.

const maxClosedTabs = 25

type closedTab struct {
	id      string // this entry's ("cl_…"): a linked computer's marker names it
	groupID string
	index   int
	name    string
	tabID   string
	layout  *pb.Layout          // the tab's, old pane ids at its leaves
	floats  []*pb.FloatingPane  // its floating panes, by old pane id
	focused string              // old pane id
	panes   map[string]*pb.Pane // what to reopen, by old pane id
	pane    string              // a pane closed on its own: its old id
	by      string              // closed from that linked computer ("" here)
	on, ref string              // a marker: closed on that linked computer, as its entry ref
	group   *pb.Group           // its Group as it was (name, icon, folder, place): made again if it's gone since
}

func (d *Daemon) snapshotTab(t *pb.Tab) *closedTab {
	ct := &closedTab{id: newID("cl"), groupID: t.GroupId, name: t.Name, tabID: t.Id, focused: t.FocusedPaneId, layout: proto.CloneOf(t.Layout), panes: map[string]*pb.Pane{}}
	for _, f := range t.Floating {
		ct.floats = append(ct.floats, proto.CloneOf(f))
	}
	if g := d.groups[t.GroupId]; g != nil {
		ct.index = slices.Index(g.TabIds, t.Id)
		// Not its picture (one set in Hammock goes with the Group) or sharing.
		ct.group = &pb.Group{Id: g.Id, Name: g.Name, Icon: g.Icon, Folder: g.Folder, Order: g.Order}
	}
	return ct
}

// restoreGroup makes a closed tab's Group again, as it was, in its place in
// the sidebar if that's free. Callers hold d.mu.
func (d *Daemon) restoreGroup(was *pb.Group) *pb.Group {
	g := &pb.Group{Id: was.Id, Name: was.Name, Icon: was.Icon, Folder: was.Folder, Order: was.Order}
	taken := func(order int32) bool {
		for _, o := range d.groups {
			if o.Order == order {
				return true
			}
		}
		for _, l := range d.links {
			for _, o := range l.groups {
				if o.Order == order {
					return true
				}
			}
		}
		for _, o := range d.remoteOrder {
			if o == order {
				return true
			}
		}
		return false
	}
	if taken(g.Order) {
		g.Order = d.nextOrder()
	}
	d.groups[g.Id] = g
	d.persist(d.store.PutGroup(g))
	return g
}

// pushClosed adds ct, keeping at most maxClosedTabs from each source: this
// computer's own (its closes and its markers), and each linked computer's
// (by), so another computer's closes never push out this one's history.
func (d *Daemon) pushClosed(ct *closedTab) {
	if len(ct.panes) == 0 && ct.on == "" {
		return
	}
	d.closedTabs = append(d.closedTabs, ct)
	n := 0
	for i := len(d.closedTabs) - 1; i >= 0; i-- {
		if d.closedTabs[i].by == ct.by {
			if n++; n > maxClosedTabs {
				d.closedTabs = slices.Delete(d.closedTabs, i, i+1)
			}
		}
	}
}

// dropClosedOf forgets what an unlinked computer closed here, and the
// markers of closes on it. Callers hold d.mu.
func (d *Daemon) dropClosedOf(uid string) {
	d.closedTabs = slices.DeleteFunc(d.closedTabs, func(ct *closedTab) bool { return ct.by == uid || ct.on == uid })
}

// dropClosed forgets entry ct (reopened, or its close didn't happen).
// Callers hold d.mu.
func (d *Daemon) dropClosed(ct *closedTab) {
	d.closedTabs = slices.DeleteFunc(d.closedTabs, func(x *closedTab) bool { return x == ct })
}

// rememberRemoteClose takes a place in your order for a tab or pane a
// person here is closing on linked computer uid: the marker's ref (the
// entry that computer keeps) is filled in once it's closed there, or the
// marker dropped if it isn't. Callers hold d.mu.
func (d *Daemon) rememberRemoteClose(uid string) *closedTab {
	ct := &closedTab{on: uid}
	d.pushClosed(ct)
	return ct
}

// rememberTab records tab id as it is, before it closes (by: the linked
// computer that closed it, "" for here), returning the entry. Callers hold d.mu.
func (d *Daemon) rememberTab(id, by string) *closedTab {
	t := d.tabs[id]
	if t == nil {
		return nil
	}
	ct := d.snapshotTab(t)
	ct.by = by
	for _, pid := range tabPanes(t) {
		if p := d.panes[pid]; p != nil {
			ct.panes[pid] = p.snapshotInfo()
		}
	}
	d.pushClosed(ct)
	return ct
}

// rememberPane records pane id before it closes: its tab, when it's the
// last pane in it, else the pane and where it sits. Callers hold d.mu.
func (d *Daemon) rememberPane(paneID, by string) *closedTab {
	p := d.panes[paneID]
	if p == nil {
		return nil
	}
	t := d.tabs[p.tabID()]
	if t == nil {
		return nil
	}
	if len(tabPanes(t)) == 1 {
		return d.rememberTab(t.Id, by)
	}
	ct := d.snapshotTab(t)
	ct.pane, ct.focused, ct.by = paneID, paneID, by
	ct.panes[paneID] = p.snapshotInfo()
	d.pushClosed(ct)
	return ct
}

// nextClosed is what Reopen Closed here does next: the newest entry closed
// here (not ones a linked computer made, which it reopens), or a marker of
// one closed on a linked computer. nil: nothing. Callers hold d.mu.
func (d *Daemon) nextClosed() *closedTab {
	for i := len(d.closedTabs) - 1; i >= 0; i-- {
		ct := d.closedTabs[i]
		switch {
		case ct.on != "" && d.linkOf(ct.on) == nil:
			d.closedTabs = slices.Delete(d.closedTabs, i, i+1) // unlinked since
		case ct.on != "" && ct.ref == "":
			// Still being closed there: not yet something to reopen.
		case ct.by == "":
			return ct
		}
	}
	return nil
}

// reopenEntry opens closed entry ct again here and returns its tab; it's
// used up either way. Callers hold d.mu.
func (d *Daemon) reopenEntry(ct *closedTab) (*pb.Tab, error) {
	d.dropClosed(ct)
	if ct.pane != "" && d.tabs[ct.tabID] != nil {
		return d.reopenPane(ct)
	}
	return d.reopen(ct)
}

// reopenFor reopens entry id that linked computer by closed here (its
// marker's ref). Callers hold d.mu.
func (d *Daemon) reopenFor(by, id string) (*pb.Tab, error) {
	for _, ct := range d.closedTabs {
		if ct.id == id && ct.by == by {
			t, err := d.reopenEntry(ct)
			if err != nil {
				// Used up, as here: the asking computer goes on to its next.
				d.log.Warn("reopen for a linked computer", "err", err)
				return nil, errClosedGone
			}
			return t, nil
		}
	}
	return nil, errClosedGone
}

var errClosedGone = errors.New("that close isn't kept there any more")

// closedReply answers a close: a linked computer's (that a person made)
// gets its entry's id, for its marker. A close that failed isn't kept.
// Callers hold d.mu.
func (d *Daemon) closedReply(c *conn, ct *closedTab, err error) (*pb.ServerMessage, error) {
	if err != nil {
		if ct != nil {
			d.dropClosed(ct)
		}
		return nil, err
	}
	if ct != nil && c.peer != "" {
		return created(ct.id), nil
	}
	return nil, nil
}

// reopenClosed is Reopen Closed from a client here: the newest close here,
// or on a linked computer (asked, off the lock); one that can't be reopened
// is dropped and the next tried. It replies to msgID when done.
func (d *Daemon) reopenClosed(c *conn, msgID uint64) {
	reply := func(m *pb.ServerMessage) {
		if msgID != 0 {
			m.ReplyTo = msgID
			c.send(m)
		}
	}
	for {
		d.mu.Lock()
		ct := d.nextClosed()
		if ct == nil {
			d.mu.Unlock()
			reply(&pb.ServerMessage{Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: "nothing closed to reopen"}}})
			return
		}
		if ct.on == "" {
			t, err := d.reopenEntry(ct)
			d.mu.Unlock()
			if err != nil {
				d.log.Warn("reopen", "err", err)
				continue
			}
			reply(created(t.Id))
			return
		}
		l := d.linkOf(ct.on)
		name := cmpOr(d.comp.links[ct.on].GetRecord().GetName(), "That computer")
		online := l.online
		d.mu.Unlock()
		offline := fmt.Errorf("%s is offline: what you closed there reopens once it's back", name)
		if !online {
			reply(errorReply(offline)) // kept: it's still the next thing to reopen
			return
		}
		r, err := l.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{ComputerId: ct.on, CloseId: ct.ref}}})
		if err != nil && err.Error() != errClosedGone.Error() {
			// It didn't answer (or failed to reopen it): kept, to try again.
			d.log.Warn("reopen there", "computer", ct.on, "err", err)
			reply(errorReply(fmt.Errorf("%s couldn't reopen it: %w", name, err)))
			return
		}
		d.mu.Lock()
		d.dropClosed(ct) // reopened there, or gone there: the next one, if so
		d.mu.Unlock()
		if err == nil {
			reply(r)
			return
		}
	}
}

func errorReply(err error) *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}}
}

// reopenPane puts a closed pane back in its tab: in the very layout it left
// when the tab's panes are the same since, else beside its neighbour.
func (d *Daemon) reopenPane(ct *closedTab) (*pb.Tab, error) {
	t := d.tabs[ct.tabID]
	spec, resume := reopenSpec(ct.panes[ct.pane])
	if spec == nil {
		return nil, errors.New("the pane can't be opened again")
	}
	p, err := d.startPane(t, spec)
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(ct.floats, func(f *pb.FloatingPane) bool { return f.PaneId == ct.pane }); i >= 0 {
		// It floated: it floats again, where it was.
		f := proto.CloneOf(ct.floats[i])
		f.PaneId, f.Minimised, f.MinimisedByAll = p.id, false, false
		t.Floating = append(t.Floating, f)
	} else if err := d.reopenInLayout(t, ct, p); err != nil {
		return nil, err
	}
	d.noteFocus(t.Id, p.id)
	t.FocusedPaneId = p.id
	d.persist(d.store.PutTab(t))
	d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}, Panes: []*pb.Pane{p.snapshotInfo()}})
	if resume != nil {
		resume(d, p)
	}
	return t, nil
}

// reopenInLayout puts reopened pane p where ct's pane was in t's layout.
func (d *Daemon) reopenInLayout(t *pb.Tab, ct *closedTab, p *pane) error {
	now := layoutPanes(t.Layout)
	then := slices.DeleteFunc(layoutPanes(ct.layout), func(id string) bool { return id == ct.pane })
	slices.Sort(now)
	slices.Sort(then)
	if slices.Equal(now, then) {
		l := proto.CloneOf(ct.layout)
		replaceLeaf(l, ct.pane, p.id)
		t.Layout = l
	} else {
		next, dir := neighbour(ct.layout, ct.pane)
		if next == "" || !splitLayout(t.Layout, next, p.id, dir, false) {
			if !splitLayout(t.Layout, t.FocusedPaneId, p.id, pb.Split_DIRECTION_HORIZONTAL, false) {
				p.kill()
				delete(d.panes, p.id)
				d.persist(d.store.DeletePane(p.id))
				return errors.New("its tab has no place for it")
			}
		}
	}
	return nil
}

// neighbour is a pane beside id in layout (the one before it, else after),
// and the direction of the split they share.
func neighbour(l *pb.Layout, id string) (string, pb.Split_Direction) {
	s := l.GetSplit()
	if s == nil {
		return "", 0
	}
	for i, c := range s.Children {
		if c.GetPaneId() == id {
			if i > 0 {
				ids := layoutPanes(s.Children[i-1])
				return ids[len(ids)-1], s.Direction
			}
			if len(s.Children) > 1 {
				return layoutPanes(s.Children[1])[0], s.Direction
			}
			return "", 0
		}
		if n, dir := neighbour(c, id); n != "" {
			return n, dir
		}
	}
	return "", 0
}

func (d *Daemon) reopen(ct *closedTab) (*pb.Tab, error) {
	s := d.groups[ct.groupID]
	restored := false
	if s == nil && ct.group != nil {
		// Its Group closed with it (its last tab, or Delete Group): back too.
		s, restored = d.restoreGroup(ct.group), true
	}
	if s == nil {
		for _, g := range d.groups {
			if s == nil || g.Order < s.Order {
				s = g
			}
		}
	}
	if s == nil {
		return nil, errors.New("no Group to reopen it in")
	}
	t := &pb.Tab{Id: newID("tb"), GroupId: s.Id, Name: ct.name}
	layout := proto.CloneOf(ct.layout)
	var started []*pane
	var resumes []func()
	newIDs := map[string]string{}
	for _, old := range layoutPanes(ct.layout) {
		info := ct.panes[old]
		spec, resume := reopenSpec(info)
		var p *pane
		var err error
		if spec != nil {
			p, err = d.startPane(t, spec)
		}
		if spec == nil || err != nil {
			layout = removeFromLayout(layout, old)
			continue
		}
		replaceLeaf(layout, old, p.id)
		newIDs[old] = p.id
		started = append(started, p)
		if resume != nil {
			resumes = append(resumes, func() { resume(d, p) })
		}
	}
	if layout == nil || len(started) == 0 {
		if restored {
			d.dropQuietGroup(s.Id) // made for it, and never published
		}
		return nil, errors.New("none of its panes could be opened")
	}
	normaliseLayout(layout)
	t.Layout = layout
	for _, f := range ct.floats {
		spec, resume := reopenSpec(ct.panes[f.PaneId])
		if spec == nil {
			continue
		}
		p, err := d.startPane(t, spec)
		if err != nil {
			continue
		}
		nf := proto.CloneOf(f)
		nf.PaneId = p.id
		nf.OriginPaneId = newIDs[f.OriginPaneId]
		t.Floating = append(t.Floating, nf)
		newIDs[f.PaneId] = p.id
		started = append(started, p)
		if resume != nil {
			resumes = append(resumes, func() { resume(d, p) })
		}
	}
	t.FocusedPaneId = started[0].id
	if nf := newIDs[ct.focused]; nf != "" {
		t.FocusedPaneId = nf // what had focus
	}
	d.tabs[t.Id] = t
	i := min(max(ct.index, 0), len(s.TabIds))
	if s.Id != ct.groupID {
		i = len(s.TabIds)
	}
	s.TabIds = slices.Insert(s.TabIds, i, t.Id)
	d.persist(d.store.PutTab(t))
	d.persist(d.store.PutGroup(s))
	u := &pb.StateUpdate{Groups: []*pb.Group{proto.CloneOf(s)}, Tabs: []*pb.Tab{proto.CloneOf(t)}}
	for _, p := range started {
		u.Panes = append(u.Panes, p.snapshotInfo())
	}
	d.publish(u)
	for _, r := range resumes {
		r()
	}
	return t, nil
}

// reopenSpec is how to open a closed pane again, and, for an agent's, how
// to resume its session once it's running (nil: nothing to open).
func reopenSpec(info *pb.Pane) (*pb.PaneSpec, func(*Daemon, *pane)) {
	if info == nil {
		return nil, nil
	}
	switch info.Kind {
	case pb.PaneKind_PANE_KIND_BROWSER:
		if info.Url == "" || strings.HasPrefix(info.Url, DevToolsURL) {
			return nil, nil // DevTools close with their page
		}
		return &pb.PaneSpec{Url: info.Url}, nil
	case pb.PaneKind_PANE_KIND_SIMULATOR:
		if info.DeviceUdid == "" {
			return nil, nil
		}
		return &pb.PaneSpec{Simulator: info.DeviceUdid}, nil
	case pb.PaneKind_PANE_KIND_EDITOR:
		if info.Editor.GetPath() == "" {
			return nil, nil // an untitled buffer goes with its pane
		}
		return &pb.PaneSpec{Editor: &pb.EditorOpen{Path: info.Editor.GetPath()}}, nil
	case pb.PaneKind_PANE_KIND_DIFF:
		if info.Diff.GetRepo() == "" {
			return nil, nil
		}
		return &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: info.Diff.GetRepo(), Source: proto.CloneOf(info.Diff.GetSource()), View: proto.CloneOf(info.Diff.GetView())}}, nil
	}
	cwd := info.Cwd
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		cwd = ""
	}
	if info.Log != nil {
		return &pb.PaneSpec{Cwd: cwd, Log: proto.CloneOf(info.Log)}, nil
	}
	if a := info.Agent; a != nil && a.State != pb.AgentState_AGENT_STATE_EXITED {
		if codec, ok := thread.CodecFor(a.Harness); ok {
			// A shell, then the agent's own resume in it (as Resume a
			// Session does), so quitting the agent leaves the shell. A
			// session never written (nothing sent) can't be resumed: the
			// agent starts afresh.
			harness, session, transcript := a.Harness, a.SessionId, sessionFile(a)
			cmd := a.Harness
			if transcript != "" && session != "" {
				cmd = shellJoin(codec.ResumeCommand(session))
			} else {
				session = ""
			}
			return &pb.PaneSpec{Cwd: cwd}, func(d *Daemon, p *pane) {
				p.mu.Lock()
				p.info.Agent = &pb.Agent{Harness: harness, State: pb.AgentState_AGENT_STATE_STARTING, StateSinceUnixMs: time.Now().UnixMilli(),
					SessionId: session, TranscriptPath: transcript, Heuristic: true}
				typeCommand(p, cmd)
				p.mu.Unlock()
				d.paneChangedIfLive(p)
			}
		}
	}
	// The login shell, or the command it ran.
	var cmd []string
	if len(info.Command) > 0 && !(len(info.Command) == 2 && info.Command[0] == loginShell() && info.Command[1] == "-l") {
		cmd = slices.Clone(info.Command)
	}
	return &pb.PaneSpec{Cwd: cwd, Command: cmd}, nil
}

// reopenable reports whether what c closes goes on Reopen Closed: c is a
// person's client on this computer, not an agent (its scratch panes), a
// guest, or another computer (a linked computer says per close, with
// CloseTab.remember, whether a person there closed it).
func (c *conn) reopenable() bool { return c.peer == "" && !c.isAgent() }

// sessionFile is an agent's session file, if it's been written ("" until
// something's been sent in it).
func sessionFile(a *pb.Agent) string {
	if t := safeTranscript(a.TranscriptPath); t != "" {
		return t
	}
	if a.SessionId == "" {
		return ""
	}
	ag := agents.Get(a.Harness)
	if ag == nil || ag.Locate == nil {
		return ""
	}
	p, ok := ag.Locate(a.SessionId)
	if !ok {
		return ""
	}
	return safeTranscript(p)
}

// typeCommand types cmd and Return into p's shell (tests watch it instead).
// Callers hold p.mu.
var typeCommand = func(p *pane, cmd string) {
	p.postLocked(func(hc *holder.Client) { _ = hc.Input([]byte(cmd + "\r")) })
}
