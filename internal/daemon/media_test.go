package daemon

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fakeEncoder stands in for a helper: 100 frames a second per session while
// not held, a keyframe first and whenever asked.
type fakeEncoder struct {
	d  *Daemon
	mu sync.Mutex
	s  map[string]*fakeSession
	// counters
	holds, keyframes, refreshes, stops int
	acked                              []uint64 // LTR tokens acknowledged
	ignoreKeys                         int      // keyframe requests to ignore (a dropped keyframe)
}

type fakeSession struct {
	held, wantKey, wantRefresh bool
	n                          uint64 // frames made
	stop                       chan struct{}
}

func (f *fakeEncoder) startEncode(paneID, sid string, bitrate, fps, maxPixels int) {
	f.mu.Lock()
	if old := f.s[sid]; old != nil {
		close(old.stop)
	}
	s := &fakeSession{wantKey: true, stop: make(chan struct{})}
	f.s[sid] = s
	f.mu.Unlock()
	go func() {
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
			}
			f.mu.Lock()
			if s.held {
				f.mu.Unlock()
				continue
			}
			key, refresh := s.wantKey, s.wantRefresh && !s.wantKey
			s.wantKey, s.wantRefresh = false, false
			s.n++
			var ltr uint64
			if s.n%5 == 0 {
				ltr = s.n // every fifth frame is an LTR frame (token n-1)
			}
			f.mu.Unlock()
			size := 2000
			if key {
				size = 20000
			}
			f.d.media.deliver(sid, &mediaFrame{key: key, refresh: refresh, ltr: ltr, pts: uint64(time.Now().UnixMicro()), w: 1206, h: 2622, data: make([]byte, size)})
		}
	}()
}

func (f *fakeEncoder) setRate(sid string, bitrate, fps int) {}

func (f *fakeEncoder) keyframe(sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keyframes++
	if f.ignoreKeys > 0 {
		f.ignoreKeys--
		return
	}
	if s := f.s[sid]; s != nil {
		s.wantKey = true
	}
}

func (f *fakeEncoder) refresh(sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	if s := f.s[sid]; s != nil {
		s.wantRefresh = true
	}
}

func (f *fakeEncoder) ackLTR(sid string, tokens []uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, tokens...)
}

func (f *fakeEncoder) hold(sid string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if on {
		f.holds++
	}
	if s := f.s[sid]; s != nil {
		s.held = on
	}
}

func (f *fakeEncoder) stopEncode(sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if s := f.s[sid]; s != nil {
		close(s.stop)
		delete(f.s, sid)
	}
}

func (f *fakeEncoder) count(n *int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *n
}

func mediaDaemon(t *testing.T) (*Daemon, *client.Client, *fakeEncoder, string) {
	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	enc := &fakeEncoder{d: d, s: map[string]*fakeSession{}}
	d.media.sourceFor = func(*pane) frameSource { return enc }
	go d.Serve()
	t.Cleanup(d.Shutdown)
	deadline := time.Now().Add(5 * time.Second)
	for len(d.RemoteAddrs()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Media"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	return d, c, enc, paneID
}

type gotFrame struct {
	seq     uint32
	key     bool
	refresh bool
	w, h    int
	n       int
}

// quicViewer connects like the iPhone does and returns its frames; acks
// are sent unless paused.
type quicViewer struct {
	qc     *quic.Conn
	ctl    *quic.Stream
	frames chan gotFrame
	mu     sync.Mutex
	paused bool
}

func dialMedia(t *testing.T, d *Daemon, ticket *pb.MediaTicket) *quicViewer {
	host, _, _ := net.SplitHostPort(d.RemoteAddrs()[0])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	qc, err := quic.DialAddr(ctx, net.JoinHostPort(host, strconv.Itoa(int(ticket.QuicPort))),
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{mediaALPN}}, &quic.Config{MaxIncomingUniStreams: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { qc.CloseWithError(0, "") })
	// An empty stream first, as Network.framework opens one.
	if _, err := qc.OpenStream(); err != nil {
		t.Fatal(err)
	}
	ctl, err := qc.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ctl.Write(ticket.Ticket); err != nil {
		t.Fatal(err)
	}
	v := &quicViewer{qc: qc, ctl: ctl, frames: make(chan gotFrame, 4096)}
	go func() {
		for {
			st, err := qc.AcceptUniStream(context.Background())
			if err != nil {
				return
			}
			go func() {
				b, err := io.ReadAll(st)
				if err != nil || len(b) < mediaFrameHeader {
					return // given up
				}
				f := gotFrame{seq: binary.BigEndian.Uint32(b), key: b[4]&1 != 0, refresh: b[4]&2 != 0,
					w: int(binary.BigEndian.Uint16(b[13:])), h: int(binary.BigEndian.Uint16(b[15:])), n: len(b) - mediaFrameHeader}
				v.mu.Lock()
				paused := v.paused
				v.mu.Unlock()
				if !paused {
					rec := []byte{1, 0, 0, 0, 0}
					binary.BigEndian.PutUint32(rec[1:], f.seq)
					v.mu.Lock()
					v.ctl.Write(rec)
					v.mu.Unlock()
				}
				v.frames <- f
			}()
		}
	}()
	return v
}

func (v *quicViewer) pause(on bool) {
	v.mu.Lock()
	v.paused = on
	v.mu.Unlock()
}

func (v *quicViewer) next(t *testing.T) gotFrame {
	t.Helper()
	select {
	case f := <-v.frames:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
	}
	return gotFrame{}
}

func TestMediaOverQUIC(t *testing.T) {
	d, c, enc, paneID := mediaDaemon(t)
	ticket := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID}}}).GetMediaTicket()
	if len(ticket.Ticket) != 32 || ticket.QuicPort == 0 {
		t.Fatalf("ticket %v", ticket)
	}
	v := dialMedia(t, d, ticket)

	first := v.next(t)
	if !first.key || first.w != 1206 || first.h != 2622 || first.n != 20000 {
		t.Fatalf("first frame %+v", first)
	}
	for i := 0; i < 50; i++ {
		v.next(t)
	}

	// LTR frames the viewer has were acknowledged to the encoder.
	for deadline := time.Now().Add(2 * time.Second); func() bool { enc.mu.Lock(); defer enc.mu.Unlock(); return len(enc.acked) == 0 }(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no LTR acknowledged")
		}
	}

	// The viewer stops acking: the encoder is held, the stuck frames are
	// given up, and a refresh (not a keyframe) follows; frames until it
	// are dropped.
	keysBefore := enc.count(&enc.keyframes)
	v.pause(true)
	deadline := time.Now().Add(3 * time.Second)
	for enc.count(&enc.holds) == 0 || enc.count(&enc.refreshes) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("holds %d refreshes %d", enc.count(&enc.holds), enc.count(&enc.refreshes))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if enc.count(&enc.keyframes) != keysBefore {
		t.Fatal("a keyframe after giving frames up, not a refresh")
	}
	v.pause(false)
	for {
		f := v.next(t)
		if f.key {
			t.Fatalf("keyframe %d instead of a refresh", f.seq)
		}
		if f.refresh {
			break
		}
	}

	// A ticket is single use.
	host, _, _ := net.SplitHostPort(d.RemoteAddrs()[0])
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	qc2, err := quic.DialAddr(ctx, net.JoinHostPort(host, strconv.Itoa(int(ticket.QuicPort))),
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{mediaALPN}}, nil)
	if err == nil {
		st, _ := qc2.OpenStream()
		st.Write(ticket.Ticket)
		if _, err := qc2.AcceptUniStream(ctx); err == nil {
			t.Fatal("a used ticket worked again")
		}
	}

	// Closing the session stops the encoder.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseMedia{CloseMedia: &pb.CloseMedia{SessionId: ticket.SessionId}}})
	deadline = time.Now().Add(2 * time.Second)
	for enc.count(&enc.stops) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("encoder not stopped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMediaInBand(t *testing.T) {
	_, c, enc, paneID := mediaDaemon(t)
	ticket := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, InBand: true}}}).GetMediaTicket()
	if len(ticket.Ticket) != 0 {
		t.Fatal("in-band session got a ticket")
	}
	n := 0
	var lastSeq uint32
	for n < 40 {
		ev := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetMediaFrame() != nil })
		f := ev.GetMediaFrame()
		if n == 0 && !f.Keyframe {
			t.Fatal("first in-band frame isn't a keyframe")
		}
		if f.Seq <= lastSeq {
			t.Fatalf("seq %d after %d", f.Seq, lastSeq)
		}
		lastSeq = f.Seq
		c.Send(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_MediaAck{MediaAck: &pb.MediaAck{SessionId: ticket.SessionId, Seqs: []uint32{f.Seq}}}})
		n++
	}
	if enc.count(&enc.holds) != 0 {
		t.Fatalf("held %d times with prompt acks", enc.count(&enc.holds))
	}
}

func TestMediaMissingPane(t *testing.T) {
	_, c, _, paneID := mediaDaemon(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: "nope"}}}); err == nil {
		t.Fatal("opened media for a missing pane")
	}
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, InBand: true}}}); err != nil {
		t.Fatal(err)
	}
}

// A guest's app watches a shared pane: its daemon relays the host's video
// (QUIC between the daemons) in band to the app. View is enough.
func TestSharedMedia(t *testing.T) {
	hostDir, guestDir := shortDir(t), shortDir(t)
	host, err := New(Config{Dir: hostDir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	enc := &fakeEncoder{d: host, s: map[string]*fakeSession{}}
	host.media.sourceFor = func(*pane) frameSource { return enc }
	go host.Serve()
	defer host.Shutdown()
	for deadline := time.Now().Add(5 * time.Second); len(host.RemoteAddrs()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
	}
	addrs := host.RemoteAddrs()
	host.mu.Lock()
	host.cfg.RemoteListen = addrs
	host.mu.Unlock()
	guest, err := New(Config{Dir: guestDir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go guest.Serve()
	defer guest.Shutdown()

	h := dial(t, hostDir)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Sim"}}}))
	tab := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid,
		Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	host.mu.Lock()
	paneID := host.tabs[tab].FocusedPaneId
	host.mu.Unlock()
	link := call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: gid}}}).GetInvite().Link

	g := dial(t, guestDir)
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		g.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_JoinShared{JoinShared: &pb.JoinShared{Link: link}}})
	}()
	ev := waitEvent(t, h, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetJoinRequests()) > 0 })
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondJoin{RespondJoin: &pb.RespondJoin{Id: ev.GetState().JoinRequests[0].Id, Role: pb.Role_ROLE_VIEWER}}})
	waitEvent(t, g, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID {
				return true
			}
		}
		return false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := g.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID}}}); err == nil {
		t.Fatal("a shared pane's video over QUIC straight to the app")
	}
	sid := call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, InBand: true}}}).GetMediaTicket().SessionId
	for n := 0; n < 30; n++ {
		f := waitEvent(t, g, func(ev *pb.ServerMessage) bool { return ev.GetMediaFrame() != nil }).GetMediaFrame()
		if f.SessionId != sid || (n == 0 && !f.Keyframe) || f.Width != 1206 {
			t.Fatalf("frame %d: session %s key %v width %d", n, f.SessionId, f.Keyframe, f.Width)
		}
	}
	// The app's keyframe request reaches the host's encoder.
	keys := enc.count(&enc.keyframes)
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_MediaAck{MediaAck: &pb.MediaAck{SessionId: sid, Keyframe: true}}})
	for deadline := time.Now().Add(3 * time.Second); enc.count(&enc.keyframes) == keys; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("keyframe request didn't reach the host")
		}
	}
	// Viewing isn't driving: a View guest's touch is refused by the host.
	if _, err := g.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: paneID, Op: "touch", ArgsJson: `{"phase":"down","x":1,"y":1}`}}}); err == nil || !strings.Contains(err.Error(), "view") {
		t.Fatalf("a View guest touched the simulator: %v", err)
	}
	if _, err := g.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: paneID, Op: "mouse", ArgsJson: `{"type":"down","x":0.5,"y":0.5}`}}}); err == nil || !strings.Contains(err.Error(), "view") {
		t.Fatalf("a View guest clicked in the browser: %v", err)
	}
	// An agent in one of the guest's own panes can't reach the shared pane
	// with the guest's role in it.
	ggid := client.CreatedID(call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Own"}}}))
	gtab := client.CreatedID(call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ggid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	guest.mu.Lock()
	gpane := guest.tabs[gtab].FocusedPaneId
	guest.mu.Unlock()
	agent := dialAs(t, guestDir, &caller{pane: gpane, agent: true}, "mcp")
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, InBand: true}}},
		{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: paneID, Op: "mouse", ArgsJson: `{"type":"down","x":0.5,"y":0.5}`}}},
		{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: paneID, Op: "screenshot"}}},
		{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("x")}}},
	} {
		if _, err := agent.Call(ctx, m); err == nil || !strings.Contains(err.Error(), "agent") {
			t.Fatalf("an agent reached a shared pane (%T): %v", m.Msg, err)
		}
	}
	// Closing it stops the host's encoder.
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseMedia{CloseMedia: &pb.CloseMedia{SessionId: sid}}})
	for deadline := time.Now().Add(3 * time.Second); enc.count(&enc.stops) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("host encoder not stopped")
		}
	}
}

// An LTR frame is acknowledged only once every frame before it has arrived,
// and never if one before it is given up.
func TestMediaLTRAcks(t *testing.T) {
	s := &mediaSession{unacked: map[uint32]sentFrame{}, ltrs: map[uint32]uint64{}}
	send := func(ltr uint64) uint32 {
		s.seq++
		s.unacked[s.seq] = sentFrame{at: time.Now(), ltr: ltr}
		return s.seq
	}
	ack := func(seq uint32) {
		f := s.unacked[seq]
		delete(s.unacked, seq)
		if f.ltr != 0 {
			s.ltrs[seq] = f.ltr
		}
	}
	a, b, c := send(0), send(11), send(12) // 1, 2 (LTR), 3 (LTR)
	ack(b)
	if got := s.readyLTRs(); len(got) != 0 {
		t.Fatalf("acked %v with frame %d missing", got, a)
	}
	ack(a)
	if got := s.readyLTRs(); !slices.Equal(got, []uint64{11}) {
		t.Fatalf("got %v, want [11]", got)
	}
	// Frame 4 goes missing; 5 (LTR) arrives; 4 is given up: 5 may depend on it.
	ack(c)
	d, e := send(0), send(14)
	ack(e)
	s.forgetUnsure()
	clear(s.unacked)
	if got := s.readyLTRs(); !slices.Equal(got, []uint64{12}) {
		t.Fatalf("got %v after giving up frame %d, want [12]", got, d)
	}
}

// A menu the page opens after remote input goes to that viewer, and its
// choice back to the host, by the host's own item index.
func TestBrowserMenuToRemoteViewer(t *testing.T) {
	d, c, _, _ := mediaDaemon(t)
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Web"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Url: "about:blank"}}}}))
	d.mu.Lock()
	paneID := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	// A fake browser host on a pipe.
	ours, theirs := net.Pipe()
	defer ours.Close()
	d.browsers.mu.Lock()
	d.browsers.c = ours
	d.browsers.mu.Unlock()
	lines := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(theirs)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil && (m["t"] == "rinput" || m["t"] == "menuResult" || m["t"] == "contextResult") {
				lines <- m
			}
		}
	}()
	next := func() map[string]any {
		select {
		case m := <-lines:
			return m
		case <-time.After(3 * time.Second):
			t.Fatal("nothing sent to the host")
		}
		return nil
	}

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: paneID, Op: "mouse", ArgsJson: `{"type":"down","x":0.5,"y":0.5}`}}})
	if m := next(); m["t"] != "rinput" || m["id"] != paneID {
		t.Fatalf("host got %v", m)
	}
	d.browsers.menu(hostMsg{T: "menu", ID: paneID, Seq: 7, Index: 2, Fx: 0.1, Items: []hostMenuItem{{Title: "A", Enabled: true}, {Title: "B", Hidden: true}, {Title: "C", Enabled: true}}})
	bm := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetBrowserMenu() != nil }).GetBrowserMenu()
	if bm.Kind != "select" || len(bm.Items) != 2 || bm.Items[1].Title != "C" || bm.Selected != 1 || bm.X != 0.1 {
		t.Fatalf("menu %v", bm)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserMenuChoice{BrowserMenuChoice: &pb.BrowserMenuChoice{PaneId: paneID, Seq: 7, Index: 1}}})
	if m := next(); m["t"] != "menuResult" || m["seq"] != float64(7) || m["index"] != float64(2) {
		t.Fatalf("host got %v", m)
	}
	// What the viewer copies comes back to it.
	d.browsers.clipboard(hostMsg{T: "clipboard", ID: paneID, Text: "copied"})
	if cb := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetBrowserClipboard() != nil }).GetBrowserClipboard(); cb.Text != "copied" || cb.PaneId != paneID {
		t.Fatalf("clipboard %v", cb)
	}
	// A menu open when its viewer leaves is dismissed.
	d.browsers.menu(hostMsg{T: "context", ID: paneID, Seq: 8, Items: []hostMenuItem{{Title: "Back", Enabled: true}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetBrowserMenu() != nil })
	c.Close()
	if m := next(); m["t"] != "contextResult" || m["seq"] != float64(8) || m["index"] != float64(-1) {
		t.Fatalf("host got %v", m)
	}
}

// A release decided after a hold, but sent first, mustn't leave the
// encoder held: the last thing the encoder hears is the current state.
func TestMediaHoldOrder(t *testing.T) {
	enc := &fakeEncoder{s: map[string]*fakeSession{"s": {stop: make(chan struct{})}}}
	s := &mediaSession{id: "s", src: enc}
	s.mu.Lock()
	s.held = true // deliver decides to hold…
	s.mu.Unlock()
	s.mu.Lock()
	s.held = false // …an ack releases it at once…
	s.mu.Unlock()
	s.syncHold() // …and the release is sent first
	s.syncHold() // then the hold
	if enc.s["s"].held || enc.holds != 0 {
		t.Fatalf("encoder held %v after %d holds", enc.s["s"].held, enc.holds)
	}
}

type nullSink struct{}

func (nullSink) send(uint32, *mediaFrame) {}
func (nullSink) abandon(uint32)           {}
func (nullSink) close()                   {}

// A keyframe the encoder drops (or never makes, on a still screen) is
// asked for again, so a viewer isn't left waiting.
func TestMediaRecoveryRetry(t *testing.T) {
	d, c, enc, paneID := mediaDaemon(t)
	_ = d
	sid := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, InBand: true}}}).GetMediaTicket().SessionId
	waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetMediaFrame().GetKeyframe() })
	enc.mu.Lock()
	enc.ignoreKeys = 2
	before := enc.keyframes
	enc.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_MediaAck{MediaAck: &pb.MediaAck{SessionId: sid, Keyframe: true}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetMediaFrame().GetKeyframe() })
	if n := enc.count(&enc.keyframes) - before; n < 3 {
		t.Fatalf("keyframe asked for %d times; want a retry after each ignored one", n)
	}
}

// Giving frames up while the viewer waits for a keyframe asks for a
// keyframe, not a refresh it can't use.
func TestMediaGiveUpKeepsKeyframe(t *testing.T) {
	enc := &fakeEncoder{s: map[string]*fakeSession{}}
	s := &mediaSession{id: "s", src: enc, sink: nullSink{}, rate: mediaStartRate, minLat: time.Hour,
		unacked: map[uint32]sentFrame{1: {at: time.Now().Add(-time.Second)}}, ltrs: map[uint32]uint64{},
		keyPending: true, seq: 1, needSince: time.Now(), stop: make(chan struct{})}
	go s.pace()
	defer close(s.stop)
	deadline := time.Now().Add(2 * time.Second)
	for enc.count(&enc.keyframes) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no keyframe asked for")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if enc.count(&enc.refreshes) != 0 {
		t.Fatal("asked for a refresh while the viewer waits for a keyframe")
	}
}

// A guest's open page menu is dismissed when their app goes, so the page
// on the host isn't left waiting.
func TestSharedBrowserMenuDismissed(t *testing.T) {
	hostDir, guestDir := shortDir(t), shortDir(t)
	host, err := New(Config{Dir: hostDir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go host.Serve()
	defer host.Shutdown()
	for deadline := time.Now().Add(5 * time.Second); len(host.RemoteAddrs()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
	}
	addrs := host.RemoteAddrs()
	host.mu.Lock()
	host.cfg.RemoteListen = addrs
	host.mu.Unlock()
	guest, err := New(Config{Dir: guestDir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go guest.Serve()
	defer guest.Shutdown()

	h := dial(t, hostDir)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Web"}}}))
	tab := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Url: "about:blank"}}}}))
	host.mu.Lock()
	paneID := host.tabs[tab].FocusedPaneId
	host.mu.Unlock()
	ours, theirs := net.Pipe()
	defer ours.Close()
	host.browsers.mu.Lock()
	host.browsers.c = ours
	host.browsers.mu.Unlock()
	lines := make(chan map[string]any, 16)
	go func() {
		sc := bufio.NewScanner(theirs)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil && (m["t"] == "rinput" || m["t"] == "menuResult") {
				lines <- m
			}
		}
	}()
	link := call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: gid}}}).GetInvite().Link

	g := dial(t, guestDir)
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		g.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_JoinShared{JoinShared: &pb.JoinShared{Link: link}}})
	}()
	ev := waitEvent(t, h, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetJoinRequests()) > 0 })
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondJoin{RespondJoin: &pb.RespondJoin{Id: ev.GetState().JoinRequests[0].Id, Role: pb.Role_ROLE_DRIVER}}})
	waitEvent(t, g, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID {
				return true
			}
		}
		return false
	})
	next := func() map[string]any {
		select {
		case m := <-lines:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("nothing reached the host's browser")
		}
		return nil
	}
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: paneID, Op: "mouse", ArgsJson: `{"type":"down","x":0.5,"y":0.5}`}}})
	if m := next(); m["t"] != "rinput" {
		t.Fatalf("host got %v", m)
	}
	host.browsers.menu(hostMsg{T: "menu", ID: paneID, Seq: 9, Items: []hostMenuItem{{Title: "A", Enabled: true}}})
	waitEvent(t, g, func(ev *pb.ServerMessage) bool { return ev.GetBrowserMenu() != nil })
	g.Close()
	if m := next(); m["t"] != "menuResult" || m["seq"] != float64(9) || m["index"] != float64(-1) {
		t.Fatalf("host got %v", m)
	}
}

// A remote viewer's input reaches the browser helper only with the fields
// and types it expects, and no clipboard commands.
func TestRemoteInputArgsChecked(t *testing.T) {
	for _, bad := range []string{
		`{"type":"down","commands":["paste"]}`,
		`{"type":"down","commands":["copy"]}`,
		`{"mods":"meta"}`,
		`{"text":5}`,
		`{"type":3}`,
		`{"type":"sideways"}`,
		`{"button":"fourth"}`,
		`{"x":[1]}`,
	} {
		var a remoteInputArgs
		if json.Unmarshal([]byte(bad), &a) == nil && a.check() == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	var a remoteInputArgs
	if err := json.Unmarshal([]byte(`{"type":"down","key":"a","mods":["meta"],"commands":["selectAll"],"clicks":9}`), &a); err != nil || a.check() != nil || a.Clicks != 3 {
		t.Fatalf("refused a ⌘A: %v %+v", err, a)
	}
}

// Only so many QUIC connections from one address may wait for their ticket
// at once; one more is closed at once.
func TestMediaQUICPendingCap(t *testing.T) {
	d, _ := startHost(t)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		d.media.mu.Lock()
		port := d.media.port
		d.media.mu.Unlock()
		if port > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("media listener did not start")
		}
	}
	host, _, _ := net.SplitHostPort(d.RemoteAddrs()[0])
	d.media.mu.Lock()
	addr := net.JoinHostPort(host, strconv.Itoa(d.media.port))
	d.media.mu.Unlock()
	dialOne := func() *quic.Conn {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		qc, err := quic.DialAddr(ctx, addr, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{mediaALPN}}, &quic.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { qc.CloseWithError(0, "") })
		return qc
	}
	var waiting []*quic.Conn
	for range maxPreAuthPerIP {
		waiting = append(waiting, dialOne())
	}
	extra := dialOne()
	select {
	case <-extra.Context().Done():
	case <-time.After(3 * time.Second):
		t.Fatal("more QUIC connections waiting for a ticket than allowed")
	}
	for _, qc := range waiting {
		select {
		case <-qc.Context().Done():
			t.Fatal("one within the limit was closed early")
		default:
		}
	}
}
