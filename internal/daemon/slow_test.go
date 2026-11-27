package daemon

import (
	"os"
	"time"
)

// ciSlowdown is how much longer a wait may take on CI, whose shared VM runs
// this package's parallel daemons several times slower than a Mac does.
const ciSlowdown = 3

// slow stretches a wait's deadline on CI (CI set, as GitHub Actions does).
// A deadline only bounds a wait for something that will happen, so a
// passing test still returns as soon as it does.
func slow(d time.Duration) time.Duration {
	if os.Getenv("CI") != "" {
		return d * ciSlowdown
	}
	return d
}
