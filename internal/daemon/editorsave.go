package daemon

// Writing an editor pane's file. Like vim's backupcopy=auto: a save normally
// writes a new file beside the old one and renames it over it (atomic: a
// crash leaves one or the other, never half), carrying over the old file's
// permissions, group, extended attributes (Finder tags, quarantine) and ACLs.
// Where renaming would change what the file is, it's written in place
// instead, with a copy of the old one kept until the write is done: a file
// with other hard links (they'd keep the old text), one owned by someone else
// (the new file would be ours), and one in a folder we can't create in.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// saveFile writes data to path (symlinks followed: the link stays a link).
// mode is a new file's; an existing one keeps its own.
func saveFile(path string, data []byte, mode fs.FileMode) error {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	st, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		st = nil
	}
	if st != nil {
		mode = st.Mode().Perm()
		if nlink, owner, _, ok := statOwner(st); ok && saveInPlace(nlink, owner, os.Getuid()) {
			return writeInPlace(path, data)
		}
	}
	err = writeReplacing(path, data, mode, st)
	if errors.Is(err, errSaveInPlace) {
		return writeInPlace(path, data)
	}
	return err
}

// saveInPlace says whether a file with nlink links, owned by owner, is
// written in place by uid rather than replaced: replacing it would leave
// its other links with the old text, or make it uid's.
func saveInPlace(nlink uint64, owner uint32, uid int) bool {
	return nlink > 1 || int(owner) != uid
}

func statOwner(st fs.FileInfo) (nlink uint64, owner, group uint32, ok bool) {
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, false
	}
	return uint64(s.Nlink), s.Uid, s.Gid, true
}

// errSaveInPlace: the new file couldn't be made like the old one (or at
// all), so the old one is written in place.
var errSaveInPlace = errors.New("save in place")

// writeReplacing writes data to a new file beside path and renames it over
// path, with old's metadata (old is path's FileInfo, nil for a new file).
func writeReplacing(path string, data []byte, mode fs.FileMode, old fs.FileInfo) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".hammock-*")
	if err != nil {
		if old != nil {
			return errSaveInPlace // a folder we can't create in
		}
		return os.WriteFile(path, data, mode)
	}
	tmp := f.Name()
	fail := func(err error) error {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if old != nil {
		if _, _, group, ok := statOwner(old); ok {
			if tst, err := f.Stat(); err == nil {
				if _, _, g, ok := statOwner(tst); ok && g != group && f.Chown(-1, int(group)) != nil {
					return fail(errSaveInPlace) // a group we're not in
				}
			}
		}
		if err := copyMetadata(path, tmp); err != nil {
			return fail(errSaveInPlace)
		}
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync() // the rename, on disk
		d.Close()
	}
	return nil
}

// writeInPlace overwrites path with data, after copying the file as it was
// (beside it, or in the temporary folder if its folder is read-only). If the
// write fails the copy is put back; if that fails too, the error says where
// the copy is.
func writeInPlace(path string, data []byte) error {
	bak, err := backupFile(path)
	if err != nil {
		return fmt.Errorf("couldn't keep a copy of %s before saving: %w", filepath.Base(path), err)
	}
	if err := overwrite(path, bytes.NewReader(data)); err != nil {
		if b, rerr := os.Open(bak); rerr == nil {
			rerr = overwrite(path, b)
			b.Close()
			if rerr == nil {
				os.Remove(bak)
				return err
			}
		}
		return fmt.Errorf("%w (the file as it was is in %s)", err, bak)
	}
	os.Remove(bak)
	return nil
}

// backupFile copies path to a new file and says where.
func backupFile(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	pattern := "." + filepath.Base(path) + ".hammock-backup-*"
	f, err := os.CreateTemp(filepath.Dir(path), pattern)
	if err != nil {
		if f, err = os.CreateTemp("", pattern); err != nil {
			return "", err
		}
	}
	if _, err := io.Copy(f, src); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func overwrite(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
