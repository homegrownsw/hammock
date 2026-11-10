package daemon

// The menu bar helper (ADR-0014, HammockMenu): the server's state in one
// answer, and ending screen sessions. This Mac's own clients only.

import (
	"slices"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// connKind is what a connection is, as the menu bar names it ("" for this
// Mac's own apps).
func (d *Daemon) connKind(c *conn) string {
	switch {
	case c.peer != "" && d.isLinked(c.peer):
		return "computer"
	case c.peer != "":
		return "guest"
	case c.remote && c.authed:
		return "phone"
	}
	return ""
}

func (d *Daemon) serverStatus() *pb.ServerMessage {
	d.mu.Lock()
	r := &pb.ServerStatusReply{
		Version: Version, StartedUnixMs: d.started.UnixMilli(), Panes: uint32(len(d.panes)),
		RemoteEnabled: d.remote.on, RemoteAddresses: slices.Clone(d.remote.addrs),
		Grants: uint32(len(d.grants)), ScreenSharing: screenSharing(),
	}
	seen := map[string]bool{}
	for c := range d.conns {
		kind := d.connKind(c)
		if kind == "" || seen[kind+c.name] {
			continue
		}
		seen[kind+c.name] = true
		r.Connected = append(r.Connected, &pb.Connected{Name: c.name, Kind: kind})
	}
	for _, a := range d.attention {
		if a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT || a.State == pb.AgentState_AGENT_STATE_ERRORED {
			r.NeedsYou = append(r.NeedsYou, proto.CloneOf(a))
		}
	}
	kinds := map[*conn]string{}
	for c := range d.conns {
		kinds[c] = d.connKind(c)
	}
	d.mu.Unlock()
	slices.SortFunc(r.NeedsYou, func(a, b *pb.AttentionItem) int { return int(a.CreatedUnixMs - b.CreatedUnixMs) })
	for _, s := range d.media.matching(func(s *mediaSession) bool { return strings.HasPrefix(s.paneID, "screen:") && !s.background }) {
		d.mu.Lock()
		name := s.owner.name
		d.mu.Unlock()
		s.mu.Lock()
		v := &pb.ScreenViewer{SessionId: s.id, Name: name, Kind: kinds[s.owner], DisplayId: screenDisplayOf(s.paneID),
			SinceUnixMs: s.began.UnixMilli(), Controlling: time.Since(s.inputAt) < 5*time.Second}
		s.mu.Unlock()
		r.ScreenViewers = append(r.ScreenViewers, v)
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_ServerStatusReply{ServerStatusReply: r}}
}

// endScreenSessions ends one screen session (with its viewer's background
// layer), or all of them, from this Mac.
func (d *Daemon) endScreenSessions(m *pb.EndScreenSession) {
	var owner *conn
	ended := 0
	if !m.All {
		for _, s := range d.media.matching(func(s *mediaSession) bool { return s.id == m.SessionId }) {
			owner = s.owner
		}
		if owner == nil {
			return
		}
	}
	for _, s := range d.media.matching(func(s *mediaSession) bool {
		return strings.HasPrefix(s.paneID, "screen:") && (m.All || s.owner == owner)
	}) {
		s.end("ended on the Mac")
		ended++
	}
	if ended > 0 {
		d.mu.Lock()
		d.audit("user", "screen-ended", "", "")
		d.mu.Unlock()
	}
}
