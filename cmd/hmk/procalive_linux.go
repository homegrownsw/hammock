package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processAlive: pid is a running process, not a zombie (kill(pid, 0)
// succeeds on one until its parent reaps it).
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	// "pid (comm) S …": the state follows the last ')', as comm may hold one.
	s := string(b)
	if i := strings.LastIndexByte(s, ')'); i >= 0 && i+2 < len(s) {
		return s[i+2] != 'Z'
	}
	return true
}
