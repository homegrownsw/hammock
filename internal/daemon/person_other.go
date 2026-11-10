//go:build !darwin && !linux

package daemon

import (
	"log/slog"

	"github.com/alexjsp/hammock/internal/codesign"
)

func isHammockApp(int, codesign.Token) bool { return false }
func responsibleFor(int) int                { return 0 }
func personPage(int) bool                   { return false }

// writePersonKey: only Linux's Unraid page needs one (person_linux.go).
func writePersonKey(string, *slog.Logger) {}
