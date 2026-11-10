package daemon

// Remote video of visual panes (ADR-0012, docs/spikes/remote-media.md).
//
// A viewer (the iPhone, a guest's daemon) asks OpenMedia on its control
// link and gets a session. The helper that owns the pane's pixels encodes
// H.264 for that session alone (a frameSource); the daemon numbers the
// frames, sends them to the viewer (a mediaSink: QUIC, one stream per frame,
// or in band on the control link), and paces the encoder:
//
//   - bitrate follows queueing delay, measured from the viewer's acks;
//   - the encoder is held while ~100 ms of video is unacked, so queues stay
//     short (a held encoder skips frames; the next one is whatever the
//     screen shows when it resumes);
//   - a frame unacked for too long is given up with everything after it,
//     and the viewer gets a keyframe next.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const (
	mediaALPN          = "hmk-media/1"
	mediaTicketTTL     = 30 * time.Second
	mediaStale         = 400 * time.Millisecond // give a frame up after this
	mediaRecoveryRetry = 300 * time.Millisecond // ask again for a keyframe or refresh that hasn't come
	mediaQueueWindow   = 100 * time.Millisecond // hold the encoder past this much unacked video
	mediaMinRate       = 300_000
	mediaScreenMinRate = 1_000_000
	mediaMaxRate       = 16_000_000
	mediaScreenMaxRate = 40_000_000 // a whole 4K display, half of it a video, needs about this for its still parts' text
	mediaStartRate     = 4_000_000
	mediaFPS           = 60
	mediaFrameHeader   = 17
	mediaMaxFrame      = 64 << 20 // the largest frame record (as from a helper)
	mediaMinPixels     = 320 * 240
)

// mediaFrame is one encoded frame from a helper.
type mediaFrame struct {
	key     bool
	refresh bool   // predicted from an acknowledged LTR: frames before it may be skipped
	ltr     uint64 // its LTR token + 1 (0: not an LTR frame)
	pts     uint64 // µs, host clock
	w, h    int
	hevc    bool // else H.264
	// cropped: the part of a display it shows (SetMediaView), x, y, w, h as
	// fractions × 65535.
	cropped bool
	crop    [4]uint16
	data    []byte
}

// viewSource is a frameSource that sends part of a display when zoomed
// into (SetMediaView), and at a size that follows the link: the screen host.
type viewSource interface {
	setView(sid string, x, y, w, h float32)
	setSize(sid string, maxPixels int)
}

// A screen's size follows its rate (a poor link gets less detail, not the
// same detail smeared): the most pixels for a rate (bit/s) and up.
var screenSizes = []struct {
	rate   float64
	pixels int
}{
	{12_000_000, 0}, // the viewer's own size
	{6_000_000, 2560 * 1440},
	{3_000_000, 1920 * 1080},
	{0, 1280 * 720},
}

func screenSizeFor(rate float64) int {
	for i, s := range screenSizes {
		if rate >= s.rate {
			return i
		}
	}
	return len(screenSizes) - 1
}

// A new view (part of a display) is all-new pixels: on a slow link, at most
// one a second (the last asked for wins).
const (
	mediaSlowViews  = 6_000_000
	mediaViewsEvery = time.Second
)

// frameSource encodes a pane's pixels for a session: a helper.
type frameSource interface {
	startEncode(paneID, sid string, bitrate, fps, maxPixels int)
	setRate(sid string, bitrate, fps int)
	keyframe(sid string)
	// refresh asks for a frame predicted from an acknowledged LTR (or a
	// keyframe if there's none), after frames were given up.
	refresh(sid string)
	// ackLTR reports LTR frames the viewer has, with everything before them.
	ackLTR(sid string, tokens []uint64)
	hold(sid string, on bool)
	stopEncode(sid string)
}

// codecSource is a frameSource that can also encode the sharp tier (HEVC
// 4:4:4): the screen host. codec is "hevc444" or "h264".
type codecSource interface {
	encodeWith(paneID, sid string, bitrate, fps, maxPixels int, codec string)
	setCodec(sid, codec string)
}

// mediaSink carries a session's frames to its viewer.
type mediaSink interface {
	send(seq uint32, f *mediaFrame)
	// abandon gives up frames from seq on still on their way (QUIC); in
	// band it's a no-op, the frames arrive anyway.
	abandon(from uint32)
	close()
}

type mediaHub struct {
	d       *Daemon
	mu      sync.Mutex
	byID    map[string]*mediaSession
	tickets map[string]*mediaSession // hex ticket → session waiting for its QUIC connection
	relays  map[string]*mediaRelay   // video of panes shared with you (peermedia.go)
	lns     []*quic.Listener
	// sourceFor picks a pane's encoder, screenSource a display's (replaced
	// in tests).
	sourceFor    func(*pane) frameSource
	screenSource func() frameSource
	trs          []*quic.Transport
	port         int
	gate         authGate // connections not yet given their ticket
}

func newMediaHub(d *Daemon) *mediaHub {
	h := &mediaHub{d: d, byID: map[string]*mediaSession{}, tickets: map[string]*mediaSession{}, relays: map[string]*mediaRelay{}}
	h.screenSource = func() frameSource { return d.screens.media }
	h.sourceFor = func(p *pane) frameSource {
		switch {
		case p.isSimulator():
			return d.simulators.media
		case p.isBrowser():
			return d.browsers.media
		}
		return nil
	}
	return h
}

// What a session waits for before sending frames again.
type recovery int

const (
	noRecovery   recovery = iota
	needKeyframe          // a new viewer, or one whose decoder failed
	needRefresh           // frames were given up: a refresh or a keyframe
)

type sentFrame struct {
	at   time.Time
	size int
	ltr  uint64
}

type mediaSession struct {
	hub    *mediaHub
	id     string
	paneID string
	owner  *conn
	src    frameSource
	max    int

	mu       sync.Mutex
	sink     mediaSink
	ended    bool
	seq      uint32
	unacked  map[uint32]sentFrame
	rate     float64
	minLat   time.Duration   // the delay floor: the least a frame took lately (this window and the last)
	nextMin  time.Duration   // this window's least, which becomes the floor next window
	floor    float64         // the least rate (screens' is higher: a whole display in less is mush)
	ceiling  float64         // the most
	gaveUpAt []time.Time     // when frames were given up lately (the tier follows recent trouble)
	lowSince time.Time       // since when the rate's been below the sharp tier's (zero: it isn't)
	recent   []time.Duration // queueing delay of frames acked since the last check
	// The link's bandwidth (bit/s), the best a frame has done lately: a
	// frame's own transmission (size over this) isn't queueing.
	linkBW     float64
	held       bool
	sentHold   bool       // what the encoder was last told (syncHold)
	needSince  time.Time  // when the current recovery was asked for (asked again if it doesn't come)
	keyPending bool       // the viewer asked for a keyframe and hasn't acked one yet
	keySeq     uint32     // the latest keyframe sent: LTRs before it are no use
	gen        int        // bumped when the encoder restarts (its LTR tokens start again)
	holdMu     sync.Mutex // orders syncHold calls
	gaveUp     int        // times frames were given up
	began      time.Time
	need       recovery          // frames are dropped until one the viewer can decode
	ltrs       map[uint32]uint64 // LTR frames the viewer has, by seq, until everything before them is acked
	stop       chan struct{}
	// The sharp tier (screens): whether the viewer and source can, whether
	// it's on, and since when the link has looked good (or bad) for it.
	fps int // frames a second at most
	// Screens: a background layer's (it takes a small share of the
	// viewer's link), the size step it's at and since when the rate has
	// wanted another, and views waiting for a slow link.
	background bool
	// The viewer takes frames as datagrams (datagram.go), over QUIC.
	datagrams bool
	sizeStep  int
	sizeWant  int
	sizeSince time.Time
	viewMu    sync.Mutex
	viewAt    time.Time
	viewNext  *[4]float32
	viewTimer *time.Timer
	sentShape map[uint32]bool // pointer shapes this viewer has the image of
	inputAt   time.Time       // when its viewer last gave the screen input (it's controlling)
	canHEVC   bool
	hevc      bool
	tierSince time.Time
}

// The sharp tier needs a link that carries it; below this the session
// drops to low-latency H.264 (which recovers from loss without keyframes),
// and goes back after mediaTierUpAfter above mediaTierUp.
const (
	mediaTierDown    = 2_500_000
	mediaTierUp      = 6_000_000
	mediaTierUpAfter = 10 * time.Second
)

func codecName(hevc bool) string {
	if hevc {
		return "hevc444"
	}
	return "h264"
}

// shareLink gives the viewer's background layer of this display (if any)
// its share, and this session the rest: one budget per viewer, not two
// controllers each probing for all of it.
func (s *mediaSession) shareLink() {
	var bgRate float64
	for _, o := range s.hub.matching(func(o *mediaSession) bool {
		return o != s && o.owner == s.owner && o.background && o.paneID == s.paneID
	}) {
		o.mu.Lock()
		bgRate += o.rate
		o.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ceiling := float64(mediaScreenMaxRate)
	if s.linkBW > 0 {
		ceiling = min(ceiling, s.linkBW*0.85-bgRate)
	}
	s.ceiling = max(ceiling, s.floor)
	s.rate = min(s.rate, s.ceiling)
}

// view passes a viewer's view to the source: at once on a good link, at
// most once a second on a slow one (the latest wins).
func (s *mediaSession) view(x, y, w, h float32) {
	vs, ok := s.src.(viewSource)
	if !ok {
		return
	}
	s.mu.Lock()
	slow := s.rate < mediaSlowViews
	s.mu.Unlock()
	s.viewMu.Lock()
	defer s.viewMu.Unlock()
	wait := mediaViewsEvery - time.Since(s.viewAt)
	if !slow || wait <= 0 {
		s.viewAt, s.viewNext = time.Now(), nil
		vs.setView(s.id, x, y, w, h)
		return
	}
	s.viewNext = &[4]float32{x, y, w, h}
	if s.viewTimer == nil {
		s.viewTimer = time.AfterFunc(wait, func() {
			s.viewMu.Lock()
			v := s.viewNext
			s.viewNext, s.viewTimer, s.viewAt = nil, nil, time.Now()
			s.viewMu.Unlock()
			if v != nil {
				vs.setView(s.id, v[0], v[1], v[2], v[3])
			}
		})
	}
}

// screenCursor passes the pointer on to each viewer of its display (once
// per viewer: its foreground session), the shape's image the first time.
func (h *mediaHub) screenCursor(m screenMsg, shape *pb.ScreenCursor) {
	ids := map[string]bool{screenPaneID(m.Display): true}
	if m.IsMain {
		ids[screenPaneID(0)] = true
	}
	for _, s := range h.matching(func(s *mediaSession) bool { return ids[s.paneID] && !s.background }) {
		c := &pb.ScreenCursor{SessionId: s.id, X: m.X, Y: m.Y, Visible: m.Visible, ShapeId: m.Shape}
		if shape != nil {
			c.HotX, c.HotY, c.Width, c.Height = shape.HotX, shape.HotY, shape.Width, shape.Height
			s.mu.Lock()
			if s.sentShape == nil {
				s.sentShape = map[uint32]bool{}
			}
			if !s.sentShape[m.Shape] {
				s.sentShape[m.Shape] = true
				c.Png = shape.Png
			}
			s.mu.Unlock()
		}
		s.owner.send(&pb.ServerMessage{Msg: &pb.ServerMessage_ScreenCursor{ScreenCursor: c}})
	}
}

// screenState tells each screen's viewer (once each) whether the Mac is
// locked.
func (h *mediaHub) screenState(locked bool) {
	for _, s := range h.matching(func(s *mediaSession) bool { return strings.HasPrefix(s.paneID, "screen:") && !s.background }) {
		s.owner.send(&pb.ServerMessage{Msg: &pb.ServerMessage_ScreenState{ScreenState: &pb.ScreenState{SessionId: s.id, Locked: locked}}})
	}
}

// startEncode starts (or restarts) the session's encoder, in its tier.
func (s *mediaSession) startEncode(rate int) {
	s.mu.Lock()
	hevc := s.hevc
	s.mu.Unlock()
	if cs, ok := s.src.(codecSource); ok {
		cs.encodeWith(s.paneID, s.id, rate, s.fps, s.max, codecName(hevc))
		return
	}
	s.src.startEncode(s.paneID, s.id, rate, s.fps, s.max)
}

// ---------- listening ----------

// listen opens the QUIC listeners on the remote addresses (UDP, same
// ports). Callers hold d.mu.
func (h *mediaHub) listen(addrs []string, cert tls.Certificate, ifaceFor func(string) string) {
	tcfg := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{mediaALPN}, MinVersion: tls.VersionTLS13}
	qcfg := &quic.Config{MaxIdleTimeout: 20 * time.Second, KeepAlivePeriod: 5 * time.Second, MaxIncomingStreams: 8, MaxIncomingUniStreams: -1, EnableDatagrams: true}
	for _, a := range addrs {
		lc := net.ListenConfig{}
		if ifaceFor != nil {
			if iface := ifaceFor(a); iface != "" {
				lc.Control = bindToInterface(iface)
			}
		}
		udp, err := lc.ListenPacket(context.Background(), "udp", a)
		if err != nil {
			h.d.log.Warn("media listen", "addr", a, "err", err)
			continue
		}
		tr := &quic.Transport{Conn: udp}
		ln, err := tr.Listen(tcfg, qcfg)
		if err != nil {
			udp.Close()
			h.d.log.Warn("media listen", "addr", a, "err", err)
			continue
		}
		h.mu.Lock()
		if _, p, err := net.SplitHostPort(udp.LocalAddr().String()); err == nil {
			fmt.Sscan(p, &h.port)
		}
		h.lns = append(h.lns, ln)
		h.trs = append(h.trs, tr)
		h.mu.Unlock()
		go h.accept(ln)
		h.d.log.Info("media listening", "addr", udp.LocalAddr().String())
	}
}

// stopListening closes the listeners now (so turning remote access straight
// back on can listen again), and ends every QUIC session in the background.
func (h *mediaHub) stopListening() {
	h.mu.Lock()
	lns, trs := h.lns, h.trs
	h.lns, h.trs = nil, nil
	var quicOnes []*mediaSession
	for _, s := range h.byID {
		s.mu.Lock()
		_, inband := s.sink.(*inbandSink)
		s.mu.Unlock()
		if !inband {
			quicOnes = append(quicOnes, s)
		}
	}
	h.mu.Unlock()
	for _, ln := range lns {
		ln.Close()
	}
	for _, tr := range trs {
		tr.Close()
	}
	go func() {
		for _, s := range quicOnes {
			s.end("remote access turned off")
		}
	}()
}

// stopAddrs stops listening on addrs (a network left: remote access
// listens on the others still). Sessions there end as their connection
// goes.
func (h *mediaHub) stopAddrs(addrs []string) {
	h.mu.Lock()
	var close []int
	for i, tr := range h.trs {
		if slices.Contains(addrs, tr.Conn.LocalAddr().String()) {
			close = append(close, i)
		}
	}
	var lns []*quic.Listener
	var trs []*quic.Transport
	for _, i := range slices.Backward(close) {
		lns, trs = append(lns, h.lns[i]), append(trs, h.trs[i])
		h.lns = slices.Delete(h.lns, i, i+1)
		h.trs = slices.Delete(h.trs, i, i+1)
	}
	h.mu.Unlock()
	for i := range lns {
		lns[i].Close()
		trs[i].Close()
	}
}

// accept serves a listener's connections: only from where the remote
// listener takes them (remoteSourceOK), and only so many at once waiting
// for their ticket (as connections waiting to sign in: authGate).
func (h *mediaHub) accept(ln *quic.Listener) {
	var local net.IP
	if ua, ok := ln.Addr().(*net.UDPAddr); ok {
		local = ua.IP
	}
	for {
		qc, err := ln.Accept(context.Background())
		if err != nil {
			return
		}
		var src net.IP
		if ua, ok := qc.RemoteAddr().(*net.UDPAddr); ok {
			src = ua.IP
		}
		if !h.d.remoteSourceOK(src, local) {
			qc.CloseWithError(1, "forbidden")
			continue
		}
		release, ok := h.gate.enter(src.String())
		if !ok {
			qc.CloseWithError(1, "too many connections")
			continue
		}
		go h.serveQUIC(qc, release)
	}
}

// serveQUIC finds the session for a connection's ticket, then reads its
// acks. The ticket comes on the first stream that carries one:
// Network.framework opens an empty stream of its own first. Its slot at
// the gate is released once it has its ticket, or is closed.
func (h *mediaHub) serveQUIC(qc *quic.Conn, release func()) {
	defer release()
	ctx, cancel := context.WithTimeout(qc.Context(), 5*time.Second)
	got := make(chan *quic.Stream, 1)
	go func() {
		for {
			st, err := qc.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func() {
				b := make([]byte, 32)
				if _, err := io.ReadFull(st, b); err != nil {
					return
				}
				h.mu.Lock()
				s := h.tickets[hex.EncodeToString(b)]
				delete(h.tickets, hex.EncodeToString(b))
				h.mu.Unlock()
				if s == nil {
					return
				}
				select {
				case got <- st:
					go s.attachQUIC(qc, st)
				default:
					s.end("") // a second ticket on one connection: nothing will attach it
				}
			}()
		}
	}()
	select {
	case <-got:
		cancel()
	case <-ctx.Done():
		cancel()
		qc.CloseWithError(1, "no ticket")
	}
}

// ---------- sessions ----------

// open starts a session for c on a pane. Callers don't hold d.mu.
func (h *mediaHub) open(c *conn, m *pb.OpenMedia) (*pb.ServerMessage, error) {
	var src frameSource
	var canHEVC bool
	paneID := m.PaneId
	if m.Screen {
		// A whole display (screen.go): its own checks, whoever asks.
		if err := h.d.screenAllowed(c); err != nil {
			return nil, err
		}
		src, paneID = h.screenSource(), screenPaneID(m.DisplayId) // 0: the main display
		_, sharp := src.(codecSource)
		canHEVC = sharp && slices.Contains(m.Codecs, "hevc444")
		if !m.Background { // a viewer's second layer isn't another share
			h.d.mu.Lock()
			h.d.audit(c.auditActor(), "screen-share", paneID, "")
			h.d.mu.Unlock()
		}
	} else {
		h.d.mu.Lock()
		p, ok := h.d.panes[m.PaneId]
		h.d.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("no such pane %q", m.PaneId)
		}
		if src = h.sourceFor(p); src == nil {
			return nil, errors.New("this pane has no video")
		}
	}
	id := newID("ms")
	floor, ceiling := float64(mediaMinRate), float64(mediaMaxRate)
	if m.Screen {
		floor, ceiling = mediaScreenMinRate, mediaScreenMaxRate
	}
	maxPx := int(m.MaxPixels) // 0: the pane's own size
	if maxPx > 0 {
		maxPx = max(maxPx, mediaMinPixels)
	}
	s := &mediaSession{hub: h, id: id, paneID: paneID, owner: c, src: src, max: maxPx,
		unacked: map[uint32]sentFrame{}, rate: mediaStartRate, minLat: time.Hour, nextMin: time.Hour, floor: mediaMinRate, need: needKeyframe, keyPending: true, ltrs: map[uint32]uint64{}, stop: make(chan struct{}),
		canHEVC: canHEVC, hevc: canHEVC, tierSince: time.Now()}
	s.floor, s.ceiling = floor, ceiling
	s.fps = mediaFPS
	if m.MaxFps > 0 && int(m.MaxFps) < mediaFPS {
		s.fps = int(m.MaxFps)
	}
	s.background = m.Screen && m.Background
	s.datagrams = m.Datagrams
	t := &pb.MediaTicket{SessionId: id}
	h.mu.Lock()
	h.byID[id] = s
	if m.InBand {
		s.sink = &inbandSink{c: c, sid: id}
	} else {
		if len(h.lns) == 0 {
			delete(h.byID, id)
			h.mu.Unlock()
			return nil, errors.New("video needs remote access on")
		}
		tk := make([]byte, 32)
		_, _ = rand.Read(tk)
		h.tickets[hex.EncodeToString(tk)] = s
		t.Ticket, t.QuicPort = tk, uint32(h.port)
	}
	h.mu.Unlock()
	if m.InBand {
		s.begin()
	} else {
		time.AfterFunc(mediaTicketTTL, func() {
			h.mu.Lock()
			waiting := false
			for k, v := range h.tickets {
				if v == s {
					delete(h.tickets, k)
					waiting = true
				}
			}
			h.mu.Unlock()
			if waiting {
				s.end("no connection for the ticket")
			}
		})
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_MediaTicket{MediaTicket: t}}, nil
}

func (h *mediaHub) session(c *conn, id string) *mediaSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.byID[id]; s != nil && s.owner == c {
		return s
	}
	return nil
}

// closeFor ends a connection's sessions and relays.
func (h *mediaHub) closeFor(c *conn) {
	for _, s := range h.matching(func(s *mediaSession) bool { return s.owner == c }) {
		s.end("")
	}
	h.mu.Lock()
	var rs []*mediaRelay
	for _, r := range h.relays {
		if r.owner == c {
			rs = append(rs, r)
		}
	}
	h.mu.Unlock()
	for _, r := range rs {
		r.end("")
	}
}

// paneClosed ends a pane's sessions.
func (h *mediaHub) paneClosed(paneID string) {
	for _, s := range h.matching(func(s *mediaSession) bool { return s.paneID == paneID }) {
		s.end("the pane closed")
	}
}

func (h *mediaHub) matching(f func(*mediaSession) bool) []*mediaSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*mediaSession
	for _, s := range h.byID {
		if f(s) {
			out = append(out, s)
		}
	}
	return out
}

// deliver hands a session its next encoded frame (from a helper).
func (h *mediaHub) deliver(sid string, f *mediaFrame) {
	h.mu.Lock()
	s := h.byID[sid]
	h.mu.Unlock()
	if s != nil {
		s.deliver(f)
	}
}

// restart re-asks a source for every session it encodes (a helper restarted).
func (h *mediaHub) restart(src frameSource) {
	for _, s := range h.matching(func(s *mediaSession) bool { return s.src == src }) {
		// Under holdMu, so no hold or LTR ack meant for the old encoder
		// reaches the new one.
		s.holdMu.Lock()
		s.mu.Lock()
		running := s.sink != nil && !s.ended
		if running {
			s.need, s.keyPending, s.needSince = needKeyframe, true, time.Now()
			s.held, s.sentHold = false, false // a new encoder isn't held
			s.gen++                           // and its LTR tokens start again
			clear(s.ltrs)
			clear(s.unacked)
		}
		rate := s.rate
		s.mu.Unlock()
		if running {
			s.startEncode(int(rate))
		}
		s.holdMu.Unlock()
	}
}

func (s *mediaSession) attachQUIC(qc *quic.Conn, ctl *quic.Stream) {
	var sink mediaSink = &quicSink{qc: qc, open: map[uint32]*quic.SendStream{}}
	var dg *dgSink
	if s.datagrams && qc.ConnectionState().SupportsDatagrams.Local && qc.ConnectionState().SupportsDatagrams.Remote {
		dg = newDgSink(qc)
		sink = dg
	}
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		qc.CloseWithError(0, "ended")
		return
	}
	s.sink = sink
	s.mu.Unlock()
	s.begin()
	go func() {
		<-qc.Context().Done()
		s.end("")
	}()
	// Acks and keyframe requests.
	b := make([]byte, 4)
	for {
		if _, err := io.ReadFull(ctl, b[:1]); err != nil {
			return
		}
		switch b[0] {
		case 1:
			if _, err := io.ReadFull(ctl, b); err != nil {
				return
			}
			s.ack(binary.BigEndian.Uint32(b))
		case 2:
			s.wantKeyframe()
		case 3: // NACK: u32 seq, u8 n, n × u16
			var h [5]byte
			if _, err := io.ReadFull(ctl, h[:]); err != nil {
				return
			}
			idx := make([]byte, 2*int(h[4]))
			if _, err := io.ReadFull(ctl, idx); err != nil {
				return
			}
			if dg == nil {
				continue
			}
			miss := make([]uint16, h[4])
			for i := range miss {
				miss[i] = binary.BigEndian.Uint16(idx[2*i:])
			}
			dg.nack(binary.BigEndian.Uint32(h[:]), miss)
		default:
			qc.CloseWithError(2, "bad control record")
			return
		}
	}
}

// begin starts encoding and pacing.
func (s *mediaSession) begin() {
	via := "quic"
	s.mu.Lock()
	switch s.sink.(type) {
	case *inbandSink:
		via = "in band"
	case *dgSink:
		via = "quic datagrams"
	}
	s.mu.Unlock()
	s.owner.d.mu.Lock()
	viewer := s.owner.name
	s.owner.d.mu.Unlock()
	// Under holdMu, with end's stopEncode: a session that ended meanwhile
	// never starts its encoder.
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.began = time.Now()
	s.needSince = s.began
	s.mu.Unlock()
	s.hub.d.log.Info("video started", "session", s.id, "pane", s.paneID, "viewer", viewer, "via", via)
	s.startEncode(mediaStartRate)
	go s.pace()
}

func (s *mediaSession) end(reason string) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	sink := s.sink
	close(s.stop)
	frames, gaveUp, rate, secs := s.seq, s.gaveUp, s.rate, time.Since(s.began).Seconds()
	began := !s.began.IsZero()
	s.mu.Unlock()
	if began {
		s.hub.d.log.Info("video ended", "session", s.id, "frames", frames, "gave_up", gaveUp,
			"secs", int(secs), "last_rate_kbps", int(rate/1000), "reason", reason)
	}
	h := s.hub
	h.mu.Lock()
	delete(h.byID, s.id)
	for k, v := range h.tickets {
		if v == s {
			delete(h.tickets, k)
		}
	}
	h.mu.Unlock()
	s.holdMu.Lock()
	s.src.stopEncode(s.id)
	s.holdMu.Unlock()
	if sink != nil {
		sink.close()
	}
	if reason != "" {
		s.owner.send(&pb.ServerMessage{Msg: &pb.ServerMessage_MediaEnded{MediaEnded: &pb.MediaEnded{SessionId: s.id, Reason: reason}}})
	}
}

func (s *mediaSession) deliver(f *mediaFrame) {
	s.mu.Lock()
	if s.ended || s.sink == nil {
		s.mu.Unlock()
		return
	}
	if !f.key && (s.need == needKeyframe || (s.need == needRefresh && !f.refresh)) {
		s.mu.Unlock()
		return // it references a frame the viewer won't have
	}
	s.need = noRecovery
	s.seq++
	seq := s.seq
	if f.key {
		clear(s.ltrs) // references don't reach back past a keyframe
		s.keySeq = seq
	}
	s.unacked[seq] = sentFrame{at: time.Now(), size: len(f.data), ltr: f.ltr}
	sink := s.sink
	hold := !s.held && s.backedUp()
	if hold {
		s.held = true
	}
	s.mu.Unlock()
	sink.send(seq, f)
	if hold {
		s.syncHold()
	}
}

// backedUp: more than mediaQueueWindow of video unacked. Callers hold s.mu.
func (s *mediaSession) backedUp() bool {
	n := 0
	for _, f := range s.unacked {
		n += f.size
	}
	return float64(n*8) > s.rate*mediaQueueWindow.Seconds()
}

func (s *mediaSession) ack(seq uint32) {
	s.mu.Lock()
	f, ok := s.unacked[seq]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.unacked, seq)
	lat := time.Since(f.at)
	// An LTR from before the latest keyframe (or an old encoder) is no use.
	if f.ltr != 0 && seq >= s.keySeq {
		s.ltrs[seq] = f.ltr
	}
	if seq == s.keySeq {
		s.keyPending = false
	}
	s.minLat = min(s.minLat, lat)
	s.nextMin = min(s.nextMin, lat)
	// What this frame says the link carries, and the delay it met past its
	// own transmission: a 500 KB keyframe takes a while on any link, and
	// that alone mustn't cut the rate.
	if extra := lat - s.minLat; extra > time.Millisecond && f.size > 4000 {
		s.linkBW = max(s.linkBW, float64(f.size)*8/extra.Seconds())
	}
	q := lat - s.minLat
	if s.linkBW > 0 {
		q -= time.Duration(float64(f.size) * 8 / s.linkBW * float64(time.Second))
	}
	s.recent = append(s.recent, max(q, 0))
	release := s.held && !s.backedUp()
	if release {
		s.held = false
	}
	s.mu.Unlock()
	if release {
		s.syncHold()
	}
}

func (s *mediaSession) wantKeyframe() {
	s.mu.Lock()
	s.need, s.keyPending, s.needSince = needKeyframe, true, time.Now()
	clear(s.ltrs) // the viewer may have thrown frames away: only a keyframe will do
	s.mu.Unlock()
	s.src.keyframe(s.id)
}

// pace adapts the bitrate to queueing delay and gives up stuck frames.
func (s *mediaSession) pace() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	lastRate := 0.0
	tick := 0
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		}
		tick++
		// Screen Sharing turned off meanwhile: no more watching.
		if tick%20 == 0 && strings.HasPrefix(s.paneID, "screen:") && !screenSharing() {
			s.end("Screen Sharing was turned off on this Mac")
			return
		}
		s.mu.Lock()
		s.linkBW *= 0.995 // about halved in 14 s without a better sample
		// Give up a frame stuck past mediaStale, and everything after it.
		var oldest uint32
		now := time.Now()
		for seq, f := range s.unacked {
			if now.Sub(f.at) > mediaStale && (oldest == 0 || seq < oldest) {
				oldest = seq
			}
		}
		var sink mediaSink
		gaveUp := oldest != 0
		if gaveUp {
			s.gaveUp++
			sink = s.sink
			s.forgetUnsure()
			clear(s.unacked)
			// A viewer waiting for a keyframe can't use a refresh.
			if s.keyPending {
				s.need = needKeyframe
			} else {
				s.need = needRefresh
			}
			s.needSince = now
			s.rate = max(s.rate*0.6, s.floor)
			s.gaveUpAt = append(s.gaveUpAt, now)
		}
		for len(s.gaveUpAt) > 0 && now.Sub(s.gaveUpAt[0]) > 5*time.Second {
			s.gaveUpAt = s.gaveUpAt[1:]
		}
		if tick%50 == 0 && s.canHEVC {
			// Screens: how the link and the rate are doing (every 5 s).
			s.hub.d.log.Info("video rate", "session", s.id, "codec", codecName(s.hevc), "rate_kbps", int(s.rate/1000),
				"link_kbps", int(s.linkBW/1000), "min_lat_ms", s.minLat.Milliseconds(), "frames", s.seq, "gave_up", s.gaveUp)
		}
		// The delay floor moves with the link (a route that got longer isn't
		// a queue): the least of this 5 s window and the last.
		if tick%50 == 0 {
			if s.nextMin < time.Hour {
				s.minLat = s.nextMin
			}
			s.nextMin = time.Hour
		}
		if tick%2 == 0 && len(s.recent) > 0 && !gaveUp {
			// The typical frame's queueing delay: Wi-Fi's jitter puts some
			// frames tens of milliseconds late on an idle link, which isn't
			// a queue building.
			slices.Sort(s.recent)
			q := s.recent[len(s.recent)/2]
			switch {
			case q > 100*time.Millisecond:
				s.rate *= 0.8
			case q > 50*time.Millisecond:
				s.rate *= 0.94
			case q < 25*time.Millisecond:
				s.rate *= 1.08
			}
			s.rate = min(max(s.rate, s.floor), s.ceiling)
			s.recent = s.recent[:0]
		}
		if s.background {
			// The background layer: a small share of the link, no more.
			s.rate = min(s.rate, min(max(s.linkBW*0.1, mediaMinRate), 1_500_000))
		}
		// A recovery frame that hasn't come (dropped by the encoder, or a
		// still page with nothing new to encode) is asked for again.
		retry := noRecovery
		if !gaveUp && s.need != noRecovery && now.Sub(s.needSince) > mediaRecoveryRetry {
			retry, s.needSince = s.need, now
		}
		need := s.need
		acked, gen := s.readyLTRs(), s.gen
		release := gaveUp && s.held
		if release {
			s.held = false
		}
		rate := s.rate
		// The tier follows the link: down at once when it can't carry the
		// sharp one, up again once it has for a while.
		switchTo := ""
		if s.canHEVC {
			// Down once the link plainly can't carry it: frames given up
			// three times in 5 s, or a rate below the tier's for 2 s (not a
			// moment's dip: a window dragged makes big frames for a while).
			if rate < mediaTierDown {
				if s.lowSince.IsZero() {
					s.lowSince = now
				}
			} else {
				s.lowSince = time.Time{}
			}
			good := rate >= mediaTierUp && len(s.gaveUpAt) == 0
			switch {
			case s.hevc && (len(s.gaveUpAt) >= 3 || !s.lowSince.IsZero() && now.Sub(s.lowSince) > 2*time.Second):
				s.hevc, s.tierSince, switchTo = false, now, "h264"
				s.gaveUpAt = nil
			case !s.hevc && !good:
				s.tierSince = now
			case !s.hevc && now.Sub(s.tierSince) > mediaTierUpAfter:
				s.hevc, s.tierSince, switchTo = true, now, "hevc444"
			}
			if switchTo != "" {
				// The new encoder starts with a keyframe; until then
				// nothing else can be decoded.
				s.need, s.keyPending, s.needSince = needKeyframe, true, now
				s.gen++
				clear(s.ltrs)
			}
		}
		// The size follows the rate, down at once and up after 3 s (each
		// change starts the encoder again, with a keyframe).
		resize := -1
		if strings.HasPrefix(s.paneID, "screen:") && !s.background {
			want := screenSizeFor(rate)
			if want != s.sizeWant {
				s.sizeWant, s.sizeSince = want, now
			}
			if want != s.sizeStep && (want > s.sizeStep || now.Sub(s.sizeSince) > 3*time.Second) {
				s.sizeStep, resize = want, want
				// A new encoder, as for a tier: its keyframe first, and LTR
				// tokens from the old one mean nothing to it.
				s.need, s.keyPending, s.needSince = needKeyframe, true, now
				s.gen++
				clear(s.ltrs)
			}
		}
		s.mu.Unlock()
		if resize >= 0 {
			px := screenSizes[resize].pixels
			if px == 0 || s.max > 0 && px > s.max {
				px = s.max
			}
			s.hub.d.log.Info("video size", "session", s.id, "max_pixels", px, "rate_kbps", int(rate/1000))
			if vs, ok := s.src.(viewSource); ok {
				vs.setSize(s.id, px)
			}
		}
		if !s.background && strings.HasPrefix(s.paneID, "screen:") && tick%5 == 0 {
			s.shareLink()
		}
		if switchTo != "" {
			s.hub.d.log.Info("video tier", "session", s.id, "codec", switchTo, "rate_kbps", int(rate/1000))
			s.src.(codecSource).setCodec(s.id, switchTo)
		}
		if len(acked) > 0 {
			// Not to an encoder that restarted since (its tokens are different).
			s.holdMu.Lock()
			s.mu.Lock()
			same := s.gen == gen
			s.mu.Unlock()
			if same {
				s.src.ackLTR(s.id, acked)
			}
			s.holdMu.Unlock()
		}
		if gaveUp {
			sink.abandon(oldest)
			retry = need
		}
		switch retry {
		case needKeyframe:
			s.src.keyframe(s.id)
		case needRefresh:
			s.src.refresh(s.id)
		}
		if release {
			s.syncHold()
		}
		if rate != lastRate && (lastRate == 0 || rate/lastRate > 1.02 || rate/lastRate < 0.98) {
			s.src.setRate(s.id, int(rate), s.fps)
			lastRate = rate
		}
	}
}

// ---------- sinks ----------

// quicSink sends each frame on its own unidirectional stream.
type quicSink struct {
	qc   *quic.Conn
	mu   sync.Mutex
	open map[uint32]*quic.SendStream
}

func (q *quicSink) send(seq uint32, f *mediaFrame) {
	st, err := q.qc.OpenUniStream()
	if err != nil {
		return // out of streams or closed: it counts as lost, and is given up
	}
	q.mu.Lock()
	q.open[seq] = st
	q.mu.Unlock()
	hdr := frameHeader(seq, f)
	go func() {
		if _, err := st.Write(hdr); err == nil {
			if _, err := st.Write(f.data); err == nil {
				st.Close()
			}
		}
		q.mu.Lock()
		delete(q.open, seq)
		q.mu.Unlock()
	}()
}

// frameHeader is a frame record's header (and its crop, when cropped): the
// record a QUIC stream carries, and datagrams cut up.
func frameHeader(seq uint32, f *mediaFrame) []byte {
	hdr := make([]byte, mediaFrameHeader, mediaFrameHeader+8+len(f.data))
	binary.BigEndian.PutUint32(hdr, seq)
	if f.key {
		hdr[4] |= 1
	}
	if f.refresh {
		hdr[4] |= 2
	}
	if f.hevc {
		hdr[4] |= 4
	}
	binary.BigEndian.PutUint64(hdr[5:], f.pts)
	binary.BigEndian.PutUint16(hdr[13:], uint16(f.w))
	binary.BigEndian.PutUint16(hdr[15:], uint16(f.h))
	if f.cropped {
		hdr[4] |= 8
		for _, v := range f.crop {
			hdr = binary.BigEndian.AppendUint16(hdr, v)
		}
	}
	return hdr
}

// frameRecord is the whole record: header, crop, picture.
func frameRecord(seq uint32, f *mediaFrame) []byte { return append(frameHeader(seq, f), f.data...) }

func (q *quicSink) abandon(from uint32) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for seq, st := range q.open {
		if seq >= from {
			st.CancelWrite(1)
			delete(q.open, seq)
		}
	}
}

func (q *quicSink) close() { q.qc.CloseWithError(0, "") }

// inbandSink sends frames as MediaFrame messages on the viewer's connection.
type inbandSink struct {
	c   *conn
	sid string
}

func (b *inbandSink) send(seq uint32, f *mediaFrame) {
	b.c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_MediaFrame{MediaFrame: &pb.MediaFrame{
		SessionId: b.sid, Seq: seq, Keyframe: f.key, Refresh: f.refresh, Hevc: f.hevc, PtsUs: f.pts, Width: uint32(f.w), Height: uint32(f.h), Data: f.data,
		CropX: cropFrac(f, 0), CropY: cropFrac(f, 1), CropW: cropFrac(f, 2), CropH: cropFrac(f, 3),
	}}})
}

// cropFrac is a cropped frame's crop as a fraction (0 when it isn't cropped).
func cropFrac(f *mediaFrame, i int) float32 {
	if !f.cropped {
		return 0
	}
	return float32(f.crop[i]) / 65535
}

func (b *inbandSink) abandon(uint32) {}
func (b *inbandSink) close()         {}

// readyLTRs takes the LTR frames the viewer has along with every frame
// before them: only those are safe to predict from (a frame received
// after a missing one may reference it). Callers hold s.mu.
func (s *mediaSession) readyLTRs() []uint64 {
	done := s.seq
	for seq := range s.unacked {
		done = min(done, seq-1)
	}
	var tokens []uint64
	for seq, tok := range s.ltrs {
		if seq <= done {
			tokens = append(tokens, tok)
			delete(s.ltrs, seq)
		}
	}
	return tokens
}

// forgetUnsure drops LTR frames received after the first frame still
// missing, before giving frames up. Callers hold s.mu.
func (s *mediaSession) forgetUnsure() {
	if len(s.unacked) == 0 {
		return
	}
	missing := ^uint32(0)
	for seq := range s.unacked {
		missing = min(missing, seq)
	}
	for seq := range s.ltrs {
		if seq > missing {
			delete(s.ltrs, seq)
		}
	}
}

// syncHold tells the encoder whether it's held, as it is now. Calls are
// serialized and each sends the current state, so a release can't
// overtake the hold it releases (on a fast link the ack can come back
// before the hold is sent), which left the encoder held for good.
func (s *mediaSession) syncHold() {
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	s.mu.Lock()
	on := s.held
	changed := on != s.sentHold
	s.sentHold = on
	s.mu.Unlock()
	if changed {
		s.src.hold(s.id, on)
	}
}
