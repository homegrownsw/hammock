package holder

import (
	"net"

	"github.com/homegrownsw/hammock/internal/codesign"
	"golang.org/x/sys/unix"
)

// sockPeer is the process at the other end of a Unix socket: its pid, and
// its audit token (codesign.Token), which a program exec'd in its place
// since doesn't have. Both are of the last process to use the socket, as it
// is when asked (codesign.PeerToken), so they don't say who's talking: a
// process can connect, hand the socket to a forked child and exec a
// suspended hmkd in its own pid, and both then name that hmkd. The holder
// takes them as a client connects, before it sends anything, and again as
// the client's proof arrives, and requires the proof's sender, whose token
// is fixed as it sent it, to be that one process both times (proof.go).
func sockPeer(uc *net.UnixConn) PeerProc {
	rc, err := uc.SyscallConn()
	if err != nil {
		return PeerProc{}
	}
	var p PeerProc
	_ = rc.Control(func(fd uintptr) {
		p.Token = codesign.PeerToken(int(fd))
		if v, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
			p.PID = v
		}
	})
	return p
}

// peerExe is the peer's executable, by its audit token: the program that
// connected, not one exec'd in its pid since.
func peerExe(p PeerProc) (string, error) { return p.Token.Path() }

// peerSignature is how the peer is signed, by its audit token.
func peerSignature(p PeerProc, requirement string) (codesign.Signature, bool) {
	return codesign.CheckToken(p.Token, requirement)
}

// peerStill reports whether the peer still runs the program that connected
// (its pid wasn't taken by another process while it was checked).
func peerStill(p PeerProc) bool { return p.Token.Live() }

// sameProc reports whether a and b, a socket's peer before and after it
// was read from, are the one process, still running the program it was.
// (Only a holder from before the proof relies on it: tests' legacy ones.)
func sameProc(a, b PeerProc) bool {
	return a.PID == b.PID && a.Token.SameProcess(b.Token) && b.Token.Live()
}
