package daemon

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// bindToInterface makes a listener take only what arrives on iface
// (SO_BINDTODEVICE): see the darwin version.
func bindToInterface(iface string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			serr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface)
		})
		if err != nil {
			return err
		}
		return serr
	}
}
