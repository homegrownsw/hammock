//go:build darwin || linux

package daemon

import (
	"os"
	"syscall"
)

// deviceOf is the device number of the device file at p, as procinfo.TTY
// gives a controlling terminal's.
func deviceOf(p string) (int64, bool) {
	fi, err := os.Stat(p)
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int64(uint32(st.Rdev)), true
}
