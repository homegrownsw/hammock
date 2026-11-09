//go:build !darwin && !linux

package daemon

import "net"

// sockPeerPID is unknown here: callers are treated as outside any pane.
func sockPeerPID(*net.UnixConn) int { return 0 }
