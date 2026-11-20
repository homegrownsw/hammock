//go:build !darwin

package daemon

import (
	"bytes"
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// copyMetadata gives dst src's extended attributes, its ACL among them
// (system.posix_acl_access). security.* (SELinux labels and the like) are
// the system's to set, so ones we can't are left to it.
func copyMetadata(src, dst string) error {
	names, err := xattrNames(src)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			return nil
		}
		return err
	}
	for _, name := range names {
		v, err := xattr(src, name)
		if err != nil {
			if errors.Is(err, unix.ENODATA) {
				continue // gone since listed
			}
			return err
		}
		if err := unix.Setxattr(dst, name, v, 0); err != nil {
			if strings.HasPrefix(name, "security.") || errors.Is(err, unix.ENOTSUP) {
				continue
			}
			return err
		}
	}
	return nil
}

func xattrNames(path string) ([]string, error) {
	for {
		n, err := unix.Listxattr(path, nil)
		if err != nil || n == 0 {
			return nil, err
		}
		buf := make([]byte, n)
		n, err = unix.Listxattr(path, buf)
		if errors.Is(err, unix.ERANGE) {
			continue // grew since sized
		}
		if err != nil {
			return nil, err
		}
		var names []string
		for _, b := range bytes.Split(buf[:n], []byte{0}) {
			if len(b) > 0 {
				names = append(names, string(b))
			}
		}
		return names, nil
	}
}

func xattr(path, name string) ([]byte, error) {
	for {
		n, err := unix.Getxattr(path, name, nil)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, n)
		n, err = unix.Getxattr(path, name, buf)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
}
