// Package mediaview is a viewer's end of a daemon's media transport over
// QUIC (ADR-0012, and ADR-0014's datagrams): what the iPhone's
// MediaStream does with Network.framework, for viewers without that (the
// Android app, through cmd/hmkquic). It hands back whole frame records, in
// the order they come; ordering and decoding are the caller's.
//
// The wire (docs/internals/remote-media.md): ALPN hmk-media/1, TLS 1.3
// pinned to the SHA-256 of the daemon's leaf certificate. The viewer sends
// its 32-byte ticket on a bidirectional control stream; the daemon sends
// each frame record on a unidirectional stream of its own, or, asked for
// datagrams (screens), cut into shards with XOR parity (datagram.go in
// internal/daemon). Control records, viewer to daemon: 1 ack (u32 seq),
// 2 keyframe, 3 NACK (u32 seq, u8 n, n × u16 shard indices).
package mediaview

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
)

const (
	ALPN        = "hmk-media/1"
	FrameHeader = 17       // u32 seq, u8 flags, u64 pts, u16 width, u16 height
	maxFrame    = 64 << 20 // the largest frame record
	shardHeader = 15
	// A datagram frame short of shards: asked again for what parity can't
	// rebuild after this long without one, then every nackAgain, nackTries
	// times; given up after dropAfter without a shard.
	nackFirst = 40 * time.Millisecond
	nackAgain = 80 * time.Millisecond
	nackTries = 4
	dropAfter = 500 * time.Millisecond
)

// ErrClosed is Next's once the video has ended; Reason says why.
var ErrClosed = errors.New("the video stopped")

// Viewer is one media session's QUIC connection.
type Viewer struct {
	qc    *quic.Conn
	drop  func() bool
	ctl   *quic.Stream
	ctlMu sync.Mutex

	out  chan []byte
	done chan struct{}
	once sync.Once

	mu         sync.Mutex
	reason     string
	assemblies map[uint32]*assembly
	assembled  map[uint32]bool // put together: later shards (parity, resends) are spare
	newest     uint32
}

// Options for Dial.
type Options struct {
	// Datagrams: the session was opened asking for them (OpenMedia.datagrams).
	Datagrams bool
	// Drop, for tests: shards it says yes to are lost on arrival.
	Drop func() bool
}

// Dial connects to the daemon at host (the address the control link
// reached) on the ticket's port, checks its certificate against pin (hex
// SHA-256 of the leaf, as paired), and presents the ticket.
func Dial(ctx context.Context, host string, port int, pin string, ticket []byte, o Options) (*Viewer, error) {
	datagrams := o.Datagrams
	tc := &tls.Config{
		NextProtos:         []string{ALPN},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // verified against the pin below instead
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if !strings.EqualFold(hex.EncodeToString(sum[:]), pin) {
				return errors.New("the computer's certificate doesn't match the one paired")
			}
			return nil
		},
	}
	qc, err := quic.DialAddr(ctx, net.JoinHostPort(host, strconv.Itoa(port)), tc, &quic.Config{
		MaxIncomingUniStreams: 1 << 20, MaxIdleTimeout: 20 * time.Second, KeepAlivePeriod: 5 * time.Second,
		EnableDatagrams: datagrams,
	})
	if err != nil {
		return nil, err
	}
	ctl, err := qc.OpenStream()
	if err == nil {
		_, err = ctl.Write(ticket)
	}
	if err != nil {
		qc.CloseWithError(0, "")
		return nil, err
	}
	v := &Viewer{
		qc: qc, ctl: ctl, drop: o.Drop, out: make(chan []byte, 64), done: make(chan struct{}),
		assemblies: map[uint32]*assembly{}, assembled: map[uint32]bool{},
	}
	go v.streams()
	if datagrams {
		go v.datagrams()
		go v.watch()
	}
	go func() {
		<-qc.Context().Done()
		v.end("")
	}()
	return v, nil
}

// Next waits up to timeout for the next whole frame record (nil, nil when
// none came). ErrClosed once the video has ended.
func (v *Viewer) Next(timeout time.Duration) ([]byte, error) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case rec := <-v.out:
		return rec, nil
	case <-v.done:
		select { // what came before the end still counts
		case rec := <-v.out:
			return rec, nil
		default:
			return nil, ErrClosed
		}
	case <-t.C:
		return nil, nil
	}
}

// Keyframe asks the daemon for a keyframe.
func (v *Viewer) Keyframe() { v.control([]byte{2}) }

// Close ends the connection (the session ends with it).
func (v *Viewer) Close() { v.end("") }

// Reason is why the video ended, if the connection said.
func (v *Viewer) Reason() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reason
}

func (v *Viewer) end(reason string) {
	v.once.Do(func() {
		v.mu.Lock()
		if reason == "" {
			if err := context.Cause(v.qc.Context()); err != nil && !errors.Is(err, context.Canceled) {
				reason = err.Error()
			}
		}
		v.reason = reason
		v.mu.Unlock()
		close(v.done)
		v.qc.CloseWithError(0, "")
	})
}

func (v *Viewer) control(b []byte) {
	v.ctlMu.Lock()
	defer v.ctlMu.Unlock()
	_, _ = v.ctl.Write(b)
}

// record acks a whole frame record and hands it on.
func (v *Viewer) record(rec []byte) {
	if len(rec) < FrameHeader {
		return
	}
	ack := []byte{1, 0, 0, 0, 0}
	copy(ack[1:], rec[:4])
	v.control(ack)
	select {
	case v.out <- rec:
	case <-v.done:
	}
}

// streams takes a frame per unidirectional stream.
func (v *Viewer) streams() {
	for {
		st, err := v.qc.AcceptUniStream(context.Background())
		if err != nil {
			return
		}
		go func() {
			b, err := io.ReadAll(io.LimitReader(st, maxFrame+1))
			if len(b) > maxFrame {
				st.CancelRead(0)
				return
			}
			if err != nil {
				return // given up by the daemon
			}
			v.record(b)
		}()
	}
}

// ---------- datagrams ----------

// assembly is a frame's shards as they come: data 0..k-1, then p parity,
// each the XOR of the data shards i with i % p == j.
type assembly struct {
	k, p, n  int
	shards   [][]byte
	have     int
	lastAt   time.Time
	nacks    int
	nackedAt time.Time
}

func (v *Viewer) datagrams() {
	for {
		b, err := v.qc.ReceiveDatagram(context.Background())
		if err != nil {
			return
		}
		if v.drop != nil && v.drop() {
			continue
		}
		v.shard(b)
	}
}

func (v *Viewer) shard(b []byte) {
	if len(b) < shardHeader || b[0] != 1 {
		return
	}
	seq := binary.BigEndian.Uint32(b[1:])
	i, k, p := int(binary.BigEndian.Uint16(b[5:])), int(binary.BigEndian.Uint16(b[7:])), int(binary.BigEndian.Uint16(b[9:]))
	n := int(binary.BigEndian.Uint32(b[11:]))
	body := b[shardHeader:]
	if k == 0 || p == 0 || i >= k+p || n > k*len(body) {
		return
	}
	v.mu.Lock()
	var rec []byte
	func() {
		defer v.mu.Unlock()
		if v.assembled[seq] {
			return
		}
		if seq > v.newest {
			// A later frame's shards: any earlier frame still short has lost
			// some (datagrams rarely overtake each other).
			v.newest = seq
			for s, a := range v.assemblies {
				if s < seq && a.nacks == 0 {
					v.nackLocked(s)
				}
			}
			if v.assembled[seq] { // put together by that
				return
			}
		}
		a := v.assemblies[seq]
		if a == nil {
			a = &assembly{k: k, p: p, n: n, shards: make([][]byte, k+p)}
			v.assemblies[seq] = a
		}
		if a.k != k || a.p != p || a.shards[i] != nil {
			return
		}
		a.shards[i] = append([]byte(nil), body...)
		a.have++
		a.lastAt = time.Now()
		if a.have >= k {
			rec, _ = v.assembleLocked(seq)
		}
	}()
	if rec != nil {
		v.record(rec)
	}
}

// assembleLocked rebuilds what parity can: the record once every data
// shard's there, else the data shards still missing.
func (v *Viewer) assembleLocked(seq uint32) ([]byte, []int) {
	a := v.assemblies[seq]
	if a == nil {
		return nil, nil
	}
	for j := 0; j < a.p; j++ {
		par := a.shards[a.k+j]
		if par == nil {
			continue
		}
		lost := -1
		count := 0
		for i := j; i < a.k; i += a.p {
			if a.shards[i] == nil {
				lost = i
				count++
			}
		}
		if count != 1 {
			continue
		}
		x := append([]byte(nil), par...)
		for i := j; i < a.k; i += a.p {
			if i != lost {
				src := a.shards[i]
				for o := 0; o < len(x) && o < len(src); o++ {
					x[o] ^= src[o]
				}
			}
		}
		a.shards[lost] = x
	}
	var missing []int
	for i := 0; i < a.k; i++ {
		if a.shards[i] == nil {
			missing = append(missing, i)
		}
	}
	if missing != nil {
		return nil, missing
	}
	rec := make([]byte, 0, a.k*len(a.shards[0]))
	for i := 0; i < a.k; i++ {
		rec = append(rec, a.shards[i]...)
	}
	if a.n > len(rec) {
		return nil, nil
	}
	delete(v.assemblies, seq)
	v.assembled[seq] = true
	if len(v.assembled) > 512 {
		for s := range v.assembled {
			if s+256 <= seq {
				delete(v.assembled, s)
			}
		}
	}
	return rec[:a.n], nil
}

// nackLocked asks again for the shards parity can't rebuild (or hands the
// frame on, if it can after all: done by the caller's next record).
func (v *Viewer) nackLocked(seq uint32) {
	rec, missing := v.assembleLocked(seq)
	if rec != nil {
		go v.record(rec)
		return
	}
	a := v.assemblies[seq]
	if a == nil || len(missing) == 0 {
		return
	}
	a.nacks++
	a.nackedAt = time.Now()
	for start := 0; start < len(missing); start += 255 {
		part := missing[start:min(start+255, len(missing))]
		b := []byte{3, 0, 0, 0, 0, byte(len(part))}
		binary.BigEndian.PutUint32(b[1:], seq)
		for _, i := range part {
			b = binary.BigEndian.AppendUint16(b, uint16(i))
		}
		go v.control(b)
	}
}

// watch asks again for a frame gone quiet (the last of a burst has no
// later frame to show it's short) and drops one past saving.
func (v *Viewer) watch() {
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-v.done:
			return
		case <-t.C:
		}
		now := time.Now()
		v.mu.Lock()
		for s, a := range v.assemblies {
			switch {
			case now.Sub(a.lastAt) > dropAfter:
				delete(v.assemblies, s)
			case a.nacks == 0 && now.Sub(a.lastAt) > nackFirst,
				a.nacks > 0 && a.nacks < nackTries && now.Sub(a.nackedAt) > nackAgain:
				v.nackLocked(s)
			}
		}
		v.mu.Unlock()
	}
}
