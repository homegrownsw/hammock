//go:build !linux

package holder

// becomeSubreaper: only Linux has subreapers. On macOS a program that
// leaves a pane is found by the holder macOS holds responsible for it
// (the daemon's person.go).
func becomeSubreaper() {}

func reapOrphans(int, <-chan struct{}) {}
