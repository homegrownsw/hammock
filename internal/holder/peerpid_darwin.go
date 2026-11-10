package holder

import (
	"net"

	"github.com/alexjsp/hammock/internal/codesign"
	"golang.org/x/sys/unix"
)

// sockPeer is the process at the other end of a Unix socket: its pid, and
// its audit token (codesign.Token), which a program exec'd in its place
// since doesn't have. Both are of the last process to use the socket
// (codesign.PeerToken): the holder takes them as a client connects and
// again once it has spoken (sameProc).
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
func sameProc(a, b PeerProc) bool {
	return a.PID == b.PID && a.Token.SameProcess(b.Token) && b.Token.Live()
}
