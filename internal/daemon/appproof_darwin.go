package daemon

/*
#cgo CFLAGS: -I${SRCDIR}/../../helpers/common
#include <stdlib.h>
#include "appproof.h"

// hmk_app_proof_hear waits up to ms for a proof (appproof.h): its sender's
// audit token as it sent it, and its body.
static kern_return_t hmk_app_proof_hear(mach_port_t port, mach_msg_timeout_t ms, audit_token_t *token, mach_port_t *reply,
                                        hmk_app_proof_body_t *body) {
	return hmk_rv_hear(port, ms, HMK_APP_PROOF_HELLO, body, sizeof *body, token, reply);
}

static kern_return_t hmk_app_proof_send(const char *service, uint64_t conn, const uint8_t *nonce, size_t n,
                                        mach_msg_timeout_t ms, char *answer, size_t cap) {
	return hmk_app_prove(service, conn, nonce, n, ms, answer, cap);
}
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// appProofHello is the app's proof's hello (HMK_APP_PROOF_HELLO).
const appProofHello = "hmk-app-proof-v1"

// appProofService is the name of the Mach service the app sends its proofs
// to (appproof.h): checked in under a random name the first time it's
// needed, and held until the daemon stops.
func (d *Daemon) appProofService() (string, error) {
	p := &d.proofs
	p.svcOnce.Do(func() {
		rv, err := newRendezvous("com.homegrownsw.hammock.app.", appProofHello)
		if err != nil {
			p.svcErr = fmt.Errorf("can't make a way for the Hammock app to prove itself (%v)", err)
			return
		}
		p.svc = rv.name
		go func() {
			defer rv.close()
			if err := d.serveAppProofs(rv); err != nil {
				d.log.Warn("the app's proofs: "+err.Error(), "service", rv.name)
			}
		}()
	})
	return p.svc, p.svcErr
}

// serveAppProofs takes the app's proofs until the daemon stops, answering
// each with what became of it. Its own goroutine.
func (d *Daemon) serveAppProofs(rv *rendezvous) error {
	for {
		select {
		case <-d.done:
			return nil
		default:
		}
		var tok C.audit_token_t
		var reply C.mach_port_t
		var body C.hmk_app_proof_body_t
		switch kr := C.hmk_app_proof_hear(rv.port, 200, &tok, &reply, &body); kr {
		case C.KERN_SUCCESS:
		case C.MACH_RCV_TIMED_OUT, C.KERN_INVALID_ARGUMENT, C.MACH_RCV_TOO_LARGE:
			continue // nothing yet, or something else, dropped
		default:
			return fmt.Errorf("the service broke (mach error %#x)", int(kr))
		}
		var w [8]uint32
		for i := range w {
			w[i] = uint32(tok.val[i])
		}
		b := C.GoBytes(unsafe.Pointer(&body), C.int(unsafe.Sizeof(body)))
		answer := d.appProof(binary.BigEndian.Uint64(b[:8]), b[8:], codesign.TokenFromWords(w))
		p := C.CBytes([]byte(answer))
		C.hmk_rv_send(reply, p, C.size_t(len(answer)))
		C.free(p)
	}
}

// sendAppProof is the app's end (hmk_app_prove), for the stand-in apps the
// tests run: the daemon's answer.
func sendAppProof(service string, conn uint64, nonce []byte, wait time.Duration) (string, error) {
	if len(nonce) == 0 {
		return "", errors.New("no nonce")
	}
	cs := C.CString(service)
	defer C.free(unsafe.Pointer(cs))
	cn := C.CBytes(nonce)
	defer C.free(cn)
	var buf [256]C.char
	if kr := C.hmk_app_proof_send(cs, C.uint64_t(conn), (*C.uint8_t)(cn), C.size_t(len(nonce)),
		C.mach_msg_timeout_t(wait.Milliseconds()), &buf[0], C.size_t(len(buf))); kr != C.KERN_SUCCESS {
		return "", fmt.Errorf("no answer from the daemon (mach error %#x)", int(kr))
	}
	return C.GoString(&buf[0]), nil
}
