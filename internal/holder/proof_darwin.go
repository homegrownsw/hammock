package holder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/rendezvous"
)

// proofSupported: the daemon proves its connections over Mach (proof.go).
const proofSupported = true

// proofHearFor is how long the service waits for a message at a time: it
// looks to see whether the holder is shutting down between waits, which
// otherwise wake nothing while the pane is idle.
var proofHearFor = 30 * time.Second

// proofSvc is a holder's Mach service for its daemon's proofs.
type proofSvc struct {
	once sync.Once
	name string
	err  error
}

// proofService is the name of the holder's Mach service for proofs:
// checked in the first time a client asks to prove itself (so a holder
// starts as fast as ever), and held for the holder's life.
func (s *Server) proofService() (string, error) {
	p := &s.proofSvc
	p.once.Do(func() {
		svc, err := rendezvous.New("com.homegrownsw.hammock.pane.", proofHello)
		if err != nil {
			p.err = fmt.Errorf("can't make a way for the daemon to prove itself (%v)", err)
			return
		}
		p.name = svc.Name
		go s.serveProofs(svc)
	})
	return p.name, p.err
}

// serveProofs takes proofs until the holder shuts down, answering each
// with what became of it. Its own goroutine; the service goes with it.
func (s *Server) serveProofs(svc *rendezvous.Service) {
	defer svc.Close()
	for {
		select {
		case <-s.done:
			return
		default:
		}
		h, err := svc.Hear(proofHearFor, proofBodyLen)
		if errors.Is(err, rendezvous.ErrNothing) {
			continue // nothing yet, or something else, dropped
		}
		if err != nil {
			logf("the daemon's proofs: %v", err)
			return // (connections waiting for one time out)
		}
		_ = h.Answer([]byte(s.proofArrived(h.Body, h.Token)))
	}
}

// sendProof is the daemon's end: proves connection id with nonce to the
// holder's service svc, and returns the holder's answer ("ok", or why not).
func sendProof(svc string, id uint64, nonce []byte, wait time.Duration) (string, error) {
	if len(nonce) != proofNonceLen {
		return "", errors.New("holder: bad nonce")
	}
	body := make([]byte, 8, proofBodyLen)
	binary.BigEndian.PutUint64(body, id)
	body = append(body, nonce...)
	b, _, err := rendezvous.Ask(svc, proofHello, body, wait)
	if err != nil {
		return "", fmt.Errorf("holder: no answer to the proof: %w", err)
	}
	return string(b), nil
}
