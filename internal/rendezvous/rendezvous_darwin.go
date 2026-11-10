// Package rendezvous is the Go end of helpers/common/rendezvous.h: a Mach
// service under a fresh random name, whose messages carry their sender's
// audit token as it was when it sent them. The daemon uses it to hand its
// helpers what only they may have, and to take the app's proof of a
// connection (internal/daemon); a pane holder, to take its daemon's proof of
// one (internal/holder). Both import it, so neither drags the other in.
//
// Why a message and not a socket's peer: macOS works a unix socket's audit
// token out when asked, from the pid that last used the other end, as that
// pid is then, so a process can make itself look like a signed program
// after the fact (rendezvous.h says how). A message's audit token is fixed
// as it's sent: once its sender execs, it names nothing.
package rendezvous

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

// ErrNothing is Hear's "nothing for us yet": its wait ran out, or what came
// wasn't a hello with this service's magic and body length, and was dropped.
var ErrNothing = errors.New("no hello")

// Service is the receiving end: a Mach service checked in under a fresh
// name, which hears only hellos with its magic.
type Service struct {
	Name  string
	magic [16]byte
	port  C.mach_port_t
}

// New checks in a service named prefix + 32 random hex digits, for hellos
// with magic (16 bytes: whose they are, and their wire format's version).
// launchd refuses a second check-in under a name while it's held, and
// forgets it once the port goes (Close).
func New(prefix, magic string) (*Service, error) {
	if len(magic) != 16 {
		return nil, errors.New("a rendezvous hello is 16 bytes")
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s := &Service{Name: prefix + hex.EncodeToString(b)}
	copy(s.magic[:], magic)
	cs := C.CString(s.Name)
	defer C.free(unsafe.Pointer(cs))
	if kr := C.hmk_rv_checkin(cs, &s.port); kr != C.KERN_SUCCESS {
		return nil, fmt.Errorf("bootstrap error %d", int(kr))
	}
	return s, nil
}

// Hello is a hello the service heard: its sender's audit token as it sent
// it, and its body. Answer it, or Drop it, once.
type Hello struct {
	Token codesign.Token
	Body  []byte
	reply C.mach_port_t
}

// Hear waits up to wait for a hello with exactly n bytes of body (0, or a
// multiple of 4 up to 64). ErrNothing if none came in time or something
// else did (dropped); any other error means the service is broken. Only one
// goroutine at a time, and not after Close.
func (s *Service) Hear(wait time.Duration, n int) (*Hello, error) {
	var tok C.audit_token_t
	var reply C.mach_port_t
	magic := C.CBytes(s.magic[:])
	defer C.free(magic)
	var body unsafe.Pointer
	if n > 0 {
		body = C.malloc(C.size_t(n))
		defer C.free(body)
	}
	ms := wait.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	switch kr := C.hmk_rv_hear(s.port, C.mach_msg_timeout_t(ms), (*C.char)(magic), body, C.size_t(n), &tok, &reply); kr {
	case C.KERN_SUCCESS:
	case C.MACH_RCV_TIMED_OUT, C.KERN_INVALID_ARGUMENT, C.MACH_RCV_TOO_LARGE:
		return nil, ErrNothing
	default:
		return nil, fmt.Errorf("the service broke (mach error %#x)", int(kr))
	}
	var w [8]uint32
	for i := range w {
		w[i] = uint32(tok.val[i])
	}
	h := &Hello{Token: codesign.TokenFromWords(w), reply: reply}
	if n > 0 {
		h.Body = C.GoBytes(body, C.int(n))
	}
	return h, nil
}

// Answer sends data to the hello's sender, on the reply right it sent.
func (h *Hello) Answer(data []byte) error {
	var p unsafe.Pointer
	if len(data) > 0 {
		p = C.CBytes(data)
		defer C.free(p)
	}
	if kr := C.hmk_rv_send(h.reply, p, C.size_t(len(data))); kr != C.KERN_SUCCESS {
		return fmt.Errorf("can't answer (mach error %#x)", int(kr))
	}
	return nil
}

// Drop lets the hello go unanswered.
func (h *Hello) Drop() { C.hmk_rv_drop(h.reply) }

// Serve answers hellos with no body until stop closes: answer says what to
// send the hello's sender, known by its audit token as it sent it (nil:
// nothing, the hello is dropped), and whether that was the last. Its own
// goroutine; the port stays open until Close.
func (s *Service) Serve(answer func(codesign.Token) (data []byte, last bool), stop <-chan struct{}) error {
	for {
		select {
		case <-stop:
			return errors.New("no hello from the helper")
		default:
		}
		h, err := s.Hear(200*time.Millisecond, 0)
		if errors.Is(err, ErrNothing) {
			continue
		}
		if err != nil {
			return fmt.Errorf("the helper's way in broke: %w", err)
		}
		data, last := answer(h.Token)
		if data == nil {
			h.Drop()
			continue
		}
		if err := h.Answer(data); err != nil {
			return fmt.Errorf("can't answer the helper: %w", err)
		}
		if last {
			return nil
		}
	}
}

// Close lets the service and its name go. Not while Hear or Serve runs.
func (s *Service) Close() { C.hmk_rv_close(s.port) }

// Ask is the other end (hmk_rv_ask): says hello to the service name with
// magic and body (a multiple of 4 bytes, at most 64), and waits up to wait
// for each step. The answer, and the audit token it was sent with: check
// who that is before believing a byte of it.
func Ask(name, magic string, body []byte, wait time.Duration) ([]byte, codesign.Token, error) {
	if len(magic) != 16 {
		return nil, codesign.Token{}, errors.New("a rendezvous hello is 16 bytes")
	}
	cs, cm := C.CString(name), C.CString(magic)
	defer C.free(unsafe.Pointer(cs))
	defer C.free(unsafe.Pointer(cm))
	var cb unsafe.Pointer
	if len(body) > 0 {
		cb = C.CBytes(body)
		defer C.free(cb)
	}
	var data unsafe.Pointer
	var n C.size_t
	var tok C.audit_token_t
	if kr := C.hmk_rv_ask(cs, cm, cb, C.size_t(len(body)), C.mach_msg_timeout_t(wait.Milliseconds()), &data, &n, &tok); kr != C.KERN_SUCCESS {
		return nil, codesign.Token{}, fmt.Errorf("no answer (mach error %#x)", int(kr))
	}
	b := C.GoBytes(data, C.int(n))
	C.vm_deallocate(C.mach_task_self_, C.vm_address_t(uintptr(data)), C.vm_size_t(n))
	var w [8]uint32
	for i := range w {
		w[i] = uint32(tok.val[i])
	}
	return b, codesign.TokenFromWords(w), nil
}

// Fetch is Ask with a hello that's only its magic.
func Fetch(name, magic string, wait time.Duration) ([]byte, codesign.Token, error) {
	return Ask(name, magic, nil, wait)
}
