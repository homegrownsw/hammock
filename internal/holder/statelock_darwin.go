package holder

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <unistd.h>

// FHASLOCK (sys/fcntl.h, kernel-only): a flock was taken through this
// open file. proc_pidfdinfo reports the file's flags as fi_openflags.
#define HMK_FHASLOCK 0x4000

static int hmk_fds(int pid, struct proc_fdinfo *buf, int size) {
	return proc_pidinfo(pid, PROC_PIDLISTFDS, 0, buf, size);
}

// hmk_vnode: the device and inode of pid's descriptor fd, if it's a file,
// and whether a flock was taken through it. 0 on success.
static int hmk_vnode(int pid, int fd, unsigned long long *dev, unsigned long long *ino, int *locked) {
	struct vnode_fdinfo vi;
	if (proc_pidfdinfo(pid, fd, PROC_PIDFDVNODEINFO, &vi, sizeof(vi)) != sizeof(vi)) return -1;
	*dev = (unsigned long long)(unsigned int)vi.pvi.vi_stat.vst_dev;
	*ino = vi.pvi.vi_stat.vst_ino;
	*locked = (vi.pfi.fi_openflags & HMK_FHASLOCK) != 0;
	return 0;
}
*/
import "C"

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// fdList is pid's descriptors.
func fdList(pid int) ([]C.struct_proc_fdinfo, error) {
	size := C.hmk_fds(C.int(pid), nil, 0)
	if size <= 0 {
		return nil, fmt.Errorf("pid %d: no descriptors to list", pid)
	}
	for {
		buf := make([]C.struct_proc_fdinfo, int(size)/C.sizeof_struct_proc_fdinfo+16)
		n := C.hmk_fds(C.int(pid), &buf[0], C.int(len(buf)*C.sizeof_struct_proc_fdinfo))
		if n <= 0 {
			return nil, fmt.Errorf("pid %d: no descriptors to list", pid)
		}
		if int(n) < len(buf)*C.sizeof_struct_proc_fdinfo {
			return buf[:int(n)/C.sizeof_struct_proc_fdinfo], nil
		}
		size = n * 2 // (more opened meanwhile)
	}
}

// lockedFiles is the files pid has a descriptor on that a flock was taken
// through.
func lockedFiles(pid int) (map[fileID]bool, error) {
	fds, err := fdList(pid)
	if err != nil {
		return nil, err
	}
	out := map[fileID]bool{}
	for _, f := range fds {
		// Not stdin, stdout or stderr: the daemon's locks are never
		// there (CloseInherited keeps 0-2 open, so lockDir's descriptors
		// come after them), and a program that starts hmkd chooses them.
		if f.proc_fd <= 2 || f.proc_fdtype != C.PROX_FDTYPE_VNODE {
			continue
		}
		var dev, ino C.ulonglong
		var locked C.int
		if C.hmk_vnode(C.int(pid), f.proc_fd, &dev, &ino, &locked) == 0 && locked != 0 {
			out[fileID{uint64(uint32(dev)), uint64(ino)}] = true
		}
	}
	return out, nil
}

// CloseInherited closes every descriptor above stderr this process has: for
// hmkd, first thing, so nothing it was started with (a descriptor of a
// state dir's lock, locked by whoever started it) is still open in it when
// it attaches to holders (statelock.go). Only the Go runtime's own kqueue
// is kept, should it have one yet. Stdin, stdout and stderr stay, unless
// one is closed (then /dev/null, so nothing hmkd opens lands there) or
// isn't something stdio is (sanitiseStdio).
func CloseInherited() {
	sanitiseStdio(func(fd int) bool {
		var dev, ino C.ulonglong
		var locked C.int
		return C.hmk_vnode(C.int(os.Getpid()), C.int(fd), &dev, &ino, &locked) == 0 && locked != 0
	})
	fds, err := fdList(os.Getpid())
	if err != nil {
		return
	}
	for _, f := range fds {
		if f.proc_fd > 2 && f.proc_fdtype != C.PROX_FDTYPE_KQUEUE {
			C.close(f.proc_fd)
		}
	}
}

func dupTo(old, fd int) error { return unix.Dup2(old, fd) }
