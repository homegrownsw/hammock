package daemon

// Calendar triggers (schedtriggers.go): the Mac app reads your calendars
// (EventKit, with your leave) and sends the daemon what's coming up in the
// next few days (CalendarEvents), again as they change. The daemon keeps
// them (so runs happen with the app closed, until they run out) and starts
// a run minutes_before each matching event. Each event fires once (kept
// across restarts), never for one whose moment came before the trigger
// was made, and not long after the event began (the computer was asleep).

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

// calState is the Mac app's calendars, as it last said. Guarded by d.mu.
type calState struct {
	events  []*pb.CalendarEvent
	through int64 // ms: events up to this are all known
	denied  bool
	heard   bool // the app has said, since it was asked (ever, by the record)
}

// calLate is how long after an event's start it may still fire (it was
// due while the computer slept).
const calLate = 5 * time.Minute

const calendarRecord = "calendar"

// loadCalendar reads the events kept from the app. Callers hold d.mu.
func (d *Daemon) loadCalendar() {
	recs, err := d.store.Records(calendarRecord)
	if err != nil {
		return
	}
	if b, ok := recs["events"]; ok {
		m := &pb.CalendarEvents{}
		if proto.Unmarshal(b, m) == nil {
			d.sched.cal = calState{events: m.Events, through: m.ThroughUnixMs, denied: m.Denied, heard: true}
		}
	}
}

// calendarEvents takes the Mac app's calendars. Callers hold d.mu.
func (d *Daemon) calendarEvents(m *pb.CalendarEvents) {
	if len(m.Events) > 5000 {
		m.Events = m.Events[:5000]
	}
	for _, e := range m.Events {
		e.Notes = oneLineKeep(e.Notes, 2000)
		e.Title, e.Calendar, e.Location, e.Url = oneLineKeep(e.Title, 500), oneLineKeep(e.Calendar, 200), oneLineKeep(e.Location, 500), oneLineKeep(e.Url, 2000)
	}
	d.sched.cal = calState{events: m.Events, through: m.ThroughUnixMs, denied: m.Denied, heard: true}
	d.persist(d.store.PutRecord(calendarRecord, "events", m))
	d.checkCalendar(schedTime())
}

// oneLineKeep is s cut to at most n bytes, on a character's boundary
// (its lines kept).
func oneLineKeep(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for len(cut) > 0 && !validUTF8End(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.ToValidUTF8(cut, "")
}

// calMatches: e is an event t starts a run before.
func calMatches(t *pb.CalendarTrigger, e *pb.CalendarEvent) bool {
	if e.Declined {
		return false // (you're not going)
	}
	if t.Title != "" && !strings.Contains(strings.ToLower(e.Title), strings.ToLower(t.Title)) {
		return false
	}
	if t.Calendar != "" && !strings.EqualFold(strings.TrimSpace(e.Calendar), t.Calendar) {
		return false
	}
	if t.WithAttendees && e.Attendees == 0 {
		return false
	}
	return true
}

// checkCalendar starts runs for events coming up (or ending, or free time
// beginning), and says why calendar triggers can't fire when they can't.
// Callers hold d.mu.
func (d *Daemon) checkCalendar(now time.Time) {
	d.calendarErrors(now)
	cal := d.sched.cal
	for _, s := range d.sched.list {
		for _, t := range s.Events {
			c := t.GetCalendar()
			if c == nil || s.Paused || d.sched.off || cal.denied {
				continue
			}
			st := d.trigStateFor(t.Id, calKey(c))
			fire := func(key string, at time.Time, ev *schedEvent) {
				if now.Before(at) || now.After(at.Add(calLate)) || at.UnixMilli() < st.Armed || containsStr(st.Seen, key) {
					return
				}
				st.Seen = append(st.Seen, key)
				st.Seen = pruneCalSeen(st.Seen, now)
				d.saveTrigState()
				ev.Trigger = t.Id
				_, _, _ = d.fireEvent(s, ev)
			}
			if c.When == pb.CalendarTrigger_WHEN_FREE {
				for _, f := range freeStarts(cal.events, c, d.sched.workHours, now.Location()) {
					fire(fmt.Sprintf("free@%d", f.at.UnixMilli()), f.at, freeEvent(f, now))
				}
				continue
			}
			for _, e := range cal.events {
				if !calMatches(c, e) {
					continue
				}
				if c.When == pb.CalendarTrigger_WHEN_END {
					if e.EndUnixMs > e.StartUnixMs {
						fire(fmt.Sprintf("end:%s@%d", e.Id, e.EndUnixMs), time.UnixMilli(e.EndUnixMs), calendarEvent("", c, e, now))
					}
					continue
				}
				start := time.UnixMilli(e.StartUnixMs)
				fire(fmt.Sprintf("%s@%d", e.Id, e.StartUnixMs), start.Add(-time.Duration(c.MinutesBefore)*time.Minute), calendarEvent("", c, e, now))
			}
		}
	}
}

// calKey is what a calendar trigger watches, for its trigState.
func calKey(c *pb.CalendarTrigger) string {
	k := fmt.Sprintf("%d|%s|%s|%v", c.MinutesBefore, c.Title, c.Calendar, c.WithAttendees)
	if c.When != pb.CalendarTrigger_WHEN_START {
		k += fmt.Sprintf("|%d|%d", c.When, c.FreeMinutes)
	}
	return k
}

// calendarProblem says why the calendars the app sent can't be gone by
// now ("" when they can). Callers hold d.mu.
func (d *Daemon) calendarProblem(now time.Time) string {
	cal := d.sched.cal
	switch {
	case !cal.heard:
		return "Open Hammock on this Mac to let it read your calendars"
	case cal.denied:
		return "Hammock can't read your calendars: allow it in System Settings ▸ Privacy & Security ▸ Calendars"
	case cal.through > 0 && cal.through < now.UnixMilli():
		return "Open Hammock on this Mac: it hasn't read your calendars since " + time.UnixMilli(cal.through).In(now.Location()).Format("Mon 2 Jan")
	}
	return ""
}

// busy: e is an event that counts as a meeting: marked busy, not one you
// declined or said maybe to (calendar: only that one's; "": any).
func busy(e *pb.CalendarEvent, calendar string) bool {
	if e.Free || e.Declined || e.Tentative || e.EndUnixMs <= e.StartUnixMs {
		return false
	}
	return calendar == "" || strings.EqualFold(strings.TrimSpace(e.Calendar), calendar)
}

// busyNow: you're in a meeting now, as your calendars say. Callers hold d.mu.
func (d *Daemon) busyNow(now time.Time) bool {
	ms := now.UnixMilli()
	for _, e := range d.sched.cal.events {
		if busy(e, "") && e.StartUnixMs <= ms && ms < e.EndUnixMs {
			return true
		}
	}
	return false
}

// workHours is the config file's working-hours: days, and from and to
// (minutes past midnight; to before from: through midnight).
type workHours struct {
	days     []int32
	from, to int32
}

// workHoursDefault is working-hours unset (or unreadable).
const workHoursDefault = "mon-fri 09:00-17:00"

// parseWorkHours reads working-hours: "[DAYS ]HH:MM-HH:MM" (days as
// --days takes them; none: weekdays).
func parseWorkHours(v string) (workHours, error) {
	v = strings.TrimSpace(v)
	days := "weekdays"
	if i := strings.LastIndexByte(v, ' '); i >= 0 {
		days, v = strings.TrimSpace(v[:i]), v[i+1:]
	}
	var wh workHours
	var err error
	if wh.days, err = schedtrig.ParseDays(days); err != nil {
		return wh, err
	}
	if len(wh.days) == 0 {
		wh.days = []int32{0, 1, 2, 3, 4, 5, 6}
	}
	wh.from, wh.to, err = schedtrig.ParseHours(v)
	return wh, err
}

// configWorkHours is the config file's working-hours (read outside d.mu).
func configWorkHours() workHours {
	v, _ := hammockconfig.Value("working-hours")
	if wh, err := parseWorkHours(cmpOr(v, workHoursDefault)); err == nil {
		return wh
	}
	wh, _ := parseWorkHours(workHoursDefault)
	return wh
}

// in: t is in working hours (a stretch through midnight belongs to the
// day it began).
func (wh workHours) in(t time.Time) bool {
	m := int32(t.Hour()*60 + t.Minute())
	if !schedtrig.InHours(m, wh.from, wh.to) {
		return false
	}
	day := int32(t.Weekday())
	if wh.to < wh.from && m < wh.to {
		day = (day + 6) % 7
	}
	return slices.Contains(wh.days, day)
}

// end is when the working day t is in ends.
func (wh workHours) end(t time.Time) time.Time {
	y, mo, dd := t.Date()
	end := time.Date(y, mo, dd, int(wh.to/60), int(wh.to%60), 0, 0, t.Location())
	if !end.After(t) {
		end = end.AddDate(0, 0, 1)
	}
	return end
}

// freeTime is a stretch of free time: from at until until.
type freeTime struct{ at, until time.Time }

// freeStarts are when free time of at least c's length begins in working
// hours: as a stretch of meetings (ones closer together than that are
// one stretch) ends, with that long before the next or the day's end.
func freeStarts(events []*pb.CalendarEvent, c *pb.CalendarTrigger, wh workHours, loc *time.Location) []freeTime {
	need := time.Duration(schedtrig.FreeMinutes(c)) * time.Minute
	var bs [][2]int64
	for _, e := range events {
		if busy(e, c.Calendar) {
			bs = append(bs, [2]int64{e.StartUnixMs, e.EndUnixMs})
		}
	}
	slices.SortFunc(bs, func(a, b [2]int64) int { return int(a[0] - b[0]) })
	var merged [][2]int64
	for _, b := range bs {
		if n := len(merged); n > 0 && b[0]-merged[n-1][1] < need.Milliseconds() {
			merged[n-1][1] = max(merged[n-1][1], b[1])
			continue
		}
		merged = append(merged, b)
	}
	var out []freeTime
	for i, b := range merged {
		at := time.UnixMilli(b[1]).In(loc)
		if !wh.in(at) {
			continue
		}
		until := wh.end(at)
		if i+1 < len(merged) && time.UnixMilli(merged[i+1][0]).Before(until) {
			until = time.UnixMilli(merged[i+1][0]).In(loc)
		}
		if until.Sub(at) >= need {
			out = append(out, freeTime{at, until})
		}
	}
	return out
}

// freeEvent is the event for free time beginning.
func freeEvent(f freeTime, now time.Time) *schedEvent {
	mins := int(f.until.Sub(f.at).Round(time.Minute).Minutes())
	return &schedEvent{Kind: "calendar", Summary: fmt.Sprintf("Free time begins: %d minutes until %s", mins, f.until.In(now.Location()).Format("15:04")),
		Source: "your calendar", Fields: [][2]string{{"free_from", f.at.In(now.Location()).Format(time.RFC3339)}, {"free_until", f.until.In(now.Location()).Format(time.RFC3339)}, {"free_minutes", fmt.Sprint(mins)}}}
}

// calendarErrors says why calendar triggers can't fire when they can't,
// and arms new ones (nothing before now fires). Starts nothing. Callers
// hold d.mu.
func (d *Daemon) calendarErrors(now time.Time) {
	problem := d.calendarProblem(now)
	for _, s := range d.sched.list {
		for _, t := range s.Events {
			c := t.GetCalendar()
			if c == nil {
				continue
			}
			d.setTriggerError(s, t, problem)
			d.trigStateFor(t.Id, calKey(c))
		}
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// pruneCalSeen drops fired events that began over two days ago.
func pruneCalSeen(seen []string, now time.Time) []string {
	cut := now.Add(-48 * time.Hour).UnixMilli()
	var out []string
	for _, k := range seen {
		var ms int64
		if i := strings.LastIndexByte(k, '@'); i >= 0 {
			_, _ = fmt.Sscan(k[i+1:], &ms)
		}
		if ms >= cut {
			out = append(out, k)
		}
	}
	return out
}

// calendarEvent is the event for a calendar event coming up.
func calendarEvent(trigger string, c *pb.CalendarTrigger, e *pb.CalendarEvent, now time.Time) *schedEvent {
	start := time.UnixMilli(e.StartUnixMs).In(now.Location())
	mins := int(start.Sub(now).Round(time.Minute).Minutes())
	summary := "An event starts now"
	switch {
	case c.When == pb.CalendarTrigger_WHEN_END:
		summary = "An event has ended"
	case mins > 0:
		summary = fmt.Sprintf("An event in %d minutes", mins)
		if mins == 1 {
			summary = "An event in 1 minute"
		}
	}
	f := [][2]string{{"title", e.Title}, {"calendar", e.Calendar}, {"start", start.Format(time.RFC3339)}}
	if e.EndUnixMs > 0 {
		f = append(f, [2]string{"end", time.UnixMilli(e.EndUnixMs).In(now.Location()).Format(time.RFC3339)})
	}
	f = append(f, [2]string{"attendees", fmt.Sprint(e.Attendees)})
	if e.Location != "" {
		f = append(f, [2]string{"location", e.Location})
	}
	if e.Url != "" {
		f = append(f, [2]string{"url", e.Url})
	}
	return &schedEvent{Trigger: trigger, Kind: "calendar", Summary: summary, Source: "your calendar", Fields: f, Body: e.Notes}
}
