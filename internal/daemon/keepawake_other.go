//go:build !darwin

package daemon

// holdAwake does nothing off a Mac: there it's as good as done.
func holdAwake(on bool) bool { return on }
