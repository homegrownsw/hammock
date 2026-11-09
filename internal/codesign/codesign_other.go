//go:build !darwin

package codesign

// Signature is what a process is signed as.
type Signature struct {
	Identifier string
	Team       string
	Runtime    bool
}

// Self is how this process is signed: nothing is, here.
func Self() (Signature, bool) { return Signature{}, false }

// Check is never satisfied here: there are no code signatures to check.
func Check(int, string) (Signature, bool) { return Signature{}, false }
