package daemon

// The computer's time zone as it is now. Go reads time.Local once, when the
// process starts, and the daemon runs for weeks: after a flight it would go
// on reading the clock in the zone it left. On macOS and Linux the zone is
// the file /etc/localtime links to, so it's read again from the link (a
// readlink, cheap enough for every check) and loaded by name when it
// changes. With TZ set, that's the zone, and there's nothing to follow.

import (
	"os"
	"strings"
	"sync"
	"time"
)

const localtimeLink = "/etc/localtime"

var sysZone struct {
	sync.Mutex
	target string
	loc    *time.Location
}

// localZone is the computer's time zone now (time.Local if it can't be
// told). The same *time.Location comes back until the zone changes.
func localZone() *time.Location {
	if _, ok := os.LookupEnv("TZ"); ok {
		return time.Local
	}
	target, err := os.Readlink(localtimeLink)
	if err != nil {
		return time.Local
	}
	sysZone.Lock()
	defer sysZone.Unlock()
	if sysZone.loc != nil && target == sysZone.target {
		return sysZone.loc
	}
	sysZone.target, sysZone.loc = target, zoneFromLink(target)
	return sysZone.loc
}

// zoneFromLink loads the zone a /etc/localtime link points to
// (/var/db/timezone/zoneinfo/Europe/London on macOS,
// /usr/share/zoneinfo/Europe/London on Linux), or time.Local.
func zoneFromLink(target string) *time.Location {
	_, name, ok := strings.Cut(target, "zoneinfo/")
	if !ok || name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return loc
}

// localNow is the time now in the computer's zone now, for times shown to
// people.
func localNow() time.Time { return time.Now().In(localZone()) }
