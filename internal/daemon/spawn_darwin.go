package daemon

/*
#include <errno.h>
#include <fcntl.h>
#include <signal.h>
#include <spawn.h>
#include <stdlib.h>

// Private but stable (Terminal, iTerm2, Ghostty's helpers and LLDB use it):
// the spawned process is responsible for itself, not for its parent.
extern int responsibility_spawnattrs_setdisclaim(posix_spawnattr_t *attrs, int disclaim) __attribute__((weak_import));

#ifndef POSIX_SPAWN_SETSID
#define POSIX_SPAWN_SETSID 0x0400
#endif

// hmk_spawn starts path in its own session and process group, with the
// given working directory, stdout/stderr on out (or /dev/null when out < 0),
// extra on fd 3 (if extra >= 0) and nothing else open, default signal
// dispositions and an empty signal mask.
static int hmk_spawn(pid_t *pid, const char *path, char *const argv[], char *const envp[],
                     const char *dir, int out, int extra) {
	posix_spawnattr_t attr;
	posix_spawn_file_actions_t fa;
	int err = posix_spawnattr_init(&attr);
	if (err) return err;
	err = posix_spawn_file_actions_init(&fa);
	if (err) { posix_spawnattr_destroy(&attr); return err; }

	sigset_t none, all;
	sigemptyset(&none);
	sigfillset(&all);
	posix_spawnattr_setsigmask(&attr, &none);
	posix_spawnattr_setsigdefault(&attr, &all);
	err = posix_spawnattr_setflags(&attr, POSIX_SPAWN_SETSID | POSIX_SPAWN_CLOEXEC_DEFAULT |
	                                      POSIX_SPAWN_SETSIGMASK | POSIX_SPAWN_SETSIGDEF);
	if (!err && responsibility_spawnattrs_setdisclaim) err = responsibility_spawnattrs_setdisclaim(&attr, 1);

	if (!err) err = posix_spawn_file_actions_addopen(&fa, 0, "/dev/null", O_RDONLY, 0);
	if (!err && out >= 0) {
		err = posix_spawn_file_actions_adddup2(&fa, out, 1);
		if (!err) err = posix_spawn_file_actions_adddup2(&fa, out, 2);
	} else if (!err) {
		err = posix_spawn_file_actions_addopen(&fa, 1, "/dev/null", O_WRONLY, 0);
		if (!err) err = posix_spawn_file_actions_adddup2(&fa, 1, 2);
	}
	if (!err && extra >= 0) err = posix_spawn_file_actions_adddup2(&fa, extra, 3);
	if (!err && dir) err = posix_spawn_file_actions_addchdir(&fa, dir);

	if (!err) err = posix_spawn(pid, path, &fa, &attr, argv, envp);
	posix_spawn_file_actions_destroy(&fa);
	posix_spawnattr_destroy(&attr);
	return err;
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// spawnHolder starts a pane holder in its own session, with extra (if any)
// as its fd 3 and its output on out (if any).
//
// It disclaims responsibility: macOS otherwise holds the process that started
// a program (here, all the way up to hmkd, a bare launchd agent) responsible
// for what it does, and asks about or refuses its privacy requests on that
// one's behalf. hmkd can't be granted anything (it has no bundle of its own),
// so programs in panes were silently refused what needs a grant, such as
// reading another app's group container: 1Password's CLI couldn't reach the
// desktop app. Disclaimed, the holder (the Hammock Pane helper) answers for
// its pane, as a terminal app does for its windows.
// dedupEnv keeps the last of each variable, as exec.Cmd does (pane.env relies
// on it): a program given two sees the first.
func dedupEnv(env []string) []string {
	last := map[string]int{}
	for i, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		last[k] = i
	}
	out := make([]string, 0, len(last))
	for i, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); last[k] == i {
			out = append(out, kv)
		}
	}
	return out
}

func spawnHolder(path string, args, env []string, dir string, out, extra *os.File) (*os.Process, error) {
	cstr := func(ss []string) (**C.char, func()) {
		arr := C.malloc(C.size_t(len(ss)+1) * C.size_t(unsafe.Sizeof(uintptr(0))))
		view := unsafe.Slice((**C.char)(arr), len(ss)+1)
		for i, s := range ss {
			view[i] = C.CString(s)
		}
		view[len(ss)] = nil
		return (**C.char)(arr), func() {
			for _, p := range view[:len(ss)] {
				C.free(unsafe.Pointer(p))
			}
			C.free(arr)
		}
	}
	argv, freeArgv := cstr(append([]string{path}, args...))
	defer freeArgv()
	envp, freeEnv := cstr(dedupEnv(env))
	defer freeEnv()
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var cdir *C.char
	if dir != "" {
		cdir = C.CString(dir)
		defer C.free(unsafe.Pointer(cdir))
	}
	outFD, extraFD := C.int(-1), C.int(-1)
	if out != nil {
		outFD = C.int(out.Fd())
	}
	if extra != nil {
		extraFD = C.int(extra.Fd())
	}
	var pid C.pid_t
	if rc := C.hmk_spawn(&pid, cpath, argv, envp, cdir, outFD, extraFD); rc != 0 {
		return nil, fmt.Errorf("posix_spawn %s: %w", path, syscall.Errno(rc))
	}
	runtime.KeepAlive(out)
	runtime.KeepAlive(extra)
	return os.FindProcess(int(pid))
}
