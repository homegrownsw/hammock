package main

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// processAlive: pid is a running process, not a zombie. A daemon the Mac
// app spawned stays a zombie until the app reaps it, and kill(pid, 0)
// succeeds on one.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return true
	}
	return k.Proc.P_stat != 5 // SZOMB
}
