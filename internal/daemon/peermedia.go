package daemon

// Video of panes in Groups shared with you (ADR-0012). Your app asks your
// daemon (in band, over the local socket); your daemon takes a ticket from
// the host over the peer link, receives the video over QUIC itself, acks
// each frame as it arrives (the link to the host is the one the host
// paces), and passes frames on to the app as MediaFrame messages.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

type mediaRelay struct {
	hub      *mediaHub
	link     *peerLink
	id       string // yours, to the app
	hostID   string // the host's session
	paneID   string
	owner    *conn
	qc       *quic.Conn
	ctl      *quic.Stream
	ctlMu    sync.Mutex
	once     sync.Once
	received int
}

func (h *mediaHub) relay(c *conn, id string) *mediaRelay {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r := h.relays[id]; r != nil && r.owner == c {
		return r
	}
	return nil
}

// pinnedTLS accepts only the certificate whose SHA-256 is pin (the host's,
// from the invite).
func pinnedTLS(pin string) *tls.Config {
	return &tls.Config{
		NextProtos:         []string{mediaALPN},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // verified against the pin below instead
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if !strings.EqualFold(hex.EncodeToString(sum[:]), pin) {
				return errors.New("certificate doesn't match the pin")
			}
			return nil
		},
	}
}

// openMedia starts relaying a shared pane's video to c.
func (l *peerLink) openMedia(c *conn, m *pb.OpenMedia) (*pb.ServerMessage, error) {
	if !m.InBand {
		return nil, errors.New("video of a shared pane comes through your own Mac (in band)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, err := l.connected(ctx, 5*time.Second)
	if err != nil {
		return nil, err
	}
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: m.PaneId, MaxPixels: m.MaxPixels, Screen: m.Screen, DisplayId: m.DisplayId, Codecs: m.Codecs, MaxFps: m.MaxFps, Background: m.Background}}})
	if err != nil {
		return nil, err
	}
	t := r.GetMediaTicket()
	l.cmu.Lock()
	addr := l.addr
	l.cmu.Unlock()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	qc, err := quic.DialAddr(ctx, net.JoinHostPort(host, strconv.Itoa(int(t.QuicPort))), pinnedTLS(l.tok.CertSha256),
		&quic.Config{MaxIncomingUniStreams: 256, MaxIdleTimeout: 20 * time.Second, KeepAlivePeriod: 5 * time.Second})
	if err != nil {
		_ = cl.Send(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_CloseMedia{CloseMedia: &pb.CloseMedia{SessionId: t.SessionId}}})
		return nil, err
	}
	ctl, err := qc.OpenStream()
	if err == nil {
		_, err = ctl.Write(t.Ticket)
	}
	if err != nil {
		qc.CloseWithError(0, "")
		_ = cl.Send(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_CloseMedia{CloseMedia: &pb.CloseMedia{SessionId: t.SessionId}}})
		return nil, err
	}
	rl := &mediaRelay{hub: l.d.media, link: l, id: newID("ms"), hostID: t.SessionId, paneID: m.PaneId, owner: c, qc: qc, ctl: ctl}
	h := l.d.media
	h.mu.Lock()
	h.relays[rl.id] = rl
	h.mu.Unlock()
	go rl.run()
	return &pb.ServerMessage{Msg: &pb.ServerMessage_MediaTicket{MediaTicket: &pb.MediaTicket{SessionId: rl.id}}}, nil
}

func (r *mediaRelay) run() {
	go func() {
		<-r.qc.Context().Done()
		r.end("the host stopped the video")
	}()
	for {
		st, err := r.qc.AcceptUniStream(context.Background())
		if err != nil {
			return
		}
		go func() {
			b, err := io.ReadAll(io.LimitReader(st, mediaMaxFrame+1))
			if len(b) > mediaMaxFrame {
				st.CancelRead(0)
				return
			}
			if err != nil || len(b) < mediaFrameHeader {
				return // given up by the host
			}
			seq := binary.BigEndian.Uint32(b)
			rec := []byte{1, 0, 0, 0, 0}
			binary.BigEndian.PutUint32(rec[1:], seq)
			r.ctlMu.Lock()
			_, _ = r.ctl.Write(rec)
			r.received++
			r.ctlMu.Unlock()
			mf := &pb.MediaFrame{
				SessionId: r.id, Seq: seq, Keyframe: b[4]&1 != 0, Refresh: b[4]&2 != 0, Hevc: b[4]&4 != 0, PtsUs: binary.BigEndian.Uint64(b[5:]),
				Width: uint32(binary.BigEndian.Uint16(b[13:])), Height: uint32(binary.BigEndian.Uint16(b[15:])), Data: b[mediaFrameHeader:],
			}
			if b[4]&8 != 0 && len(b) >= mediaFrameHeader+8 {
				c := func(i int) float32 { return float32(binary.BigEndian.Uint16(b[mediaFrameHeader+2*i:])) / 65535 }
				mf.CropX, mf.CropY, mf.CropW, mf.CropH = c(0), c(1), c(2), c(3)
				mf.Data = b[mediaFrameHeader+8:]
			}
			r.owner.send(&pb.ServerMessage{Msg: &pb.ServerMessage_MediaFrame{MediaFrame: mf}})
		}()
	}
}

// relayCursor passes a host's pointer message on to the viewer of the
// relayed session it's for (the host names its own session).
func (h *mediaHub) relayCursor(l *peerLink, c *pb.ScreenCursor) {
	h.mu.Lock()
	var to *mediaRelay
	for _, r := range h.relays {
		if r.link == l && r.hostID == c.SessionId {
			to = r
		}
	}
	h.mu.Unlock()
	if to == nil {
		return
	}
	out := proto.CloneOf(c)
	out.SessionId = to.id
	to.owner.send(&pb.ServerMessage{Msg: &pb.ServerMessage_ScreenCursor{ScreenCursor: out}})
}

// relayState passes a host's lock state on, as relayCursor does.
func (h *mediaHub) relayState(l *peerLink, st *pb.ScreenState) {
	h.mu.Lock()
	var to *mediaRelay
	for _, r := range h.relays {
		if r.link == l && r.hostID == st.SessionId {
			to = r
		}
	}
	h.mu.Unlock()
	if to != nil {
		to.owner.send(&pb.ServerMessage{Msg: &pb.ServerMessage_ScreenState{ScreenState: &pb.ScreenState{SessionId: to.id, Locked: st.Locked}}})
	}
}

// setView passes a zoomed-in view on to the host's session.
func (r *mediaRelay) setView(m *pb.SetMediaView) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if cl, err := r.link.connected(ctx, time.Second); err == nil {
			_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SetMediaView{SetMediaView: &pb.SetMediaView{
				SessionId: r.hostID, X: m.X, Y: m.Y, W: m.W, H: m.H}}})
		}
	}()
}

func (r *mediaRelay) wantKeyframe() {
	r.ctlMu.Lock()
	defer r.ctlMu.Unlock()
	_, _ = r.ctl.Write([]byte{2})
}

func (r *mediaRelay) end(reason string) {
	r.once.Do(func() {
		h := r.hub
		h.mu.Lock()
		delete(h.relays, r.id)
		h.mu.Unlock()
		r.qc.CloseWithError(0, "")
		r.ctlMu.Lock()
		n := r.received
		r.ctlMu.Unlock()
		h.d.log.Info("shared video ended", "session", r.id, "pane", r.paneID, "frames", n, "reason", reason)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if cl, err := r.link.connected(ctx, time.Second); err == nil {
				_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseMedia{CloseMedia: &pb.CloseMedia{SessionId: r.hostID}}})
			}
		}()
		if reason != "" {
			r.owner.send(&pb.ServerMessage{Msg: &pb.ServerMessage_MediaEnded{MediaEnded: &pb.MediaEnded{SessionId: r.id, Reason: reason}}})
		}
	})
}
