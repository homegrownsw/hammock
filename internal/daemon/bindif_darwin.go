package daemon

import (
	"net"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// bindToInterface makes a listener take only what arrives on iface (macOS's
// IP_BOUND_IF): an address bound alone is still reached from any network
// this computer is on (the weak host model), so a chosen VPN's address
// isn't reached from an unchosen café Wi-Fi (ADR-0015).
func bindToInterface(iface string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		ifi, err := net.InterfaceByName(iface)
		if err != nil {
			return err
		}
		var serr error
		err = c.Control(func(fd uintptr) {
			if strings.HasSuffix(network, "6") || strings.Count(address, ":") > 1 {
				serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, ifi.Index)
			} else {
				serr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, ifi.Index)
			}
		})
		if err != nil {
			return err
		}
		return serr
	}
}
