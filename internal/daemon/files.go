package daemon

// Files panes (PANE_KIND_FILES): a folder's tree, like an editor's file
// explorer. The pane keeps what it shows (FilesInfo: its folder, the folders
// open, the selection), so it's the same on every client and after a
// restart; clients list a folder at a time (ListDir) as they open it. While a
// client is attached, the folders it can see are watched (fileswatch*.go),
// and it's told which changed (FilesChanged), their entries or their git
// status, to list them again.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/gitdiff"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// filesListMax is the most entries a folder's listing sends.
const filesListMax = 5000

// filesSettle is how long changes on disk are gathered before clients are
// told: a build or a checkout touches many files at once.
var filesSettle = 150 * time.Millisecond

type filesHub struct {
	d  *Daemon
	mu sync.Mutex
	st map[string]*filesState // by pane, while a client is attached
}

type filesState struct {
	paneID   string
	watchers map[*conn]bool
	w        dirWatcher
	root     string
	repo     string
	watched  []string // the folders the watcher has (sorted)
	pending  map[string]bool
	timer    *time.Timer
	gitDirty bool
	git      *gitStatus // as last worked out, nil before
}

func newFilesHub(d *Daemon) *filesHub {
	return &filesHub{d: d, st: map[string]*filesState{}}
}

func (h *filesHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, s := range h.st {
		s.stop()
		delete(h.st, id)
	}
}

func (s *filesState) stop() {
	if s.w != nil {
		s.w.close()
		s.w = nil
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// cleanRoot is folder made absolute, cleaned and with its links followed, so
// paths under it compare with git's (which follows them too).
func cleanRoot(folder string) string {
	folder = filepath.Clean(expandHome(folder))
	if r, err := filepath.EvalSymlinks(folder); err == nil {
		folder = r
	}
	return folder
}

// repoOf is the repository folder is in ("" none).
func repoOf(folder string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	top, err := gitdiff.TopLevel(ctx, folder)
	if err != nil {
		return ""
	}
	return top
}

// startFiles creates a files pane for tab t (not yet placed in its layout).
// Callers hold d.mu.
func (d *Daemon) startFiles(t *pb.Tab, spec *pb.PaneSpec) (*pane, error) {
	open := spec.Files
	info := &pb.FilesInfo{}
	root := expandHome(open.Root)
	if root != "" && !filepath.IsAbs(root) {
		base := spec.Cwd
		if base == "" {
			base = d.folderOf(t.GroupId)
		}
		if base == "" {
			return nil, fmt.Errorf("%s: relative to what? (an absolute path)", open.Root)
		}
		root = filepath.Join(base, root)
	}
	if root == "" {
		root = spec.Cwd
	}
	if root == "" {
		root = d.folderOf(t.GroupId)
		info.FollowGroup = true
	}
	if root == "" {
		root, _ = os.UserHomeDir()
	}
	root = cleanRoot(root)
	if !isDir(root) {
		return nil, fmt.Errorf("no folder at %s", root)
	}
	info.Root = root
	info.Repo = repoOf(root)
	id := newID("pn")
	pi := &pb.Pane{
		Id: id, TabId: t.Id, Kind: pb.PaneKind_PANE_KIND_FILES, Title: filepath.Base(root), Cwd: root,
		CreatedByPaneId: spec.CreatedByPaneId, Files: info,
	}
	if open.Reveal != "" {
		revealIn(pi.Files, resolveIn(open.Reveal, root))
	}
	p := newPane(d, pi)
	d.panes[id] = p
	d.persist(d.store.PutPane(pi))
	return p, nil
}

// resolveIn is path made absolute against base.
func resolveIn(path, base string) string {
	path = expandHome(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path)
}

// revealIn selects path in f, opening the folders down to it (and moving
// the root to its folder if it's outside it), to be scrolled to.
func revealIn(f *pb.FilesInfo, path string) {
	if !insideFolder(path, f.Root) || path == f.Root {
		if path != f.Root {
			f.Root, f.FollowGroup = filepath.Dir(path), false
			f.Repo = repoOf(f.Root)
		}
	}
	for dir := filepath.Dir(path); dir != f.Root && insideFolder(dir, f.Root); dir = filepath.Dir(dir) {
		if !slices.Contains(f.Expanded, dir) {
			f.Expanded = append(f.Expanded, dir)
		}
	}
	f.Selected = []string{path}
	f.Reveal = path
	f.RevealSeq++
}

// filesInfo is pane id's FilesInfo, a copy, and the pane. Callers hold d.mu.
func (d *Daemon) filesPane(id string) (*pane, *pb.FilesInfo, error) {
	p := d.panes[id]
	if p == nil {
		return nil, nil, fmt.Errorf("no pane %s", id)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info.Kind != pb.PaneKind_PANE_KIND_FILES {
		return nil, nil, fmt.Errorf("%s isn't a files pane", id)
	}
	f := proto.CloneOf(p.info.Files)
	if f == nil {
		f = &pb.FilesInfo{}
	}
	return p, f, nil
}

// setFiles keeps f as p's FilesInfo, publishing it if it changed, and
// moving its watcher along. Callers hold d.mu.
func (d *Daemon) setFiles(p *pane, f *pb.FilesInfo) {
	p.mu.Lock()
	same := proto.Equal(p.info.Files, f)
	p.info.Files = f
	if !same {
		p.info.Title, p.info.Cwd = filepath.Base(f.Root), f.Root
	}
	p.mu.Unlock()
	if same {
		return
	}
	d.paneChangedIfLive(p)
	d.files.rewatch(p.id, f)
}

// groupMoved moves the root of g's files panes that follow its folder,
// when it moves somewhere outside what they show (cd'ing into a folder
// they show leaves them be). Callers hold d.mu.
func (h *filesHub) groupMoved(g *pb.Group) {
	d := h.d
	folder := cleanRoot(g.Folder)
	for _, tid := range g.TabIds {
		t := d.tabs[tid]
		if t == nil {
			continue
		}
		for _, id := range tabPanes(t) {
			p, f, err := d.filesPane(id)
			if err != nil || !f.FollowGroup || insideFolder(folder, f.Root) || !isDir(folder) {
				continue
			}
			f.Root, f.Repo, f.Expanded, f.Selected, f.Error = folder, repoOf(folder), nil, nil, ""
			d.setFiles(p, f)
		}
	}
}

// command answers FilesCommand. limit refuses paths the sender may not
// browse (filesLimit).
func (h *filesHub) command(m *pb.FilesCommand, limit func(string) error) error {
	d := h.d
	d.mu.Lock()
	defer d.mu.Unlock()
	p, f, err := d.filesPane(m.PaneId)
	if err != nil {
		return err
	}
	inside := limit
	clean := func(paths []string) ([]string, error) {
		var out []string
		for _, x := range paths {
			x = resolveIn(x, f.Root)
			if err := inside(x); err != nil {
				return nil, err
			}
			out = append(out, x)
		}
		return out, nil
	}
	switch m.Action {
	case pb.FilesCommand_ACTION_SET_ROOT:
		if m.Path == "" {
			folder := d.folderOf(d.groupOfPane(m.PaneId))
			if folder == "" {
				return errors.New("this Group has no folder yet")
			}
			f.Root, f.FollowGroup = cleanRoot(folder), true
		} else {
			root := cleanRoot(resolveIn(m.Path, f.Root))
			if !isDir(root) {
				return fmt.Errorf("no folder at %s", root)
			}
			f.Root, f.FollowGroup = root, false
		}
		if err := inside(f.Root); err != nil {
			return err
		}
		f.Repo, f.Error = repoOf(f.Root), ""
		// Folders open under the new root stay open; the rest go.
		f.Expanded = slices.DeleteFunc(f.Expanded, func(x string) bool { return !insideFolder(x, f.Root) || x == f.Root })
		f.Selected = slices.DeleteFunc(f.Selected, func(x string) bool { return !insideFolder(x, f.Root) })
	case pb.FilesCommand_ACTION_EXPAND:
		paths, err := clean(m.Paths)
		if err != nil {
			return err
		}
		for _, x := range paths {
			if !slices.Contains(f.Expanded, x) && x != f.Root {
				f.Expanded = append(f.Expanded, x)
			}
		}
	case pb.FilesCommand_ACTION_COLLAPSE:
		paths, err := clean(m.Paths)
		if err != nil {
			return err
		}
		f.Expanded = slices.DeleteFunc(f.Expanded, func(x string) bool { return slices.Contains(paths, x) })
	case pb.FilesCommand_ACTION_SELECT:
		paths, err := clean(m.Paths)
		if err != nil {
			return err
		}
		f.Selected = paths
	case pb.FilesCommand_ACTION_SHOW_HIDDEN:
		f.ShowHidden = m.On
	case pb.FilesCommand_ACTION_REVEAL:
		if m.Path == "" {
			return errors.New("reveal what? (a path)")
		}
		path := resolveIn(m.Path, f.Root)
		if err := inside(path); err != nil {
			return err
		}
		if _, err := os.Lstat(path); err != nil {
			return fmt.Errorf("nothing at %s", path)
		}
		revealIn(f, path)
	default:
		return fmt.Errorf("unknown files command %v", m.Action)
	}
	sort.Strings(f.Expanded)
	d.setFiles(p, f)
	return nil
}

// list answers ListDir.
func (h *filesHub) list(m *pb.ListDir, limit func(string) error) (*pb.DirListing, error) {
	d := h.d
	d.mu.Lock()
	_, f, err := d.filesPane(m.PaneId)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	dir := f.Root
	if m.Path != "" {
		dir = resolveIn(m.Path, f.Root)
	}
	if err := limit(dir); err != nil {
		return nil, err
	}
	out := &pb.DirListing{Path: dir}
	ents, err := os.ReadDir(dir)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out.Error = "This folder is gone"
		case errors.Is(err, fs.ErrPermission):
			out.Error = "Hammock isn't allowed to look in this folder"
		default:
			out.Error = err.Error()
		}
		return out, nil
	}
	if len(ents) > filesListMax {
		ents, out.Truncated = ents[:filesListMax], true
	}
	for _, e := range ents {
		de := &pb.DirEntry{Name: e.Name(), Dir: e.IsDir()}
		if st, err := e.Info(); err == nil {
			de.Size, de.MtimeUnixMs = st.Size(), st.ModTime().UnixMilli()
			if st.Mode()&fs.ModeSymlink != 0 {
				de.Symlink = true
				if t, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && t.IsDir() {
					de.Dir = true
				}
			}
		}
		if de.Dir {
			de.Size = 0
		}
		out.Entries = append(out.Entries, de)
	}
	sort.SliceStable(out.Entries, func(i, j int) bool {
		a, b := out.Entries[i], out.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return naturalLess(a.Name, b.Name)
	})
	if f.Repo != "" || repoOf(dir) != "" {
		h.decorate(m.PaneId, dir, out.Entries)
	}
	return out, nil
}

// naturalLess orders names as Finder does: case aside, and numbers by value
// ("file2" before "file10").
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digits(a), digits(b)
		if da > 0 && db > 0 {
			na, nb := strings.TrimLeft(a[:da], "0"), strings.TrimLeft(b[:db], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[da:], b[db:]
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// decorate sets entries' git status and whether git ignores them.
func (h *filesHub) decorate(paneID, dir string, entries []*pb.DirEntry) {
	repo := repoOf(dir)
	if repo == "" {
		return
	}
	g := h.gitFor(paneID, repo)
	rel, err := filepath.Rel(repo, dir)
	if err != nil {
		return
	}
	if rel == "." {
		rel = ""
	}
	for _, e := range entries {
		e.Git = g.status(filepath.ToSlash(filepath.Join(rel, e.Name)), e.Dir)
	}
	ignored := checkIgnored(repo, rel, entries)
	for _, e := range entries {
		e.Ignored = ignored[e.Name]
	}
}

// gitFor is repo's git status for paneID's watcher, worked out again if
// it's out of date (or there's no watcher to keep it).
func (h *filesHub) gitFor(paneID, repo string) *gitStatus {
	h.mu.Lock()
	s := h.st[paneID]
	if s != nil && s.repo == repo && s.git != nil && !s.gitDirty {
		g := s.git
		h.mu.Unlock()
		return g
	}
	h.mu.Unlock()
	g := readGitStatus(repo)
	h.mu.Lock()
	if s := h.st[paneID]; s != nil && s.repo == repo {
		s.git, s.gitDirty = g, false
	}
	h.mu.Unlock()
	return g
}

// checkIgnored is which of dir's entries (rel to repo) git ignores: all of
// them in a folder it ignores.
func checkIgnored(repo, rel string, entries []*pb.DirEntry) map[string]bool {
	out := map[string]bool{}
	if len(entries) == 0 {
		return out
	}
	var in bytes.Buffer
	byPath := map[string]string{}
	if rel != "" {
		in.WriteString(filepath.ToSlash(rel) + "/\x00")
	}
	for _, e := range entries {
		p := filepath.ToSlash(filepath.Join(rel, e.Name))
		if e.Dir {
			p += "/"
		}
		byPath[p] = e.Name
		in.WriteString(p + "\x00")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "check-ignore", "-z", "--stdin")
	cmd.Stdin = &in
	o, _ := cmd.Output() // exits 1 when nothing is ignored
	for _, p := range strings.Split(string(o), "\x00") {
		if p == "" {
			continue
		}
		if rel != "" && p == filepath.ToSlash(rel)+"/" {
			for _, e := range entries {
				out[e.Name] = true
			}
			return out
		}
		if name, ok := byPath[p]; ok {
			out[name] = true
		} else if name, ok := byPath[strings.TrimSuffix(p, "/")]; ok {
			out[name] = true
		}
	}
	return out
}

// attach starts watching pane id for c.
func (h *filesHub) attach(c *conn, id string) {
	d := h.d
	d.mu.Lock()
	_, f, err := d.filesPane(id)
	d.mu.Unlock()
	if err != nil {
		return
	}
	h.mu.Lock()
	s := h.st[id]
	if s == nil {
		s = &filesState{paneID: id, watchers: map[*conn]bool{}, pending: map[string]bool{}}
		h.st[id] = s
	}
	s.watchers[c] = true
	h.mu.Unlock()
	h.rewatch(id, f)
}

func (h *filesHub) detach(c *conn, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.st[id]; s != nil {
		delete(s.watchers, c)
		if len(s.watchers) == 0 {
			s.stop()
			delete(h.st, id)
		}
	}
}

func (h *filesHub) paneClosed(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.st[id]; s != nil {
		s.stop()
		delete(h.st, id)
	}
}

// visibleDirs is the folders f shows: its root, and those open whose
// folders above (to the root) are all open too.
func visibleDirs(f *pb.FilesInfo) []string {
	open := map[string]bool{f.Root: true}
	for _, x := range f.Expanded {
		open[x] = true
	}
	out := []string{f.Root}
	for _, x := range f.Expanded {
		if !insideFolder(x, f.Root) || x == f.Root {
			continue
		}
		ok := true
		for up := filepath.Dir(x); up != f.Root; up = filepath.Dir(up) {
			if !open[up] || up == filepath.Dir(up) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

// rewatch points pane id's watcher (if a client is attached) at what f
// shows. Not under h.mu.
func (h *filesHub) rewatch(id string, f *pb.FilesInfo) {
	dirs := visibleDirs(f)
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.st[id]
	if s == nil {
		return
	}
	if s.root != f.Root || s.repo != f.Repo {
		s.stop()
		s.root, s.repo, s.watched, s.git = f.Root, f.Repo, nil, nil
	}
	if s.w == nil {
		w, err := newDirWatcher(f.Root, gitDirOf(f.Repo), func(dirs []string, git bool) { h.changed(id, s, dirs, git) })
		if err != nil {
			return
		}
		s.w = w
	}
	if !slices.Equal(s.watched, dirs) {
		s.watched = dirs
		s.w.set(dirs)
	}
}

// gitDirOf is repo's .git folder ("" none).
func gitDirOf(repo string) string {
	if repo == "" {
		return ""
	}
	g := filepath.Join(repo, ".git")
	if isDir(g) {
		return g
	}
	return "" // a worktree's .git is a file: its index is elsewhere, and edits show anyway
}

// changed gathers what s's watcher saw, to tell its clients once it settles.
func (h *filesHub) changed(id string, s *filesState, dirs []string, git bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.st[id] != s {
		return
	}
	for _, x := range dirs {
		s.pending[x] = true
	}
	if git || len(dirs) > 0 {
		s.gitDirty = true
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(filesSettle, func() { h.flush(id, s) })
	}
}

// flush tells s's clients which folders to list again: those that changed
// on disk, and those whose git status did.
func (h *filesHub) flush(id string, s *filesState) {
	h.mu.Lock()
	if h.st[id] != s {
		h.mu.Unlock()
		return
	}
	s.timer = nil
	changed := s.pending
	s.pending = map[string]bool{}
	repo, old, dirty, watched := s.repo, s.git, s.gitDirty, s.watched
	h.mu.Unlock()
	if repo != "" && dirty {
		g := readGitStatus(repo)
		for _, dir := range watched {
			if old == nil || !old.sameIn(g, repo, dir) {
				changed[dir] = true
			}
		}
		h.mu.Lock()
		if h.st[id] == s {
			s.git, s.gitDirty = g, false
		}
		h.mu.Unlock()
	}
	var paths []string
	for _, dir := range watched {
		if changed[dir] {
			paths = append(paths, dir)
		}
	}
	if len(paths) == 0 {
		return
	}
	msg := &pb.ServerMessage{Msg: &pb.ServerMessage_FilesChanged{FilesChanged: &pb.FilesChanged{PaneId: id, Paths: paths}}}
	h.mu.Lock()
	for c := range s.watchers {
		c.send(msg)
	}
	h.mu.Unlock()
}

// filesLimit is what c may browse in pane id: anything, unless it's a
// guest's daemon, which stays inside the Group's folder.
func (d *Daemon) filesLimit(c *conn, id string) func(string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c.peer == "" || d.isLinked(c.peer) {
		return func(string) error { return nil }
	}
	folder := d.folderOf(d.groupOfPane(id))
	return func(path string) error {
		if !insideFolder(path, folder) {
			return errors.New("guests browse only inside the Group's folder")
		}
		return nil
	}
}
