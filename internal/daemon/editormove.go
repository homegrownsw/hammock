package daemon

// Editor files renamed or moved: by Rename (EditorCommand), or by anything
// else (the Finder, git mv, an agent). The buffer keeps the file's identity
// (its device and inode): when its path goes, the file is looked for by
// that, so the pane follows it, unsaved edits and all, instead of saying
// it's missing. A file moved to the Trash was deleted, not moved.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fileID is st's device and inode (0, 0 if it has none).
func fileID(st fs.FileInfo) (dev, ino uint64) {
	if st == nil {
		return 0, 0
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(s.Dev), uint64(s.Ino)
	}
	return 0, 0
}

// isFile says whether path is a regular file with that identity.
func isFile(path string, dev, ino uint64) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	d, i := fileID(st)
	return d == dev && i == ino
}

// inTrash says whether path is in a Trash: the Mac's (~/.Trash, a
// volume's .Trashes) or a Linux desktop's (…/Trash/files, .Trash-1000).
func inTrash(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".Trash" || part == ".Trashes" || strings.HasPrefix(part, ".Trash-") {
			return true
		}
	}
	return strings.Contains(path, "/.local/share/Trash/")
}

// movedTo finds where the file that was at path (dev, ino) went: by its id
// where the system can say (pathByID), else in the same folder. "" if
// it's gone (deleted, or moved to the Trash).
func movedTo(path string, dev, ino uint64) string {
	if ino == 0 {
		return ""
	}
	if p := pathByID(path, dev, ino); p != "" && p != path && !inTrash(p) && isFile(p, dev, ino) {
		return p
	}
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if st, err := e.Info(); err == nil {
			if d, i := fileID(st); d == dev && i == ino {
				return filepath.Join(dir, e.Name())
			}
		}
	}
	return ""
}

// rekey moves b to path: its key, its panes, its kept edits. Callers hold
// h.mu, and have checked nothing else has path open.
func (h *editorHub) rekey(b *editorBuf, path string) {
	if b.saveDue || b.dirty {
		h.d.persist(h.d.store.DeleteRecord(editorRecord, b.key))
		b.saveDue = b.dirty // kept again under its new name
	}
	delete(h.bufs, b.key)
	b.key, b.path = path, path
	h.bufs[path] = b
	for id := range b.panes {
		h.pane[id] = path
	}
	b.tabWidth, b.indent = indentFor(path, b.text)
}

// rename answers ACTION_RENAME: the file moves on disk (if it's there yet)
// and the buffer with it.
func (h *editorHub) rename(m *pb.EditorCommand, folder string, guest bool) error {
	h.mu.Lock()
	b, err := h.buf(m.PaneId)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	from := b.path
	h.mu.Unlock()
	if from == "" {
		return errors.New("it hasn't been saved yet: save it to name it")
	}
	name := strings.TrimSpace(m.Path)
	if name == "" {
		return errors.New("rename it to what?")
	}
	target, err := resolveEditorPath(name, filepath.Dir(from))
	if err != nil {
		return err
	}
	if target == from {
		return nil
	}
	if guest && !insideFolder(target, folder) {
		return errors.New("guests rename only inside the Group's folder")
	}
	if err := h.moveFile(b, from, target); err != nil {
		return err
	}
	h.moved(b)
	return nil
}

// moveFile renames b's file from → target, and b with it.
func (h *editorHub) moveFile(b *editorBuf, from, target string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b.path != from {
		return errors.New("it was renamed meanwhile")
	}
	if o := h.bufs[target]; o != nil && o != b {
		return fmt.Errorf("%s is open in another pane", filepath.Base(target))
	}
	if !b.missing {
		// A name taken by another file; not the same one (a change of
		// case, on a disk that ignores it).
		if st, err := os.Lstat(target); err == nil {
			if src, err := os.Lstat(from); err != nil || !os.SameFile(st, src) {
				return fmt.Errorf("%s already exists", filepath.Base(target))
			}
		}
		if err := os.Rename(from, target); err != nil {
			var le *os.LinkError
			if errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV) {
				return errors.New("it can't be moved to another disk by renaming it: use Save As")
			}
			return err
		}
	} else if _, err := os.Lstat(target); err == nil {
		return fmt.Errorf("%s already exists", filepath.Base(target))
	}
	h.rekey(b, target)
	return nil
}

// moved brings a renamed buffer's git marks and panes up to date.
func (h *editorHub) moved(b *editorBuf) {
	h.loadHead(b)
	h.refreshMarks(b, true)
	h.publish(b)
}

// follow notices b's file moved (it's gone from path, at the same
// identity elsewhere), and moves the buffer to it. Says whether it did.
func (h *editorHub) follow(b *editorBuf, path string, dev, ino uint64) bool {
	to := movedTo(path, dev, ino)
	if to == "" {
		return false
	}
	h.mu.Lock()
	if b.path != path || h.bufs[to] != nil {
		// Renamed here meanwhile, or the new name's open in another pane
		// (that one has it: this one says it's missing).
		h.mu.Unlock()
		return b.path != path
	}
	h.rekey(b, to)
	h.mu.Unlock()
	h.moved(b)
	return true
}
