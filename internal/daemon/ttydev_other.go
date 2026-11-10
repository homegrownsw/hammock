//go:build !darwin && !linux

package daemon

func deviceOf(string) (int64, bool) { return 0, false }
