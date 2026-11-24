package daemon

// Files triggers (schedtriggers.go): a task's folder (or another) is looked
// over every few seconds for files matching its glob that are new, changed
// or gone. Changes are gathered until a look finds no more (they've
// settled: a save, a checkout, a build), then fire once with the list.
//
// A folder in a git repository is listed as git lists it (tracked files,
// and untracked ones .gitignore doesn't leave out); elsewhere it's walked,
// leaving out .git, node_modules and the usual build folders. Each folder
// is listed once a look, however many triggers watch it, and only the
// files a glob matches are looked at (and counted towards maxWatched).
//
// While an automation's run is going in or around the folder (its own, or
// any other's: so two can't start each other in a loop), what changes is
// taken as what's there, not news; changes gathered before it started
// still fire once it's over.

import (
	"bytes"
	"context"
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
// many matching files one trigger may watch.
var (
	filesEvery = 5 * time.Second
	maxWatched = 50000
)

// skipDirs are folders a walk (outside git) leaves out: version control,
// dependencies and build output.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, ".build": true, "build": true, "dist": true,
	"target": true, "DerivedData": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true,
	".gradle": true, "Pods": true, ".cache": true, ".terraform": true,
}

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
	quiet             bool // an automation's run is going in or around dir
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

// dirsOverlap: a and b are the same folder, or one is inside the other.
func dirsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

// filesJobs are the files triggers to look at now. Callers hold d.mu.
func (d *Daemon) filesJobs() []filesJob {
	now := schedNow()
	// Where automations' runs are going (or just ended: their last writes
	// may not have been looked at yet).
	var busy []string
	for _, s := range d.sched.list {
		for _, r := range s.Runs {
			if runActive(r) || r.EndedUnixMs > 0 && now.Sub(time.UnixMilli(r.EndedUnixMs)) < 2*filesEvery {
				busy = append(busy, s.Cwd)
				break
			}
		}
	}
	var jobs []filesJob
	for _, s := range d.sched.list {
		for _, t := range d.eventsOf(s) {
			if f := t.GetFiles(); f != nil {
				dir := cmpOr(f.Folder, s.Cwd)
				quiet := false
				for _, b := range busy {
					if dirsOverlap(b, dir) {
						quiet = true
					}
				}
				jobs = append(jobs, filesJob{schedule: s.Id, trigger: t.Id, dir: dir, glob: f.Glob, quiet: quiet})
			}
		}
	}
	return jobs
}

// lookAtFiles has one look for every files trigger.
func (d *Daemon) lookAtFiles(watches map[string]*filesWatch) {
	d.mu.Lock()
	jobs := d.filesJobs()
	d.mu.Unlock()
	listed := map[string][]string{} // folder → its files (relative, slash-separated)
	listErr := map[string]error{}
	live := map[string]bool{}
	for _, j := range jobs {
		live[j.trigger] = true
		if _, ok := listed[j.dir]; !ok && listErr[j.dir] == nil {
			listed[j.dir], listErr[j.dir] = listFolder(j.dir)
		}
		re, err := schedtrig.GlobRegexp(cmpOr(j.glob, "**"))
		if err != nil {
			continue
		}
		var files map[string]watchStamp
		if err = listErr[j.dir]; err == nil {
			files, err = stampMatching(j.dir, listed[j.dir], re)
		}
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
		key := j.dir + "\x00" + j.glob
		w := watches[j.trigger]
		if w == nil || w.key != key {
			watches[j.trigger] = &filesWatch{key: key, files: files, changed: map[string]string{}} // the first look
			continue
		}
		fresh := diffFiles(w.files, files)
		w.files = files
		if j.quiet {
			continue // a run's changes: what's there now (what was gathered before it waits)
		}
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

// filesEvent is the event for changed files (path → what happened). Its
// summary says how many, not their names (they're anyone's).
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
	if len(paths) != 1 {
		summary = fmt.Sprintf("%d files changed", len(paths))
	}
	return &schedEvent{Trigger: trigger, Kind: "files", Summary: summary, Source: "file names in " + dir,
		Fields: [][2]string{{"folder", dir}, {"count", fmt.Sprint(len(paths))}}, Body: b.String()}
}

// gitList lists dir's files as git does, relative to it ("" and false
// outside a work tree). Tests replace it.
var gitList = func(dir string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := gitRun(ctx, dir, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, false
	}
	out, err := gitRun(ctx, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, false
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files, true
}

// listFolder is dir's files (relative, slash-separated): git's list in a
// repository, else a walk that leaves out skipDirs.
func listFolder(dir string) ([]string, error) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("there's no folder %s", dir)
	}
	if files, ok := gitList(dir); ok {
		return files, nil
	}
	var out []string
	_ = filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil // (unreadable: left out)
		}
		if e.IsDir() {
			if path != dir && skipDirs[e.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(dir, path); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out, nil
}

// stampMatching notes the files of list that re matches (gone ones left
// out), at most maxWatched.
func stampMatching(dir string, list []string, re *regexp.Regexp) (map[string]watchStamp, error) {
	out := map[string]watchStamp{}
	for _, rel := range list {
		if !re.MatchString(rel) {
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || info.IsDir() {
			continue
		}
		if len(out) >= maxWatched {
			return nil, fmt.Errorf("more than %d files in %s match: narrow the glob, or watch a folder inside it", maxWatched, dir)
		}
		out[rel] = watchStamp{info.ModTime().UnixNano(), info.Size()}
	}
	return out, nil
}

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
