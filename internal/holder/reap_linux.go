package holder

import (
	"bytes"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// becomeSubreaper makes this process its descendants' reaper: a program
// that leaves its parents (a double fork, setsid, nohup) is reparented to
// the holder rather than init, so hmkd still finds it under the pane and
// scopes it as the pane's (an agent's double fork stays an agent's), not
// as a stranger it can't place.
func becomeSubreaper() {
	_ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

// reapOrphans waits for the children the holder adopted as they exit, so
// they don't linger as zombies. Never the command's own (main): exec.Cmd
// waits for that, and its exit status is the pane's.
func reapOrphans(main int, done <-chan struct{}) {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGCHLD)
	defer signal.Stop(ch)
	tick := time.NewTicker(time.Minute) // a signal coalesced or missed
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-ch:
		case <-tick.C:
		}
		for _, pid := range exitedChildren(main) {
			var ws unix.WaitStatus
			_, _ = unix.Wait4(pid, &ws, unix.WNOHANG, nil)
		}
	}
}

// exitedChildren are this process's zombie children other than main.
func exitedChildren(main int) []int {
	self := os.Getpid()
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == main || pid == self {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// pid (comm) state ppid …: comm can hold anything, so after its last ')'.
		i := bytes.LastIndexByte(b, ')')
		if i < 0 {
			continue
		}
		f := bytes.Fields(b[i+1:])
		if len(f) < 2 || string(f[0]) != "Z" {
			continue
		}
		if ppid, _ := strconv.Atoi(string(f[1])); ppid == self {
			out = append(out, pid)
		}
	}
	return out
}
