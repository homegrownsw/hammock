//go:build !darwin

package daemon

func cloneFile(src, dst string) bool { return false }
