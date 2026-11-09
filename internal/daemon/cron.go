package daemon

// When a scheduled task runs next (schedules.go): weekdays at a time of
// day, or a five-field cron expression, both in local time.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a parsed cron expression: the allowed values of each field.
type cronSpec struct {
	minute, hour, dom, month, dow uint64 // bit n set: value n allowed
	domStar, dowStar              bool   // the field was * (for the either-day rule)
}

var cronMacros = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

var cronNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// parseCron reads "minute hour day-of-month month day-of-week", with *,
// lists, ranges, steps, names (mon, jan) and the @daily-style shorthands.
// Day of week 7 is Sunday too.
func parseCron(expr string) (*cronSpec, error) {
	expr = strings.TrimSpace(expr)
	if m, ok := cronMacros[strings.ToLower(expr)]; ok {
		expr = m
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, errors.New("a cron expression has five fields: minute hour day-of-month month day-of-week")
	}
	var s cronSpec
	var err error
	parts := []struct {
		name     string
		lo, hi   int
		dst      *uint64
		starFlag *bool
	}{
		{"minute", 0, 59, &s.minute, nil}, {"hour", 0, 23, &s.hour, nil},
		{"day of month", 1, 31, &s.dom, &s.domStar}, {"month", 1, 12, &s.month, nil},
		{"day of week", 0, 7, &s.dow, &s.dowStar},
	}
	for i, p := range parts {
		if *p.dst, err = cronField(f[i], p.lo, p.hi); err != nil {
			return nil, fmt.Errorf("cron %s: %w", p.name, err)
		}
		if p.starFlag != nil {
			// As cron does: a field starting with * (*/2 too) doesn't
			// restrict the day for the either-day rule.
			*p.starFlag = strings.HasPrefix(f[i], "*") || f[i] == "?"
		}
	}
	if s.dow&(1<<7) != 0 {
		s.dow |= 1 // 7 is Sunday
	}
	return &s, nil
}

func cronField(field string, lo, hi int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 {
				return 0, fmt.Errorf("bad step in %q", part)
			}
			step, part = n, part[:i]
		}
		a, b := lo, hi
		switch {
		case part == "*" || part == "?":
		case strings.Contains(part, "-"):
			x, y, _ := strings.Cut(part, "-")
			var err error
			if a, err = cronValue(x); err != nil {
				return 0, err
			}
			if b, err = cronValue(y); err != nil {
				return 0, err
			}
		default:
			v, err := cronValue(part)
			if err != nil {
				return 0, err
			}
			a, b = v, v
			if step > 1 {
				b = hi // "5/15": from 5, every 15
			}
		}
		if a < lo || b > hi || a > b {
			return 0, fmt.Errorf("%q is outside %d–%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

func cronValue(s string) (int, error) {
	if v, ok := cronNames[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q isn't a number", s)
	}
	return v, nil
}

func (s *cronSpec) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<t.Day()) != 0
	dow := s.dow&(1<<int(t.Weekday())) != 0
	// Cron's rule: with both day fields restricted, either may match.
	if !s.domStar && !s.dowStar {
		return dom || dow
	}
	return dom && dow
}

// next is the first time after t that matches (zero if none within a few
// years). Around clock changes: a time in the hour skipped in spring runs
// when the clock resumes (02:30 at 03:30; 00:30 at 01:30 where the change
// is at midnight), and one in the hour repeated in
// autumn runs once, the first time.
func (s *cronSpec) next(t time.Time) time.Time {
	loc := t.Location()
	prev := t.Truncate(time.Minute) // (so a jump in the very next minute counts)
	t = prev.Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	allHours := s.hour == 1<<24-1
	for t.Before(limit) {
		// The clock jumped forward since prev: an hour it skipped that's due
		// runs now, at the minute it would have. Within a day, the hours
		// between; on a new day that starts after 00:00 (some zones change
		// at midnight), those before its first.
		gapFrom := -1
		if sameDay(t, prev) {
			if t.Hour() > prev.Hour()+1 {
				gapFrom = prev.Hour() + 1
			}
		} else if t.Hour() > 0 {
			gapFrom = 0
		}
		if gapFrom >= 0 && s.month&(1<<int(t.Month())) != 0 && s.dayMatches(t) {
			for h := gapFrom; h < t.Hour(); h++ {
				if s.hour&(1<<h) != 0 {
					for m := 0; m < 60; m++ {
						if s.minute&(1<<m) != 0 {
							return time.Date(t.Year(), t.Month(), t.Day(), h, m, 0, 0, loc) // (normalised past the gap)
						}
					}
				}
			}
		}
		prev = t
		if s.month&(1<<int(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if s.hour&(1<<t.Hour()) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			continue
		}
		if s.minute&(1<<t.Minute()) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		// The second pass of an hour repeated in autumn: it ran the first time.
		if !allHours {
			if b := t.Add(-time.Hour); b.Hour() == t.Hour() && b.YearDay() == t.YearDay() {
				t = t.Add(time.Minute)
				continue
			}
		}
		return t
	}
	return time.Time{}
}

func sameDay(a, b time.Time) bool {
	return a.YearDay() == b.YearDay() && a.Year() == b.Year()
}

// weekdaySpec is the cron form of "these weekdays (none: every day) at
// minute m of the day".
func weekdaySpec(days []int32, m int32) *cronSpec {
	s := &cronSpec{minute: 1 << (m % 60), hour: 1 << (m / 60), dom: ^uint64(0), month: ^uint64(0), domStar: true}
	if len(days) == 0 {
		s.dow, s.dowStar = ^uint64(0), true
	}
	for _, d := range days {
		s.dow |= 1 << d
	}
	return s
}
