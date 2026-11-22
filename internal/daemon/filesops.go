package daemon

// Files panes changing files: renaming, creating, moving, copying and
// moving to the Trash (never deleting outright), each undoable from the
// pane. What changed is selected once it's done; the watcher shows it.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// filesUndoMax is how many changes a pane can undo.
const filesUndoMax = 20

// filesChange is one change a pane made, enough to undo it.
type filesChange struct {
	moves   [][2]string // renamed or moved, from → to
	made    []string    // created or copied: undone by trashing them
	trashed [][2]string // moved to the Trash, from → where it went there
}

func isFileOp(a pb.FilesCommand_Action) bool {
	switch a {
	case pb.FilesCommand_ACTION_RENAME, pb.FilesCommand_ACTION_CREATE_FILE, pb.FilesCommand_ACTION_CREATE_FOLDER,
		pb.FilesCommand_ACTION_TRASH, pb.FilesCommand_ACTION_MOVE, pb.FilesCommand_ACTION_COPY, pb.FilesCommand_ACTION_UNDO:
		return true
	}
	return false
}

// cleanName refuses names that aren't one entry's.
func cleanName(name string) (string, error) {
	n := strings.TrimSpace(name)
	switch {
	case n == "":
		return "", errors.New("a name is needed")
	case n == "." || n == "..":
		return "", fmt.Errorf("%q can't be a name", n)
	case strings.ContainsRune(n, '/') || strings.ContainsRune(n, 0):
		return "", errors.New("names can't have a / in them")
	}
	return n, nil
}

// exists reports whether something's at path (a broken link counts).
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// fileOp answers FilesCommand's changes. The disk is changed without d.mu.
func (h *filesHub) fileOp(m *pb.FilesCommand, limit func(string) error) error {
	d := h.d
	d.mu.Lock()
	_, f, err := d.filesPane(m.PaneId)
	d.mu.Unlock()
	if err != nil {
		return err
	}
	// Inside what the pane shows (and, for a guest, the Group's folder).
	check := func(path string) (string, error) {
		path = resolveIn(path, f.Root)
		if !insideFolder(path, f.Root) {
			return "", fmt.Errorf("%s isn't in this pane's folder", path)
		}
		return path, limit(path)
	}
	var ch filesChange
	var show []string
	switch m.Action {
	case pb.FilesCommand_ACTION_RENAME:
		from, err := check(m.Path)
		if err != nil {
			return err
		}
		if from == f.Root {
			return errors.New("rename the folder from the one above it")
		}
		name, err := cleanName(m.Name)
		if err != nil {
			return err
		}
		to := filepath.Join(filepath.Dir(from), name)
		if to == from {
			return nil
		}
		// A change of case only is the same file on a Mac's disk.
		if exists(to) && !sameFile(from, to) {
			return fmt.Errorf("there's already a %s here", name)
		}
		if err := os.Rename(from, to); err != nil {
			return opError("rename", err)
		}
		ch.moves = append(ch.moves, [2]string{from, to})
		show = []string{to}
	case pb.FilesCommand_ACTION_CREATE_FILE, pb.FilesCommand_ACTION_CREATE_FOLDER:
		dir, err := check(cmpOr(m.Path, f.Root))
		if err != nil {
			return err
		}
		name, err := cleanName(m.Name)
		if err != nil {
			return err
		}
		path := filepath.Join(dir, name)
		if exists(path) {
			return fmt.Errorf("there's already a %s here", name)
		}
		if m.Action == pb.FilesCommand_ACTION_CREATE_FOLDER {
			err = os.Mkdir(path, 0o755)
		} else {
			var fh *os.File
			if fh, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
				err = fh.Close()
			}
		}
		if err != nil {
			return opError("create", err)
		}
		ch.made = []string{path}
		show = []string{path}
	case pb.FilesCommand_ACTION_TRASH:
		paths, err := h.opPaths(m.Paths, f.Root, check)
		if err != nil {
			return err
		}
		for _, p := range paths {
			where, err := moveToTrash(p)
			if err != nil {
				h.remember(m.PaneId, ch) // what went before it can still come back
				return fmt.Errorf("couldn't move %s to the Trash: %w", filepath.Base(p), err)
			}
			ch.trashed = append(ch.trashed, [2]string{p, where})
		}
	case pb.FilesCommand_ACTION_MOVE, pb.FilesCommand_ACTION_COPY:
		dest, err := check(cmpOr(m.Path, f.Root))
		if err != nil {
			return err
		}
		if st, err := os.Stat(dest); err != nil || !st.IsDir() {
			return fmt.Errorf("%s isn't a folder", dest)
		}
		// From anywhere (the Finder, another pane), as far as limit goes.
		from := func(p string) (string, error) {
			p = resolveIn(p, f.Root)
			return p, limit(p)
		}
		paths, err := h.opPaths(m.Paths, f.Root, from)
		if err != nil {
			return err
		}
		copying := m.Action == pb.FilesCommand_ACTION_COPY
		for _, p := range paths {
			if dest == p || insideFolder(dest, p) {
				return fmt.Errorf("%s can't go inside itself", filepath.Base(p))
			}
			to := filepath.Join(dest, filepath.Base(p))
			if !copying && to == p {
				continue // already there
			}
			if copying {
				to = freeName(to)
				if err := copyTree(p, to); err != nil {
					h.remember(m.PaneId, ch)
					return opError("copy", err)
				}
				ch.made = append(ch.made, to)
			} else {
				if exists(to) {
					h.remember(m.PaneId, ch)
					return fmt.Errorf("there's already a %s in %s", filepath.Base(p), filepath.Base(dest))
				}
				if err := moveFile(p, to); err != nil {
					h.remember(m.PaneId, ch)
					return opError("move", err)
				}
				ch.moves = append(ch.moves, [2]string{p, to})
			}
			show = append(show, to)
		}
	case pb.FilesCommand_ACTION_UNDO:
		return h.undo(m.PaneId)
	}
	h.remember(m.PaneId, ch)
	h.afterOp(m.PaneId, ch, show)
	return nil
}

// opPaths is paths checked, with any inside another of them left out
// (moving a folder moves what's in it).
func (h *filesHub) opPaths(in []string, root string, check func(string) (string, error)) ([]string, error) {
	if len(in) == 0 {
		return nil, errors.New("which files? (paths)")
	}
	var out []string
	for _, p := range in {
		c, err := check(p)
		if err != nil {
			return nil, err
		}
		if c == root {
			return nil, errors.New("not the pane's own folder")
		}
		if !exists(c) {
			return nil, fmt.Errorf("nothing at %s", c)
		}
		out = append(out, c)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	return slices.DeleteFunc(out, func(p string) bool {
		for _, q := range out {
			if q != p && insideFolder(p, q) {
				return true
			}
		}
		return false
	}), nil
}

func opError(what string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		err = le.Err
	}
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("couldn't %s it: Hammock isn't allowed to", what)
	}
	return fmt.Errorf("couldn't %s it: %v", what, err)
}

// sameFile reports whether a and b are the same file (a and b differing
// only in case, on a disk that doesn't mind).
func sameFile(a, b string) bool {
	sa, err1 := os.Lstat(a)
	sb, err2 := os.Lstat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// freeName is path, or "name copy", "name copy 2"… where it's taken, as the
// Finder names duplicates.
func freeName(path string) string {
	if !exists(path) {
		return path
	}
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	if st, err := os.Lstat(path); err == nil && st.IsDir() {
		ext = ""
	}
	stem := strings.TrimSuffix(base, ext)
	for i := 1; ; i++ {
		n := stem + " copy" + ext
		if i > 1 {
			n = fmt.Sprintf("%s copy %d%s", stem, i, ext)
		}
		if p := filepath.Join(dir, n); !exists(p) {
			return p
		}
	}
}

// moveFile renames from to to, copying across volumes.
func moveFile(from, to string) error {
	err := os.Rename(from, to)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyTree(from, to); err != nil {
		os.RemoveAll(to)
		return err
	}
	return os.RemoveAll(from)
}

// copyTree copies src (a file, a folder and what's in it, or a link) to dst,
// which mustn't exist: a clone where the volume can, else byte by byte.
func copyTree(src, dst string) error {
	if cloneFile(src, dst) {
		return nil
	}
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case st.IsDir():
		if err := os.Mkdir(dst, st.Mode().Perm()|0o700); err != nil {
			return err
		}
		ents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(dst, st.Mode().Perm())
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// remember keeps ch to undo (nothing, if nothing changed).
func (h *filesHub) remember(paneID string, ch filesChange) {
	if len(ch.moves)+len(ch.made)+len(ch.trashed) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.undos == nil {
		h.undos = map[string][]filesChange{}
	}
	u := append(h.undos[paneID], ch)
	if len(u) > filesUndoMax {
		u = u[len(u)-filesUndoMax:]
	}
	h.undos[paneID] = u
}

// undo reverses pane id's last change.
func (h *filesHub) undo(id string) error {
	h.mu.Lock()
	u := h.undos[id]
	if len(u) == 0 {
		h.mu.Unlock()
		return errors.New("nothing to undo")
	}
	ch := u[len(u)-1]
	h.undos[id] = u[:len(u)-1]
	h.mu.Unlock()
	var show []string
	var errs []error
	back := filesChange{}
	for i := len(ch.moves) - 1; i >= 0; i-- {
		from, to := ch.moves[i][0], ch.moves[i][1]
		if exists(from) && !sameFile(from, to) {
			errs = append(errs, fmt.Errorf("there's a %s where it was", filepath.Base(from)))
			continue
		}
		if err := moveFile(to, from); err != nil {
			errs = append(errs, err)
			continue
		}
		back.moves = append(back.moves, [2]string{to, from})
		show = append(show, from)
	}
	for _, p := range ch.made {
		if where, err := moveToTrash(p); err == nil {
			back.trashed = append(back.trashed, [2]string{p, where})
		} else if !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	for _, t := range ch.trashed {
		if exists(t[0]) {
			errs = append(errs, fmt.Errorf("there's a %s where it was", filepath.Base(t[0])))
			continue
		}
		if err := restoreFromTrash(t[1], t[0]); err != nil {
			errs = append(errs, fmt.Errorf("couldn't put %s back: %w", filepath.Base(t[0]), err))
			continue
		}
		show = append(show, t[0])
	}
	h.afterOp(id, back, show)
	return errors.Join(errs...)
}

// afterOp selects show in pane id, opening the folders down to them, and
// carries the folders open (and selected) along with what moved.
func (h *filesHub) afterOp(id string, ch filesChange, show []string) {
	d := h.d
	d.mu.Lock()
	defer d.mu.Unlock()
	p, f, err := d.filesPane(id)
	if err != nil {
		return
	}
	moved := func(x string) string {
		for _, m := range ch.moves {
			if x == m[0] {
				return m[1]
			}
			if insideFolder(x, m[0]) {
				return filepath.Join(m[1], strings.TrimPrefix(x, m[0]+"/"))
			}
		}
		return x
	}
	for i, x := range f.Expanded {
		f.Expanded[i] = moved(x)
	}
	gone := func(x string) bool {
		for _, t := range ch.trashed {
			if insideFolder(x, t[0]) {
				return true
			}
		}
		return false
	}
	f.Expanded = slices.DeleteFunc(f.Expanded, gone)
	if len(show) > 0 {
		f.Selected = slices.Clone(show)
		slices.Sort(f.Selected)
		for _, s := range show {
			for dir := filepath.Dir(s); dir != f.Root && insideFolder(dir, f.Root); dir = filepath.Dir(dir) {
				if !slices.Contains(f.Expanded, dir) {
					f.Expanded = append(f.Expanded, dir)
				}
			}
		}
		f.Reveal = show[0]
		f.RevealSeq++
	} else {
		f.Selected = slices.DeleteFunc(f.Selected, gone)
	}
	slices.Sort(f.Expanded)
	f.Expanded = slices.Compact(f.Expanded)
	d.setFiles(p, f)
}

// moveToTrash and restoreFromTrash are the platform's (trash_*.go); tests
// put a Trash of their own in their place.
var (
	moveToTrash      = trashFile
	restoreFromTrash = untrashFile
)
