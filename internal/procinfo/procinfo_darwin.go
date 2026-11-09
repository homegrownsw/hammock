// Package procinfo reads information about other processes (working
// directory, name) for cwd tracking and agent heuristics.
package procinfo

/*
#include <libproc.h>
#include <string.h>
#include <sys/proc_info.h>

static int pid_start(int pid, long long *sec, int *usec) {
	struct proc_bsdinfo bi;
	int n = proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &bi, sizeof(bi));
	if (n != sizeof(bi)) return -1;
	*sec = (long long)bi.pbi_start_tvsec;
	*usec = (int)bi.pbi_start_tvusec;
	return 0;
}

static int pid_cwd(int pid, char *buf, int len) {
	struct proc_vnodepathinfo vpi;
	int n = proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, &vpi, sizeof(vpi));
	if (n != sizeof(vpi)) return -1;
	strlcpy(buf, vpi.pvi_cdir.vip_path, len);
	return 0;
}
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Path returns the path of pid's executable (links resolved, as the
// kernel loaded it).
func Path(pid int) (string, error) {
	buf := make([]byte, C.PROC_PIDPATHINFO_MAXSIZE)
	n := C.proc_pidpath(C.int(pid), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf)))
	if n <= 0 {
		return "", errors.New("procinfo: path unavailable")
	}
	return string(buf[:n]), nil
}

// Cwd returns the current working directory of pid.
func Cwd(pid int) (string, error) {
	buf := make([]byte, C.MAXPATHLEN)
	if C.pid_cwd(C.int(pid), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))) != 0 {
		return "", errors.New("procinfo: cwd unavailable")
	}
	return C.GoString((*C.char)(unsafe.Pointer(&buf[0]))), nil
}

// Children returns the direct child process ids of pid.
func Children(pid int) []int {
	n := C.proc_listchildpids(C.pid_t(pid), nil, 0)
	if n <= 0 {
		return nil
	}
	buf := make([]C.int, int(n)+16)
	n = C.proc_listchildpids(C.pid_t(pid), unsafe.Pointer(&buf[0]), C.int(len(buf)*4))
	var out []int
	for i := 0; i < int(n) && i < len(buf); i++ {
		if buf[i] > 0 {
			out = append(out, int(buf[i]))
		}
	}
	return out
}

// Name returns the short command name of pid.
func Name(pid int) (string, error) {
	buf := make([]byte, 2*C.MAXCOMLEN+1)
	n := C.proc_name(C.int(pid), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf)))
	if n > 0 {
		return string(buf[:n]), nil
	}
	// Another user's process (a setuid one, like top) refuses proc_name;
	// the kernel's short name is there for anyone.
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k.Proc.P_pid != int32(pid) {
		return "", errors.New("procinfo: name unavailable")
	}
	comm := k.Proc.P_comm[:]
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}
	if len(comm) == 0 {
		return "", errors.New("procinfo: name unavailable")
	}
	return string(comm), nil
}

// Parent returns pid's parent process id.
func Parent(pid int) (int, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	if k.Proc.P_pid != int32(pid) {
		return 0, errors.New("procinfo: no such process")
	}
	return int(k.Eproc.Ppid), nil
}

// TTY returns the device of pid's controlling terminal, or -1 if it has none.
func TTY(pid int) int64 {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k.Proc.P_pid != int32(pid) || k.Eproc.Tdev == -1 {
		return -1
	}
	return int64(uint32(k.Eproc.Tdev))
}

// Groups returns pid's process group and the foreground process group of
// its controlling terminal (0 when it has none).
func Groups(pid int) (pgid, tpgid int, err error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, 0, err
	}
	if k.Proc.P_pid != int32(pid) {
		return 0, 0, errors.New("procinfo: no such process")
	}
	tpgid = int(k.Eproc.Tpgid)
	if k.Eproc.Tdev == -1 || tpgid < 0 {
		tpgid = 0
	}
	return int(k.Eproc.Pgid), tpgid, nil
}

// Env returns pid's environment as it was when it started (a process of
// the same user only).
func Env(pid int) []string {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return nil
	}
	// argc, the executable path, padding, argv[argc], then the environment.
	argc := int(binary.LittleEndian.Uint32(b))
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return nil
	}
	b = b[i:]
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	var out []string
	for n := 0; len(b) > 0; n++ {
		j := bytes.IndexByte(b, 0)
		if j <= 0 {
			break
		}
		if n >= argc {
			out = append(out, string(b[:j]))
		}
		b = b[j+1:]
	}
	return out
}

// Args returns pid's arguments (argv) as it was started (a process of the
// same user only).
func Args(pid int) []string {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(b))
	b = b[4:]
	i := bytes.IndexByte(b, 0) // past the executable path and its padding
	if i < 0 {
		return nil
	}
	b = b[i:]
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	var out []string
	for len(out) < argc && len(b) > 0 {
		j := bytes.IndexByte(b, 0)
		if j < 0 {
			break
		}
		out = append(out, string(b[:j]))
		b = b[j+1:]
	}
	return out
}

// StartTime returns when pid started.
func StartTime(pid int) (time.Time, error) {
	var sec C.longlong
	var usec C.int
	if C.pid_start(C.int(pid), &sec, &usec) != 0 {
		return time.Time{}, errors.New("procinfo: start time unavailable")
	}
	return time.Unix(int64(sec), int64(usec)*1000), nil
}
