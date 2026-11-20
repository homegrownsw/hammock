package tui

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// link is the TUI's connection to hmkd: the client of the moment, replaced
// when it reconnects, so calls made from goroutines use the new one.
type link struct{ p atomic.Pointer[client.Client] }

func newLink(c *client.Client) *link {
	l := &link{}
	l.p.Store(c)
	return l
}

func (l *link) client() *client.Client { return l.p.Load() }

// swap makes c the connection, and closes the one it replaces.
func (l *link) swap(c *client.Client) {
	if old := l.p.Swap(c); old != nil && old != c {
		old.Close()
	}
}

func (l *link) Send(ctx context.Context, m *pb.ClientMessage) error { return l.client().Send(ctx, m) }

func (l *link) Call(ctx context.Context, m *pb.ClientMessage) (*pb.ServerMessage, error) {
	return l.client().Call(ctx, m)
}

// Dialer connects to hmkd again after the connection drops.
type Dialer func(ctx context.Context) (*client.Client, error)

// How long reconnecting waits between tries: doubling from the first to the
// last.
var (
	redialFirst = 250 * time.Millisecond
	redialMax   = 5 * time.Second
)

// lost is the connection to hmkd going away: the screen stays, dimmed, and
// it's dialled again until it answers (or the TUI quits).
func (a *App) lost() {
	if a.offline {
		return
	}
	a.offline = true
	a.dirty = true
	if a.redial == nil || a.stop == nil {
		return
	}
	go a.reconnect(a.stop)
}

// reconnect dials hmkd, waiting longer between tries, and subscribes; the
// loop takes the connection and its state from there (restored).
func (a *App) reconnect(stop context.Context) {
	wait := redialFirst
	for {
		select {
		case <-stop.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, redialMax)
		ctx, cancel := context.WithTimeout(stop, 5*time.Second)
		c, err := a.redial(ctx)
		if err != nil {
			cancel()
			continue
		}
		r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
		cancel()
		if err != nil {
			c.Close()
			continue
		}
		select {
		case a.actions <- func() { a.restored(c, r.GetState()) }:
		case <-stop.Done():
			c.Close()
		}
		return
	}
}

// restored takes the new connection: the panes on screen attach to it again
// (terminals keep what they show until their snapshot comes; editors start
// afresh), then its state replaces what was shown.
func (a *App) restored(c *client.Client, st *pb.StateUpdate) {
	a.c.swap(c)
	a.offline = false
	ctx := context.Background()
	for id := range a.terms {
		sz := a.sizes[id]
		_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: id, Cols: uint32(sz[0]), Rows: uint32(sz[1])}}})
		_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: id}}})
	}
	clear(a.editors)
	a.apply(st)
	a.flash("Reconnected to hmkd")
}

// drawOffline dims the panes while hmkd is away, and says so over them.
func (a *App) drawOffline() {
	if !a.offline {
		return
	}
	th, s := a.th, a.scr
	m := a.mainRect()
	a.dimRect(m, 0.6)
	y := m.y + m.h/2 - 1
	for i, line := range []string{reconnecting, offlineQuit} {
		label := "  " + line + "  "
		w := min(textWidth(label), m.w)
		c := Cell{FG: th.fg, HasFG: true, BG: th.menu, HasBG: true, Bold: i == 0}
		if i > 0 {
			c = Cell{FG: th.dim, HasFG: true}
		}
		s.Text(m.x+(m.w-w)/2, y+i, w, truncate(label, w), c)
	}
}

// offlineQuit says how to leave while hmkd is away (command() binds q).
const offlineQuit = "⌃B q  Quit"

const reconnecting = "Reconnecting to hmkd…"
