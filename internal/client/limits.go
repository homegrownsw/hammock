package client

import (
	"fmt"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// LimitResumeWords says when an agent waiting out its usage limit carries
// on (Agent.limit_resume), in this computer's time, as the apps say it:
// "Continues at 15:01", "Continues tomorrow at 09:00", "Continues Thursday
// at 10:00", "Continues 3 Dec at 10:00"; "Tries again at …" when the
// limit's reset isn't known.
func LimitResumeWords(lr *pb.LimitResume, now time.Time) string {
	at := time.UnixMilli(lr.GetAtUnixMs()).Local()
	verb := "Continues"
	if !lr.GetResetKnown() {
		verb = "Tries again"
	}
	now = now.Local()
	day := func(t time.Time) time.Time { y, m, d := t.Date(); return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }
	days := int(day(at).Sub(day(now)).Hours()/24 + 0.5)
	clock := at.Format("15:04")
	switch {
	case days <= 0:
		return fmt.Sprintf("%s at %s", verb, clock)
	case days == 1:
		return fmt.Sprintf("%s tomorrow at %s", verb, clock)
	case days <= 6:
		return fmt.Sprintf("%s %s at %s", verb, at.Format("Monday"), clock)
	}
	return fmt.Sprintf("%s %s at %s", verb, at.Format("2 Jan"), clock)
}
