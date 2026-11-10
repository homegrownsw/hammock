//go:build !darwin

package holder

import (
	"errors"
	"time"
)

// proofSupported: there's no Mach here, so no proof (proof.go). Holders
// check their client by its socket (SO_PEERCRED on Linux), and clients
// attach without one.
const proofSupported = false

type proofSvc struct{}

var errNoProof = errors.New("holder: proofs need a Mac")

func (s *Server) proofService() (string, error) { return "", errNoProof }

func sendProof(string, uint64, []byte, time.Duration) (string, error) { return "", errNoProof }
