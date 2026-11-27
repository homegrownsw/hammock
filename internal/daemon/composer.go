package daemon

import (
	"fmt"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The agent view's message box, shared by your devices: what you've typed
// in a pane's box on one (SetComposerDraft) the daemon keeps with the pane
// and sends the others watching its agent view (AgentView.composer_draft),
// so you can start a message on the Mac and finish it on the phone. Each
// app takes it only while its own box has nothing unsent of its own.
//
// Kept in memory: after the daemon restarts, the first app to show the
// pane sends what it kept itself. Not a guest's (a shared Group's box is
// each person's own): guests neither set it nor get it.

// composerLimit is the longest draft kept (a long paste in the box).
const composerLimit = 256 << 10

// composerDraft is a pane's box, as last typed on one of your devices.
type composerDraft struct {
	text   string
	rev    int64  // counts changes, from 1
	writer string // the box it came from (SetComposerDraft.writer)
}

// setComposerDraft keeps what c's box has for a pane and sends it on to
// the pane's other watchers.
func (d *Daemon) setComposerDraft(m *pb.SetComposerDraft) error {
	if len(m.Text) > composerLimit || !utf8.ValidString(m.Text) {
		return fmt.Errorf("draft too long, or not text")
	}
	d.mu.Lock()
	p := d.panes[m.PaneId]
	w := d.agentWatches[m.PaneId] // the pane's own view (not a subagent's or shell's)
	d.mu.Unlock()
	if p == nil {
		return fmt.Errorf("no such pane %q", m.PaneId)
	}
	p.mu.Lock()
	if p.composer.text == m.Text && p.composer.rev > 0 {
		p.mu.Unlock()
		return nil
	}
	p.composer = composerDraft{text: m.Text, rev: p.composer.rev + 1, writer: m.Writer}
	c := p.composer
	p.mu.Unlock()
	if w == nil {
		return nil
	}
	u := &pb.AgentView{PaneId: m.PaneId}
	c.put(u)
	w.mu.Lock()
	w.withState(u)
	conns := make([]*conn, 0, len(w.conns))
	for cn := range w.conns {
		conns = append(conns, cn)
	}
	w.mu.Unlock()
	for _, cn := range conns {
		if d.getsComposer(cn) {
			cn.send(&pb.ServerMessage{Msg: &pb.ServerMessage_AgentView{AgentView: u}})
		}
	}
	return nil
}

// put sets v's composer fields.
func (c composerDraft) put(v *pb.AgentView) {
	v.ComposerDraft, v.ComposerRev, v.ComposerWriter = c.text, c.rev, c.writer
}

// withComposer is v for c with the pane's box in it, when v replaces what
// c shows of the pane's own view (so rev 0 there means there's none): a
// copy, as each watcher's may differ. A guest's has none. Callers hold w.mu.
func (w *agentWatch) withComposer(c *conn, v *pb.AgentView) *pb.AgentView {
	if !v.Replace || w.sub != "" || w.shell != "" || !w.d.getsComposer(c) {
		return v
	}
	w.p.mu.Lock()
	cd := w.p.composer
	w.p.mu.Unlock()
	if cd.rev == 0 {
		return v
	}
	v = proto.CloneOf(v)
	cd.put(v)
	return v
}

// getsComposer: c is one of your own (an app here, a paired phone, another
// of your computers), not a guest's daemon.
func (d *Daemon) getsComposer(c *conn) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return c.peer == "" || d.isLinked(c.peer)
}
