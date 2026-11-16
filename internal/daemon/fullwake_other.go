//go:build !darwin

package daemon

// fullyAwake: there's no dark wake off a Mac.
func fullyAwake() bool { return true }
