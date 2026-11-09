//go:build !darwin && !linux

package holder

import "net"

// sockPeerPID is unknown here.
func sockPeerPID(*net.UnixConn) int { return 0 }
