package daemon

/*
#cgo CFLAGS: -I${SRCDIR}/../../helpers/common
#include <stdlib.h>
#include "rendezvous.h"
*/
import "C"

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// rendezvous is the daemon's end of handing a helper it starts something
// only that helper may have (helpers/common/rendezvous.h, which both ends
// build from): a Mach service under a fresh name, put on the helper's
// command line, which answers only the hellos it's told to.
type rendezvous struct {
	name  string
	magic *C.char // the helper's hello, 16 bytes
	port  C.mach_port_t
}

// newRendezvous checks in a service named prefix + 32 random hex digits,
// for hellos with magic (16 bytes: the helper's, and its version).
func newRendezvous(prefix, magic string) (*rendezvous, error) {
	if len(magic) != 16 {
		return nil, errors.New("a rendezvous hello is 16 bytes")
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	name := prefix + hex.EncodeToString(b)
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	var port C.mach_port_t
	if kr := C.hmk_rv_checkin(cs, &port); kr != C.KERN_SUCCESS {
		return nil, fmt.Errorf("bootstrap error %d", int(kr))
	}
	return &rendezvous{name: name, magic: C.CString(magic), port: port}, nil
}

// serve answers hellos until stop closes: answer says what to send the
// hello's sender, known by its audit token as it sent it (nil: nothing,
// the hello is dropped), and whether that was the last. Its own goroutine;
// the port stays open until close.
func (r *rendezvous) serve(answer func(codesign.Token) (data []byte, last bool), stop <-chan struct{}) error {
	for {
		select {
		case <-stop:
			return errors.New("no hello from the helper")
		default:
		}
		var tok C.audit_token_t
		var reply C.mach_port_t
		switch kr := C.hmk_rv_hello(r.port, 200, r.magic, &tok, &reply); kr {
		case C.KERN_SUCCESS:
		case C.MACH_RCV_TIMED_OUT, C.KERN_INVALID_ARGUMENT, C.MACH_RCV_TOO_LARGE: // nothing yet, or something else's, dropped
			continue
		default:
			return fmt.Errorf("the helper's way in broke (mach error %#x)", int(kr))
		}
		var w [8]uint32
		for i := range w {
			w[i] = uint32(tok.val[i])
		}
		data, last := answer(codesign.TokenFromWords(w))
		if data == nil {
			C.hmk_rv_drop(reply)
			continue
		}
		p := C.CBytes(data)
		kr := C.hmk_rv_send(reply, p, C.size_t(len(data)))
		C.free(p)
		if kr != C.KERN_SUCCESS {
			return fmt.Errorf("can't answer the helper (mach error %#x)", int(kr))
		}
		if last {
			return nil
		}
	}
}

// close lets the service and its name go. After serve has returned.
func (r *rendezvous) close() {
	C.hmk_rv_close(r.port)
	C.free(unsafe.Pointer(r.magic))
}

// rendezvousFetch is the helper's end (hmk_rv_fetch), for the stand-in
// helpers the tests run: the answer, and the audit token it was sent
// with.
func rendezvousFetch(name, magic string, wait time.Duration) ([]byte, codesign.Token, error) {
	cs, cm := C.CString(name), C.CString(magic)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(cm))
	var data unsafe.Pointer
	var n C.size_t
	var tok C.audit_token_t
	if kr := C.hmk_rv_fetch(cs, cm, C.mach_msg_timeout_t(wait.Milliseconds()), &data, &n, &tok); kr != C.KERN_SUCCESS {
		return nil, codesign.Token{}, fmt.Errorf("no answer from the daemon (mach error %#x)", int(kr))
	}
	b := C.GoBytes(data, C.int(n))
	C.vm_deallocate(C.mach_task_self_, C.vm_address_t(uintptr(data)), C.vm_size_t(n))
	var w [8]uint32
	for i := range w {
		w[i] = uint32(tok.val[i])
	}
	return b, codesign.TokenFromWords(w), nil
}
