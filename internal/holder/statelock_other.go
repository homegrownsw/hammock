//go:build !darwin && !linux

package holder

import "errors"

func lockedFiles(int) (map[fileID]bool, error) { return nil, errors.New("not supported here") }

// CloseInherited: nothing to go by here.
func CloseInherited() {}
