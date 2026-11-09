// Media transport spike: server side. Compares QUIC (one stream per frame)
// with TLS over TCP (one ordered stream), both sending synthetic video with
// the same sender policy, to one client at a time.
//
// The sender behaves like the real one should:
//   - Rate: a target bitrate adapted to queueing delay (frame ack latency
//     over the lowest seen); frame sizes follow it (keyframes 8x).
//   - Skipping: a frame isn't "encoded" while more than ~100 ms of data is
//     still in flight, so queues stay short.
//   - Abandoning (QUIC only; TCP can only drop frames not yet written): a
//     frame not delivered within -stale is cancelled with everything after
//     it, and the next frame is a recovery frame that references the last
//     frame shown (VideoToolbox's long-term references), not a keyframe.
//
// A frame can be shown once it and the frame it references have arrived;
// latency is capture -> shown, as acked back (so it includes the ack's trip).
//
// Wire format, both transports: a 13-byte header (seq u32, ref u32, flags
// u8, length u32) then the payload. Acks are seq u32, on the control stream
// (QUIC) or the same connection (TCP). The client first sends a 32-byte
// ticket.
//
// Loss and delay come from outside (dummynet), or -loss/-delay for QUIC
// only (they drop the server's own outgoing packets).
package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	mrand "math/rand/v2"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

var (
	addr   = flag.String("addr", "127.0.0.1:7781", "address to listen on (UDP for QUIC, TCP for -tcp)")
	useTCP = flag.Bool("tcp", false, "TLS over TCP instead of QUIC")
	fps    = flag.Int("fps", 60, "frames per second")
	start  = flag.Float64("rate", 6, "starting bitrate, Mbit/s")
	maxR   = flag.Float64("max", 12, "highest bitrate, Mbit/s")
	fixed  = flag.Bool("fixed", false, "don't adapt the bitrate or skip frames")
	gop    = flag.Duration("gop", 4*time.Second, "keyframe interval")
	stale  = flag.Duration("stale", 400*time.Millisecond, "abandon a frame not delivered within this (QUIC)")
	loss   = flag.Float64("loss", 0, "QUIC only: fraction of outgoing packets to drop")
	delay  = flag.Duration("delay", 0, "QUIC only: extra one-way delay on outgoing packets")
	secs   = flag.Int("secs", 15, "seconds to stream per client")
)

const (
	headerLen = 13
	flagKey   = 1
	flagRecov = 2
	noRef     = 0xffffffff
)

func main() {
	flag.Parse()
	cert, fp := makeCert()
	fmt.Println("fingerprint", fp)
	tcfg := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"hmk-media/1"}, MinVersion: tls.VersionTLS13}
	if *useTCP {
		ln, err := tls.Listen("tcp", *addr, tcfg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("listening tcp", ln.Addr())
		for {
			c, err := ln.Accept()
			if err != nil {
				log.Fatal(err)
			}
			go func() {
				defer c.Close()
				t := newTCP(c)
				if err := t.handshake(); err != nil {
					log.Println(err)
					return
				}
				run(t)
			}()
		}
	}
	udp, err := net.ListenPacket("udp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	pc := net.PacketConn(udp)
	if *loss > 0 || *delay > 0 {
		pc = &lossy{PacketConn: udp}
	}
	tr := &quic.Transport{Conn: pc}
	ln, err := tr.Listen(tcfg, &quic.Config{MaxIdleTimeout: 15 * time.Second, KeepAlivePeriod: 5 * time.Second})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("listening quic", udp.LocalAddr())
	for {
		c, err := ln.Accept(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		go func() {
			defer c.CloseWithError(0, "bye")
			t, err := newQUIC(c)
			if err != nil {
				log.Println(err)
				return
			}
			run(t)
		}()
	}
}

// transport sends whole frames and delivers acks.
type transport interface {
	send(seq uint32, b []byte)
	// abandon cancels frames from seq on that aren't written yet (or, for
	// QUIC, not delivered); returns how many.
	abandon(from uint32) int
	acks() <-chan uint32
	canAbandonInFlight() bool
}

type frame struct {
	seq, ref  uint32
	size      int
	captured  time.Time
	acked     time.Time
	shown     time.Time
	abandoned bool
}

func run(t transport) {
	var (
		mu                       sync.Mutex
		frames                   = map[uint32]*frame{}
		inflight                 = map[uint32]int{} // seq -> bytes, sent but not acked
		rate                     = *start * 1e6
		minLat                   = time.Hour
		recent                   []time.Duration // ack latencies since the last rate update
		lastShown                uint32
		haveShown                bool
		needKey                  = true
		recov                    bool
		lastKey                  time.Time
		seq                      uint32
		prev                     uint32 = noRef
		sent, skipped, abandoned int
		bytesOut                 int
		shownLat                 []time.Duration
		shownN                   int
	)

	// show marks a frame shown once it and its reference have arrived, then
	// any frames that were waiting on it.
	var show func(f *frame)
	show = func(f *frame) {
		if !f.shown.IsZero() || f.acked.IsZero() || f.abandoned {
			return
		}
		if f.ref != noRef {
			if r := frames[f.ref]; r == nil || r.shown.IsZero() {
				return
			}
		}
		f.shown = time.Now()
		shownN++
		shownLat = append(shownLat, f.shown.Sub(f.captured))
		if !haveShown || f.seq > lastShown {
			lastShown, haveShown = f.seq, true
		}
		for _, g := range frames {
			if g.ref == f.seq {
				show(g)
			}
		}
	}

	go func() {
		for s := range t.acks() {
			now := time.Now()
			mu.Lock()
			if f := frames[s]; f != nil && f.acked.IsZero() {
				f.acked = now
				lat := now.Sub(f.captured)
				minLat = min(minLat, lat)
				recent = append(recent, lat)
				delete(inflight, s)
				show(f)
			}
			mu.Unlock()
		}
	}()

	tick := time.NewTicker(time.Second / time.Duration(*fps))
	defer tick.Stop()
	adapt := time.NewTicker(200 * time.Millisecond)
	defer adapt.Stop()
	began := time.Now()
	end := began.Add(time.Duration(*secs) * time.Second)
	var rates []float64
loop:
	for {
		select {
		case <-adapt.C:
			mu.Lock()
			if !*fixed && len(recent) > 0 {
				slices.Sort(recent)
				q := recent[len(recent)*9/10] - minLat // queueing delay, p90
				switch {
				case q > 80*time.Millisecond:
					rate *= 0.75
				case q > 40*time.Millisecond:
					rate *= 0.92
				case q < 15*time.Millisecond:
					rate *= 1.06
				}
				rate = min(max(rate, 0.3e6), *maxR*1e6)
			}
			recent = recent[:0]
			rates = append(rates, rate)
			mu.Unlock()
		case now := <-tick.C:
			if now.After(end) {
				break loop
			}
			mu.Lock()
			// Abandon the oldest frame stuck too long (QUIC) and everything
			// after it; the next frame recovers from the last one shown.
			if t.canAbandonInFlight() {
				var oldest *frame
				for _, f := range frames {
					if f.acked.IsZero() && !f.abandoned && now.Sub(f.captured) > *stale && (oldest == nil || f.seq < oldest.seq) {
						oldest = f
					}
				}
				if oldest != nil {
					abandoned += t.abandon(oldest.seq)
					for s, f := range frames {
						if s >= oldest.seq && f.acked.IsZero() {
							f.abandoned = true
							delete(inflight, s)
						}
					}
					recov = true
					if !*fixed {
						rate = max(rate*0.6, 0.3e6)
					}
				}
			}
			var queued int
			for _, b := range inflight {
				queued += b
			}
			if !*fixed && !needKey && float64(queued*8) > rate*0.1 {
				skipped++
				mu.Unlock()
				continue
			}
			size := int(rate / 8 / float64(*fps))
			ref, flags := prev, byte(0)
			switch {
			case needKey || now.Sub(lastKey) >= *gop || (recov && !haveShown):
				size *= 8
				ref, flags = noRef, flagKey
				needKey, recov, lastKey = false, false, now
			case recov:
				size = size * 3 / 2
				ref, flags = lastShown, flagRecov
				recov = false
			}
			seq++
			frames[seq] = &frame{seq: seq, ref: ref, size: size, captured: now}
			inflight[seq] = size
			prev = seq
			sent++
			bytesOut += size
			mu.Unlock()

			b := make([]byte, headerLen+size)
			binary.BigEndian.PutUint32(b, seq)
			binary.BigEndian.PutUint32(b[4:], ref)
			b[8] = flags
			binary.BigEndian.PutUint32(b[9:], uint32(size))
			t.send(seq, b)
		}
	}
	time.Sleep(time.Second)
	mu.Lock()
	defer mu.Unlock()
	el := time.Since(began).Seconds() - 1
	slices.Sort(shownLat)
	pct := func(p float64) time.Duration {
		if len(shownLat) == 0 {
			return 0
		}
		return shownLat[min(len(shownLat)-1, int(float64(len(shownLat))*p))].Round(time.Millisecond)
	}
	slices.Sort(rates)
	kind := "quic"
	if *useTCP {
		kind = "tcp "
	}
	fmt.Printf("%s shown %.0f fps (sent %d, skipped %d, abandoned %d)  %.1f Mbit/s (rate median %.1f)  latency p50 %v p90 %v p99 %v max %v\n",
		kind, float64(shownN)/el, sent, skipped, abandoned, float64(bytesOut*8)/el/1e6, rates[len(rates)/2]/1e6,
		pct(.5), pct(.9), pct(.99), pct(1))
}

// MARK: QUIC

type quicT struct {
	c    *quic.Conn
	ctl  *quic.Stream
	ackC chan uint32
	mu   sync.Mutex
	open map[uint32]*quic.SendStream
}

func newQUIC(c *quic.Conn) (*quicT, error) {
	// The control stream is the first one that carries a ticket
	// (Network.framework opens and leaves an empty stream first).
	ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
	defer cancel()
	got := make(chan *quic.Stream, 1)
	go func() {
		for {
			s, err := c.AcceptStream(ctx)
			if err != nil {
				return
			}
			go func() {
				b := make([]byte, 32)
				if _, err := io.ReadFull(s, b); err != nil {
					return
				}
				select {
				case got <- s:
				default:
				}
			}()
		}
	}()
	t := &quicT{c: c, ackC: make(chan uint32, 1024), open: map[uint32]*quic.SendStream{}}
	select {
	case t.ctl = <-got:
	case <-ctx.Done():
		return nil, fmt.Errorf("no ticket in time")
	}
	go readAcks(t.ctl, t.ackC)
	return t, nil
}

func (t *quicT) canAbandonInFlight() bool { return true }
func (t *quicT) acks() <-chan uint32      { return t.ackC }

func (t *quicT) send(seq uint32, b []byte) {
	st, err := t.c.OpenUniStream()
	if err != nil {
		log.Println("open stream:", err)
		return
	}
	t.mu.Lock()
	t.open[seq] = st
	t.mu.Unlock()
	go func() {
		if _, err := st.Write(b); err == nil {
			st.Close()
		}
		t.mu.Lock()
		delete(t.open, seq)
		t.mu.Unlock()
	}()
}

// abandon resets frame streams from seq on that are still being written.
// (A frame fully written but still being retransmitted can't be recalled;
// the client drops it as it doesn't lead anywhere.)
func (t *quicT) abandon(from uint32) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for s, st := range t.open {
		if s >= from {
			st.CancelWrite(1)
			delete(t.open, s)
			n++
		}
	}
	return n
}

// MARK: TCP

type tcpT struct {
	c     net.Conn
	ackC  chan uint32
	mu    sync.Mutex
	queue []queued
	wake  chan struct{}
}

type queued struct {
	seq uint32
	b   []byte
}

func newTCP(c net.Conn) *tcpT {
	return &tcpT{c: c, ackC: make(chan uint32, 1024), wake: make(chan struct{}, 1)}
}

func (t *tcpT) handshake() error {
	b := make([]byte, 32)
	if _, err := io.ReadFull(t.c, b); err != nil {
		return fmt.Errorf("no ticket: %w", err)
	}
	go readAcks(t.c, t.ackC)
	go t.writer()
	return nil
}

func (t *tcpT) canAbandonInFlight() bool { return false }
func (t *tcpT) acks() <-chan uint32      { return t.ackC }

func (t *tcpT) send(seq uint32, b []byte) {
	t.mu.Lock()
	t.queue = append(t.queue, queued{seq, b})
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *tcpT) abandon(from uint32) int { return 0 }

func (t *tcpT) writer() {
	w := bufio.NewWriterSize(t.c, 64<<10)
	for range t.wake {
		for {
			t.mu.Lock()
			if len(t.queue) == 0 {
				t.mu.Unlock()
				break
			}
			q := t.queue[0]
			t.queue = t.queue[1:]
			t.mu.Unlock()
			if _, err := w.Write(q.b); err != nil {
				return
			}
			if err := w.Flush(); err != nil {
				return
			}
		}
	}
}

func readAcks(r io.Reader, out chan<- uint32) {
	defer close(out)
	b := make([]byte, 4)
	for {
		if _, err := io.ReadFull(r, b); err != nil {
			return
		}
		out <- binary.BigEndian.Uint32(b)
	}
}

// MARK: helpers

// lossy drops and delays outgoing packets.
type lossy struct{ net.PacketConn }

func (l *lossy) WriteTo(p []byte, a net.Addr) (int, error) {
	if mrand.Float64() < *loss {
		return len(p), nil
	}
	if *delay == 0 {
		return l.PacketConn.WriteTo(p, a)
	}
	b := slices.Clone(p)
	time.AfterFunc(*delay, func() { l.PacketConn.WriteTo(b, a) })
	return len(p), nil
}

func makeCert() (tls.Certificate, string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "hmk media spike"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ips, err := net.InterfaceAddrs(); err == nil {
		for _, a := range ips {
			if n, ok := a.(*net.IPNet); ok {
				tmpl.IPAddresses = append(tmpl.IPAddresses, n.IP)
			}
		}
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:])
}
