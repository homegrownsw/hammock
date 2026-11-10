//go:build !darwin

// Package rendezvous is Mach's (helpers/common/rendezvous.h): only on a
// Mac. Elsewhere every call fails.
package rendezvous

import (
	"errors"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// ErrNothing is Hear's "nothing for us yet".
var ErrNothing = errors.New("no hello")

var errNoMach = errors.New("this needs a Mac")

// Service is a Mach service: there are none here.
type Service struct{ Name string }

// Hello is a hello a service heard.
type Hello struct {
	Token codesign.Token
	Body  []byte
}

func New(string, string) (*Service, error)                 { return nil, errNoMach }
func (s *Service) Hear(time.Duration, int) (*Hello, error) { return nil, errNoMach }
func (h *Hello) Answer([]byte) error                       { return errNoMach }
func (h *Hello) Drop()                                     {}
func (s *Service) Close()                                  {}
func Ask(string, string, []byte, time.Duration) ([]byte, codesign.Token, error) {
	return nil, codesign.Token{}, errNoMach
}
func Fetch(string, string, time.Duration) ([]byte, codesign.Token, error) {
	return nil, codesign.Token{}, errNoMach
}
func (s *Service) Serve(func(codesign.Token) ([]byte, bool), <-chan struct{}) error {
	return errNoMach
}
