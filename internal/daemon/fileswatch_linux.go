package daemon

import (
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// inotify watches each folder the pane shows (it isn't recursive), and the
// repository's .git folder and its branches.
type inotifyWatcher struct {
	dirSet
	f    *os.File
	fd   int
	on   func([]string, bool)
	wmu  sync.Mutex
	byWD map[int32]string
	wds  map[string]int32
}

const inotifyMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_MODIFY | unix.IN_ATTRIB | unix.IN_CLOSE_WRITE | unix.IN_DELETE_SELF | unix.IN_ONLYDIR

func newDirWatcher(root, gitDir string, on func([]string, bool)) (dirWatcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	w := &inotifyWatcher{dirSet: dirSet{root: root, gitDir: gitDir}, fd: fd, on: on,
		f: os.NewFile(uintptr(fd), "inotify"), byWD: map[int32]string{}, wds: map[string]int32{}}
	if gitDir != "" {
		w.add(gitDir)
		w.add(filepath.Join(gitDir, "refs", "heads"))
	}
	go w.read()
	return w, nil
}

func (w *inotifyWatcher) add(dir string) {
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if _, ok := w.wds[dir]; ok {
		return
	}
	wd, err := unix.InotifyAddWatch(w.fd, dir, inotifyMask)
	if err != nil {
		return
	}
	w.wds[dir], w.byWD[int32(wd)] = int32(wd), dir
}

func (w *inotifyWatcher) set(dirs []string) {
	w.dirSet.set(dirs)
	keep := map[string]bool{}
	for _, d := range dirs {
		keep[d] = true
		w.add(d)
	}
	if w.gitDir != "" {
		keep[w.gitDir], keep[filepath.Join(w.gitDir, "refs", "heads")] = true, true
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	for d, wd := range w.wds {
		if !keep[d] {
			_, _ = unix.InotifyRmWatch(w.fd, uint32(wd))
			delete(w.wds, d)
			delete(w.byWD, wd)
		}
	}
}

func (w *inotifyWatcher) close() { _ = w.f.Close() }

func (w *inotifyWatcher) read() {
	buf := make([]byte, 64*1024)
	for {
		n, err := w.f.Read(buf)
		if err != nil {
			return
		}
		var dirs []string
		git := false
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			off += unix.SizeofInotifyEvent + int(ev.Len)
			w.wmu.Lock()
			dir, ok := w.byWD[ev.Wd]
			w.wmu.Unlock()
			if !ok {
				continue
			}
			sub := ev.Mask&unix.IN_Q_OVERFLOW != 0
			ds, g := w.match(dir, sub)
			dirs, git = append(dirs, ds...), git || g
		}
		if len(dirs) > 0 || git {
			w.on(dirs, git)
		}
	}
}
