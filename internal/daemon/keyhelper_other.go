//go:build !darwin

package daemon

import (
	"errors"
	"time"
)

// The Hammock Key is the Mac's (its Secure Enclave): no helper elsewhere.

var errNoKeyHelper = errors.New("the Hammock Key needs a Mac")

type keyRendezvous struct{ name string }

func newKeyRendezvous() (*keyRendezvous, error)                   { return nil, errNoKeyHelper }
func (r *keyRendezvous) serve(int, []byte, <-chan struct{}) error { return errNoKeyHelper }
func (r *keyRendezvous) close()                                   {}
func keyHelperFetch(string, time.Duration) ([]byte, int, error)   { return nil, 0, errNoKeyHelper }
