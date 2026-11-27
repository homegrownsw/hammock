package daemon

// Only If (pb.Schedule.only_if): conditions a run an automation starts by
// itself (at its times, or on a trigger; not Run Now) must meet, or it's
// skipped, its record saying which didn't hold ("Only if: not plugged
// in"). They're looked at as the run would start: power, disk and you
// being away from the watcher's last look at the Mac (macwatch.go), or a
// new one; the Wi-Fi network and the place the Mac is at from the Mac app
// (so they never hold while the app is closed); a meeting from the
// calendars it sent.

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

// maxConditions is how many conditions an automation may have.
const maxConditions = 10

// cleanConditions checks s's Only If conditions as sent. Callers hold d.mu.
func cleanConditions(s *pb.Schedule) error {
	if len(s.OnlyIf) > maxConditions {
		return fmt.Errorf("at most %d Only If conditions", maxConditions)
	}
	for _, c := range s.OnlyIf {
		if _, ok := pb.Condition_Kind_name[int32(c.Kind)]; !ok || c.Kind == pb.Condition_KIND_UNSPECIFIED {
			return errors.New("an unknown Only If condition (from a newer Hammock?)")
		}
		if schedtrig.ConditionMacOnly(c) && runtime.GOOS != "darwin" {
			return fmt.Errorf("Only if %s: that needs a Mac", schedtrig.DescribeCondition(c))
		}
		switch c.Kind {
		case pb.Condition_KIND_AWAY:
			if c.Minutes < 0 || c.Minutes > 7*24*60 {
				return errors.New("Only If away: 0 to 10080 minutes")
			}
		case pb.Condition_KIND_WIFI:
			c.Wifi = strings.TrimSpace(c.Wifi)
			if c.Wifi == "" || len(c.Wifi) > 64 || strings.ContainsFunc(c.Wifi, func(r rune) bool { return r < 0x20 || r == 0x7f || bidiControl(r) || directionMark(r) }) {
				return errors.New("Only If Wi-Fi: give the network's name, on one line")
			}
		case pb.Condition_KIND_PLACE:
			c.Place = strings.TrimSpace(c.Place)
			if c.Place == "" || len([]rune(c.Place)) > schedtrig.MaxPlaceName || strings.ContainsFunc(c.Place, func(r rune) bool { return r < 0x20 || r == 0x7f || bidiControl(r) || directionMark(r) }) {
				return errors.New("Only If place: give the place's name, on one line")
			}
		case pb.Condition_KIND_HOURS:
			if c.FromMinute < 0 || c.FromMinute > 24*60 || c.ToMinute < 0 || c.ToMinute > 24*60 || c.FromMinute == c.ToMinute {
				return errors.New("Only If hours: from and until are times of day, and differ")
			}
		case pb.Condition_KIND_DAYS:
			days := map[int32]bool{}
			for _, w := range c.Weekdays {
				if w < 0 || w > 6 {
					return fmt.Errorf("Only If days: %d isn't a weekday (0 is Sunday, 6 Saturday)", w)
				}
				days[w] = true
			}
			if len(days) == 0 {
				return errors.New("Only If days: which days?")
			}
			c.Weekdays = nil
			for w := int32(0); w < 7; w++ {
				if days[w] {
					c.Weekdays = append(c.Weekdays, w)
				}
			}
		case pb.Condition_KIND_DISK:
			if c.Percent < 1 || c.Percent > 99 {
				return errors.New("Only If disk: more than 1 to 99% free")
			}
		}
		// What doesn't apply to its kind isn't kept.
		if c.Kind != pb.Condition_KIND_AWAY {
			c.Minutes = 0
		}
		if c.Kind != pb.Condition_KIND_WIFI {
			c.Wifi = ""
		}
		if c.Kind != pb.Condition_KIND_PLACE {
			c.Place = ""
		}
		if c.Kind != pb.Condition_KIND_HOURS {
			c.FromMinute, c.ToMinute = 0, 0
		}
		if c.Kind != pb.Condition_KIND_DAYS {
			c.Weekdays = nil
		}
		if c.Kind != pb.Condition_KIND_DISK {
			c.Percent = 0
		}
	}
	return nil
}

// onlyIfWhy says which of s's conditions doesn't hold now, as its skipped
// run's record says it ("Only if: not plugged in"), or "" if they all
// do. Callers hold d.mu.
func (d *Daemon) onlyIfWhy(s *pb.Schedule, now time.Time) string {
	var need macNeed
	for _, c := range s.OnlyIf {
		switch c.Kind {
		case pb.Condition_KIND_POWER:
			need.power = true
		case pb.Condition_KIND_AWAY:
			need.away = true
		case pb.Condition_KIND_DISK:
			need.volumes = true
		}
	}
	var sm macSample
	if need.any() {
		sm = d.macFreshSample(schedNow(), need)
	}
	for _, c := range s.OnlyIf {
		if why := d.conditionFails(c, now, sm); why != "" {
			return "Only if: " + why
		}
	}
	return ""
}

// conditionFails says how c doesn't hold now ("not plugged in"), or "".
// Callers hold d.mu.
func (d *Daemon) conditionFails(c *pb.Condition, now time.Time, sm macSample) string {
	now = now.In(schedZone())
	switch c.Kind {
	case pb.Condition_KIND_POWER:
		switch {
		case !sm.power.Known:
			return "Hammock can't tell whether the Mac is plugged in"
		case !sm.power.OnAC:
			return "not plugged in"
		}
	case pb.Condition_KIND_AWAY:
		if !sm.away {
			return "you're not away"
		}
		if c.Minutes > 0 {
			since := d.sched.mac.awaySince
			if since.IsZero() {
				since = now.Add(-sm.idle)
			}
			if now.Sub(since) < time.Duration(c.Minutes)*time.Minute {
				return "you haven't been away for " + humanMinutes(time.Duration(c.Minutes)*time.Minute)
			}
		}
	case pb.Condition_KIND_WIFI:
		app := d.sched.mac.app
		switch {
		case app == nil:
			return "Hammock isn't open to see the Wi-Fi"
		case app.LocationDenied || !app.WifiKnown:
			return "Hammock has no Location access to see the Wi-Fi"
		case !strings.EqualFold(strings.TrimSpace(app.Wifi), c.Wifi):
			return "not on the Wi-Fi “" + c.Wifi + "”"
		}
	case pb.Condition_KIND_PLACE:
		app := d.sched.mac.app
		in, known := d.placeIn(c.Place)
		switch {
		case app == nil:
			return "Hammock isn't open to see where the Mac is"
		case app.LocationDenied:
			return "Hammock has no Location access to see where the Mac is"
		case !known:
			return "Hammock can't tell yet whether the Mac is at “" + c.Place + "”"
		case !in:
			return "not at “" + c.Place + "”"
		}
	case pb.Condition_KIND_HOURS:
		if !schedtrig.InHours(int32(now.Hour()*60+now.Minute()), c.FromMinute, c.ToMinute) {
			return "not " + schedtrig.DescribeCondition(c)
		}
	case pb.Condition_KIND_DAYS:
		if !slices.Contains(c.Weekdays, int32(now.Weekday())) {
			return "not " + schedtrig.DescribeCondition(c)
		}
	case pb.Condition_KIND_FREE:
		if p := d.calendarProblem(now); p != "" {
			return "Hammock can't read your calendars"
		}
		if d.busyNow(now) {
			return "in a meeting"
		}
	case pb.Condition_KIND_DISK:
		v := findVolume(sm.volumes, "/")
		if v == nil || v.Total <= 0 {
			return "Hammock can't tell how full the startup disk is"
		}
		if free := float64(v.Avail) * 100 / float64(v.Total); free <= float64(c.Percent) {
			return fmt.Sprintf("the startup disk has only %d%% free", int(free))
		}
	default:
		return "an unknown condition"
	}
	return ""
}

// onlyIfNeedsCalendar: some automation here has a free (not in a meeting)
// condition. Callers hold d.mu.
func (d *Daemon) onlyIfNeedsCalendar() bool {
	for _, s := range d.sched.list {
		for _, c := range s.OnlyIf {
			if c.Kind == pb.Condition_KIND_FREE {
				return true
			}
		}
	}
	return false
}
