// Package procinfo reads information about other processes (working
// directory, name) for cwd tracking and agent heuristics.
package procinfo

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cwd returns the current working directory of pid.
func Cwd(pid int) (string, error) { return os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)) }

// Path returns the path of pid's executable (links resolved).
func Path(pid int) (string, error) { return os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)) }

// Name returns the short command name of pid.
func Name(pid int) (string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	return strings.TrimSpace(string(b)), err
}

// Children returns the direct child process ids of pid: from
// /proc/<pid>/task/<pid>/children where the kernel has it
// (CONFIG_PROC_CHILDREN), else from every process's parent in /proc.
func Children(pid int) []int {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && procExists(pid) {
			return childrenByScan(pid)
		}
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		var c int
		if _, err := fmt.Sscan(f, &c); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func procExists(pid int) bool {
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

// scanEvery is how long one scan of /proc answers Children for: callers
// ask about each pane's processes in turn, and a scan reads every
// process's stat.
const scanEvery = 500 * time.Millisecond

var scan struct {
	sync.Mutex
	at       time.Time
	children map[int][]int
}

// childrenByScan finds pid's children by reading every process's parent
// (kernels built without CONFIG_PROC_CHILDREN, as Unraid's are).
func childrenByScan(pid int) []int {
	scan.Lock()
	defer scan.Unlock()
	if scan.children == nil || time.Since(scan.at) > scanEvery {
		scan.children = map[int][]int{}
		ents, _ := os.ReadDir("/proc")
		for _, e := range ents {
			c, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			if pp, err := Parent(c); err == nil {
				scan.children[pp] = append(scan.children[pp], c)
			}
		}
		scan.at = time.Now()
	}
	return slices.Clone(scan.children[pid])
}

// Parent returns pid's parent process id.
func Parent(pid int) (int, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// pid (comm) state ppid ...: comm may hold spaces and parentheses.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, fmt.Errorf("procinfo: bad stat for %d", pid)
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0, fmt.Errorf("procinfo: bad stat for %d", pid)
	}
	var ppid int
	_, err = fmt.Sscan(f[1], &ppid)
	return ppid, err
}

// TTY returns the device of pid's controlling terminal, or -1 if it has none.
func TTY(pid int) int64 {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return -1
	}
	f := strings.Fields(s[i+1:])
	var tty int64
	if len(f) < 5 || func() bool { _, err := fmt.Sscan(f[4], &tty); return err != nil }() || tty == 0 {
		return -1
	}
	return tty
}

// Groups returns pid's process group and the foreground process group of
// its controlling terminal (0 when it has none).
func Groups(pid int) (pgid, tpgid int, err error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, err
	}
	// After the command name: state ppid pgrp session tty_nr tpgid ...
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, 0, fmt.Errorf("procinfo: bad stat for %d", pid)
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 6 {
		return 0, 0, fmt.Errorf("procinfo: bad stat for %d", pid)
	}
	if _, err := fmt.Sscan(f[2], &pgid); err != nil {
		return 0, 0, err
	}
	if _, err := fmt.Sscan(f[5], &tpgid); err != nil || tpgid < 0 {
		tpgid = 0
	}
	return pgid, tpgid, nil
}

// Env returns pid's environment as it was when it started.
func Env(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil
	}
	var out []string
	for _, kv := range strings.Split(string(b), "\x00") {
		if kv != "" {
			out = append(out, kv)
		}
	}
	return out
}

// Args returns pid's arguments (argv).
func Args(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}

// StartTime returns when pid started: its start in clock ticks after boot
// (/proc/<pid>/stat, field 22), taken back from now by how long ago that
// was (/proc/uptime, to a hundredth of a second). Not from /proc/stat's
// btime: that's whole seconds, and could put a start up to a second early.
func StartTime(pid int) (time.Time, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return time.Time{}, err
	}
	// The command name (field 2) can hold spaces: count from after it.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return time.Time{}, errors.New("procinfo: bad stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return time.Time{}, errors.New("procinfo: bad stat")
	}
	ticks, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	now := time.Now()
	up, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return time.Time{}, err
	}
	fs := strings.Fields(string(up))
	if len(fs) == 0 {
		return time.Time{}, errors.New("procinfo: bad uptime")
	}
	secs, err := strconv.ParseFloat(fs[0], 64)
	if err != nil {
		return time.Time{}, err
	}
	const hz = 100 // USER_HZ
	since := time.Duration(secs*float64(time.Second)) - time.Duration(ticks)*time.Second/hz
	return now.Add(-since).Round(10 * time.Millisecond), nil
}
