package daemon

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
char *hmk_trash(const char *path, char **err);
*/
import "C"

import (
	"errors"
	"io/fs"
	"unsafe"
)

// trashFile moves path to the Trash (the Finder's, so Put Back works),
// returning where it is now.
func trashFile(path string) (string, error) {
	if !exists(path) {
		return "", fs.ErrNotExist
	}
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var cerr *C.char
	out := C.hmk_trash(cp, &cerr)
	if out == nil {
		msg := C.GoString(cerr)
		C.free(unsafe.Pointer(cerr))
		return "", errors.New(msg)
	}
	defer C.free(unsafe.Pointer(out))
	return C.GoString(out), nil
}

// untrashFile puts what trashFile moved back where it was.
func untrashFile(where, original string) error {
	return moveFile(where, original)
}
