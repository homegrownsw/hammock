// Package linkalive notices a network connection that has silently died:
// a Wi-Fi switch, sleep or a NAT forgetting it leaves TCP thinking it's
// up for minutes. The side that watches pings the other every so often
// (a WebSocket ping, answered by every client and daemon, old ones
// included) and gives up on the connection once nothing at all, pong or
// message, has come back for two of those.
package linkalive

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// ErrSilent: nothing has come from the other end for too long.
var ErrSilent = errors.New("no answer from the other end")

// base is what Watch's times count from: monotonic, so a clock change
// isn't taken for silence (sleep doesn't count: the wake check is for that).
var base = time.Now()

func now() int64 { return int64(time.Since(base)) }

// Watch follows when a connection last heard from the other end.
type Watch struct {
	last  atomic.Int64
	check chan struct{}
	// Busy, when set, says the connection's reader is held up (handling a
	// message, or its reader's queue full): nothing can be heard meanwhile,
	// so that isn't counted as silence.
	Busy func() bool
}

// New starts a Watch, as if just heard from.
func New() *Watch {
	w := &Watch{check: make(chan struct{}, 1)}
	w.Heard()
	return w
}

// Heard notes that something came from the other end.
func (w *Watch) Heard() { w.last.Store(now()) }

// Check asks Run to make sure of the connection now (the network changed,
// or the computer woke): a ping, which must be answered within Run's
// within.
func (w *Watch) Check() {
	select {
	case w.check <- struct{}{}:
	default:
	}
}

func (w *Watch) silentSince(at int64) bool {
	return w.last.Load() < at && (w.Busy == nil || !w.Busy())
}

// Run pings every every and returns ErrSilent once nothing has been heard
// for two of those (or, after Check, for within), or nil when ctx ends.
// ping sends a ping and waits for its pong; its error is ignored (a pong,
// or anything else that comes, is what counts: Heard).
func (w *Watch) Run(ctx context.Context, every, within time.Duration, ping func(context.Context) error) error {
	t := time.NewTicker(every)
	defer t.Stop()
	send := func(wait time.Duration) {
		go func() {
			pctx, cancel := context.WithTimeout(ctx, wait)
			defer cancel()
			_ = ping(pctx)
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if w.silentSince(now() - int64(2*every)) {
				return ErrSilent
			}
			send(2 * every)
		case <-w.check:
			at := now()
			send(within)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(within):
			}
			if w.silentSince(at) {
				return ErrSilent
			}
		}
	}
}
