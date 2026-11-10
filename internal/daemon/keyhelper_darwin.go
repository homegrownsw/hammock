package daemon

/*
#cgo CFLAGS: -I${SRCDIR}/../../helpers/key
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
)

// keyRendezvous is the daemon's end of handing the Hammock Key helper its
// request (helpers/key/rendezvous.h, which both ends build from): a Mach
// service under a fresh name, which answers only the helper it started.
type keyRendezvous struct {
	name string
	port C.mach_port_t
}

func newKeyRendezvous() (*keyRendezvous, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	name := "com.homegrownsw.hammock.key." + hex.EncodeToString(b)
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	var port C.mach_port_t
	if kr := C.hmk_key_checkin(cs, &port); kr != C.KERN_SUCCESS {
		return nil, fmt.Errorf("can't make a way to the Hammock Key helper (bootstrap error %d)", int(kr))
	}
	return &keyRendezvous{name: name, port: port}, nil
}

// serve sends request to the helper when it says hello: only to pid, the
// helper this daemon started (by the hello's audit trailer: a process that
// read the name from its arguments gets nothing). Until it's sent, or stop
// closes. Its own goroutine; the port stays open until close.
func (r *keyRendezvous) serve(pid int, request []byte, stop <-chan struct{}) error {
	data := C.CBytes(request)
	defer C.free(data)
	for {
		select {
		case <-stop:
			return errors.New("the Hammock Key helper didn't ask for its request")
		default:
		}
		var from C.pid_t
		var reply C.mach_port_t
		switch kr := C.hmk_key_hello(r.port, 200, &from, &reply); kr {
		case C.KERN_SUCCESS:
		case C.MACH_RCV_TIMED_OUT, C.KERN_INVALID_ARGUMENT, C.MACH_RCV_TOO_LARGE: // nothing yet, or something else's, dropped
			continue
		default:
			return fmt.Errorf("the Hammock Key helper's way in broke (mach error %#x)", int(kr))
		}
		if int(from) != pid {
			C.hmk_key_drop(reply)
			continue
		}
		if kr := C.hmk_key_send(reply, data, C.size_t(len(request))); kr != C.KERN_SUCCESS {
			return fmt.Errorf("can't send the Hammock Key helper its request (mach error %#x)", int(kr))
		}
		return nil
	}
}

// close lets the service and its name go. After serve has returned.
func (r *keyRendezvous) close() { C.hmk_key_close(r.port) }

// keyHelperFetch is the helper's end (hmk_key_fetch), for the stand-in
// helper the tests run (fakeKeyHelper): the request, and the pid that sent
// it. The real helper also checks the sender's code signature.
func keyHelperFetch(name string, wait time.Duration) ([]byte, int, error) {
	cs := C.CString(name)
	defer C.free(unsafe.Pointer(cs))
	var data unsafe.Pointer
	var n C.size_t
	var token C.audit_token_t
	if kr := C.hmk_key_fetch(cs, C.mach_msg_timeout_t(wait.Milliseconds()), &data, &n, &token); kr != C.KERN_SUCCESS {
		return nil, 0, fmt.Errorf("no request from the daemon (mach error %#x)", int(kr))
	}
	b := C.GoBytes(data, C.int(n))
	C.vm_deallocate(C.mach_task_self_, C.vm_address_t(uintptr(data)), C.vm_size_t(n))
	return b, int(token.val[5]), nil
}
