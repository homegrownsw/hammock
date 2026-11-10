package daemon

import (
	"time"

	"golang.org/x/sys/unix"
)

// fileBorn is when path was made, where the filesystem keeps it (ext4,
// btrfs, xfs); else when it last changed (its ctime, which is no earlier).
func fileBorn(path string) (time.Time, bool) {
	var st unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME|unix.STATX_CTIME, &st) != nil {
		return time.Time{}, false
	}
	if st.Mask&unix.STATX_BTIME != 0 {
		return time.Unix(st.Btime.Sec, int64(st.Btime.Nsec)), true
	}
	return time.Unix(st.Ctime.Sec, int64(st.Ctime.Nsec)), true
}
