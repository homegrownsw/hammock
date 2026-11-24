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
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
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

// checkCalendar starts runs for events coming up, and says why calendar
// triggers can't fire when they can't. Callers hold d.mu.
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
			for _, e := range cal.events {
				if !calMatches(c, e) {
					continue
				}
				start := time.UnixMilli(e.StartUnixMs)
				at := start.Add(-time.Duration(c.MinutesBefore) * time.Minute)
				if now.Before(at) || now.After(start.Add(calLate)) || at.UnixMilli() < st.Armed {
					continue
				}
				key := fmt.Sprintf("%s@%d", e.Id, e.StartUnixMs)
				if containsStr(st.Seen, key) {
					continue
				}
				st.Seen = append(st.Seen, key)
				st.Seen = pruneCalSeen(st.Seen, now)
				d.saveTrigState()
				_, _, _ = d.fireEvent(s, calendarEvent(t.Id, c, e, now))
			}
		}
	}
}

// calKey is what a calendar trigger watches, for its trigState.
func calKey(c *pb.CalendarTrigger) string {
	return fmt.Sprintf("%d|%s|%s|%v", c.MinutesBefore, c.Title, c.Calendar, c.WithAttendees)
}

// calendarErrors says why calendar triggers can't fire when they can't,
// and arms new ones (nothing before now fires). Starts nothing. Callers
// hold d.mu.
func (d *Daemon) calendarErrors(now time.Time) {
	cal := d.sched.cal
	problem := ""
	switch {
	case !cal.heard:
		problem = "Open Hammock on this Mac to let it read your calendars"
	case cal.denied:
		problem = "Hammock can't read your calendars: allow it in System Settings ▸ Privacy & Security ▸ Calendars"
	case cal.through > 0 && cal.through < now.UnixMilli():
		problem = "Open Hammock on this Mac: it hasn't read your calendars since " + time.UnixMilli(cal.through).In(now.Location()).Format("Mon 2 Jan")
	}
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
	if mins > 0 {
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
