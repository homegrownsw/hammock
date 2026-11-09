//go:build !darwin

package daemon

import "time"

// Off a Mac, hmkd runs on a server (a VPS, an Unraid box): nobody sits at
// it, so it's never in use and its screen never counts. A push waits only
// for your other computers (inUseElsewhere): it goes to your phone once
// none of them is in use, as it would from a Mac you'd walked away from.

func hostScreenLocked() bool { return false }

func hostIdleTime() time.Duration { return headlessIdle }
