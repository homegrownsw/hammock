package daemon

/*
#include <stdlib.h>
#include <copyfile.h>
*/
import "C"

import "unsafe"

// copyMetadata gives dst src's extended attributes (Finder tags, quarantine,
// com.apple.* metadata) and ACL, by copyfile(3). Not its times: the saved
// file is new.
func copyMetadata(src, dst string) error {
	cs, cd := C.CString(src), C.CString(dst)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(cd))
	if r, err := C.copyfile(cs, cd, nil, C.COPYFILE_ACL|C.COPYFILE_XATTR); r < 0 {
		return err
	}
	return nil
}
