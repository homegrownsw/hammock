package holder

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strconv"

	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
	"golang.org/x/sys/unix"
)

// sockPeer is the process at the other end of a Unix socket, by its pid
// (Linux has no audit tokens).
func sockPeer(uc *net.UnixConn) PeerProc {
	rc, err := uc.SyscallConn()
	if err != nil {
		return PeerProc{}
	}
	var p PeerProc
	_ = rc.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			p.PID = int(cred.Pid)
		}
	})
	return p
}

// peerExe is the peer's executable, by pid. On Linux this is a speed bump
// against programs of yours, not a boundary: a process of the same user can
// exec hmkd after connecting (its pid then shows hmkd), or run hmkd with
// LD_PRELOAD or under ptrace. A preloaded library is refused, and so is a
// pid that changed hands while it was looked at (its start time); an exec
// keeps both, so isn't caught.
func peerExe(p PeerProc) (string, error) {
	start, err := procinfo.StartTime(p.PID)
	if err != nil {
		return "", err
	}
	exe, err := procinfo.Path(p.PID)
	if err != nil {
		return "", err
	}
	env, err := os.ReadFile("/proc/" + strconv.Itoa(p.PID) + "/environ")
	if err != nil {
		return "", err
	}
	for _, kv := range bytes.Split(env, []byte{0}) {
		if bytes.HasPrefix(kv, []byte("LD_PRELOAD=")) || bytes.HasPrefix(kv, []byte("LD_AUDIT=")) {
			return "", errors.New("runs with a preloaded library")
		}
	}
	if again, err := procinfo.StartTime(p.PID); err != nil || !again.Equal(start) {
		return "", errors.New("the process went")
	}
	return exe, nil
}

// peerSignature: there are no code signatures here.
func peerSignature(PeerProc, string) (codesign.Signature, bool) { return codesign.Signature{}, false }

// peerStill: there's no audit token to ask (see peerExe).
func peerStill(PeerProc) bool { return true }
