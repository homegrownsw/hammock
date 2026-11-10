//go:build !darwin && !linux

package daemon

import "time"

func fileBorn(string) (time.Time, bool) { return time.Time{}, false }
