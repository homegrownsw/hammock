//go:build !darwin

package daemon

// pathByID: only the Mac can say where a file is by its id; elsewhere a
// file is followed only within its folder (movedTo).
func pathByID(path string, dev, ino uint64) string { return "" }
