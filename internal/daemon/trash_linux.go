package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// trashFile moves path to the home Trash (the freedesktop.org trash, as
// file managers use it), returning where it is now. Only from the home
// folder's volume: elsewhere it would mean copying it.
func trashFile(path string) (string, error) {
	if !exists(path) {
		return "", fs.ErrNotExist
	}
	trash := os.Getenv("XDG_DATA_HOME")
	if trash == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		trash = filepath.Join(home, ".local", "share")
	}
	trash = filepath.Join(trash, "Trash")
	if err := os.MkdirAll(filepath.Join(trash, "files"), 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(trash, "info"), 0o700); err != nil {
		return "", err
	}
	if !sameDevice(path, trash) {
		return "", errors.New("it's on another volume than the Trash")
	}
	base := filepath.Base(path)
	for i := 1; ; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		info := filepath.Join(trash, "info", name+".trashinfo")
		f, err := os.OpenFile(info, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		esc := (&url.URL{Path: path}).EscapedPath()
		_, err = fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", esc, time.Now().Format("2006-01-02T15:04:05"))
		f.Close()
		if err != nil {
			os.Remove(info)
			return "", err
		}
		where := filepath.Join(trash, "files", name)
		if err := os.Rename(path, where); err != nil {
			os.Remove(info)
			return "", err
		}
		return where, nil
	}
}

// untrashFile puts what trashFile moved back where it was.
func untrashFile(where, original string) error {
	if err := moveFile(where, original); err != nil {
		return err
	}
	dir := filepath.Dir(filepath.Dir(where))
	os.Remove(filepath.Join(dir, "info", strings.TrimSuffix(filepath.Base(where), "/")+".trashinfo"))
	return nil
}

func sameDevice(a, b string) bool {
	var sa, sb syscall.Stat_t
	if syscall.Stat(a, &sa) != nil || syscall.Stat(b, &sb) != nil {
		return false
	}
	return sa.Dev == sb.Dev
}
