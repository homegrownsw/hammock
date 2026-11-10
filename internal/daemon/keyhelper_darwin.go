package daemon

import (
	"fmt"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// keyHello is the Key helper's hello (HMK_KEY_HELLO, helpers/key/rendezvous.h).
const keyHello = "hmk-key-hello-v1"

// keyRendezvous is the daemon's end of handing the Hammock Key helper its
// request (helpers/key/rendezvous.h, on helpers/common/rendezvous.h): a
// Mach service under a fresh name, which answers only the helper it
// started.
type keyRendezvous struct {
	*rendezvous
}

func newKeyRendezvous() (*keyRendezvous, error) {
	r, err := newRendezvous("com.homegrownsw.hammock.key.", keyHello)
	if err != nil {
		return nil, fmt.Errorf("can't make a way to the Hammock Key helper (%v)", err)
	}
	return &keyRendezvous{r}, nil
}

// serve sends request to the helper when it says hello: only to pid, the
// helper this daemon started (by the hello's audit trailer: a process that
// read the name from its arguments gets nothing). Until it's sent, or stop
// closes. Its own goroutine; the port stays open until close.
func (r *keyRendezvous) serve(pid int, request []byte, stop <-chan struct{}) error {
	err := r.rendezvous.serve(func(t codesign.Token) ([]byte, bool) {
		if t.PID() != pid {
			return nil, false
		}
		return request, true
	}, stop)
	if err != nil {
		return fmt.Errorf("the Hammock Key helper didn't get its request: %v", err)
	}
	return nil
}

// keyHelperFetch is the helper's end (hmk_key_fetch), for the stand-in
// helper the tests run (fakeKeyHelper): the request, and the pid that sent
// it. The real helper also checks the sender's code signature.
func keyHelperFetch(name string, wait time.Duration) ([]byte, int, error) {
	b, t, err := rendezvousFetch(name, keyHello, wait)
	if err != nil {
		return nil, 0, err
	}
	return b, t.PID(), nil
}
