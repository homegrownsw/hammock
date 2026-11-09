//go:build !darwin && !linux

package main

import "syscall"

// processAlive: pid is a running process.
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
