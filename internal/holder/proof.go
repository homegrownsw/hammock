package holder

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// The daemon's proof that a connection to a holder is its own (macOS).
//
// A socket's peer can't say who's talking. macOS works a unix socket's
// audit token (LOCAL_PEERTOKEN) out when asked, from the pid that last used
// the other end, as that pid is then: a process of yours can connect, hand
// the socket to a forked child and exec a suspended, never-running copy of
// the signed hmkd in its own pid (stopping the holder until it has), and
// every check of the socket's peer then sees a live, validly signed hmkd,
// with whatever descriptors the impostor left it: a suspended hmkd never
// runs CloseInherited, so flocks it took on the state dir while no daemon
// ran pass the state lock check (statelock.go) once the real daemon has the
// locks again. A Mach message's audit token is its sender's as it was sent,
// pid version and all, so the daemon proves itself with one:
//
//   - As a connection's first frame, the daemon sends FrameProve. The holder
//     answers FrameChallenge (the name of a Mach service it holds for its
//     life, checked in under a random name the first time it's needed; the
//     connection's number; a fresh 32-byte nonce for that connection alone)
//     and then reads nothing more from the connection until the proof is in,
//     or ProofFor has passed: no attach, input, resize, signal or close.
//   - The daemon sends the number and nonce to the service. The holder takes
//     a proof once (its nonce compared in constant time; a wrong one is
//     ignored, so a guess can't spoil the daemon's), while fresh, and only if
//     the message's audit token is live, the very process at the other end
//     of the connection (its pid and pid version as the holder took them as
//     it accepted, before the nonce went out, and as the socket says when
//     the proof arrives), the daemon program (Config.Peer, DaemonPeer's
//     rules), and the holder of the state dir's locks (Config.State,
//     HeldBy), all of that process as it sent the proof.
//   - Only then does the connection's FrameAttach (with the pane's token, as
//     before) count.
//
// Why that makes the lock check hold: to send, the sender was running, and a
// running hmkd has closed every descriptor it inherited above stderr before
// anything else (cmd/hmkd, CloseInherited), so the locks it holds are its
// own, taken as its state dir's daemon. Exec'ing hmkd after connecting
// doesn't pass: the exec'd hmkd never sends a proof while suspended, and
// once it runs it has a new pid version and none of the impostor's
// descriptors; a proof sent before the exec names nothing once it has. A
// program between the daemon and a holder (its own socket put in the
// holder's place, relaying) can hand the daemon a nonce issued to its own
// connection, but that connection's peer is the relay, not the daemon.
//
// A holder that checks its client (Config.Peer or State) takes no attach
// without the proof, whatever the client asks. Holders started by builds
// before it close a connection whose first frame isn't FrameAttach, and the
// daemon then attaches as it used to (Dial); they keep the socket checks,
// which the trick above passes, until their panes are restarted. Linux has
// no Mach: its holders keep the socket checks (SO_PEERCRED), a speed bump
// there (peerpid_linux.go).

// ProofFor is how long a nonce is good for, and how long a connection waits
// for its proof.
var ProofFor = 5 * time.Second

// proofHello is the daemon's proof's hello (rendezvous.h's magic, 16 bytes):
// whose it is, and its wire format's version.
const proofHello = "hmk-holder-pf-v1"

const (
	proofNonceLen = 32
	proofBodyLen  = 8 + proofNonceLen // the connection's number (big-endian), its nonce
)

// proofHeardHook and proofVerdictHook (tests) are told when a proof arrives,
// before it's looked at, and what became of it.
var (
	proofHeardHook   func()
	proofVerdictHook func(error)
)

// proofs are a holder's nonces issued and not yet proven.
type proofs struct {
	mu      sync.Mutex
	next    uint64
	pending map[uint64]*proofWait
}

// proofWait is a connection waiting for its proof.
type proofWait struct {
	id    uint64
	nonce []byte
	until time.Time
	at    PeerProc      // the connection's peer as the holder accepted it
	uc    *net.UnixConn // the connection, to ask who its peer is now
	done  chan error    // its verdict, once
}

// issue makes a fresh nonce for w and keeps it until it's taken or dropped.
func (p *proofs) issue(w *proofWait, now time.Time) error {
	w.nonce = make([]byte, proofNonceLen)
	if _, err := rand.Read(w.nonce); err != nil {
		return err
	}
	w.until = now.Add(ProofFor)
	w.done = make(chan error, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[uint64]*proofWait{}
	}
	p.next++
	w.id = p.next
	p.pending[w.id] = w
	return nil
}

// take is the wait for connection id if nonce is its nonce, taken: it's
// never taken again, and its verdict must be sent (fresh says whether it's
// still in time). nil if there's none, and a wrong nonce leaves it waiting.
func (p *proofs) take(id uint64, nonce []byte, now time.Time) (w *proofWait, fresh bool) {
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
func (p *proofs) drop(w *proofWait) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending[w.id] == w {
		delete(p.pending, w.id)
		return true
	}
	return false
}

var (
	errProofStale     = errors.New("too late: its nonce has expired")
	errProofGone      = errors.New("its sender has gone, or isn't the program it was when it sent it")
	errProofPeer      = errors.New("its sender isn't the process at the other end of the connection")
	errProofNotDaemon = errors.New("its sender isn't the daemon")
)

// proofSender says why t, a proof's sender as it sent it, isn't the daemon
// at the other end of w's connection (nil: it is).
func (s *Server) proofSender(w *proofWait, t codesign.Token) error {
	if !t.Valid() || !t.Live() {
		return errProofGone
	}
	// The process that connected, as accept took it before the nonce went
	// out (so before anything could have read it), and the one at the other
	// end now (the last to use it): both the sender, pid version and all.
	if t.PID() != w.at.PID || !w.at.Token.Valid() || !t.SameProcess(w.at.Token) {
		return errProofPeer
	}
	if now := sockPeer(w.uc); now.PID != t.PID() || !t.SameProcess(now.Token) {
		return errProofPeer
	}
	p := PeerProc{PID: t.PID(), Token: t}
	if s.cfg.Peer != nil && !s.cfg.Peer(p) {
		return errProofNotDaemon
	}
	if s.cfg.State != nil {
		if err := s.cfg.State.HeldBy(p.PID); err != nil {
			return fmt.Errorf("its sender is not the daemon of this pane's state: %w", err)
		}
	}
	if !t.Live() { // (its pid wasn't taken by another process while it was checked)
		return errProofGone
	}
	return nil
}

// proofArrived is a proof as the holder's service got it (body: the
// connection's number and nonce) from t, its sender as it sent it: the
// verdict goes to that connection, and what to answer the sender is
// returned.
func (s *Server) proofArrived(body []byte, t codesign.Token) string {
	if proofHeardHook != nil {
		proofHeardHook()
	}
	if len(body) != proofBodyLen {
		return "not a proof"
	}
	w, fresh := s.proofs.take(binary.BigEndian.Uint64(body[:8]), body[8:], time.Now())
	if w == nil {
		logf("ignored a proof for no connection waiting with that nonce (from pid %d)", t.PID())
		return "no connection is waiting with that nonce"
	}
	err := errProofStale
	if fresh {
		err = s.proofSender(w, t)
	}
	w.done <- err
	if proofVerdictHook != nil {
		proofVerdictHook(err)
	}
	if err != nil {
		return err.Error()
	}
	return "ok"
}

// awaitProof is a connection's FrameProve: its challenge sent, it waits
// (reading nothing from the connection) for the proof. nil: the connection
// is the daemon's.
func (s *Server) awaitProof(conn net.Conn, at PeerProc) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok || at.PID <= 0 || !at.Token.Valid() {
		return errors.New("can't tell who connected")
	}
	svc, err := s.proofService()
	if err != nil {
		return err
	}
	w := &proofWait{at: at, uc: uc}
	if err := s.proofs.issue(w, time.Now()); err != nil {
		return err
	}
	ch := make([]byte, 0, 8+proofNonceLen+len(svc))
	ch = append(append(append(ch, u64(w.id)...), w.nonce...), svc...)
	if err := WriteFrame(conn, FrameChallenge, ch); err != nil {
		s.proofs.drop(w)
		return err
	}
	t := time.NewTimer(ProofFor)
	defer t.Stop()
	select {
	case err = <-w.done:
	case <-t.C:
		err = errors.New("no proof came")
		if !s.proofs.drop(w) {
			err = <-w.done // (taken as it timed out: the verdict's coming)
		}
	case <-s.done:
		s.proofs.drop(w)
		return errors.New("shutting down")
	}
	return err
}

// parseChallenge reads a FrameChallenge: the connection's number, its
// nonce, and the holder's service.
func parseChallenge(p []byte) (id uint64, nonce []byte, svc string, err error) {
	if len(p) <= 8+proofNonceLen {
		return 0, nil, "", errors.New("holder: short challenge")
	}
	return binary.BigEndian.Uint64(p), p[8 : 8+proofNonceLen], string(p[8+proofNonceLen:]), nil
}
