package daemon

import (
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var hidIdle = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)

var consoleLocked = regexp.MustCompile(`"IOConsoleLocked" = Yes`)

// hostScreenLocked: the login window is over the session (locked, or
// switched to another user).
func hostScreenLocked() bool {
	out, err := exec.Command("/usr/sbin/ioreg", "-n", "Root", "-d", "1", "-k", "IOConsoleLocked").Output()
	return err == nil && consoleLocked.Match(out)
}

// hostIdleTime is how long since the Mac's keyboard or mouse was touched.
func hostIdleTime() time.Duration {
	out, err := exec.Command("/usr/sbin/ioreg", "-c", "IOHIDSystem", "-d", "4", "-k", "HIDIdleTime").Output()
	if err != nil {
		return 0
	}
	m := hidIdle.FindSubmatch(out)
	if m == nil {
		return 0
	}
	ns, _ := strconv.ParseInt(string(m[1]), 10, 64)
	return time.Duration(ns)
}
