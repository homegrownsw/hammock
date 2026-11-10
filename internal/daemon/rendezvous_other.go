//go:build !darwin

package daemon

import (
	"errors"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// The rendezvous is Mach's (helpers/common/rendezvous.h): only on a Mac,
// where the helpers that use it are.

var errNoRendezvous = errors.New("this needs a Mac")

type rendezvous struct{ name string }

func newRendezvous(string, string) (*rendezvous, error) { return nil, errNoRendezvous }
func (r *rendezvous) serve(func(codesign.Token) ([]byte, bool), <-chan struct{}) error {
	return errNoRendezvous
}
func (r *rendezvous) close() {}
func rendezvousFetch(string, string, time.Duration) ([]byte, codesign.Token, error) {
	return nil, codesign.Token{}, errNoRendezvous
}
