package daemon

// Media transport v2 (ADR-0014): a frame over QUIC datagrams. QUIC resends
// a lost stream packet, so one loss holds its whole frame for a round trip;
// a datagram is never resent. So each frame (the same record a stream
// carries: the 17-byte header, the crop when cropped, then the picture) is
// cut into shards with XOR parity, and a lost shard is rebuilt from the
// rest of its stripe; what can't be is asked for again (a NACK on the
// control stream) and resent only while the frame's fresh.
//
// A datagram: u8 version (1), u32 frame seq, u16 shard index, u16 data
// shards k, u16 parity shards p, u32 the record's length, then the shard.
// Shards 0..k-1 are the record in order, each dgShard bytes (the last
// padded with zeros); shard k+j is the XOR of the data shards i with
// i % p == j (so p stripes, each rebuilt if it lost one shard).
//
// The control stream gains record 3, a NACK: u32 seq, u8 n, n × u16 shard
// indices missing (asked once the frame's late).

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

const (
	dgShard  = 1080 // fits a Tailscale packet (MTU 1280) with QUIC's and our headers
	dgHeader = 15
	dgKeep   = time.Second // shards kept for resends
)

type dgFrame struct {
	at     time.Time
	grams  [][]byte // every datagram of the frame, data then parity
	nacked bool     // its loss counted (once, however often it's asked for)
}

// dgSink sends a session's frames as datagrams (mediaSink).
type dgSink struct {
	qc    *quic.Conn
	out   chan []byte // datagrams, sent in order by a worker
	mu    sync.Mutex
	cache map[uint32]*dgFrame
	// The parity: one shard per stripe of this many data shards, from the
	// loss the viewer's NACKs show (2: a shard in two lost; 10: one in twenty).
	stripe float64
	lossW  float64 // shards lost over sent, a moving average
	closed chan struct{}
	once   sync.Once
}

func newDgSink(qc *quic.Conn) *dgSink {
	d := &dgSink{qc: qc, out: make(chan []byte, 4096), cache: map[uint32]*dgFrame{}, stripe: 10, closed: make(chan struct{})}
	go d.run()
	return d
}

func (d *dgSink) run() {
	for {
		select {
		case <-d.closed:
			return
		case b := <-d.out:
			// A frame given up (or past keeping) meanwhile: its shards go no further.
			d.mu.Lock()
			live := d.cache[binary.BigEndian.Uint32(b[1:])] != nil
			d.mu.Unlock()
			if !live {
				continue
			}
			// Blocks while quic-go's queue is full (congestion control
			// pacing it); an error is the connection gone.
			if d.qc.SendDatagram(b) != nil && d.qc.Context().Err() != nil {
				return
			}
		}
	}
}

func (d *dgSink) queue(b []byte) {
	select {
	case d.out <- b:
	default: // hopelessly behind: lost (the session gives the frame up)
	}
}

// shard cuts a record into its datagrams.
func shard(seq uint32, rec []byte, stripe float64) [][]byte {
	k := (len(rec) + dgShard - 1) / dgShard
	if k == 0 {
		k = 1
	}
	p := int(float64(k)/stripe + 0.999)
	if k <= 3 {
		p = 1 // a small frame's one parity is cheap: a copy of it, near enough
	}
	grams := make([][]byte, 0, k+p)
	head := func(i int) []byte {
		b := make([]byte, dgHeader+dgShard)
		b[0] = 1
		binary.BigEndian.PutUint32(b[1:], seq)
		binary.BigEndian.PutUint16(b[5:], uint16(i))
		binary.BigEndian.PutUint16(b[7:], uint16(k))
		binary.BigEndian.PutUint16(b[9:], uint16(p))
		binary.BigEndian.PutUint32(b[11:], uint32(len(rec)))
		return b
	}
	for i := 0; i < k; i++ {
		b := head(i)
		copy(b[dgHeader:], rec[i*dgShard:min((i+1)*dgShard, len(rec))])
		grams = append(grams, b)
	}
	for j := 0; j < p; j++ {
		b := head(k + j)
		par := b[dgHeader:]
		for i := j; i < k; i += p {
			src := grams[i][dgHeader:]
			for x := range par {
				par[x] ^= src[x]
			}
		}
		grams = append(grams, b)
	}
	return grams
}

func (d *dgSink) send(seq uint32, f *mediaFrame) {
	rec := frameRecord(seq, f)
	d.mu.Lock()
	grams := shard(seq, rec, d.stripe)
	now := time.Now()
	d.cache[seq] = &dgFrame{at: now, grams: grams}
	// Loss fades as frames go without NACKs: back to light parity once a
	// bad patch is over.
	d.lossW *= 0.98
	d.restripe()
	for s, c := range d.cache {
		if now.Sub(c.at) > dgKeep {
			delete(d.cache, s)
		}
	}
	d.mu.Unlock()
	for _, g := range grams {
		d.queue(g)
	}
}

// nack resends the shards of a frame the viewer is missing, if it's still
// fresh, and takes the loss into the parity.
func (d *dgSink) nack(seq uint32, idx []uint16) {
	d.mu.Lock()
	c := d.cache[seq]
	var again [][]byte
	if c != nil && time.Since(c.at) < mediaStale {
		for _, i := range idx {
			if int(i) < len(c.grams) {
				again = append(again, c.grams[i])
			}
		}
	}
	if c != nil && !c.nacked {
		// Loss: shards it lost of those sent (after parity, so a floor).
		c.nacked = true
		lost := float64(len(idx)) / float64(len(c.grams))
		d.lossW = d.lossW*0.8 + lost*0.2
		d.restripe()
	}
	d.mu.Unlock()
	for _, g := range again {
		d.queue(g)
	}
}

// restripe sets the parity from the loss: a shard in twice the loss's
// worth, between one in two and one in ten. Under d.mu.
func (d *dgSink) restripe() { d.stripe = min(max(1/(2*max(d.lossW, 0.01)), 2), 10) }

func (d *dgSink) abandon(from uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for s := range d.cache {
		if s >= from {
			delete(d.cache, s)
		}
	}
}

func (d *dgSink) close() {
	d.once.Do(func() { close(d.closed) })
	d.qc.CloseWithError(0, "")
}
