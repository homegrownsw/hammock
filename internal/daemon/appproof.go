package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The Mac app's proof that a connection to this daemon's socket is its own
// (helpers/common/appproof.h, which both ends build from): what makes a
// local connection the app, positivelyPerson's "proven" (person.go).
//
// A socket's peer alone can't say it's the app. macOS works a unix
// socket's audit token (LOCAL_PEERTOKEN) out when asked, from the pid that
// last used the other end, as that pid is then: a process of yours can
// connect, write, hand the socket to a forked child and exec a suspended,
// never-running copy of the signed app in its own pid (stopping the daemon
// until it has), and every check of the socket's peer then sees a live,
// validly signed app while the child goes on talking. A Mach message's
// audit token is its sender's as it was sent, pid version and all, so the
// app proves itself with one:
//
//   - As a local connection's first message, the app sends ProveApp. The
//     daemon answers AppChallenge (its Mach service, the connection's
//     number, a fresh 32-byte nonce for that connection alone) and then
//     reads nothing more from the connection until the proof is in, or
//     appProofFor has passed: nothing the app sends after its proof can be
//     handled before the proof is.
//   - The app sends the number and nonce to the service. The daemon takes a
//     proof once (its nonce compared in constant time; a wrong one is
//     ignored, so a guess can't spoil the app's), while fresh, and only if
//     the message's audit token is a live Hammock app (isHammockApp: this
//     daemon's team, the app's identifiers and the hardened runtime; ad hoc,
//     the program of the app this daemon is in) and the very process at the
//     other end of the connection the nonce was issued to: its pid and pid
//     version as the listener took them before the nonce was sent, and as
//     the socket says now. Exec'ing the app after connecting doesn't pass:
//     the exec'd app never sends a proof (suspended), and a running one has
//     a new pid version; a proof sent before the exec names nothing once it
//     has. A program between the app and the daemon (its own socket put in
//     the daemon's place, relaying) can hand the app a nonce issued to its
//     own connection, but that connection's peer is the relay, not the app.
//   - Only then is the connection the app's: what only you may do, and
//     where notifications, requests and pairing codes go (c.kind "mac").

// appProofFor is how long a nonce is good for, and how long a connection
// waits for its proof.
var appProofFor = 5 * time.Second

// appSignedHook, when set (tests), stands in for the check that a proof's
// sender is signed as the Hammock app.
var appSignedHook func(codesign.Token) bool

// appProofs are the nonces issued and not yet proven.
type appProofs struct {
	mu      sync.Mutex
	next    uint64
	pending map[uint64]*appProofWait

	svcOnce sync.Once
	svc     string // the Mach service's name
	svcErr  error
}

// appProofWait is a connection waiting for its proof.
type appProofWait struct {
	id     uint64
	nonce  []byte
	until  time.Time
	pid    int            // the connection's peer as the listener took it
	tok    codesign.Token // (its audit token then)
	nc     net.Conn       // the connection, to ask who its peer is now
	inPane bool           // identify placed it in a pane: never the app
	done   chan error     // its verdict, once
}

// issue makes a fresh nonce for w and keeps it until it's taken or dropped.
func (p *appProofs) issue(w *appProofWait, now time.Time) {
	w.nonce = make([]byte, 32)
	_, _ = rand.Read(w.nonce)
	w.until = now.Add(appProofFor)
	w.done = make(chan error, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[uint64]*appProofWait{}
	}
	p.next++
	w.id = p.next
	p.pending[w.id] = w
}

// take is the wait for connection id if nonce is its nonce, taken: it's
// never taken again, and its verdict must be sent (fresh says whether it's
// still in time). nil if there's none, and a wrong nonce leaves it
// waiting.
func (p *appProofs) take(id uint64, nonce []byte, now time.Time) (w *appProofWait, fresh bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w = p.pending[id]
	if w == nil || len(nonce) != len(w.nonce) || subtle.ConstantTimeCompare(nonce, w.nonce) != 1 {
		return nil, false
	}
	delete(p.pending, id)
	return w, !now.After(w.until)
}

// drop forgets w's nonce (its connection stopped waiting), reporting
// whether it was still there: if not, it was taken, and its verdict is on
// its way.
func (p *appProofs) drop(w *appProofWait) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending[w.id] == w {
		delete(p.pending, w.id)
		return true
	}
	return false
}

var (
	errProofLate     = errors.New("too late: an app's proof is a connection's first message")
	errProofHere     = errors.New("only the Hammock app on this Mac proves itself")
	errProofStale    = errors.New("too late: its nonce has expired")
	errProofGone     = errors.New("its sender has gone, or isn't the program it was when it sent it")
	errProofUnsigned = errors.New("its sender isn't the Hammock app")
	errProofPeer     = errors.New("its sender isn't the process at the other end of the connection")
)

// proofSender says why t, a proof's sender as it sent it, isn't the app at
// the other end of w's connection (nil: it is).
func proofSender(w *appProofWait, t codesign.Token) error {
	if !t.Valid() || !t.Live() {
		return errProofGone
	}
	if w.inPane {
		return errProofPeer // (the app is in no pane)
	}
	// The process that connected, as the listener took it before the nonce
	// went out (so before anything could have read it), and the one at the
	// other end now (the last to use it): both the sender, pid version and
	// all.
	if t.PID() != w.pid || !w.tok.Valid() || !t.SameProcess(w.tok) {
		return errProofPeer
	}
	if now := (localPeer{pid: peerPID(w.nc), tok: peerToken(w.nc)}); now.pid != t.PID() || !t.SameProcess(now.tok) {
		return errProofPeer
	}
	signed := appSignedHook
	if signed == nil {
		signed = func(t codesign.Token) bool { return isHammockApp(t.PID(), t) }
	}
	if !signed(t) || !t.Live() {
		return errProofUnsigned
	}
	return nil
}

// proveApp is a local connection's ProveApp: its challenge sent, it waits
// (reading nothing more) for the proof, and the connection is the app's if
// it holds (c.proven, which scope reads). Read loop only.
func (c *conn) proveApp(id uint64) (*pb.ServerMessage, error) {
	ft, ok := c.t.(*frameTransport)
	switch {
	case c.remote || c.peer != "" || !ok:
		return nil, errProofHere
	case c.read != 1 || c.proofTried:
		return nil, errProofLate
	}
	c.proofTried = true
	svc, err := c.d.appProofService()
	if err != nil {
		return nil, err
	}
	w := &appProofWait{pid: c.pid, tok: c.tok, nc: ft.c, inPane: c.caller != nil}
	c.d.proofs.issue(w, time.Now())
	c.send(&pb.ServerMessage{ReplyTo: id, Msg: &pb.ServerMessage_AppChallenge{AppChallenge: &pb.AppChallenge{
		Service: svc, Conn: w.id, Nonce: w.nonce,
	}}})
	t := time.NewTimer(appProofFor)
	defer t.Stop()
	select {
	case err = <-w.done:
	case <-t.C:
		err = errors.New("no proof came")
		if !c.d.proofs.drop(w) {
			err = <-w.done // (taken as it timed out: the verdict's coming)
		}
	case <-c.ctx.Done():
		c.d.proofs.drop(w)
		return nil, errDeferred
	}
	if err != nil {
		c.d.log.Info("a local client that asked to prove it's the Hammock app didn't", "pid", c.pid, "why", err)
	} else {
		c.proven = true
		c.d.log.Info("the Hammock app proved its connection", "pid", c.pid)
	}
	return nil, errDeferred // (answered: the challenge)
}

// appProof is a proof as the service got it, for connection id with
// nonce from t (its sender as it sent it): the verdict goes to that
// connection, and what to answer the sender is returned.
func (d *Daemon) appProof(id uint64, nonce []byte, t codesign.Token) string {
	w, fresh := d.proofs.take(id, nonce, time.Now())
	if w == nil {
		d.log.Info("ignored an app proof for no connection waiting with that nonce", "pid", t.PID())
		return "no connection is waiting with that nonce"
	}
	err := errProofStale
	if fresh {
		err = proofSender(w, t)
	}
	w.done <- err
	if err != nil {
		return err.Error()
	}
	return "ok"
}
