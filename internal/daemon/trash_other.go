//go:build !darwin && !linux

package daemon

import "errors"

func trashFile(path string) (string, error) {
	return "", errors.New("there's no Trash here")
}

func untrashFile(where, original string) error {
	return errors.New("there's no Trash here")
}
