package daemon

// Files triggers (schedtriggers.go): a task's folder (or another) is looked
// over every few seconds for files matching its glob that are new, changed
// or gone. Changes are gathered until a look finds no more (they've
// settled: a save, a checkout, a build), then fire once with the list.
// .git and node_modules aren't looked in. While one of the task's own runs
// is going its changes are taken as what's there, not news: a run editing
// files doesn't start another.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/schedtrig"
)

// filesEvery is how often watched folders are looked over; maxWatched how
// many files one trigger may watch.
var (
	filesEvery = 5 * time.Second
	maxWatched = 50000
)

// watchStamp is what a look notes of a file.
type watchStamp struct {
	mod  int64
	size int64
}

// filesWatch is one files trigger's watching, kept between looks.
type filesWatch struct {
	key     string // what it watches (folder and glob): a change starts afresh
	files   map[string]watchStamp
	changed map[string]string // path → "added", "changed" or "removed", not yet fired
}

// filesJob is one trigger, for a look.
type filesJob struct {
	schedule, trigger string
	dir, glob         string
	running           bool // one of the task's runs is going
}

// watchFiles looks over the folders files triggers watch, until the
// daemon stops.
func (d *Daemon) watchFiles() {
	wake := make(chan struct{}, 1)
	d.mu.Lock()
	d.sched.filesWake = wake
	d.mu.Unlock()
	watches := map[string]*filesWatch{}
	t := time.NewTicker(filesEvery)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		case <-wake:
		}
		d.lookAtFiles(watches)
	}
}

// lookAtFiles has one look for every files trigger.
func (d *Daemon) lookAtFiles(watches map[string]*filesWatch) {
	d.mu.Lock()
	var jobs []filesJob
	for _, s := range d.sched.list {
		// A run going, or just ended (its last writes may not have been
		// looked at yet).
		running := false
		for _, r := range s.Runs {
			if runActive(r) || r.EndedUnixMs > 0 && schedNow().Sub(time.UnixMilli(r.EndedUnixMs)) < 2*filesEvery {
				running = true
			}
		}
		for _, t := range d.eventsOf(s) {
			if f := t.GetFiles(); f != nil {
				jobs = append(jobs, filesJob{schedule: s.Id, trigger: t.Id, dir: cmpOr(f.Folder, s.Cwd), glob: f.Glob, running: running})
			}
		}
	}
	d.mu.Unlock()
	live := map[string]bool{}
	for _, j := range jobs {
		live[j.trigger] = true
		w := watches[j.trigger]
		key := j.dir + "\x00" + j.glob
		re, err := schedtrig.GlobRegexp(cmpOr(j.glob, "**"))
		if err != nil {
			continue
		}
		files, err := scanWatched(j.dir, re)
		d.mu.Lock()
		if s := d.sched.list[j.schedule]; s != nil {
			for _, t := range s.Events {
				if t.Id == j.trigger {
					msg := ""
					if err != nil {
						msg = err.Error()
					}
					d.setTriggerError(s, t, msg)
				}
			}
		}
		d.mu.Unlock()
		if err != nil {
			delete(watches, j.trigger)
			continue
		}
		if w == nil || w.key != key || j.running {
			// The first look, or the task's run is changing things: what's
			// there is what's there.
			watches[j.trigger] = &filesWatch{key: key, files: files, changed: map[string]string{}}
			continue
		}
		fresh := diffFiles(w.files, files)
		w.files = files
		for p, what := range fresh {
			if old, ok := w.changed[p]; ok && old == "added" && what == "changed" {
				continue // still new
			}
			w.changed[p] = what
		}
		if len(fresh) > 0 || len(w.changed) == 0 {
			continue // still changing (or nothing has)
		}
		changed := w.changed
		w.changed = map[string]string{}
		d.mu.Lock()
		if s := d.sched.list[j.schedule]; s != nil {
			for _, t := range d.eventsOf(s) {
				if t.Id == j.trigger {
					_, _, _ = d.fireEvent(s, filesEvent(t.Id, j.dir, changed))
				}
			}
		}
		d.mu.Unlock()
	}
	for id := range watches {
		if !live[id] {
			delete(watches, id)
		}
	}
}

// filesEvent is the event for changed files (path → what happened).
func filesEvent(trigger, dir string, changed map[string]string) *schedEvent {
	var paths []string
	for p := range changed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	for i, p := range paths {
		if i == 200 {
			fmt.Fprintf(&b, "… and %d more\n", len(paths)-i)
			break
		}
		fmt.Fprintf(&b, "%s %s\n", changed[p], p)
	}
	summary := "1 file changed"
	if len(paths) == 1 {
		summary = filepath.Base(paths[0]) + " changed"
	} else {
		summary = fmt.Sprintf("%d files changed", len(paths))
	}
	return &schedEvent{Trigger: trigger, Kind: "files", Summary: summary, Source: "file names in " + dir,
		Fields: [][2]string{{"folder", dir}, {"count", fmt.Sprint(len(paths))}}, Body: b.String()}
}

// scanWatched notes the files under dir (relative, slash-separated) that re
// matches, leaving out .git and node_modules.
func scanWatched(dir string, re *regexp.Regexp) (map[string]watchStamp, error) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("there's no folder %s", dir)
	}
	out := map[string]watchStamp{}
	seen := 0
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil // (unreadable: left out)
		}
		if e.IsDir() {
			if n := e.Name(); path != dir && (n == ".git" || n == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if seen++; seen > maxWatched {
			return errTooMany
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !re.MatchString(rel) {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return nil
		}
		out[rel] = watchStamp{info.ModTime().UnixNano(), info.Size()}
		return nil
	})
	if err == errTooMany {
		return nil, fmt.Errorf("%s has more than %d files: watch a folder inside it", dir, maxWatched)
	}
	return out, nil
}

var errTooMany = fmt.Errorf("too many files")

// diffFiles is what changed from a to b: path → "added", "changed" or "removed".
func diffFiles(a, b map[string]watchStamp) map[string]string {
	out := map[string]string{}
	for p, st := range b {
		if old, ok := a[p]; !ok {
			out[p] = "added"
		} else if old != st {
			out[p] = "changed"
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			out[p] = "removed"
		}
	}
	return out
}
