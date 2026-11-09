//go:build !darwin && !linux

package daemon

func isHammockApp(int) bool          { return false }
func responsibleFor(int) int         { return 0 }
func loginAnchor(int, int, int) bool { return false }
