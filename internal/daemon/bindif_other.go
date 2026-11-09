//go:build !darwin && !linux

package daemon

import "syscall"

// bindToInterface: elsewhere, the address alone.
func bindToInterface(string) func(network, address string, c syscall.RawConn) error { return nil }
