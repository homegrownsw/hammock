package holder

import (
	"net"

	"golang.org/x/sys/unix"
)

// sockPeerPID returns the process id at the other end of a Unix socket.
func sockPeerPID(uc *net.UnixConn) int {
	rc, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	_ = rc.Control(func(fd uintptr) {
		if v, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
			pid = v
		}
	})
	return pid
}
