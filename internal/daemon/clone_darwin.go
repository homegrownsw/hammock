package daemon

import "golang.org/x/sys/unix"

// cloneFile makes dst a copy of src that shares its blocks (APFS), so even
// a big video costs nothing; false where the volume can't (copyFile then).
func cloneFile(src, dst string) bool {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW) == nil
}
