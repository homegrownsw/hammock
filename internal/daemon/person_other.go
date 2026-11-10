//go:build !darwin && !linux

package daemon

import "github.com/alexjsp/hammock/internal/codesign"

func isHammockApp(int, codesign.Token) bool { return false }
func responsibleFor(int) int                { return 0 }
func loginAnchor(int, int, int) bool        { return false }
