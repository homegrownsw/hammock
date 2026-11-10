package holder

import (
	"bytes"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// lockedFiles is the files pid has a descriptor on that a flock was taken
// through: a "lock:" line in the descriptor's fdinfo names a lock taken
// through that open file.
func lockedFiles(pid int) (map[fileID]bool, error) {
	base := "/proc/" + strconv.Itoa(pid)
	ents, err := os.ReadDir(base + "/fd")
	if err != nil {
		return nil, err
	}
	out := map[fileID]bool{}
	for _, e := range ents {
		info, err := os.ReadFile(base + "/fdinfo/" + e.Name())
		if err != nil || !flocked(info) {
			continue
		}
		var st unix.Stat_t
		if unix.Stat(base+"/fd/"+e.Name(), &st) == nil {
			out[idOf(&st)] = true
		}
	}
	return out, nil
}

// flocked reports whether fdinfo names a flock taken through its file.
func flocked(fdinfo []byte) bool {
	for _, l := range bytes.Split(fdinfo, []byte("\n")) {
		if rest, ok := bytes.CutPrefix(l, []byte("lock:")); ok && bytes.Contains(rest, []byte("FLOCK")) {
			return true
		}
	}
	return false
}

// CloseInherited closes every descriptor above stderr this process has: for
// hmkd, first thing, so nothing it was started with (a descriptor of a
// state dir's lock, locked by whoever started it) is still open in it when
// it attaches to holders (statelock.go). The Go runtime's own (epoll,
// eventfd: anonymous inodes) are kept, should it have them yet. Read with
// raw calls: os.ReadDir would set up the runtime's poller.
func CloseInherited() {
	dir, err := unix.Open("/proc/self/fd", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	var fds []int
	buf := make([]byte, 8192)
	for {
		n, err := unix.ReadDirent(dir, buf)
		if err != nil || n <= 0 {
			break
		}
		var names []string
		_, _, names = unix.ParseDirent(buf[:n], -1, names)
		for _, name := range names {
			if fd, err := strconv.Atoi(name); err == nil && fd > 2 && fd != dir {
				fds = append(fds, fd)
			}
		}
	}
	unix.Close(dir)
	for _, fd := range fds {
		lb := make([]byte, 256)
		if n, err := unix.Readlink("/proc/self/fd/"+strconv.Itoa(fd), lb); err == nil && strings.HasPrefix(string(lb[:n]), "anon_inode:") {
			continue
		}
		unix.Close(fd)
	}
}
