package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const prefixKey = 0x02 // Ctrl-B

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool { return x >= r.x && y >= r.y && x < r.x+r.w && y < r.y+r.h }

// App is a running TUI session: the Mac app's layout (a sidebar of Groups
// and tabs, a toolbar, the selected tab's panes with headers) drawn in the
// terminal, driven by keyboard and mouse.
type App struct {
	c   *client.Client
	in  *os.File
	out io.Writer
	scr *Screen
	w   int
	h   int
	th  theme

	groups    []*pb.Group
	tabs      map[string]*pb.Tab
	panes     map[string]*pb.Pane
	attention []*pb.AttentionItem
	grants    []*pb.GrantRequest
	computers *pb.Computers           // linked computers (computers.go)
	me        string                  // this daemon's user id
	linkReqs  []*pb.LinkRequest       // links showing a code (computers.go)
	linkShown map[string]bool         // link requests already put in front of you
	linking   bool                    // this TUI asked to link a computer (computers.go)
	schedules map[string]*pb.Schedule // scheduled tasks, here and on linked computers (schedules.go)

	selTab     string
	opened     map[string]int64    // when this TUI last showed each tab (Unix ms; not kept across runs): the palette lists recent ones first
	openedTab  string              // the tab opened was last noted for
	selGroup   string              // a Group without tabs, shown while no tab is selected
	shownKept  map[string][]string // the tabs each collapsed Group kept when its rows last settled (visibleTabs)
	focus      string
	terms      map[string]*paneTerm
	sizes      map[string][2]int
	prefix     bool
	dirty      bool
	actions    chan func()
	pendingTab string
	quit       bool

	// UI state
	prefs      prefs
	cfg        *config
	anim       *springs
	frame      int
	zones      []zone
	prevZones  []zone
	pointer    string // mouse pointer shape last set (OSC 22)
	mouseX     int
	mouseY     int
	hover      string // key of the zone under the mouse
	drag       *drag
	sel        *selection
	lastClick  time.Time
	clickCount int
	mousePane  string // pane a press was forwarded to, so its release goes there too
	overlay    overlay
	toast      string
	toastAt    time.Time
	pics       pictures               // Groups' pictures, as Kitty images
	live       map[string]*liveView   // browser and simulator panes drawn as pictures
	editors    map[string]*editorView // editor panes on screen (editor.go)
	// The terminal, for handing it to an external editor and back
	// (external.go): its reader, its fd and its mode before the TUI.
	tty        *ttyReader
	fd         int
	rawOld     *term.State
	suspended  bool        // an external editor has the terminal
	ext        *extSession // the editor pane open in it
	extShown   string      // the pane last sent there (not again until focus leaves it)
	livePress  string      // the live pane a click went to, for its drag and release
	cellW      int         // the terminal's cell size in pixels
	cellH      int
	sideScroll int
	dividers   []divider
	paneRects  map[string]rect
	layoutSent time.Time
	// Floating panes (floating.go): each one's card (border included), what
	// to draw in order (the splits, then the floats back to front), a float
	// being moved or resized, and where a drag with Alt held would dock it.
	floatCards map[string]rect
	paneOrder  []string
	floatLive  map[string]rect
	dock       *dockTarget
}

// Run takes over the terminal on in/out until the user quits.
func Run(c *client.Client, in *os.File, out io.Writer) error {
	if !Available {
		_, err := newPaneTerm(1, 1)
		return err
	}
	fd := int(in.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	io.WriteString(out, ttyEnter)
	defer func() {
		io.WriteString(out, ttyLeave)
		term.Restore(fd, old)
	}()

	stdin := make(chan []byte, 64)
	tty, err := newTTYReader(in, stdin)
	if err != nil {
		return err
	}
	bg, fg, _, rest := queryColors(func(s string) { io.WriteString(out, s) }, stdin)

	w, h, _ := term.GetSize(fd)
	a := &App{
		c: c, in: in, out: out, w: w, h: h, scr: NewScreen(w, h), th: newTheme(bg, fg),
		tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{},
		terms: map[string]*paneTerm{}, sizes: map[string][2]int{},
		actions: make(chan func(), 64), anim: newSprings(), prefs: loadPrefs(), cfg: newConfig(),
		paneRects: map[string]rect{}, floatCards: map[string]rect{}, floatLive: map[string]rect{},
		editors: map[string]*editorView{}, tty: tty, fd: fd, rawOld: old,
	}
	a.live = map[string]*liveView{}
	a.cellW, a.cellH = cellPixels(fd)
	ctx := context.Background()
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	if err != nil {
		return err
	}
	a.apply(r.GetState())
	return a.loop(stdin, rest)
}

func (a *App) loop(stdin <-chan []byte, initial []byte) error {
	defer a.dropLive()
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	var parser inputParser
	if len(initial) > 0 {
		a.input(parser.parse(initial))
	}
	tick := time.NewTicker(time.Second / fps)
	defer tick.Stop()
	a.syncPanes()
	a.dirty = true
	for !a.quit {
		select {
		case ev, ok := <-a.c.Events:
			if !ok {
				return fmt.Errorf("connection to hmkd lost")
			}
			a.handle(ev)
		case b, ok := <-stdin:
			if !ok {
				return nil
			}
			a.input(parser.parse(b))
		case f := <-a.actions:
			f()
			a.dirty = true
		case <-winch:
			if a.suspended {
				continue // resumeTTY measures it again
			}
			a.w, a.h, _ = term.GetSize(int(a.in.Fd()))
			a.cellW, a.cellH = cellPixels(int(a.in.Fd()))
			a.scr = NewScreen(a.w, a.h)
			a.syncPanes()
			a.dirty = true
		case <-tick.C:
			a.frame++
			// Not while a read waits in stdin (a slow frame let the tick
			// win the select): it may be the rest of what's held.
			if len(stdin) == 0 {
				if ev := parser.stale(time.Now()); ev != nil {
					a.input(ev)
				}
			}
			a.pollLive()
			a.editorTick()
			a.checkExternal()
			if a.cfg.poll() {
				a.dirty = true
			}
			moving := a.anim.step()
			// Spinners and the working arc keep a slow beat; springs run at frame rate.
			if moving || a.dirty || (a.hasLiveGlyphs() && a.frame%6 == 0) || a.toastVisible() {
				a.dirty = false
				a.render()
			}
		}
	}
	return nil
}

// ---------- state ----------

func (a *App) apply(u *pb.StateUpdate) {
	if u == nil {
		return
	}
	// The selected tab's Group: shown, empty, if that tab was its last.
	selGroupBefore := a.currentGroup()
	if u.Full {
		a.groups, a.tabs, a.panes, a.attention = nil, map[string]*pb.Tab{}, map[string]*pb.Pane{}, nil
		a.grants = nil
	}
	for _, s := range u.Groups {
		if i := slices.IndexFunc(a.groups, func(x *pb.Group) bool { return x.Id == s.Id }); i >= 0 {
			a.groups[i] = s
		} else {
			a.groups = append(a.groups, s)
		}
	}
	a.groups = slices.DeleteFunc(a.groups, func(s *pb.Group) bool { return slices.Contains(u.DeletedGroupIds, s.Id) })
	slices.SortFunc(a.groups, func(x, y *pb.Group) int { return int(x.Order - y.Order) })
	for _, t := range u.Tabs {
		a.tabs[t.Id] = t
	}
	for _, id := range u.DeletedTabIds {
		delete(a.tabs, id)
	}
	for _, p := range u.Panes {
		a.panes[p.Id] = p
	}
	for _, id := range u.DeletedPaneIds {
		delete(a.panes, id)
	}
	for _, at := range u.Attention {
		a.attention = slices.DeleteFunc(a.attention, func(x *pb.AttentionItem) bool { return x.Id == at.Id })
		a.attention = append([]*pb.AttentionItem{at}, a.attention...)
	}
	a.attention = slices.DeleteFunc(a.attention, func(x *pb.AttentionItem) bool { return slices.Contains(u.ClearedAttentionIds, x.Id) })
	for _, g := range u.Grants {
		if !slices.ContainsFunc(a.grants, func(x *pb.GrantRequest) bool { return x.Id == g.Id }) {
			a.grants = append([]*pb.GrantRequest{g}, a.grants...)
		}
	}
	a.grants = slices.DeleteFunc(a.grants, func(x *pb.GrantRequest) bool { return slices.Contains(u.ResolvedGrantIds, x.Id) })
	if u.Computers != nil {
		a.setComputers(u.Computers)
	}
	a.applyLinkRequests(u)
	a.applySchedules(u)

	if a.pendingTab != "" {
		if t := a.tabs[a.pendingTab]; t != nil {
			a.selTab, a.focus, a.pendingTab = t.Id, t.FocusedPaneId, ""
		}
	}
	if _, ok := a.tabs[a.selTab]; !ok {
		if a.selTab != "" && selGroupBefore != "" {
			a.selGroup = selGroupBefore
		}
		a.selTab = ""
		if g := a.group(a.selGroup); g != nil && len(g.TabIds) > 0 {
			// A tab arrived in the empty Group you're on: show it.
			if _, ok := a.tabs[g.TabIds[0]]; ok {
				a.selTab, a.selGroup = g.TabIds[0], ""
			}
		} else if g == nil {
			a.selGroup = ""
		}
		if a.selGroup == "" && a.selTab == "" {
			for _, s := range a.groups {
				if len(s.TabIds) > 0 {
					a.selTab = s.TabIds[0]
					break
				}
			}
		}
	}
	a.noteOpened()
	if t := a.tabs[a.selTab]; t != nil && !slices.Contains(shownIDs(t), a.focus) {
		a.focus = t.FocusedPaneId
	}
	a.syncPanes()
	a.dirty = true
}

func (a *App) handle(ev *pb.ServerMessage) {
	switch m := ev.Msg.(type) {
	case *pb.ServerMessage_State:
		a.apply(m.State)
	case *pb.ServerMessage_PaneSnapshot:
		if t := a.terms[m.PaneSnapshot.PaneId]; t != nil {
			t.reset(int(m.PaneSnapshot.Cols), int(m.PaneSnapshot.Rows))
			t.write(m.PaneSnapshot.Data)
			a.dirty = true
		}
	case *pb.ServerMessage_PaneOutput:
		if t := a.terms[m.PaneOutput.PaneId]; t != nil {
			t.write(m.PaneOutput.Data)
			a.dirty = true
		}
	case *pb.ServerMessage_EditorUpdate:
		a.editorUpdate(m.EditorUpdate)
	case *pb.ServerMessage_DevicePaired:
		a.flash(m.DevicePaired.Name + " paired")
	}
}

// showPairing asks the Mac app to show its pairing code (the secret stays
// on the Mac's screen).
func (a *App) showPairing() {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ShowPairing{ShowPairing: &pb.ShowPairing{}}}, func(r *pb.ServerMessage) {
		a.flash("Showing the pairing code on " + r.GetShowPairing().GetHostName())
	})
}

// showHammockKey says what this Mac's Hammock Key is: its fingerprint, who
// approves it, and where its public key is. Creating and deleting it, with
// Touch ID, are the Mac app's and hmk key's.
func (a *App) showHammockKey() {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_HammockKeyCommand{HammockKeyCommand: &pb.HammockKeyCommand{Op: pb.HammockKeyCommand_OP_STATUS}}}, func(r *pb.ServerMessage) {
		st := r.GetHammockKeyStatus()
		var lines []string
		switch {
		case st.GetExists():
			approve := "Touch ID on the Mac"
			for _, ap := range st.Approvers {
				approve += ", " + ap.Name
			}
			lines = []string{
				"An SSH key in the Mac's Secure Enclave that signs only when you approve.",
				"",
				"Fingerprint  " + st.Fingerprint,
				"Public key   " + st.PublicKeyPath,
				"Approve with " + approve,
				"",
				"hmk key pub prints it; hmk key copy-id HOST adds it to a server.",
			}
		case st.GetError() != "":
			lines = []string{st.Error}
		default:
			lines = []string{"No Hammock Key on this Mac yet.", "", "Create one in Settings ▸ SSH on the Mac, or with hmk key create."}
		}
		a.openDialog(&dialog{title: "Hammock Key", okLabel: "Done", lines: lines})
	})
}

func layoutIDs(l *pb.Layout) []string {
	if l == nil {
		return nil
	}
	if id := l.GetPaneId(); id != "" {
		return []string{id}
	}
	var out []string
	for _, c := range l.GetSplit().GetChildren() {
		out = append(out, layoutIDs(c)...)
	}
	return out
}

// syncPanes attaches the selected tab's panes and keeps their sizes in step
// with the layout.
func (a *App) syncPanes() {
	rects := map[string]rect{}
	a.floatCards, a.paneOrder = map[string]rect{}, nil
	if t := a.tabs[a.selTab]; t != nil {
		a.dividers = a.dividers[:0]
		area := a.splitArea(t)
		a.layoutPanes(t.Layout, area, rects, nil)
		a.paneOrder = layoutIDs(t.Layout)
		a.placeFloats(t, area, rects)
	}
	a.paneRects = rects
	defer a.syncEditors(rects)
	ctx := context.Background()
	for id, t := range a.terms {
		if _, ok := rects[id]; !ok {
			t.close()
			delete(a.terms, id)
			delete(a.sizes, id)
			_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_DetachPane{DetachPane: &pb.DetachPane{PaneId: id}}})
		}
	}
	for id, r := range rects {
		if p := a.panes[id]; p != nil && !isTerm(p) {
			continue
		}
		cols, rows := r.w, max(r.h-1, 1) // the header takes a row
		if _, ok := a.terms[id]; !ok {
			pt, err := newPaneTerm(cols, rows)
			if err != nil {
				a.flash(err.Error())
				continue
			}
			a.terms[id] = pt
			_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: id, Cols: uint32(cols), Rows: uint32(rows)}}})
			_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: id}}})
			a.sizes[id] = [2]int{cols, rows}
			continue
		}
		if a.sizes[id] != [2]int{cols, rows} {
			a.sizes[id] = [2]int{cols, rows}
			_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: id, Cols: uint32(cols), Rows: uint32(rows)}}})
		}
		if p := a.panes[id]; p != nil {
			a.terms[id].resize(int(p.Cols), int(p.Rows))
		}
	}
}

// ---------- names ----------

// tabTitle is the tab's name, else what its work looks like (an agent's
// session title, the branch); failing those, a browser tab is named after its
// page and a simulator after its device.
func (a *App) tabTitle(t *pb.Tab) string {
	if t.Name == "" && t.AutoName == "" {
		if p := a.panes[t.FocusedPaneId]; p != nil && !isTerm(p) {
			if p.Title != "" {
				return p.Title
			}
			return "New Tab"
		}
	}
	return client.TabTitle(t, a.group(t.GroupId))
}

func (a *App) groupTitle(s *pb.Group) string { return client.GroupTitle(s, a.groups, a.panes) }

func (a *App) group(id string) *pb.Group {
	for _, s := range a.groups {
		if s.Id == id {
			return s
		}
	}
	return nil
}

func shortPath(p string) string {
	if home, _ := os.UserHomeDir(); home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

// ---------- actions ----------

func (a *App) flash(msg string) {
	a.toast, a.toastAt, a.dirty = msg, time.Now(), true
	a.anim.get("toast", gentle).from(0)
}

func (a *App) toastVisible() bool { return a.toast != "" && time.Since(a.toastAt) < 3*time.Second }

func (a *App) call(m *pb.ClientMessage, then func(*pb.ServerMessage)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, m)
		a.actions <- func() {
			if err != nil {
				a.flash("⚠ " + err.Error())
				return
			}
			if then != nil {
				then(r)
			}
		}
	}()
}

func (a *App) send(b []byte) {
	if a.focus == "" || len(b) == 0 {
		return
	}
	if v := a.focusedEditor(); v != nil {
		a.editorInput(v, b)
		return
	}
	if p := a.panes[a.focus]; p != nil && !isTerm(p) {
		return
	}
	if t := a.terms[a.focus]; t != nil {
		t.scrollToBottom() // typing returns to the live screen
	}
	a.sel = nil
	_ = a.c.Send(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: a.focus, Data: append([]byte(nil), b...)}}})
}

func (a *App) setFocus(id string) {
	if id == "" {
		return
	}
	a.focus = id
	a.dirty = true
	_ = a.c.Send(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: id}}})
}

func (a *App) currentGroup() string {
	if t := a.tabs[a.selTab]; t != nil {
		return t.GroupId
	}
	if a.group(a.selGroup) != nil {
		return a.selGroup
	}
	if len(a.groups) > 0 {
		return a.groups[0].Id
	}
	return ""
}

// newTab opens a tab in group (a browser tab when url is set).
func (a *App) newTab(group, url string) {
	create := func(group string) {
		spec := &pb.PaneSpec{}
		if url != "" {
			spec.Url = url
		}
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group, Pane: spec}}}, func(r *pb.ServerMessage) {
			a.selectTab(client.CreatedID(r))
		})
	}
	if group == "" {
		group = a.currentGroup()
	}
	if group == "" {
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Home"}}}, func(r *pb.ServerMessage) { create(client.CreatedID(r)) })
		return
	}
	create(group)
}

func (a *App) newGroup() { a.newGroupOn(a.filterComputer()) }

// openNewGroupInFolder asks for a folder, then makes an empty Group there,
// named after it, and shows it (New Group in Directory…).
func (a *App) openNewGroupInFolder() {
	a.openPrompt("New Group in Directory", "Folder", "~", func(v string) {
		if v == "" {
			return
		}
		dir := filepath.Clean(expandTilde(v))
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: filepath.Base(dir)}}}, func(r *pb.ServerMessage) {
			id := client.CreatedID(r)
			a.setGroupFolder(id, dir)
			go func() {
				for range 50 {
					found := make(chan bool, 1)
					a.actions <- func() { found <- a.group(id) != nil }
					if <-found {
						a.actions <- func() { a.selectGroup(id) }
						return
					}
					time.Sleep(40 * time.Millisecond)
				}
			}()
		})
	})
}

// newGroupOn makes a Group with a tab on a computer ("": this one).
func (a *App) newGroupOn(computer string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{ComputerId: computer}}}, func(r *pb.ServerMessage) {
		id := client.CreatedID(r)
		if computer == "" {
			a.newTab(id, "")
			return
		}
		// Its copy comes back from the other computer a moment later.
		go func() {
			for range 50 {
				found := make(chan bool, 1)
				a.actions <- func() { found <- a.group(id) != nil }
				if <-found {
					a.actions <- func() { a.newTab(id, "") }
					return
				}
				time.Sleep(40 * time.Millisecond)
			}
		}()
	})
}

func (a *App) split(paneID string, dir pb.Split_Direction, url string) {
	a.splitAt(paneID, dir, url, false)
}

// splitAt is split with the new pane before paneID (left of it, or above)
// when before is set.
func (a *App) splitAt(paneID string, dir pb.Split_Direction, url string, before bool) {
	if paneID == "" {
		return
	}
	spec := &pb.PaneSpec{Url: url}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: paneID, Direction: dir, Before: before, Spec: spec}}}, func(r *pb.ServerMessage) {
		a.setFocus(client.CreatedID(r))
	})
}

func (a *App) closePane(id string) {
	a.confirmUnsaved([]string{id}, func() { a.closePaneNow(id) })
}

func (a *App) closePaneNow(id string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: id}}}, nil)
}

func (a *App) closeTab(id string) {
	var ids []string
	if t := a.tabs[id]; t != nil {
		ids = tabIDs(t)
	}
	a.confirmUnsaved(ids, func() { a.closeTabNow(id) })
}

func (a *App) closeTabNow(id string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: id}}}, nil)
}

// reopenClosed opens the last closed tab or pane again, and shows it.
func (a *App) reopenClosed() {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}}, func(r *pb.ServerMessage) {
		if id := client.CreatedID(r); id != "" {
			a.selectTab(id)
		}
	})
}

func (a *App) closeGroup(id string) {
	var ids []string
	if g := a.group(id); g != nil {
		for _, tid := range g.TabIds {
			if t := a.tabs[tid]; t != nil {
				ids = append(ids, tabIDs(t)...)
			}
		}
	}
	a.confirmUnsaved(ids, func() { a.closeGroupNow(id) })
}

func (a *App) closeGroupNow(id string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: id}}}, nil)
}

func (a *App) renameTab(id, name string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_RenameTab{RenameTab: &pb.RenameTab{TabId: id, Name: name}}}, nil)
}

func (a *App) configureGroup(id, name, icon string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_RenameGroup{RenameGroup: &pb.RenameGroup{GroupId: id, Name: name}}}, nil)
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIcon{SetGroupIcon: &pb.SetGroupIcon{GroupId: id, Icon: icon}}}, nil)
}

func (a *App) moveTab(id, group string, index int) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_MoveTab{MoveTab: &pb.MoveTab{TabId: id, GroupId: group, Index: int32(index)}}}, nil)
}

func (a *App) switchAgent(pane, harness string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SwitchAgent{SwitchAgent: &pb.SwitchAgent{PaneId: pane, TargetHarness: harness}}}, func(*pb.ServerMessage) {
		a.flash("Continuing in " + harness)
	})
}

// sleepPane puts a pane's idle agent to sleep (its process ended, to free
// memory, until it's woken), or with wake, wakes it.
func (a *App) sleepPane(pane string, wake bool) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SleepPane{SleepPane: &pb.SleepPane{PaneId: pane, Wake: wake}}}, func(*pb.ServerMessage) {
		if !wake {
			a.flash("☾ Asleep: Wake Agent, or type in it, brings it back")
		}
	})
}

// canSleep: the pane's agent is at rest, so it may be put to sleep (the
// daemon says if it can't, and why).
func canSleep(p *pb.Pane) bool {
	if p == nil || p.Asleep || p.Exited || p.Agent == nil || p.Agent.SessionId == "" {
		return false
	}
	s := p.Agent.State
	return (s == pb.AgentState_AGENT_STATE_IDLE || s == pb.AgentState_AGENT_STATE_ERRORED) && !p.Agent.RunningCommand
}

func (a *App) respondGrant(g *pb.GrantRequest, d pb.RespondGrant_Decision) {
	a.grants = slices.DeleteFunc(a.grants, func(x *pb.GrantRequest) bool { return x.Id == g.Id })
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: g.Id, Decision: d}}}, nil)
}

// sendLayout pushes a tab's edited layout (divider drags), at most every
// 80ms while dragging.
func (a *App) sendLayout(t *pb.Tab, force bool) {
	if !force && time.Since(a.layoutSent) < 80*time.Millisecond {
		return
	}
	a.layoutSent = time.Now()
	l := proto.CloneOf(t.Layout)
	_ = a.c.Send(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_SetLayout{SetLayout: &pb.SetLayout{TabId: t.Id, Layout: l}}})
}

// copyText puts text on the host terminal's clipboard (OSC 52).
func (a *App) copyText(s string) {
	if s == "" {
		return
	}
	io.WriteString(a.out, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(s))+"\x07")
	lines := strings.Count(s, "\n") + 1
	if lines == 1 {
		a.flash("✓ Copied")
	} else {
		a.flash(fmt.Sprintf("✓ Copied %d lines", lines))
	}
}

func (a *App) selectTab(id string) {
	a.pendingTab = ""
	if t := a.tabs[id]; t == nil {
		a.pendingTab = id // its state update hasn't arrived yet
	} else {
		a.selTab, a.selGroup = id, ""
		a.noteOpened()
		a.sel = nil
		a.setFocus(t.FocusedPaneId)
		a.syncPanes()
	}
	a.dirty = true
}

// noteOpened notes when the tab you're on was opened, however it came to
// be selected (picked, or arriving after you asked for it).
func (a *App) noteOpened() {
	if a.selTab == "" || a.selTab == a.openedTab {
		return
	}
	if a.opened == nil {
		a.opened = map[string]int64{}
	}
	a.opened[a.selTab], a.openedTab = time.Now().UnixMilli(), a.selTab
	for id := range a.opened {
		if a.tabs[id] == nil {
			delete(a.opened, id) // closed
		}
	}
}

// selectGroup shows a Group: the tab you were last on there, or, with no
// tabs, the Group itself (what to open in it next).
func (a *App) selectGroup(id string) {
	g := a.group(id)
	if g == nil {
		return
	}
	if len(g.TabIds) > 0 {
		a.openGroup(id)
		return
	}
	a.pendingTab, a.selTab, a.selGroup, a.sel = "", "", id, nil
	a.syncPanes()
	a.dirty = true
}

func (a *App) orderedTabs() []string {
	var all []string
	for _, sp := range a.groups {
		all = append(all, sp.TabIds...)
	}
	return all
}

// cycleTab moves through the tabs the sidebar shows (one computer's, when
// it shows one), wrapping.
func (a *App) cycleTab(d int) {
	var all []string
	for _, sp := range a.shownGroups() {
		all = append(all, sp.TabIds...)
	}
	if len(all) == 0 {
		return
	}
	i := slices.Index(all, a.selTab)
	if i < 0 {
		// None of these is selected (it's on a computer not shown): the
		// first, going on; the last, going back.
		if d > 0 {
			a.selectTab(all[0])
		} else {
			a.selectTab(all[len(all)-1])
		}
		return
	}
	a.selectTab(all[(i+d+len(all))%len(all)])
}

func (a *App) cyclePane(d int) {
	t := a.tabs[a.selTab]
	if t == nil {
		return
	}
	ids := shownIDs(t) // the splits, then the floats showing
	if len(ids) == 0 {
		return
	}
	i := max(slices.Index(ids, a.focus), 0)
	a.setFocus(ids[(i+d+len(ids))%len(ids)])
}

// reveal shows a pane: its tab selected, the pane focused, its Group open.
func (a *App) reveal(paneID string) {
	p := a.panes[paneID]
	if p == nil {
		return
	}
	if t := a.tabs[p.TabId]; t != nil {
		a.selectTab(t.Id)
	}
	a.setFocus(paneID)
}

func (a *App) toggleGroup(id string) {
	a.prefs.Collapsed[id] = !a.prefs.Collapsed[id]
	if !a.prefs.Collapsed[id] {
		delete(a.prefs.Collapsed, id)
	}
	a.prefs.save()
	a.dirty = true
}

// ---------- keyboard ----------

func (a *App) input(evs []event) {
	for _, ev := range evs {
		switch ev.kind {
		case evMouse:
			a.mouse(ev.mouse)
		case evPaste:
			if a.overlay != nil {
				a.overlay.key(a, keysOf(ev.data))
				continue
			}
			if v := a.focusedEditor(); v != nil {
				a.editorPaste(v, ev.data)
				continue
			}
			data := ev.data
			if t := a.terms[a.focus]; t != nil && t.bracketedPaste() {
				data = append(append([]byte("\x1b[200~"), data...), "\x1b[201~"...)
			}
			a.send(data)
		case evFocus:
			// Nothing yet: the daemon tracks presence from the Mac app.
		case evBytes:
			a.keys(ev.data)
		}
	}
	a.dirty = true
}

func (a *App) keys(b []byte) {
	for len(b) > 0 {
		if a.overlay != nil {
			a.overlay.key(a, keysOf(b))
			return
		}
		if a.prefix {
			a.prefix = false
			n := a.command(b)
			b = b[n:]
			continue
		}
		i := slices.Index(b, prefixKey)
		if i < 0 {
			a.send(b)
			return
		}
		if i > 0 {
			a.send(b[:i])
		}
		a.prefix = true
		b = b[i+1:]
	}
}

// command handles the key(s) after the prefix; returns bytes consumed.
func (a *App) command(b []byte) int {
	t := a.tabs[a.selTab]
	switch b[0] {
	case prefixKey:
		a.send([]byte{prefixKey})
	case 'q', 'd':
		a.quit = true
	case 'c', 't':
		a.newTab("", "")
	case 'b':
		if toolOff("browser") {
			break
		}
		a.openPrompt("New Browser Tab", "Address", "", func(v string) {
			if v != "" {
				a.newTab("", v)
			}
		})
	case 'S':
		a.newGroup()
	case 'e':
		if !toolOff("editor") {
			a.openQuickOpen()
		}
	case 'E':
		if !toolOff("editor") {
			a.openEditor("", "", false)
		}
	case 'M':
		a.cycleComputer()
	case 'T':
		a.reopenClosed()
	case 's':
		a.openSettings()
	case 'k', ' ':
		a.openPalette()
	case 'O':
		a.openSessionFinder("")
	case '@':
		a.openSchedules("")
	case ',':
		if t != nil {
			a.openPrompt("Rename Tab", "Name", t.Name, func(v string) { a.renameTab(t.Id, v) })
		}
	case '.':
		if s := a.group(a.currentGroup()); s != nil {
			a.openConfigureGroup(s)
		}
	case '?':
		a.openHelp()
	case '%', '|':
		a.split(a.focus, pb.Split_DIRECTION_HORIZONTAL, "")
	case '"', '-':
		a.split(a.focus, pb.Split_DIRECTION_VERTICAL, "")
	case 'y', 'Y', 'N':
		// Answer the newest permission request: allow once, always in Group, deny.
		if len(a.grants) > 0 {
			dec := map[byte]pb.RespondGrant_Decision{'y': pb.RespondGrant_DECISION_ALLOW_ONCE, 'Y': pb.RespondGrant_DECISION_ALLOW_GROUP, 'N': pb.RespondGrant_DECISION_DENY}[b[0]]
			a.respondGrant(a.grants[0], dec)
		}
	case 'o':
		a.cyclePane(1)
	case 'x':
		if a.focus != "" {
			a.closePane(a.focus)
		}
	case 'n':
		a.cycleTab(1)
	case 'p':
		a.cycleTab(-1)
	case '[':
		if tm := a.terms[a.focus]; tm != nil {
			tm.scroll(-max(a.paneRects[a.focus].h-2, 1))
		}
	case ']':
		if tm := a.terms[a.focus]; tm != nil {
			tm.scroll(max(a.paneRects[a.focus].h-2, 1))
		}
	case 'f':
		a.newFloating(a.focus)
	case 'F':
		a.toggleFloating(a.focus)
	case 'm':
		a.arrange(a.focus, pb.FloatPane_ACTION_MINIMISE)
	case 'j':
		if t != nil {
			a.toggleFloats(t.Id)
		}
	case 'z':
		a.arrange(a.focus, pb.FloatPane_ACTION_ZOOM)
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		if t != nil {
			if sp := a.group(t.GroupId); sp != nil {
				if i := int(b[0] - '1'); i < len(sp.TabIds) {
					a.selectTab(sp.TabIds[i])
				}
			}
		}
	case 0x1b:
		// Prefix + arrow keys move between panes.
		if len(b) >= 3 && b[1] == '[' {
			switch b[2] {
			case 'A', 'D':
				a.cyclePane(-1)
			case 'B', 'C':
				a.cyclePane(1)
			}
			return 3
		}
	}
	return 1
}

// mergeTab puts a tab's panes beside the focused pane of another, and shows it.
func (a *App) mergeTab(id string, into *pb.Tab) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_MergeTab{MergeTab: &pb.MergeTab{TabId: id, IntoPaneId: into.FocusedPaneId, Direction: pb.Split_DIRECTION_HORIZONTAL}}}, func(*pb.ServerMessage) {
		a.selectTab(into.Id)
	})
}

// breakOut moves a pane into a new tab of its own, and shows it.
func (a *App) breakOut(paneID string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_BreakOutPane{BreakOutPane: &pb.BreakOutPane{PaneId: paneID}}}, func(r *pb.ServerMessage) {
		a.selectTab(client.CreatedID(r))
	})
}

func (a *App) paneCount(tabID string) int {
	if t := a.tabs[tabID]; t != nil {
		return len(tabIDs(t))
	}
	return 0
}

// grantText is what a permission request asks, in Hammock's words (an older
// daemon's: the agent's).
func grantText(g *pb.GrantRequest) string {
	if g.Headline != "" {
		return g.Headline
	}
	return "An agent wants to " + g.Description
}

// logSource reads what the log prompt was given, as hmk log takes it:
// "file PATH", "macos [PROCESS]", "sim [UDID]", "logcat [SERIAL]", or else
// a command whose output is the log.
func logSource(v string) *pb.LogSource {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	word, rest, _ := strings.Cut(v, " ")
	rest = strings.TrimSpace(rest)
	switch word {
	case "file":
		if rest == "" {
			return nil
		}
		if strings.HasPrefix(rest, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				rest = filepath.Join(home, rest[2:])
			}
		}
		return &pb.LogSource{Kind: "file", Path: rest}
	case "macos":
		return &pb.LogSource{Kind: "macos", Process: rest}
	case "sim", "simulator":
		return &pb.LogSource{Kind: "simulator", Device: rest}
	case "logcat":
		return &pb.LogSource{Kind: "logcat", Device: rest}
	}
	return &pb.LogSource{Kind: "command", Command: []string{v}}
}

// openLog opens a log pane: split beside paneID, or a new tab in group.
func (a *App) openLog(group, paneID string, v string) {
	src := logSource(v)
	if src == nil {
		return
	}
	spec := &pb.PaneSpec{Log: src}
	if paneID != "" {
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: paneID, Direction: pb.Split_DIRECTION_HORIZONTAL, Spec: spec}}}, func(r *pb.ServerMessage) {
			a.setFocus(client.CreatedID(r))
		})
		return
	}
	if group == "" {
		group = a.currentGroup()
	}
	if group == "" {
		return
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group, Pane: spec}}}, func(r *pb.ServerMessage) {
		a.selectTab(client.CreatedID(r))
	})
}

// logPromptHint is what the log prompt takes.
const logPromptHint = "file PATH · macos [PROCESS] · sim · logcat · or a command"

// openStartAgent asks which agent, where, and what first, then starts it in
// a new tab.
func (a *App) openStartAgent(group string) {
	if group == "" {
		group = a.currentGroup()
	}
	dir := ""
	if p := a.panes[a.focus]; p != nil {
		dir = p.Cwd
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home) {
		dir = "~" + strings.TrimPrefix(dir, home)
	}
	a.openDialog(&dialog{title: "Start an Agent", okLabel: "Start",
		fields: []*field{{label: "Agent (" + strings.Join(agents.IDs(), ", ") + ")", value: "claude"}, {label: "Folder", value: dir}, {label: "First message (optional)"}},
		submit: func(v []string) {
			harness := strings.ToLower(strings.TrimSpace(v[0]))
			if ag := agents.Parse(harness); ag != nil {
				harness = ag.ID
			}
			a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{
				Harness: harness, Cwd: strings.TrimSpace(v[1]), GroupId: group, Prompt: strings.TrimSpace(v[2]),
			}}}, func(r *pb.ServerMessage) { a.selectTab(client.CreatedID(r)) })
		}})
}

// openStartAgentOn starts an agent on linked computer (in a folder there:
// its Group, or a new one), as openStartAgent does here.
func (a *App) openStartAgentOn(computer, name string) {
	a.openDialog(&dialog{title: "Start an Agent on " + name, okLabel: "Start",
		fields: []*field{{label: "Agent (" + strings.Join(agents.IDs(), ", ") + ")", value: "claude"}, {label: "Folder on " + name, value: "~"}, {label: "First message (optional)"}},
		submit: func(v []string) {
			harness := strings.ToLower(strings.TrimSpace(v[0]))
			if ag := agents.Parse(harness); ag != nil {
				harness = ag.ID
			}
			a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{
				Harness: harness, Cwd: strings.TrimSpace(v[1]), Prompt: strings.TrimSpace(v[2]), ComputerId: computer,
			}}}, func(r *pb.ServerMessage) {
				a.selectTab(client.CreatedID(r)) // its tab shows once it comes back from there
			})
		}})
}
