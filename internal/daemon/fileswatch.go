package daemon

import (
	"path/filepath"
	"strings"
	"sync"
)

// dirWatcher tells a files pane which of the folders it shows changed on
// disk: entries added, removed, renamed or written. Each platform's
// (fileswatch_*.go) is made by newDirWatcher(root, gitDir, on): root is the
// pane's folder; gitDir its repository's .git folder ("" none), whose
// changes (a commit, staging) are reported as git; on is called from the
// watcher's own goroutine.
type dirWatcher interface {
	set(dirs []string) // the folders to report on (all under root)
	close()
}

// dirSet is the folders a watcher reports on, and what it makes of the
// paths its platform says changed.
type dirSet struct {
	mu     sync.Mutex
	dirs   map[string]bool
	root   string
	gitDir string
}

func (s *dirSet) set(dirs []string) {
	m := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		m[d] = true
	}
	s.mu.Lock()
	s.dirs = m
	s.mu.Unlock()
}

// match is which watched folders a change at dir means, and whether git's
// status may have changed: anything in the repository's .git folder, or a
// folder not shown (whose change can colour the ones that are). sub:
// everything under dir may have changed (the platform dropped events).
func (s *dirSet) match(dir string, sub bool) (out []string, git bool) {
	dir = filepath.Clean(dir)
	if s.gitDir != "" && (dir == s.gitDir || strings.HasPrefix(dir, s.gitDir+"/")) {
		return nil, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirs[dir] {
		out = append(out, dir)
	}
	if sub {
		for d := range s.dirs {
			if strings.HasPrefix(d, dir+"/") {
				out = append(out, d)
			}
		}
	}
	return out, len(out) == 0
}
