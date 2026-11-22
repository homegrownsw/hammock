package daemon

/*
#cgo LDFLAGS: -framework CoreServices -framework CoreFoundation
#include <stdint.h>
#include <stdlib.h>

void *hmk_files_start(uintptr_t handle, const char **paths, int n);
void hmk_files_stop(void *stream);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// FSEvents watches the pane's folder as a whole (it's recursive, and costs
// the same however many folders are open), reporting folders whose entries
// changed; the dirSet keeps the ones the pane shows.
type fsWatcher struct {
	dirSet
	handle uintptr
	stream unsafe.Pointer
	on     func([]string, bool)
}

var (
	fsWatchMu   sync.Mutex
	fsWatchers  = map[uintptr]*fsWatcher{}
	fsWatchNext uintptr
)

const (
	fsMustScanSubDirs = 0x00000001
	fsRootChanged     = 0x00000020
)

func newDirWatcher(root, gitDir string, on func([]string, bool)) (dirWatcher, error) {
	w := &fsWatcher{dirSet: dirSet{root: root, gitDir: gitDir}, on: on}
	paths := []string{root}
	if gitDir != "" && !insideFolder(gitDir, root) {
		paths = append(paths, gitDir)
	}
	fsWatchMu.Lock()
	fsWatchNext++
	w.handle = fsWatchNext
	fsWatchers[w.handle] = w
	fsWatchMu.Unlock()
	cs := make([]*C.char, len(paths))
	for i, p := range paths {
		cs[i] = C.CString(p)
	}
	w.stream = C.hmk_files_start(C.uintptr_t(w.handle), (**C.char)(unsafe.Pointer(&cs[0])), C.int(len(cs)))
	for _, c := range cs {
		C.free(unsafe.Pointer(c))
	}
	if w.stream == nil {
		fsWatchMu.Lock()
		delete(fsWatchers, w.handle)
		fsWatchMu.Unlock()
		return nil, errors.New("couldn't watch " + root)
	}
	return w, nil
}

func (w *fsWatcher) close() {
	fsWatchMu.Lock()
	delete(fsWatchers, w.handle) // anything still on its way is dropped
	fsWatchMu.Unlock()
	if w.stream != nil {
		C.hmk_files_stop(w.stream)
		w.stream = nil
	}
}

//export hmkFilesEvent
func hmkFilesEvent(handle C.uintptr_t, path *C.char, flags C.uint) {
	fsWatchMu.Lock()
	w := fsWatchers[uintptr(handle)]
	fsWatchMu.Unlock()
	if w == nil {
		return
	}
	dir := C.GoString(path)
	sub := flags&(fsMustScanSubDirs|fsRootChanged) != 0
	dirs, git := w.match(dir, sub)
	if len(dirs) > 0 || git {
		w.on(dirs, git)
	}
}
