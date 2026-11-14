package daemon

/*
#include <stdint.h>
#include <sys/types.h>
#include <sys/mount.h>
#include <sys/fsgetpath.h>
*/
import "C"

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// pathByID is where the file with inode ino on dev is now (fsgetpath, by
// the volume of the nearest folder of path still there on dev). "" if it
// can't say.
func pathByID(path string, dev, ino uint64) string {
	dir := filepath.Dir(path)
	for {
		st, err := os.Stat(dir)
		if err == nil {
			if d, _ := fileID(st); d != dev {
				return "" // a folder on another volume: not where it was
			}
			var fs syscall.Statfs_t
			if syscall.Statfs(dir, &fs) != nil {
				return ""
			}
			var id C.fsid_t
			id.val[0] = C.int32_t(fs.Fsid.Val[0])
			id.val[1] = C.int32_t(fs.Fsid.Val[1])
			buf := make([]byte, 4096)
			n := C.fsgetpath((*C.char)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)), &id, C.uint64_t(ino))
			if n <= 0 {
				return ""
			}
			return C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
		}
		up := filepath.Dir(dir)
		if up == dir {
			return ""
		}
		dir = up
	}
}
