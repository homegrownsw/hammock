package daemon

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
	"github.com/alexjsp/hammock/internal/vt"
)

// sendQueue bounds how far a client can fall behind before it is dropped.
// Long before that, pane output stops being queued for it (lagHigh) and it
// gets each such pane's screen afresh once it has caught up (lagLow): a
// client on a slow link (a phone, a guest) catches up instead of being cut
// off and loading everything again.
const (
	sendQueue = 8192
	lagHigh   = sendQueue / 2
	lagLow    = sendQueue / 8
)

// conn is one connected client.
type conn struct {
	d      *Daemon
	t      transport
	out    chan *pb.ServerMessage
	ctx    context.Context
	cancel context.CancelFunc

	// guarded by d.mu
	subscribed bool
	kind, name string
	presence   *pb.Presence
	lastInput  time.Time

	// remote clients (Tailscale) must authenticate before anything else
	remote   bool
	srcIP    string // a remote client's address (Tailscale's, normally)
	localAt  string // the listener address a remote client reached (host:port)
	authed   bool
	nonce    []byte
	deviceID string

	// multiplayer (guarded by d.mu)
	peer     string               // a guest daemon: its user id (share.go)
	peerKey  []byte               // the user key it signed in with (identity.go)
	seen     map[string]bool      // ids a peer has been sent, so it's told when they go
	fromPane string               // a CLI run inside a pane (an agent, likely)
	auditAt  map[string]time.Time // a peer's typing, audited once a minute per pane
	focus    string               // the pane this client last focused
	caller   *caller              // a local client's process, as the daemon sees it (caller.go)

	attached map[string]*pane // only touched by the read loop

	// Panes whose output was skipped while this client was behind
	// (sendOutput), to be resent as snapshots (resync).
	lagMu   sync.Mutex
	lagging map[*pane]bool
	hasLag  atomic.Bool
	once    sync.Once
	hangUp  atomic.Bool // close once the message being handled is answered
}

var engineName = sync.OnceValue(func() string {
	m := vt.New(2, 2)
	defer m.Close()
	return m.Engine()
})

func stateRuntime(paneID, socket string) state.PaneRuntime {
	return state.PaneRuntime{PaneID: paneID, Socket: socket}
}

func (d *Daemon) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"hammock.v1"}})
	if err != nil {
		return
	}
	ws.SetReadLimit(32 << 20)
	pid, _ := r.Context().Value(peerPIDKey{}).(int)
	cl := d.identify(pid)
	d.serveConnWith(&wsTransport{ws: ws}, func(c *conn) { c.caller = cl })
}

func (d *Daemon) serveFrames(nc net.Conn, r *bufio.Reader) {
	cl := d.identify(peerPID(nc))
	d.serveConnWith(&frameTransport{c: nc, r: r}, func(c *conn) { c.caller = cl })
}

func (d *Daemon) serveConn(t transport) { d.serveConnWith(t, nil) }

func (d *Daemon) serveConnWith(t transport, setup func(*conn)) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{d: d, t: t, out: make(chan *pb.ServerMessage, sendQueue), ctx: ctx, cancel: cancel, attached: map[string]*pane{}}
	if setup != nil {
		setup(c)
	}
	d.mu.Lock()
	d.conns[c] = struct{}{}
	d.mu.Unlock()

	go c.writeLoop()
	c.readLoop()
	c.close()
}

// send queues msg without blocking; a client that can't keep up is dropped
// and must reconnect (re-attaching gives it a fresh snapshot).
func (c *conn) send(msg *pb.ServerMessage) {
	select {
	case c.out <- msg:
	default:
		c.cancel()
	}
}

// sendOutput queues a pane's output for c, unless c has fallen behind (its
// queue past lagHigh): then that pane's output is skipped until c has caught
// up, when resync sends the pane's screen instead. Callers hold p.mu.
func (c *conn) sendOutput(p *pane, msg *pb.ServerMessage) {
	c.lagMu.Lock()
	if c.lagging[p] {
		c.lagMu.Unlock()
		return
	}
	if len(c.out) >= lagHigh {
		if c.lagging == nil {
			c.lagging = map[*pane]bool{}
		}
		c.lagging[p] = true
		c.hasLag.Store(true)
		c.lagMu.Unlock()
		return
	}
	c.lagMu.Unlock()
	c.send(msg)
}

// resync sends the screen of each pane whose output c missed while behind.
// A snapshot replaces the client's screen, so output queued just before it
// does no harm.
func (c *conn) resync() {
	c.lagMu.Lock()
	panes := make([]*pane, 0, len(c.lagging))
	for p := range c.lagging {
		panes = append(panes, p)
	}
	c.lagMu.Unlock()
	for _, p := range panes {
		p.mu.Lock()
		c.lagMu.Lock()
		delete(c.lagging, p)
		if len(c.lagging) == 0 {
			c.hasLag.Store(false)
		}
		c.lagMu.Unlock()
		if _, ok := p.attached[c]; ok && p.mirror != nil {
			c.send(p.snapshotMsgLocked())
		}
		p.mu.Unlock()
	}
}

func (c *conn) writeLoop() {
	defer c.t.Close() // unblocks readLoop (frame reads ignore ctx)
	for {
		select {
		case <-c.ctx.Done():
			c.t.Close()
			return
		case msg := <-c.out:
			if msg == nil {
				c.cancel() // hung up (everything before it written)
				return
			}
			b, err := proto.Marshal(msg)
			if err != nil {
				continue
			}
			wctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			if wt, ok := c.t.(*wsTransport); ok {
				err = wt.writeMsg(wctx, b, msg.GetMediaFrame() == nil) // video is compressed already
			} else {
				err = c.t.Write(wctx, b)
			}
			cancel()
			if err != nil {
				c.cancel()
				return
			}
			if c.hasLag.Load() && len(c.out) <= lagLow {
				c.resync()
			}
		}
	}
}

func (c *conn) readLoop() {
	for {
		b, err := c.t.Read(c.ctx)
		if err != nil {
			return
		}
		msg := &pb.ClientMessage{}
		if err := proto.Unmarshal(b, msg); err != nil {
			c.replyErr(0, fmt.Errorf("bad message: %w", err))
			continue
		}
		// A close is described before it happens, while what it closes has
		// its name.
		var pre *state.AuditEvent
		if c.audited(msg) && closes(msg) {
			c.d.mu.Lock()
			pre = c.d.auditEvent(c.auditActor(), auditAction(msg), auditTarget(msg), "")
			c.d.mu.Unlock()
		}
		reply, err := c.handle(msg)
		if err == errDeferred {
			continue
		}
		c.agentMadePanes(msg, reply, err)
		if c.audited(msg) {
			c.d.mu.Lock()
			switch {
			case pre != nil:
				c.d.persist(c.d.store.Audit(*pre))
			case c.throttleAudit(msg):
				c.d.audit(c.auditActor(), auditAction(msg), auditTarget(msg), "")
			}
			c.d.mu.Unlock()
		}
		if msg.Id != 0 {
			if err != nil {
				c.replyErr(msg.Id, err)
			} else {
				if reply == nil {
					reply = &pb.ServerMessage{Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}}
				}
				reply.ReplyTo = msg.Id
				c.send(reply)
			}
		}
		if c.hangUp.CompareAndSwap(true, false) {
			c.send(nil) // the write loop closes it there, once the answer is written
		}
	}
}

func (c *conn) replyErr(id uint64, err error) {
	c.send(&pb.ServerMessage{ReplyTo: id, Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}})
}

func (c *conn) close() {
	c.once.Do(func() {
		c.cancel()
		for _, p := range c.attached {
			p.detach(c)
		}
		c.d.dropAgentWatches(c)
		c.d.dropLogWatches(c)
		c.d.media.closeFor(c)
		c.d.browsers.closeMenusFor(c)
		c.d.mu.Lock()
		delete(c.d.conns, c)
		if c.kind == "mac" {
			c.d.appChanged()
		}
		c.d.dropGrantsFor(c)
		if c.peer != "" {
			c.d.peerClosed(c)
		} else {
			c.d.localClosed(c)
		}
		c.d.mu.Unlock()
	})
}

func created(id string) *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Created{Created: &pb.Created{Id: id}}}
}

func (c *conn) pane(id string) (*pane, error) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	p, ok := c.d.panes[id]
	if !ok {
		return nil, fmt.Errorf("no such pane %q", id)
	}
	return p, nil
}

func (c *conn) paneResized(p *pane) {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if _, ok := c.d.panes[p.id]; ok {
		c.d.paneChanged(p)
	}
}

func (c *conn) handle(msg *pb.ClientMessage) (*pb.ServerMessage, error) {
	d := c.d
	if c.remote {
		if reply, err, done := c.handleRemoteAuth(msg); done {
			return reply, err
		}
	}
	c.d.mu.Lock()
	peer := c.peer
	if c.deviceID != "" && phoneAction(msg) {
		c.d.phoneUsed() // you're on your phone: prompts go there (usingPhone)
	}
	c.d.mu.Unlock()
	if peer != "" {
		ignore, err := c.peerCheck(msg)
		if err != nil || ignore {
			return nil, err
		}
	} else if err := c.agentCheck(msg); err != nil {
		// An agent's scope is checked first, so it can't reach a shared
		// Group's panes with your role in it.
		return nil, err
	} else if l, err := c.d.routeFor(c, msg); l != nil || err != nil {
		// Something in a Group shared with you: it's handled on its host.
		if err != nil {
			return nil, err
		}
		if m, ok := msg.Msg.(*pb.ClientMessage_OpenMedia); ok {
			return l.openMedia(c, m.OpenMedia)
		}
		switch msg.Msg.(type) {
		case *pb.ClientMessage_CloseTab, *pb.ClientMessage_ClosePane:
			// A person closing it on another of your computers: that one
			// keeps it, and it's in your order here (its place taken now,
			// filled in once it's closed). Only this computer says whether
			// it's kept: what the client sent is overwritten (an agent's
			// close never is).
			d.mu.Lock()
			remember, uid := l.linked && c.reopenable(), l.tok.HostUserId
			var spot *closedTab
			if remember {
				spot = d.rememberRemoteClose(uid)
			}
			d.mu.Unlock()
			up := proto.CloneOf(msg)
			switch x := up.Msg.(type) {
			case *pb.ClientMessage_CloseTab:
				x.CloseTab.Remember = remember
			case *pb.ClientMessage_ClosePane:
				x.ClosePane.Remember = remember
			}
			if !remember {
				return l.forward(c, up)
			}
			r, err := l.forwardThen(c, up, func(r *pb.ServerMessage, err error) *pb.ServerMessage {
				d.mu.Lock()
				if ref := r.GetCreated().GetId(); err == nil && ref != "" {
					spot.ref = ref
				} else {
					d.dropClosed(spot) // not closed there (or not kept): no place in your order
				}
				d.mu.Unlock()
				if err != nil {
					return nil
				}
				return &pb.ServerMessage{Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}} // closed, as here
			})
			if err != nil && err != errDeferred {
				// Never sent (it's offline): no place in your order either.
				d.mu.Lock()
				d.dropClosed(spot)
				d.mu.Unlock()
			}
			return r, err
		}
		return l.forward(c, msg)
	}
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Hello:
		d.mu.Lock()
		if c.peer == "" {
			c.kind, c.name, c.fromPane = m.Hello.ClientKind, m.Hello.ClientName, m.Hello.FromPaneId
		}
		remoteOn := !c.remote && d.remote.on
		anon := c.remote && !c.authed
		host := d.macName()
		var addrs, lan []string
		if c.remote && c.authed && (c.deviceID != "" || c.peer != "" && d.isLinked(c.peer)) {
			addrs, lan = d.reachAt() // (a paired phone keeps them: where to find this computer next time)
		}
		d.mu.Unlock()
		hr := &pb.HelloReply{
			ProtocolVersion: ProtocolVersion, DaemonVersion: Version, HostName: host, VtEngine: engineName(), RemoteEnabled: remoteOn,
			UserId: d.id.me.UserId, Addresses: addrs, LanSubnets: lan,
		}
		if anon {
			hr.HostName, hr.UserId = "", "" // who this is, only once signed in
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_HelloReply{HelloReply: hr}}, nil

	case *pb.ClientMessage_Subscribe:
		// Reply under the lock so no newer delta can be queued ahead of the
		// full state.
		d.mu.Lock()
		defer d.mu.Unlock()
		c.subscribed = true
		full := d.fullState()
		if c.peer != "" {
			c.seen = nil
			full = d.filterFor(c, full)
		}
		c.send(&pb.ServerMessage{ReplyTo: msg.Id, Msg: &pb.ServerMessage_State{State: full}})
		if c.kind == "mac" {
			d.appChanged() // this computer can notify now (after its base state: deltas follow it)
		}
		return nil, errDeferred

	case *pb.ClientMessage_CreateGroup:
		d.mu.Lock()
		defer d.mu.Unlock()
		// An empty name means automatic: clients name it after its directory.
		s := d.createGroup(m.CreateGroup.Name)
		return created(s.Id), nil

	case *pb.ClientMessage_RenameGroup:
		d.mu.Lock()
		defer d.mu.Unlock()
		s, ok := d.groups[m.RenameGroup.GroupId]
		if !ok {
			return nil, errors.New("no such group")
		}
		s.Name = m.RenameGroup.Name
		d.persist(d.store.PutGroup(s))
		d.publish(&pb.StateUpdate{Groups: []*pb.Group{proto.CloneOf(s)}})
		return nil, nil

	case *pb.ClientMessage_ShutdownDaemon:
		d.log.Info("shutdown requested by client", "client", c.kind, "end_panes", m.ShutdownDaemon.EndPanes)
		if m.ShutdownDaemon.EndPanes {
			d.mu.Lock()
			d.endPanes = true
			d.mu.Unlock()
		}
		// After the reply is on its way.
		time.AfterFunc(100*time.Millisecond, d.Shutdown)
		return nil, nil

	case *pb.ClientMessage_SetGroupIcon:
		d.mu.Lock()
		defer d.mu.Unlock()
		s, ok := d.groups[m.SetGroupIcon.GroupId]
		if !ok {
			return nil, errors.New("no such group")
		}
		s.Icon = m.SetGroupIcon.Icon
		d.persist(d.store.PutGroup(s))
		d.publish(&pb.StateUpdate{Groups: []*pb.Group{proto.CloneOf(s)}})
		return nil, nil

	case *pb.ClientMessage_SetGroupFolder:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.setGroupFolder(m.SetGroupFolder.GroupId, m.SetGroupFolder.Folder)

	case *pb.ClientMessage_ReadGroupIcon:
		ic, err := d.readGroupIcon(m.ReadGroupIcon)
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_GroupIcon{GroupIcon: ic}}, nil

	case *pb.ClientMessage_SetGroupIconImage:
		// Decoding and writing a picture takes a moment: reply when done.
		id, sm := msg.Id, m.SetGroupIconImage
		go func() {
			reply := &pb.ServerMessage{ReplyTo: id, Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}}
			if err := d.setGroupIconImage(sm); err != nil {
				reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
			}
			if id != 0 {
				c.send(reply)
			}
		}()
		return nil, errDeferred

	case *pb.ClientMessage_CloseGroup:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.closeGroup(m.CloseGroup.GroupId, c.reopenable())

	case *pb.ClientMessage_CreateTab:
		d.mu.Lock()
		defer d.mu.Unlock()
		t, err := d.createTab(m.CreateTab.GroupId, m.CreateTab.Name, m.CreateTab.Pane, m.CreateTab.TabId)
		if err != nil {
			return nil, err
		}
		return created(t.Id), nil

	case *pb.ClientMessage_RenameTab:
		d.mu.Lock()
		defer d.mu.Unlock()
		t, ok := d.tabs[m.RenameTab.TabId]
		if !ok {
			return nil, errors.New("no such tab")
		}
		t.Name = m.RenameTab.Name
		d.persist(d.store.PutTab(t))
		d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}})
		d.nameSessions(t)
		return nil, nil

	case *pb.ClientMessage_CloseTab:
		d.mu.Lock()
		defer d.mu.Unlock()
		var ct *closedTab
		if c.reopenable() || m.CloseTab.Remember && d.isLinked(c.peer) {
			ct = d.rememberTab(m.CloseTab.TabId, c.peer)
		}
		return d.closedReply(c, ct, d.closeTab(m.CloseTab.TabId))

	case *pb.ClientMessage_ReopenClosed:
		if c.peer != "" {
			// A linked computer's marker: the very entry it names.
			d.mu.Lock()
			defer d.mu.Unlock()
			if !d.isLinked(c.peer) {
				return nil, errors.New("nothing closed to reopen")
			}
			t, err := d.reopenFor(c.peer, m.ReopenClosed.CloseId)
			if err != nil {
				return nil, err
			}
			return created(t.Id), nil
		}
		go d.reopenClosed(c, msg.Id) // may ask another computer: off this connection's loop
		return nil, errDeferred

	case *pb.ClientMessage_CreatePane:
		d.mu.Lock()
		defer d.mu.Unlock()
		if m.CreatePane.SplitPaneId == "" {
			return nil, errors.New("split_pane_id is required (use CreateTab for a tab's first pane)")
		}
		if m.CreatePane.Floating {
			p, err := d.createFloating(m.CreatePane.SplitPaneId, m.CreatePane.Spec, m.CreatePane.Frame)
			if err != nil {
				return nil, err
			}
			return created(p.id), nil
		}
		p, err := d.splitPane(m.CreatePane.SplitPaneId, m.CreatePane.Direction, m.CreatePane.Spec, m.CreatePane.Before)
		if err != nil {
			return nil, err
		}
		return created(p.id), nil

	case *pb.ClientMessage_ClosePane:
		d.mu.Lock()
		defer d.mu.Unlock()
		var ct *closedTab
		if c.reopenable() || m.ClosePane.Remember && d.isLinked(c.peer) {
			ct = d.rememberPane(m.ClosePane.PaneId, c.peer)
		}
		return d.closedReply(c, ct, d.closePane(m.ClosePane.PaneId))

	case *pb.ClientMessage_WatchAgent:
		return nil, d.watchAgent(c, m.WatchAgent)

	case *pb.ClientMessage_WatchLog:
		return nil, d.watchLog(c, m.WatchLog)

	case *pb.ClientMessage_EditorPush:
		ok, err := d.editors.push(m.EditorPush)
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_EditorPushReply{EditorPushReply: &pb.EditorPushReply{Accepted: ok}}}, nil

	case *pb.ClientMessage_EditorSelect:
		d.editors.selectPane(m.EditorSelect)
		return nil, nil

	case *pb.ClientMessage_EditorCommand:
		d.mu.Lock()
		folder := d.folderOf(d.groupOfPane(m.EditorCommand.PaneId))
		guest := c.peer != "" && !d.isLinked(c.peer)
		d.mu.Unlock()
		return nil, d.editors.command(m.EditorCommand, folder, guest)

	case *pb.ClientMessage_ReadEditor:
		u, err := d.editors.read(m.ReadEditor)
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_EditorUpdate{EditorUpdate: u}}, nil

	case *pb.ClientMessage_ListFiles:
		d.mu.Lock()
		g := m.ListFiles.GroupId
		if g == "" {
			g = d.groupOfPane(m.ListFiles.PaneId)
		}
		folder := d.folderOf(g)
		d.mu.Unlock()
		l, err := d.editors.listFiles(folder, m.ListFiles.Query, int(m.ListFiles.Limit))
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_FileList{FileList: l}}, nil

	case *pb.ClientMessage_ReadLog:
		lp, err := d.logOf(m.ReadLog.PaneId)
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_LogView{LogView: lp.read(m.ReadLog)}}, nil

	case *pb.ClientMessage_ClearLog:
		lp, err := d.logOf(m.ClearLog.PaneId)
		if err != nil {
			return nil, err
		}
		lp.clear()
		return nil, nil

	case *pb.ClientMessage_AgentAnswer:
		return nil, d.answerAgent(c, msg.Id, m.AgentAnswer)

	case *pb.ClientMessage_AgentSend:
		d.mu.Lock()
		if p := d.panes[m.AgentSend.PaneId]; p != nil {
			d.keepRun(p) // a scheduled run you've talked to stays
		}
		d.mu.Unlock()
		return nil, d.sendAgent(c, msg.Id, m.AgentSend)

	case *pb.ClientMessage_ShowOutput:
		return d.showOutputMsg(c, m.ShowOutput)

	case *pb.ClientMessage_SaveSchedule, *pb.ClientMessage_DeleteSchedule, *pb.ClientMessage_RunSchedule,
		*pb.ClientMessage_SetSchedulePaused, *pb.ClientMessage_SkipScheduleRun:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.handleSchedule(c, msg)

	case *pb.ClientMessage_ReadAgentFile:
		return d.readAgentFile(c, m.ReadAgentFile)

	case *pb.ClientMessage_BrowserCall:
		return nil, d.browserCall(c, msg.Id, m.BrowserCall)

	case *pb.ClientMessage_SimulatorCall:
		return nil, d.simulatorCall(c, msg.Id, m.SimulatorCall)

	case *pb.ClientMessage_BrowserInput:
		return nil, d.browserInput(c, m.BrowserInput)

	case *pb.ClientMessage_BrowserMenuChoice:
		return nil, d.browsers.menuChoice(c, m.BrowserMenuChoice)

	case *pb.ClientMessage_OpenMedia:
		return d.media.open(c, m.OpenMedia)

	case *pb.ClientMessage_ServerStatus:
		return d.serverStatus(), nil

	case *pb.ClientMessage_EndScreenSession:
		d.endScreenSessions(m.EndScreenSession)
		return nil, nil

	case *pb.ClientMessage_SetMediaView:
		v := m.SetMediaView
		if s := d.media.session(c, v.SessionId); s != nil {
			s.view(v.X, v.Y, v.W, v.H)
		} else if r := d.media.relay(c, v.SessionId); r != nil {
			r.setView(v)
		}
		return nil, nil

	case *pb.ClientMessage_ListDisplays:
		if a := m.ListDisplays.RequestAccess; a != "" {
			d.mu.Lock()
			here := !c.remote && c.peer == "" && !c.isAgent() && !c.caller.isAgent()
			d.mu.Unlock()
			if !here {
				return nil, errors.New("only available on this Mac")
			}
		}
		// Off this connection's loop: it waits on the screen host (started
		// on demand, and on macOS's prompt when asking for access), and the
		// app's other requests shouldn't.
		id, ask := msg.Id, m.ListDisplays.RequestAccess
		go func() {
			if ask != "" {
				if _, _, err := d.requestScreenAccess(ask); err != nil {
					if id != 0 {
						c.replyErr(id, err)
					}
					return
				}
			}
			r, err := d.listDisplays(c)
			if id == 0 {
				return
			}
			if err != nil {
				c.replyErr(id, err)
				return
			}
			r.ReplyTo = id
			c.send(r)
		}()
		return nil, errDeferred

	case *pb.ClientMessage_ScreenInput:
		return nil, d.screenInput(c, m.ScreenInput)

	case *pb.ClientMessage_ScreenSnapshot:
		// Off this connection's loop, as ListDisplays: it waits on the screen host.
		id := msg.Id
		go func() {
			r, err := d.screenSnapshot(c, m.ScreenSnapshot)
			if id == 0 {
				return
			}
			if err != nil {
				c.replyErr(id, err)
				return
			}
			r.ReplyTo = id
			c.send(r)
		}()
		return nil, errDeferred

	case *pb.ClientMessage_CloseMedia:
		if s := d.media.session(c, m.CloseMedia.SessionId); s != nil {
			s.end("")
		} else if r := d.media.relay(c, m.CloseMedia.SessionId); r != nil {
			r.end("")
		}
		return nil, nil

	case *pb.ClientMessage_MediaAck:
		if s := d.media.session(c, m.MediaAck.SessionId); s != nil {
			for _, seq := range m.MediaAck.Seqs {
				s.ack(seq)
			}
			if m.MediaAck.Keyframe {
				s.wantKeyframe()
			}
		} else if r := d.media.relay(c, m.MediaAck.SessionId); r != nil && m.MediaAck.Keyframe {
			r.wantKeyframe() // frames were acked to the host on arrival
		}
		return nil, nil

	case *pb.ClientMessage_BrowserLogs:
		p, err := c.browserPane(m.BrowserLogs.PaneId)
		if err != nil {
			return nil, err
		}
		j, err := d.browsers.readLogs(p.id, m.BrowserLogs.Kind, m.BrowserLogs.Clear)
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_BrowserResult{BrowserResult: &pb.BrowserResult{Json: j}}}, nil

	case *pb.ClientMessage_BrowserNavigate:
		p, err := c.pane(m.BrowserNavigate.PaneId)
		if err != nil {
			return nil, err
		}
		if !p.isBrowser() {
			return nil, errors.New("not a browser pane")
		}
		switch a := m.BrowserNavigate.Action; a {
		case "load":
			u := BrowserURL(m.BrowserNavigate.Url)
			if strings.HasPrefix(u, DevToolsURL) {
				return nil, errors.New("open DevTools from a page (Show DevTools), not by its address")
			}
			d.browsers.navigate(p.id, a, u)
		case "back", "forward", "reload", "stop":
			d.browsers.navigate(p.id, a, "")
		default:
			return nil, fmt.Errorf("unknown browser action %q", a)
		}
		return nil, nil

	case *pb.ClientMessage_AttachPane:
		p, err := c.pane(m.AttachPane.PaneId)
		if err != nil {
			return nil, err
		}
		// Attaching again re-sends the snapshot (a guest's daemon does, for
		// each of its clients that starts viewing).
		c.attached[p.id] = p
		p.attach(c)
		return nil, nil

	case *pb.ClientMessage_DetachPane:
		if p, ok := c.attached[m.DetachPane.PaneId]; ok {
			p.detach(c)
			delete(c.attached, p.id)
		}
		return nil, nil

	case *pb.ClientMessage_Input:
		p, err := c.pane(m.Input.PaneId)
		if err != nil {
			return nil, err
		}
		resized, err := p.input(c, m.Input.Data)
		if resized {
			c.paneResized(p)
		}
		d.mu.Lock()
		c.lastInput = time.Now()
		d.agentHub.userInput(p)
		d.typed(cmp.Or(c.peer, d.id.me.UserId), p.id)
		d.sshHub.noteTyped(c, p.id)
		if strings.ContainsAny(string(m.Input.Data), "\r\n") {
			d.keepRun(p) // a scheduled run you've typed into stays
		}
		if esc := string(m.Input.Data); esc == "\x1b" || esc == "\x1b[27u" || esc == "\x1b[27;1u" {
			d.agentHub.interrupted(p)
		}
		d.mu.Unlock()
		return nil, err

	case *pb.ClientMessage_Resize:
		p, err := c.pane(m.Resize.PaneId)
		if err != nil {
			return nil, err
		}
		if m.Resize.CellWidthPx > 0 && m.Resize.CellHeightPx > 0 {
			p.setCellSize(int(m.Resize.CellWidthPx), int(m.Resize.CellHeightPx))
		}
		if p.resize(c, m.Resize.Cols, m.Resize.Rows) {
			c.paneResized(p)
		}
		return nil, nil

	case *pb.ClientMessage_Focus:
		p, err := c.pane(m.Focus.PaneId)
		if err != nil {
			return nil, err
		}
		if p.focus(c) {
			c.paneResized(p)
		}
		d.mu.Lock()
		if c.peer == "" {
			// A guest focusing doesn't move your focus; it's their presence.
			d.focusPane(p.id)
			c.focus = p.id
			d.peerPresence(d.id.me.UserId, p.id)
		}
		d.mu.Unlock()
		return nil, nil

	case *pb.ClientMessage_ReadPane:
		p, err := c.pane(m.ReadPane.PaneId)
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_PaneText{PaneText: &pb.PaneText{
			PaneId: p.id, Text: p.text(m.ReadPane.Scrollback),
		}}}, nil

	case *pb.ClientMessage_SignalPane:
		p, err := c.pane(m.SignalPane.PaneId)
		if err != nil {
			return nil, err
		}
		return nil, p.signal(syscall.Signal(m.SignalPane.Signal))

	case *pb.ClientMessage_SetColors:
		d.mu.Lock()
		d.setColors(m.SetColors)
		d.mu.Unlock()
		return nil, nil

	case *pb.ClientMessage_Presence:
		d.mu.Lock()
		c.presence = m.Presence
		switch {
		case c.peer != "":
			d.peerPresence(c.peer, m.Presence.FocusedPaneId)
		case m.Presence.FocusedPaneId != "":
			c.focus = m.Presence.FocusedPaneId
			d.peerPresence(d.id.me.UserId, m.Presence.FocusedPaneId)
		}
		d.presenceChanged()
		d.mu.Unlock()
		return nil, nil

	case *pb.ClientMessage_RequestGrant:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.requestGrant(c, msg.Id, m.RequestGrant)

	case *pb.ClientMessage_RespondGrant:
		d.mu.Lock()
		defer d.mu.Unlock()
		if _, ok := d.grants[m.RespondGrant.Id]; !ok {
			return nil, errors.New("no such grant request")
		}
		d.resolveGrant(m.RespondGrant.Id, m.RespondGrant.Decision, "")
		return nil, nil

	case *pb.ClientMessage_CreatePairing:
		d.mu.Lock()
		defer d.mu.Unlock()
		if err := d.mayShowPairingSecret(c); err != nil {
			d.audit("user", "pairing-refused", "", err.Error())
			return nil, err
		}
		p, err := d.createPairing()
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_Pairing{Pairing: p}}, nil

	case *pb.ClientMessage_ShowPairing:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.showPairing()

	case *pb.ClientMessage_ListDevices:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.listDevices()

	case *pb.ClientMessage_ListAudit:
		n := int(m.ListAudit.Limit)
		if n <= 0 || n > 1000 {
			n = 200
		}
		events, err := d.store.RecentAudit(n)
		if err != nil {
			return nil, err
		}
		d.mu.Lock()
		log := &pb.AuditLog{Events: d.activity(events)}
		d.mu.Unlock()
		return &pb.ServerMessage{Msg: &pb.ServerMessage_AuditLog{AuditLog: log}}, nil

	case *pb.ClientMessage_RevokeDevice:
		d.mu.Lock()
		err := d.revokeDevice(m.RevokeDevice.DeviceId)
		d.mu.Unlock()
		if err == nil {
			go d.hammockKey.forgetDevice(m.RevokeDevice.DeviceId)
		}
		return nil, err

	case *pb.ClientMessage_HammockKeyCommand:
		// The helper asks you with Touch ID: the reply waits for it.
		go func() {
			reply := &pb.ServerMessage{ReplyTo: msg.Id}
			if st, err := d.hammockKey.command(m.HammockKeyCommand); err != nil {
				reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
			} else {
				reply.Msg = &pb.ServerMessage_HammockKeyStatus{HammockKeyStatus: st}
			}
			c.send(reply)
		}()
		return nil, errDeferred

	case *pb.ClientMessage_RespondHammockKey:
		// An approval replies once the helper has checked it.
		id := msg.Id
		reply := func(err error) {
			r := &pb.ServerMessage{ReplyTo: id, Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}}
			if err != nil {
				r.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
			}
			if id != 0 {
				c.send(r)
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if err := d.hammockKey.respond(c, m.RespondHammockKey, reply); err != nil {
			return nil, err
		}
		return nil, errDeferred

	case *pb.ClientMessage_SetApprovalKey:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.hammockKey.setApprovalKey(c, m.SetApprovalKey)

	case *pb.ClientMessage_SshSetup:
		r, err := d.sshSetup(m.SshSetup)
		if err != nil {
			return nil, err
		}
		return &pb.ServerMessage{Msg: &pb.ServerMessage_SshSetupReply{SshSetupReply: r}}, nil

	case *pb.ClientMessage_SetRemoteAccess:
		d.warmRemote()
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.setRemoteAccess(m.SetRemoteAccess.Enabled)

	case *pb.ClientMessage_SetRemoteNetworks:
		return nil, d.setRemoteNetworks(m.SetRemoteNetworks)

	case *pb.ClientMessage_SwitchAgent:
		// Slow (waits for the agent to exit): reply when done without
		// blocking this connection's other requests.
		id, sw := msg.Id, m.SwitchAgent
		go func() {
			reply := &pb.ServerMessage{ReplyTo: id}
			if report, err := d.switchAgent(sw.PaneId, sw.TargetHarness); err != nil {
				reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
			} else {
				reply.Msg = &pb.ServerMessage_AgentSwitched{AgentSwitched: &pb.AgentSwitched{Report: report}}
			}
			if id != 0 {
				c.send(reply)
			}
		}()
		return nil, errDeferred

	case *pb.ClientMessage_ListSessions:
		return d.listSessions(c, m.ListSessions)

	case *pb.ClientMessage_SessionExcerpts:
		// Reading a long session takes a moment: reply when done.
		id, se := msg.Id, m.SessionExcerpts
		go func() {
			reply := &pb.ServerMessage{ReplyTo: id}
			if v, err := d.sessionExcerpts(c, se); err != nil {
				reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
			} else {
				reply.Msg = &pb.ServerMessage_SessionExcerpt{SessionExcerpt: v}
			}
			if id != 0 {
				c.send(reply)
			}
		}()
		return nil, errDeferred

	case *pb.ClientMessage_ReadSession:
		// A long session takes a moment to parse: reply when done.
		id, rs := msg.Id, m.ReadSession
		go func() {
			reply := &pb.ServerMessage{ReplyTo: id}
			if v, err := d.readSession(rs); err != nil {
				reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
			} else {
				reply.Msg = &pb.ServerMessage_AgentView{AgentView: v}
			}
			if id != 0 {
				c.send(reply)
			}
		}()
		return nil, errDeferred

	case *pb.ClientMessage_StartAgent, *pb.ClientMessage_ListFolders:
		// Both may ask the login shell or the disk: reply when done.
		id := msg.Id
		go func() {
			var r *pb.ServerMessage
			var err error
			if sa := msg.GetStartAgent(); sa != nil {
				r, err = d.startAgent(sa)
			} else {
				r = &pb.ServerMessage{Msg: &pb.ServerMessage_FolderList{FolderList: d.listFolders(msg.GetListFolders())}}
			}
			if err != nil {
				r = &pb.ServerMessage{Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}}
			}
			if id != 0 {
				r.ReplyTo = id
				c.send(r)
			}
		}()
		return nil, errDeferred

	case *pb.ClientMessage_ResumeSession:
		// Converting a session can take a moment: reply when done.
		id, rs := msg.Id, m.ResumeSession
		go func() {
			reply := &pb.ServerMessage{ReplyTo: id}
			if r, err := d.resumeSession(rs); err != nil {
				reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
			} else {
				reply.Msg = &pb.ServerMessage_SessionResumed{SessionResumed: r}
			}
			if id != 0 {
				c.send(reply)
			}
		}()
		return nil, errDeferred

	case *pb.ClientMessage_MoveTab:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.moveTab(m.MoveTab.TabId, m.MoveTab.GroupId, int(m.MoveTab.Index))

	case *pb.ClientMessage_FloatPane:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.floatPane(m.FloatPane)

	case *pb.ClientMessage_SetLayout:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.setLayout(m.SetLayout.TabId, m.SetLayout.Layout)

	case *pb.ClientMessage_MergeTab:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.mergeTab(m.MergeTab.TabId, m.MergeTab.IntoPaneId, m.MergeTab.Direction)

	case *pb.ClientMessage_ReplacePane:
		d.mu.Lock()
		defer d.mu.Unlock()
		p, err := d.replacePane(m.ReplacePane.PaneId, m.ReplacePane.Spec)
		if err != nil {
			return nil, err
		}
		return created(p.id), nil

	case *pb.ClientMessage_BreakOutPane:
		d.mu.Lock()
		defer d.mu.Unlock()
		t, err := d.breakOutPane(m.BreakOutPane.PaneId)
		if err != nil {
			return nil, err
		}
		return created(t.Id), nil

	case *pb.ClientMessage_CreateInvite:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.createInvite(m.CreateInvite)

	case *pb.ClientMessage_RevokeInvite:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.revokeInvite(m.RevokeInvite.GroupId)

	case *pb.ClientMessage_RespondJoin:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.respondJoin(m.RespondJoin)

	case *pb.ClientMessage_SetMember:
		d.mu.Lock()
		defer d.mu.Unlock()
		if c.peer != "" {
			// A guest leaving.
			if g := d.groups[m.SetMember.GroupId]; g != nil {
				d.setMemberRole(g, c.peer, pb.Role_ROLE_UNSPECIFIED)
				d.resendState(c)
				d.hangUpIfOutside(c, true)
				d.audit("peer:"+c.name, "left", g.Id, "")
			}
			return nil, nil
		}
		return nil, d.setMember(m.SetMember)

	case *pb.ClientMessage_StopSharing:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.stopSharing(m.StopSharing.GroupId)

	case *pb.ClientMessage_SetProfile:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.setProfile(m.SetProfile)

	case *pb.ClientMessage_PeerJoin:
		d.mu.Lock()
		defer d.mu.Unlock()
		if c.peer == "" {
			return nil, errors.New("only for other Macs")
		}
		return d.peerJoin(c, msg.Id, m.PeerJoin)

	case *pb.ClientMessage_JoinShared:
		return d.joinShared(c, msg.Id, m.JoinShared.Link)

	case *pb.ClientMessage_DiscoverComputers:
		id := msg.Id
		d.mu.Lock()
		d.expectLinks() // (Link a Computer is open here: a computer you're at may ask)
		d.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r := d.discoverComputers(ctx)
			if id != 0 {
				c.send(&pb.ServerMessage{ReplyTo: id, Msg: &pb.ServerMessage_DiscoveredComputers{DiscoveredComputers: r}})
			}
		}()
		return nil, errDeferred
	case *pb.ClientMessage_LinkComputer:
		return d.linkComputer(c, msg.Id, m.LinkComputer)
	case *pb.ClientMessage_RespondLink:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.respondLink(m.RespondLink)
	case *pb.ClientMessage_UnlinkComputer:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.unlinkComputer(m.UnlinkComputer.UserId, m.UnlinkComputer.Forget)
	case *pb.ClientMessage_SetComputerTint:
		// Each computer's record is its own: another's colour and icon are set there.
		d.mu.Lock()
		uid := m.SetComputerTint.UserId
		if uid == "" || uid == d.id.me.UserId {
			defer d.mu.Unlock()
			return nil, d.setOwnLook(m.SetComputerTint.Tint, m.SetComputerTint.Icon)
		}
		l, name := d.linkOf(uid), d.comp.links[uid].GetRecord().GetName()
		online := l != nil && l.online
		d.mu.Unlock()
		if !d.isLinkedLocked(uid) {
			return nil, errors.New("no such linked computer")
		}
		if !online {
			return nil, fmt.Errorf("%s is offline: its icon is set there", cmpOr(name, "that computer"))
		}
		return l.forward(c, msg)
	// Between linked computers (links.go): each only from a computer
	// linked with this one, except the introduced link's handshake.
	case *pb.ClientMessage_IntroduceComputers:
		if c.peer == "" {
			return nil, errors.New("only for other computers")
		}
		return nil, d.takeIntroductions(c, m.IntroduceComputers)
	case *pb.ClientMessage_SshAgentCall:
		return d.sshAgentCall(c, msg.Id, m.SshAgentCall)
	case *pb.ClientMessage_UnlinkRequest:
		if c.peer == "" {
			return nil, errors.New("only for other computers")
		}
		return d.takeUnlinkRequest(c, m.UnlinkRequest)
	case *pb.ClientMessage_PeerIntroLink:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.peerIntroLink(c, m.PeerIntroLink)
	case *pb.ClientMessage_CreateLinkInvite:
		d.warmRemote()
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.createLinkInvite()
	case *pb.ClientMessage_PeerLinkHello:
		return d.peerLinkHelloFrom(c, msg.Id, m.PeerLinkHello)
	case *pb.ClientMessage_PeerLinkReveal:
		d.mu.Lock()
		defer d.mu.Unlock()
		if c.peer == "" {
			return nil, errors.New("only for other computers")
		}
		return d.peerLinkReveal(c, msg.Id, m.PeerLinkReveal)
	case *pb.ClientMessage_PeerLinkConfirm:
		d.mu.Lock()
		defer d.mu.Unlock()
		if c.peer == "" {
			return nil, errors.New("only for other computers")
		}
		return d.peerLinkConfirm(c, msg.Id, m.PeerLinkConfirm)

	case *pb.ClientMessage_LeaveShared:
		return nil, d.leaveShared(m.LeaveShared.GroupId)

	case *pb.ClientMessage_UploadFile:
		return nil, d.uploadFile(c, m.UploadFile)

	case *pb.ClientMessage_RegisterPush:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.registerPush(c, m.RegisterPush)

	case *pb.ClientMessage_MoveGroup:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.moveGroup(m.MoveGroup.GroupId, int(m.MoveGroup.Index))

	case *pb.ClientMessage_ClearAttention:
		d.mu.Lock()
		defer d.mu.Unlock()
		d.clearAttention(m.ClearAttention.Id)
		return nil, nil
	}
	return nil, errors.New("unsupported message")
}

// audited reports whether msg from c goes in the audit log: mutating
// requests made by agents (the MCP server) and, later, remote peers.
func (c *conn) audited(msg *pb.ClientMessage) bool {
	if c.kind != "mcp" && !c.remote && !c.caller.isAgent() {
		return false
	}
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Input, *pb.ClientMessage_CreatePane, *pb.ClientMessage_CreateTab,
		*pb.ClientMessage_ClosePane, *pb.ClientMessage_CloseTab, *pb.ClientMessage_SignalPane,
		*pb.ClientMessage_BrowserNavigate, *pb.ClientMessage_AgentAnswer, *pb.ClientMessage_AgentSend, *pb.ClientMessage_UploadFile, *pb.ClientMessage_ClearLog, *pb.ClientMessage_StartAgent, *pb.ClientMessage_SetGroupIconImage, *pb.ClientMessage_SetGroupFolder,
		*pb.ClientMessage_EditorPush:
		return true
	case *pb.ClientMessage_EditorCommand:
		// Saving, reloading; not an agent showing you a line.
		return m.EditorCommand.Action != pb.EditorCommand_ACTION_SELECT && m.EditorCommand.Action != pb.EditorCommand_ACTION_SET_LANGUAGE
	case *pb.ClientMessage_FloatPane:
		// Floating, docking and minimising; not every move or raise.
		switch m.FloatPane.Action {
		case pb.FloatPane_ACTION_FRAME, pb.FloatPane_ACTION_RAISE:
			return false
		}
		return true
	}
	return false
}

// browserCallMutates reports whether a DevTools method acts on the page
// (and so goes in the audit log) rather than only reading it.
func browserCallMutates(method string) bool {
	switch method {
	case "Page.captureScreenshot", "Accessibility.getFullAXTree", "DOM.getDocument", "Page.getLayoutMetrics":
		return false
	}
	return true
}

// throttleAudit keeps a guest's typing to one audit row per pane a minute.
// Callers hold d.mu.
func (c *conn) throttleAudit(msg *pb.ClientMessage) bool {
	var paneID string
	switch {
	case msg.GetInput() != nil:
		paneID = msg.GetInput().PaneId
	case msg.GetEditorPush() != nil:
		paneID = "editor:" + msg.GetEditorPush().PaneId
	default:
		return true
	}
	if c.auditAt == nil {
		c.auditAt = map[string]time.Time{}
	}
	if time.Since(c.auditAt[paneID]) < time.Minute {
		return false
	}
	c.auditAt[paneID] = time.Now()
	return true
}

func auditAction(msg *pb.ClientMessage) string {
	switch msg.Msg.(type) {
	case *pb.ClientMessage_Input:
		return "input"
	case *pb.ClientMessage_CreatePane:
		return "create-pane"
	case *pb.ClientMessage_CreateTab:
		return "create-tab"
	case *pb.ClientMessage_ClosePane:
		return "close-pane"
	case *pb.ClientMessage_CloseTab:
		return "close-tab"
	case *pb.ClientMessage_SignalPane:
		return "signal"
	case *pb.ClientMessage_BrowserNavigate:
		return "browse"
	case *pb.ClientMessage_AgentAnswer:
		return "answer"
	case *pb.ClientMessage_AgentSend:
		return "message"
	case *pb.ClientMessage_UploadFile:
		return "upload"
	case *pb.ClientMessage_ClearLog:
		return "clear-log"
	case *pb.ClientMessage_FloatPane:
		return "arrange-pane"
	case *pb.ClientMessage_StartAgent:
		return "start-agent"
	case *pb.ClientMessage_SetGroupIconImage:
		return "group-picture"
	case *pb.ClientMessage_SetGroupFolder:
		return "group-folder"
	case *pb.ClientMessage_EditorPush:
		return "edit"
	case *pb.ClientMessage_EditorCommand:
		return "editor-" + strings.ToLower(strings.TrimPrefix(msg.GetEditorCommand().Action.String(), "ACTION_"))
	}
	return "other"
}

func auditTarget(msg *pb.ClientMessage) string {
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_SetGroupIconImage:
		return m.SetGroupIconImage.GroupId
	case *pb.ClientMessage_SetGroupFolder:
		return m.SetGroupFolder.GroupId + " → " + m.SetGroupFolder.Folder
	case *pb.ClientMessage_StartAgent:
		return m.StartAgent.Harness + " in " + m.StartAgent.Cwd
	case *pb.ClientMessage_EditorPush:
		return m.EditorPush.PaneId
	case *pb.ClientMessage_EditorCommand:
		if m.EditorCommand.Path != "" {
			return m.EditorCommand.PaneId + " → " + m.EditorCommand.Path
		}
		return m.EditorCommand.PaneId
	case *pb.ClientMessage_ClearLog:
		return m.ClearLog.PaneId
	case *pb.ClientMessage_FloatPane:
		return floatTarget(m.FloatPane)
	case *pb.ClientMessage_Input:
		return m.Input.PaneId
	case *pb.ClientMessage_CreatePane:
		return m.CreatePane.SplitPaneId
	case *pb.ClientMessage_CreateTab:
		return m.CreateTab.GroupId
	case *pb.ClientMessage_ClosePane:
		return m.ClosePane.PaneId
	case *pb.ClientMessage_CloseTab:
		return m.CloseTab.TabId
	case *pb.ClientMessage_SignalPane:
		return m.SignalPane.PaneId
	case *pb.ClientMessage_BrowserNavigate:
		return m.BrowserNavigate.PaneId
	case *pb.ClientMessage_AgentAnswer:
		return m.AgentAnswer.PaneId
	case *pb.ClientMessage_AgentSend:
		return m.AgentSend.PaneId
	}
	return ""
}

// handleRemoteAuth gates remote connections: until authenticated only Hello,
// Pair and Authenticate are accepted, and local-only administration is never
// available remotely. done=true means the message was fully handled here.
func (c *conn) handleRemoteAuth(msg *pb.ClientMessage) (*pb.ServerMessage, error, bool) {
	d := c.d
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Hello:
		return nil, nil, false
	case *pb.ClientMessage_Pair:
		d.mu.Lock()
		defer d.mu.Unlock()
		r, err := d.pair(c, m.Pair)
		return r, err, true
	case *pb.ClientMessage_Authenticate:
		d.mu.Lock()
		defer d.mu.Unlock()
		return nil, d.authenticate(c, m.Authenticate), true
	case *pb.ClientMessage_PeerHello:
		d.mu.Lock()
		defer d.mu.Unlock()
		r, err := d.peerHello(c, m.PeerHello)
		return r, err, true
	case *pb.ClientMessage_CreatePairing, *pb.ClientMessage_ShowPairing, *pb.ClientMessage_ListDevices, *pb.ClientMessage_RevokeDevice, *pb.ClientMessage_SetRemoteAccess, *pb.ClientMessage_SetRemoteNetworks,
		*pb.ClientMessage_ShutdownDaemon, *pb.ClientMessage_ListAudit, *pb.ClientMessage_BrowserCall, *pb.ClientMessage_BrowserLogs,
		*pb.ClientMessage_ServerStatus, *pb.ClientMessage_EndScreenSession,
		*pb.ClientMessage_HammockKeyCommand, *pb.ClientMessage_SshSetup:
		return nil, errors.New("only available on this Mac"), true
	case *pb.ClientMessage_CreateInvite, *pb.ClientMessage_RevokeInvite, *pb.ClientMessage_StopSharing,
		*pb.ClientMessage_SetProfile, *pb.ClientMessage_JoinShared, *pb.ClientMessage_LeaveShared:
		return nil, errors.New("only available on this Mac"), true
	case *pb.ClientMessage_DiscoverComputers, *pb.ClientMessage_LinkComputer, *pb.ClientMessage_RespondLink,
		*pb.ClientMessage_UnlinkComputer, *pb.ClientMessage_CreateLinkInvite:
		return nil, errors.New("only available on this computer"), true
	case *pb.ClientMessage_SetComputerTint, *pb.ClientMessage_SshAgentCall:
		// From a linked computer (its person picked this one's colour or icon), not a phone.
		d.mu.Lock()
		peer := c.peer
		d.mu.Unlock()
		if peer == "" {
			return nil, errors.New("only available on this computer"), true
		}
	}
	d.mu.Lock()
	authed, peer := c.authed, c.peer
	d.mu.Unlock()
	if _, ok := msg.Msg.(*pb.ClientMessage_SetMember); ok && peer == "" {
		// A guest's daemon may send it (to leave); a phone may not.
		return nil, errors.New("only available on this Mac"), true
	}
	switch msg.Msg.(type) {
	case *pb.ClientMessage_SaveSchedule, *pb.ClientMessage_DeleteSchedule:
		// Making and changing schedules: on a computer (here, or another
		// of yours), not a phone; a phone runs, pauses and skips them.
		d.mu.Lock()
		linked := d.isLinked(peer)
		d.mu.Unlock()
		if !linked {
			return nil, errors.New("only available on this Mac"), true
		}
	case *pb.ClientMessage_ListSessions, *pb.ClientMessage_ResumeSession, *pb.ClientMessage_ReadSession, *pb.ClientMessage_SessionExcerpts:
		// Your transcripts: your phone and your other computers may (the
		// session finder), a guest may not.
		d.mu.Lock()
		linked := d.isLinked(peer)
		d.mu.Unlock()
		if peer != "" && !linked {
			return nil, errors.New("only available on this Mac"), true
		}
	}
	if !authed {
		return nil, errors.New("not authenticated"), true
	}
	return nil, nil, false
}
