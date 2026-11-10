package daemon

import (
	"os"
	"testing"
	"time"
)

// The daemon hands a request only to the helper it started: a process that
// read the service's name (from the helper's arguments) and says hello
// gets nothing; the helper gets it, with the daemon as its sender; and the
// name is gone once the daemon is done.
func TestKeyRendezvous(t *testing.T) {
	serve := func(pid int) (*keyRendezvous, chan struct{}, chan error) {
		rv, err := newKeyRendezvous()
		if err != nil {
			t.Fatal(err)
		}
		stop, served := make(chan struct{}), make(chan error, 1)
		go func() { served <- rv.serve(pid, []byte(`{"v":1}`), stop) }()
		return rv, stop, served
	}

	// Another process's helper (here pid 1): no answer.
	rv, stop, served := serve(1)
	if b, _, err := keyHelperFetch(rv.name, 500*time.Millisecond); err == nil {
		t.Fatalf("a stranger got the request: %s", b)
	}
	close(stop)
	if err := <-served; err == nil {
		t.Fatal("served nobody, and said it had")
	}
	rv.close()

	rv, stop, served = serve(os.Getpid())
	b, from, err := keyHelperFetch(rv.name, 2*time.Second)
	if err != nil || string(b) != `{"v":1}` || from != os.Getpid() {
		t.Fatalf("%q from %d: %v", b, from, err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	close(stop)
	rv.close()
	if _, _, err := keyHelperFetch(rv.name, 200*time.Millisecond); err == nil {
		t.Fatal("the name outlived the request")
	}
}
