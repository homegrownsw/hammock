//go:build !darwin && !linux

package daemon

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Elsewhere, the folders shown are looked at every couple of seconds.
type pollWatcher struct {
	dirSet
	on   func([]string, bool)
	stop chan struct{}
}

func newDirWatcher(root, gitDir string, on func([]string, bool)) (dirWatcher, error) {
	w := &pollWatcher{dirSet: dirSet{root: root, gitDir: gitDir}, on: on, stop: make(chan struct{})}
	go w.run()
	return w, nil
}

func (w *pollWatcher) close() { close(w.stop) }

func (w *pollWatcher) run() {
	seen := map[string]string{}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
		}
		w.mu.Lock()
		var dirs []string
		for d := range w.dirs {
			dirs = append(dirs, d)
		}
		w.mu.Unlock()
		if w.gitDir != "" {
			dirs = append(dirs, w.gitDir)
		}
		var changed []string
		git := false
		for _, d := range dirs {
			sig := dirSignature(d)
			if old, ok := seen[d]; ok && old != sig {
				if d == w.gitDir {
					git = true
				} else {
					changed = append(changed, d)
				}
			}
			seen[d] = sig
		}
		if len(changed) > 0 || git {
			w.on(changed, git)
		}
	}
}

func dirSignature(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "!"
	}
	var b strings.Builder
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if st, err := os.Lstat(dir + "/" + n); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", n, st.Size(), st.ModTime().UnixNano())
		}
	}
	return b.String()
}
