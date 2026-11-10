//go:build !darwin

package daemon

import (
	"errors"
	"time"
)

// The app's proof is Mach's (appproof.h): only on a Mac, where the app is.

func (d *Daemon) appProofService() (string, error) { return "", errProofHere }

func sendAppProof(string, uint64, []byte, time.Duration) (string, error) {
	return "", errors.New("this needs a Mac")
}
