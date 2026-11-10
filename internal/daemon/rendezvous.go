package daemon

import (
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
	machrv "github.com/homegrownsw/hammock/internal/rendezvous"
)

// rendezvous is the daemon's end of handing a helper it starts something
// only that helper may have (helpers/common/rendezvous.h, which both ends
// build from, through internal/rendezvous): a Mach service under a fresh
// name, put on the helper's command line, which answers only the hellos
// it's told to. (Only on a Mac: elsewhere newRendezvous fails.)
type rendezvous struct {
	svc  *machrv.Service
	name string
}

// newRendezvous checks in a service named prefix + 32 random hex digits,
// for hellos with magic (16 bytes: the helper's, and its version).
func newRendezvous(prefix, magic string) (*rendezvous, error) {
	s, err := machrv.New(prefix, magic)
	if err != nil {
		return nil, err
	}
	return &rendezvous{svc: s, name: s.Name}, nil
}

// serve answers hellos until stop closes: answer says what to send the
// hello's sender, known by its audit token as it sent it (nil: nothing,
// the hello is dropped), and whether that was the last. Its own goroutine;
// the port stays open until close.
func (r *rendezvous) serve(answer func(codesign.Token) (data []byte, last bool), stop <-chan struct{}) error {
	return r.svc.Serve(answer, stop)
}

// close lets the service and its name go. After serve has returned.
func (r *rendezvous) close() { r.svc.Close() }

// rendezvousFetch is the helper's end (hmk_rv_fetch), for the stand-in
// helpers the tests run: the answer, and the audit token it was sent
// with.
func rendezvousFetch(name, magic string, wait time.Duration) ([]byte, codesign.Token, error) {
	return machrv.Fetch(name, magic, wait)
}
