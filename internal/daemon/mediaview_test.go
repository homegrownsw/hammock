package daemon

import (
	"context"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/mediaview"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The viewer the Android app uses (internal/mediaview, through
// cmd/hmkquic) against the daemon: pinned to its certificate, it takes
// frames over streams, and over datagrams through loss, whole and acked.
func TestMediaViewer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		datagrams bool
		loss      float64
	}{
		{"streams", false, 0},
		{"datagrams", true, 0.15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, c, _, paneID := mediaDaemon(t)
			ticket := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, Datagrams: tc.datagrams}}}).GetMediaTicket()
			host, _, _ := net.SplitHostPort(d.RemoteAddrs()[0])
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := mediaview.Dial(ctx, host, int(ticket.QuicPort), wrongPin(d.remote.fingerprint), ticket.Ticket, mediaview.Options{}); err == nil {
				t.Fatal("a certificate that isn't the pinned one was accepted")
			}

			var mu sync.Mutex
			rng := rand.New(rand.NewSource(1))
			lost := 0
			drop := func() bool {
				mu.Lock()
				defer mu.Unlock()
				if rng.Float64() < tc.loss {
					lost++
					return true
				}
				return false
			}
			v, err := mediaview.Dial(ctx, host, int(ticket.QuicPort), d.remote.fingerprint, ticket.Ticket, mediaview.Options{Datagrams: tc.datagrams, Drop: drop})
			if err != nil {
				t.Fatal(err)
			}
			defer v.Close()
			sawKey := false
			for n := 0; n < 40; n++ {
				rec, err := v.Next(5 * time.Second)
				if err != nil || rec == nil {
					t.Fatalf("frame %d: %v (lost %d shards)", n, err, lost)
				}
				key := rec[4]&1 != 0
				if len(rec) <= mediaFrameHeader || (key && len(rec) != mediaFrameHeader+20000) {
					t.Fatalf("frame %d: %d bytes, key %v", n, len(rec), key)
				}
				sawKey = sawKey || key
			}
			if !sawKey {
				t.Fatal("no keyframe")
			}
			mu.Lock()
			if tc.loss > 0 && lost == 0 {
				t.Fatal("nothing was lost")
			}
			mu.Unlock()

			// The session ending ends the viewer.
			call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseMedia{CloseMedia: &pb.CloseMedia{SessionId: ticket.SessionId}}})
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := v.Next(100 * time.Millisecond); err == mediaview.ErrClosed {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("still open after CloseMedia")
				}
			}
		})
	}
}

// wrongPin is pin with its first digit changed.
func wrongPin(pin string) string {
	if pin[0] == '0' {
		return "1" + pin[1:]
	}
	return "0" + pin[1:]
}
