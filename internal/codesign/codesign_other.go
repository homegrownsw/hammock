//go:build !darwin

package codesign

import "errors"

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

// CheckFile is never satisfied here.
func CheckFile(string, string) bool { return false }

// Token stands for a process's audit token, which only macOS has.
type Token struct{}

// PeerToken: no audit tokens here.
func PeerToken(int) Token { return Token{} }

func TokenFromWords([8]uint32) Token             { return Token{} }
func (Token) Valid() bool                        { return false }
func (Token) PID() int                           { return 0 }
func (Token) Path() (string, error)              { return "", errNoToken }
func (Token) Live() bool                         { return false }
func (Token) SameProcess(Token) bool             { return false }
func CheckToken(Token, string) (Signature, bool) { return Signature{}, false }

var errNoToken = errors.New("no audit tokens here")
