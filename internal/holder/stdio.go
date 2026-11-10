//go:build darwin || linux

package holder

import "golang.org/x/sys/unix"

// sanitiseStdio makes stdin, stdout and stderr safe to keep in hmkd: each is
// left as it is if it's what a program is started with (a terminal or other
// device, a pipe, a socket, or a log file), and otherwise is replaced with
// /dev/null: a closed one (so the next file hmkd opens doesn't become its
// stdout), a folder, or a file through which a flock was taken (locked, by
// locked's word), since that is how a program that once locked a state dir
// would hand hmkd the descriptors (statelock.go). hmkd is started with
// /dev/null (launchd, the app), the journal (systemd) or its log file (hmk),
// none of them locked.
func sanitiseStdio(locked func(fd int) bool) {
	null := -1
	for fd := 0; fd <= 2; fd++ {
		var st unix.Stat_t
		keep := false
		if unix.Fstat(fd, &st) == nil {
			switch st.Mode & unix.S_IFMT {
			case unix.S_IFCHR, unix.S_IFIFO, unix.S_IFSOCK:
				keep = true
			case unix.S_IFREG:
				keep = !locked(fd)
			}
		}
		if keep {
			continue
		}
		if null < 0 {
			// A closed descriptor is the lowest free one: open takes it.
			n, err := unix.Open("/dev/null", unix.O_RDWR, 0)
			if err != nil {
				// Without /dev/null, at least don't keep what was there.
				unix.Close(fd)
				continue
			}
			null = n
		}
		if null != fd {
			_ = dupTo(null, fd)
		}
	}
	if null > 2 {
		unix.Close(null)
	}
}
