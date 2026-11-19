// Package regfile opens and reads files someone else named (a client, an
// agent's transcript, a repository's contents) only if they're regular
// files, and reads no more of them than asked.
//
// os.Stat then os.ReadFile isn't enough: a device or a FIFO reports a size
// of 0, so /dev/zero would be read until memory runs out and a FIFO would
// block the reader for good, and the file can change between the two calls
// (swapped for a FIFO, or grown past a limit).
package regfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

var (
	// ErrNotRegular: a folder, a device, a FIFO or a socket.
	ErrNotRegular = errors.New("not a regular file")
	// ErrTooLarge: more than the limit.
	ErrTooLarge = errors.New("file too large")
)

// Open opens path (following links) for reading if it's a regular file.
// It never blocks on a FIFO or reads a device: the type is checked before
// opening, the open doesn't wait (O_NONBLOCK) or take a terminal
// (O_NOCTTY), and the open file is checked again. When path isn't a regular file it returns its FileInfo
// with ErrNotRegular, so callers can say what it is.
func Open(path string) (*os.File, fs.FileInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, st, notRegular(path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	// Swapped since the Stat?
	st, err = f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, st, notRegular(path)
	}
	return f, st, nil
}

func notRegular(path string) error {
	return fmt.Errorf("%s: %w", filepath.Base(path), ErrNotRegular)
}

// ReadFile reads the regular file at path, refusing it (ErrTooLarge) if
// it's more than limit bytes, by its size or by what's read.
func ReadFile(path string, limit int64) ([]byte, error) {
	f, st, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st.Size() > limit {
		return nil, ErrTooLarge
	}
	return Read(f, limit)
}

// Read reads r to its end, refusing it (ErrTooLarge) past limit bytes.
func Read(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}
