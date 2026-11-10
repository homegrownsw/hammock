package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/quic-go/quic-go"
)

// dgAssembly gathers one frame's shards, as a viewer does.
type dgAssembly struct {
	k, p, n int
	have    map[int][]byte
}

func (a *dgAssembly) add(i int, b []byte) { a.have[i] = b }

// rebuild fills in what parity can; then the record, or the data shards
// still missing.
func (a *dgAssembly) rebuild() ([]byte, []uint16) {
	for j := 0; j < a.p; j++ {
		var lost []int
		for i := j; i < a.k; i += a.p {
			if a.have[i] == nil {
				lost = append(lost, i)
			}
		}
		par := a.have[a.k+j]
		if len(lost) != 1 || par == nil {
			continue
		}
		x := append([]byte(nil), par...)
		for i := j; i < a.k; i += a.p {
			if i != lost[0] {
				for o := range x {
					x[o] ^= a.have[i][o]
				}
			}
		}
		a.have[lost[0]] = x
	}
	var miss []uint16
	for i := 0; i < a.k; i++ {
		if a.have[i] == nil {
			miss = append(miss, uint16(i))
		}
	}
	if miss != nil {
		return nil, miss
	}
	rec := make([]byte, 0, a.k*dgShard)
	for i := 0; i < a.k; i++ {
		rec = append(rec, a.have[i]...)
	}
	return rec[:a.n], nil
}

func parseGram(b []byte) (seq uint32, i, k, p, n int, body []byte) {
	return binary.BigEndian.Uint32(b[1:]), int(binary.BigEndian.Uint16(b[5:])), int(binary.BigEndian.Uint16(b[7:])),
		int(binary.BigEndian.Uint16(b[9:])), int(binary.BigEndian.Uint32(b[11:])), b[dgHeader:]
}

func TestShardParity(t *testing.T) {
	rec := make([]byte, 20000)
	rand.Read(rec)
	for _, stripe := range []float64{2, 5, 10} {
		grams := shard(7, rec, stripe)
		_, _, k, p, _, _ := parseGram(grams[0])
		if len(grams) != k+p || k != 19 {
			t.Fatalf("stripe %v: %d grams, k %d p %d", stripe, len(grams), k, p)
		}
		// Lose one data shard in each stripe: parity rebuilds them all.
		a := &dgAssembly{k: k, p: p, n: len(rec), have: map[int][]byte{}}
		for idx, g := range grams {
			if idx < p {
				continue
			}
			_, i, _, _, _, body := parseGram(g)
			a.add(i, body)
		}
		got, miss := a.rebuild()
		if miss != nil || !bytes.Equal(got, rec) {
			t.Fatalf("stripe %v: missing %v", stripe, miss)
		}
	}
	if grams := shard(1, []byte("tiny"), 10); len(grams) != 2 {
		t.Fatalf("a one-shard frame: %d grams", len(grams))
	}
}

// Frames over datagrams through 15% loss: parity and NACKed resends get
// every frame through whole.
func TestMediaDatagrams(t *testing.T) {
	d, c, _, paneID := mediaDaemon(t)
	ticket := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, Datagrams: true}}}).GetMediaTicket()
	host, _, _ := net.SplitHostPort(d.RemoteAddrs()[0])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	qc, err := quic.DialAddr(ctx, net.JoinHostPort(host, strconv.Itoa(int(ticket.QuicPort))),
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{mediaALPN}}, &quic.Config{EnableDatagrams: true, MaxIncomingUniStreams: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer qc.CloseWithError(0, "")
	ctl, _ := qc.OpenStream()
	ctl.Write(ticket.Ticket)
	go func() { // no frames on streams
		if _, err := qc.AcceptUniStream(context.Background()); err == nil {
			t.Error("a frame on a stream")
		}
	}()

	var mu sync.Mutex
	frames := map[uint32]*dgAssembly{}
	done := map[uint32]bool{}
	whole := make(chan []byte, 256)
	write := func(b []byte) { ctl.Write(b) } // always under mu
	try := func(seq uint32, a *dgAssembly, nack bool) {
		rec, miss := a.rebuild()
		if rec != nil {
			done[seq] = true
			delete(frames, seq)
			ack := []byte{1, 0, 0, 0, 0}
			binary.BigEndian.PutUint32(ack[1:], seq)
			write(ack)
			whole <- rec
		} else if nack {
			b := []byte{3, 0, 0, 0, 0, byte(len(miss))}
			binary.BigEndian.PutUint32(b[1:], seq)
			for _, i := range miss {
				b = binary.BigEndian.AppendUint16(b, i)
			}
			write(b)
		}
	}
	rng := rand.New(rand.NewSource(1))
	var lost, got int
	go func() {
		for {
			b, err := qc.ReceiveDatagram(context.Background())
			if err != nil {
				return
			}
			seq, i, k, p, n, body := parseGram(b)
			mu.Lock()
			if rng.Float64() < 0.15 {
				lost++
				mu.Unlock()
				continue
			}
			got++
			if done[seq] {
				mu.Unlock()
				continue
			}
			a := frames[seq]
			if a == nil {
				a = &dgAssembly{k: k, p: p, n: n, have: map[int][]byte{}}
				frames[seq] = a
				s := seq
				time.AfterFunc(30*time.Millisecond, func() {
					mu.Lock()
					defer mu.Unlock()
					if f := frames[s]; f != nil {
						try(s, f, true)
					}
				})
			}
			a.add(i, body)
			// Every shard in: whole (or a NACK now rather than waiting).
			mu.Unlock()
			mu.Lock()
			if len(a.have) >= k {
				try(seq, a, false)
			}
			mu.Unlock()
		}
	}()
	sawKey := false
	for n := 0; n < 40; n++ {
		select {
		case rec := <-whole:
			key := rec[4]&1 != 0
			if len(rec) <= mediaFrameHeader || (key && len(rec) != mediaFrameHeader+20000) {
				t.Fatalf("frame %d: %d bytes, key %v", n, len(rec), key)
			}
			sawKey = sawKey || key
		case <-time.After(5 * time.Second):
			mu.Lock()
			t.Fatalf("frame %d never whole (got %d shards, lost %d)", n, got, lost)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if lost == 0 || !sawKey {
		t.Fatalf("lost %d, keyframe %v", lost, sawKey)
	}
}
