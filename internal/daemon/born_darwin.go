package daemon

import (
	"os"
	"syscall"
	"time"
)

// fileBorn is when path was made (APFS and HFS+ keep it).
func fileBorn(path string) (time.Time, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec), true
}
