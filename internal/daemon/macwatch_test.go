package daemon

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fakeMac stands in for the Mac the watcher looks at.
type fakeMac struct {
	mu      sync.Mutex
	power   macPower
	volumes []macVolume
	idle    time.Duration
	locked  bool
}

func (f *fakeMac) setPower(p macPower) { f.mu.Lock(); f.power = p; f.mu.Unlock() }
func (f *fakeMac) setVolumes(v ...macVolume) {
	f.mu.Lock()
	f.volumes = v
	f.mu.Unlock()
}
func (f *fakeMac) setAway(idle time.Duration, locked bool) {
	f.mu.Lock()
	f.idle, f.locked = idle, locked
	f.mu.Unlock()
}

// useFakeMac has the watcher look at f, and never on its own (the test
// looks, with lookAtMac). Before startDaemon.
func useFakeMac(t *testing.T) *fakeMac {
	if runtime.GOOS != "darwin" {
		t.Skip("This Mac triggers are a Mac's")
	}
	f := &fakeMac{}
	oldV, oldP, oldIdle, oldLocked, oldTick := macVolumesNow, macPowerNow, idleTime, screenLocked, macTick
	macVolumesNow = func() []macVolume { f.mu.Lock(); defer f.mu.Unlock(); return append([]macVolume(nil), f.volumes...) }
	macPowerNow = func() macPower { f.mu.Lock(); defer f.mu.Unlock(); return f.power }
	idleTime = func() time.Duration { f.mu.Lock(); defer f.mu.Unlock(); return f.idle }
	screenLocked = func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.locked }
	macTick = time.Hour
	t.Cleanup(func() {
		macVolumesNow, macPowerNow, idleTime, screenLocked, macTick = oldV, oldP, oldIdle, oldLocked, oldTick
	})
	return f
}

// quietMacWatcher stops triggers waking the watcher (the test looks).
func quietMacWatcher(t *testing.T, d *Daemon) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		d.mu.Lock()
		w := d.sched.mac.wake
		d.sched.mac.wake = nil
		d.mu.Unlock()
		if w != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no Mac watcher")
		}
	}
}

func macTrig(m *pb.MacTrigger) *pb.EventTrigger {
	return &pb.EventTrigger{Kind: &pb.EventTrigger_Mac{Mac: m}}
}

// firedAt is when each of s's triggers last fired.
func firedAt(d *Daemon, id string) []int64 {
	var out []int64
	for _, t := range schedOf(d, id).Events {
		out = append(out, t.FiredUnixMs)
	}
	return out
}

const gb = int64(1e9)

// Disk, battery, power and drives fire on a change, never for how things
// were at the first look; a disk or the battery fires once as it falls
// below its mark, and again only after it's been back over it.
func TestMacTriggersFireOnChanges(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	f := useFakeMac(t)
	root := func(freePct int64) macVolume {
		return macVolume{Path: "/", Name: "Macintosh HD", Total: 100 * gb, Avail: freePct * gb}
	}
	f.setVolumes(root(50))
	f.setPower(macPower{Known: true, HasBattery: true, Percent: 50})
	now := time.Now().Truncate(time.Minute)
	clock := useSchedClock(t, now)
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	quietMacWatcher(t, d)
	c := dial(t, dir)
	id := eventSchedule(t, c, work,
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_DISK_LOW, Percent: 10}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_BATTERY_LOW, Percent: 20}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLUGGED_IN}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_UNPLUGGED}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_DRIVE, Name: "back"}),
	)
	look := func(min int) {
		clock.set(now.Add(time.Duration(min) * time.Minute))
		d.lookAtMac()
	}
	look(1) // how things are
	if got := firedAt(d, id); got[0]+got[1]+got[2]+got[3]+got[4] != 0 {
		t.Fatalf("fired at the first look: %v", got)
	}
	f.setVolumes(root(5), macVolume{Path: "/Volumes/Backup", Name: "Backup", Total: gb, Avail: gb, External: true},
		macVolume{Path: "/Volumes/Photos", Name: "Photos", Total: gb, Avail: gb, External: true})
	f.setPower(macPower{Known: true, OnAC: true, HasBattery: true, Percent: 15})
	look(2)
	got := firedAt(d, id)
	at2 := now.Add(2 * time.Minute).UnixMilli()
	if got[0] != at2 || got[1] != at2 || got[2] != at2 || got[3] != 0 || got[4] != at2 {
		t.Fatalf("after things changed: %v", got)
	}
	f.setPower(macPower{Known: true, OnAC: true, HasBattery: true, Percent: 14})
	look(3) // still low: not again
	if g := firedAt(d, id); g[0] != at2 || g[1] != at2 {
		t.Fatalf("fired again while still low: %v", g)
	}
	f.setVolumes(root(10)) // at its mark, not over it
	look(4)
	f.setVolumes(root(5))
	look(5)
	if g := firedAt(d, id); g[0] != at2 {
		t.Fatalf("fired again without having been back over its mark: %v", g)
	}
	f.setVolumes(root(12))
	look(6)
	f.setVolumes(root(4))
	f.setPower(macPower{Known: true, OnAC: false, HasBattery: true, Percent: 14})
	look(7)
	g := firedAt(d, id)
	if at7 := now.Add(7 * time.Minute).UnixMilli(); g[0] != at7 || g[3] != at7 || g[2] != at2 {
		t.Fatalf("after it was back above, then low, and unplugged: %v", g)
	}
	// A trigger on a disk that isn't there says so.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Id: id, Name: "On events", Kind: "command", Cwd: work, Timing: pb.Schedule_TIMING_EVENTS, Prompt: "true",
		Events: []*pb.EventTrigger{macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_DISK_LOW, Volume: "Gone", Gigabytes: 5})},
	}}}})
	look(8)
	if tr := schedOf(d, id).Events[0]; tr.GetMac().GetVolume() != "/Volumes/Gone" || !strings.Contains(tr.Error, "/Volumes/Gone") {
		t.Fatalf("a disk that isn't there: %v", tr)
	}
}

// Away fires once you've been away its minutes (once a stretch); back as
// you return after at least its minutes; wake after a sleep of at least
// its minutes.
func TestMacAwayBackAndWake(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	f := useFakeMac(t)
	now := time.Now().Truncate(time.Minute)
	clock := useSchedClock(t, now)
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	quietMacWatcher(t, d)
	c := dial(t, dir)
	id := eventSchedule(t, c, work,
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_AWAY, Minutes: 15}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_BACK}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WAKE, Minutes: 30}),
	)
	at := func(min int) int64 { return now.Add(time.Duration(min) * time.Minute).UnixMilli() }
	look := func(min int) {
		clock.set(now.Add(time.Duration(min) * time.Minute))
		d.lookAtMac()
	}
	look(0)
	f.setAway(0, true) // locked
	look(1)
	look(2)
	look(10)
	if g := firedAt(d, id); g[0] != 0 {
		t.Fatalf("away fired early: %v", g)
	}
	look(16)
	look(17)
	if g := firedAt(d, id); g[0] != at(16) || g[1] != 0 || g[2] != 0 {
		t.Fatalf("away for 15 minutes: %v", g)
	}
	f.setAway(0, false)
	look(18)
	if g := firedAt(d, id); g[0] != at(16) || g[1] != at(18) {
		t.Fatalf("back: %v", g)
	}
	// Away a short while: back doesn't fire.
	f.setAway(3*time.Minute, false)
	look(21)
	f.setAway(0, false)
	look(22)
	if g := firedAt(d, id); g[1] != at(18) {
		t.Fatalf("back after 4 minutes away fired: %v", g)
	}
	// Asleep: no looks for 20 minutes (too short), then 45.
	look(42)
	if g := firedAt(d, id); g[2] != 0 {
		t.Fatalf("woke after 20 minutes: %v", g)
	}
	look(87)
	if g := firedAt(d, id); g[2] != at(87) {
		t.Fatalf("woke after 45 minutes: %v", g)
	}
	// A dark wake isn't waking: only a full wake is, and the sleep counts
	// from the last full look.
	clock.dark.Store(true)
	look(130)
	if g := firedAt(d, id); g[2] != at(87) {
		t.Fatalf("a dark wake fired: %v", g)
	}
	clock.dark.Store(false)
	look(131)
	if g := firedAt(d, id); g[2] != at(131) {
		t.Fatalf("the full wake after it: %v", g)
	}
}

// The Mac app's Wi-Fi, apps and displays fire what changed since it last
// said, never what was so when it connected; while it isn't open, its
// triggers say so.
func TestMacAppTriggers(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	useFakeMac(t)
	now := time.Now().Truncate(time.Minute)
	clock := useSchedClock(t, now)
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	id := eventSchedule(t, c, work,
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WIFI_LEFT, Name: "Home"}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WIFI_JOINED}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_APP_OPENED, Name: "xcode"}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_APP_QUIT, Name: "com.apple.dt.Xcode"}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_DISPLAY}),
	)
	if e := schedOf(d, id).Events[0].Error; e != "Needs Hammock open" {
		t.Fatalf("with the app closed: %q", e)
	}
	app := &conn{}
	send := func(min int, m *pb.MacAppState) {
		clock.set(now.Add(time.Duration(min) * time.Minute))
		d.mu.Lock()
		d.macAppState(app, m)
		d.mu.Unlock()
	}
	finder := &pb.MacApp{Name: "Finder", BundleId: "com.apple.finder"}
	xcode := &pb.MacApp{Name: "Xcode", BundleId: "com.apple.dt.Xcode"}
	send(1, &pb.MacAppState{WifiKnown: true, Wifi: "Home", Apps: []*pb.MacApp{finder, xcode}, Displays: []*pb.MacDisplay{{Id: "1", Name: "Built-in"}}})
	if g := firedAt(d, id); g[0]+g[1]+g[2]+g[3]+g[4] != 0 {
		t.Fatalf("fired for what was so as it connected: %v", g)
	}
	if e := schedOf(d, id).Events[0].Error; e != "" {
		t.Fatalf("with the app open: %q", e)
	}
	send(2, &pb.MacAppState{WifiKnown: true, Wifi: "Office", Apps: []*pb.MacApp{finder}, Displays: []*pb.MacDisplay{{Id: "1", Name: "Built-in"}, {Id: "2", Name: "Studio Display"}}})
	at2 := now.Add(2 * time.Minute).UnixMilli()
	if g := firedAt(d, id); g[0] != at2 || g[1] != at2 || g[2] != 0 || g[3] != at2 || g[4] != at2 {
		t.Fatalf("after changes: %v", g)
	}
	send(3, &pb.MacAppState{WifiKnown: true, Wifi: "Office", Apps: []*pb.MacApp{finder, xcode}})
	if g := firedAt(d, id); g[2] != now.Add(3*time.Minute).UnixMilli() || g[4] != at2 {
		t.Fatalf("Xcode opened (and a display went): %v", g)
	}
	// Location access taken away: the Wi-Fi triggers say so.
	send(4, &pb.MacAppState{LocationDenied: true, Apps: []*pb.MacApp{finder, xcode}})
	if e := schedOf(d, id).Events[1].Error; !strings.HasPrefix(e, "No Location access") {
		t.Fatalf("no Location access: %q", e)
	}
	d.mu.Lock()
	d.macAppClosed(&conn{}) // another connection: nothing
	closedErr := d.sched.list[id].Events[2].Error
	d.macAppClosed(app)
	d.mu.Unlock()
	if closedErr != "" {
		t.Fatalf("another connection closing: %q", closedErr)
	}
	if e := schedOf(d, id).Events[2].Error; e != "Needs Hammock open" {
		t.Fatalf("the app closed: %q", e)
	}
}

// What changed while the Mac app wasn't watching (its automations were
// off, or places weren't wanted) isn't news when it starts again: the
// first look after the gap only notes how things are.
func TestMacAppStateAfterAGap(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	useFakeMac(t)
	now := time.Now().Truncate(time.Minute)
	clock := useSchedClock(t, now)
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	id := eventSchedule(t, c, work,
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WIFI_JOINED, Name: "Office"}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_ARRIVE, Name: "Office"}),
	)
	app := &conn{}
	send := func(min int, m *pb.MacAppState) {
		clock.set(now.Add(time.Duration(min) * time.Minute))
		d.mu.Lock()
		d.macAppState(app, m)
		d.mu.Unlock()
	}
	places := []string{"Office"}
	send(1, &pb.MacAppState{WifiKnown: true, Wifi: "Home", Places: places, PlacesOut: places})
	// Nothing wants it for a while (the automation paused): it says so.
	send(2, &pb.MacAppState{Idle: true})
	if e := schedOf(d, id).Events[0].Error; e != "" {
		t.Fatalf("idle, with the app open: %q", e)
	}
	send(90, &pb.MacAppState{WifiKnown: true, Wifi: "Office", Places: places, PlacesIn: places})
	if g := firedAt(d, id); g[0]+g[1] != 0 {
		t.Fatalf("fired for what changed while it wasn't watching: %v", g)
	}
	// Out of the office, then places aren't watched for a while (no place
	// wanted), the Wi-Fi still is; back in when they're watched again.
	send(100, &pb.MacAppState{WifiKnown: true, Wifi: "Office", Places: places, PlacesOut: places})
	send(101, &pb.MacAppState{WifiKnown: true, Wifi: "Office"})
	send(160, &pb.MacAppState{WifiKnown: true, Wifi: "Office", Places: places, PlacesIn: places})
	if g := firedAt(d, id); g[1] != 0 {
		t.Fatalf("arrived while places weren't watched: %v", g)
	}
	// Watched throughout, it's news.
	send(220, &pb.MacAppState{WifiKnown: true, Wifi: "Office", Places: places, PlacesOut: places})
	send(280, &pb.MacAppState{WifiKnown: true, Wifi: "Office", Places: places, PlacesIn: places})
	if g := firedAt(d, id); g[1] != now.Add(280*time.Minute).UnixMilli() {
		t.Fatalf("arriving: %v", g)
	}
}

// Only If away, with the watcher's look too old: it never asks ioreg under
// d.mu, using a look a little older instead, else can't tell.
func TestOnlyIfAwayOffTheLock(t *testing.T) {
	useFakeMac(t)
	idleTime = func() time.Duration { t.Error("ioreg asked under d.mu"); return 0 }
	now := time.Now()
	d := &Daemon{}
	cond := &pb.Condition{Kind: pb.Condition_KIND_AWAY}
	d.sched.mac.sample = macSample{at: now.Add(-time.Minute), lookedAway: true, away: true}
	if sm := d.macFreshSample(now, macNeed{away: true}); d.conditionFails(cond, now, sm) != "" {
		t.Errorf("a minute-old look: %+v", sm)
	}
	d.sched.mac.sample.at = now.Add(-10 * time.Minute)
	if sm := d.macFreshSample(now, macNeed{away: true}); d.conditionFails(cond, now, sm) != "Hammock can't tell yet whether you're away" {
		t.Errorf("an old look: %q", d.conditionFails(cond, now, sm))
	}
}

// Only If: a run an automation starts itself is skipped, its record saying
// why, unless every condition holds; Run Now isn't held to them.
func TestOnlyIf(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	f := useFakeMac(t)
	f.setPower(macPower{Known: true, HasBattery: true, Percent: 80})
	f.setVolumes(macVolume{Path: "/", Total: 100 * gb, Avail: 30 * gb})
	now := time.Now().Truncate(time.Minute)
	clock := useSchedClock(t, now)
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	quietMacWatcher(t, d)
	c := dial(t, dir)
	s := &pb.Schedule{Name: "Only if", Kind: "command", Cwd: work, Timing: pb.Schedule_TIMING_EVENTS, Prompt: "true", CooldownSeconds: proto.Int32(0), Overlap: pb.Schedule_OVERLAP_RUN_BOTH,
		Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Webhook{Webhook: &pb.WebhookTrigger{}}}},
		OnlyIf: []*pb.Condition{{Kind: pb.Condition_KIND_POWER}, {Kind: pb.Condition_KIND_DISK, Percent: 20, Wifi: "dropped"}}}
	id := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}}).GetScheduleReply().GetSchedule().GetId()
	if got := schedOf(d, id).OnlyIf[1]; got.Wifi != "" || got.Percent != 20 {
		t.Fatalf("kept: %v", got)
	}
	trigger := func() *pb.ScheduleRun {
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: id, Trigger: true}}})
		return schedOf(d, id).Runs[0]
	}
	if r := trigger(); r.Status != pb.ScheduleRun_STATUS_SKIPPED || r.Note != "Only if: not plugged in" {
		t.Fatalf("on battery: %v", r)
	}
	f.setPower(macPower{Known: true, OnAC: true, HasBattery: true, Percent: 80})
	clock.set(now.Add(time.Minute)) // (a look older than macFresh: it looks again)
	if r := trigger(); r.Status == pb.ScheduleRun_STATUS_SKIPPED {
		t.Fatalf("plugged in: %v", r)
	}
	f.setVolumes(macVolume{Path: "/", Total: 100 * gb, Avail: 15 * gb})
	clock.set(now.Add(2 * time.Minute))
	if r := trigger(); r.Note != "Only if: the startup disk has only 15% free" {
		t.Fatalf("a full disk: %v", r)
	}
	// Run Now runs anyway.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: id}}})
	if r := schedOf(d, id).Runs[0]; r.Status == pb.ScheduleRun_STATUS_SKIPPED || !r.Manual {
		t.Fatalf("Run Now: %v", r)
	}

	// Each kind's words for what doesn't hold.
	day := time.Date(2026, 11, 27, 12, 30, 0, 0, time.Local) // a Friday
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sched.mac.app = nil
	for _, tc := range []struct {
		c    *pb.Condition
		sm   macSample
		want string
	}{
		{&pb.Condition{Kind: pb.Condition_KIND_HOURS, FromMinute: 9 * 60, ToMinute: 12 * 60}, macSample{}, "not between 09:00 and 12:00"},
		{&pb.Condition{Kind: pb.Condition_KIND_HOURS, FromMinute: 22 * 60, ToMinute: 13 * 60}, macSample{}, ""},
		{&pb.Condition{Kind: pb.Condition_KIND_DAYS, Weekdays: []int32{1, 2, 3, 4, 5}}, macSample{}, ""},
		{&pb.Condition{Kind: pb.Condition_KIND_DAYS, Weekdays: []int32{0, 6}}, macSample{}, "not on weekends"},
		{&pb.Condition{Kind: pb.Condition_KIND_WIFI, Wifi: "Home"}, macSample{}, "Hammock isn't open to see the Wi-Fi"},
		{&pb.Condition{Kind: pb.Condition_KIND_AWAY}, macSample{}, "you're not away"},
		{&pb.Condition{Kind: pb.Condition_KIND_AWAY, Minutes: 30}, macSample{away: true, idle: 10 * time.Minute}, "you haven't been away for 30 minutes"},
		{&pb.Condition{Kind: pb.Condition_KIND_AWAY, Minutes: 30}, macSample{away: true, idle: 40 * time.Minute}, ""},
		{&pb.Condition{Kind: pb.Condition_KIND_FREE}, macSample{}, "Hammock can't read your calendars"},
	} {
		if got := d.conditionFails(tc.c, day, tc.sm); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.c, got, tc.want)
		}
	}
	d.sched.mac.app = &pb.MacAppState{WifiKnown: true, Wifi: "home"}
	if got := d.conditionFails(&pb.Condition{Kind: pb.Condition_KIND_WIFI, Wifi: "Home"}, day, macSample{}); got != "" {
		t.Errorf("on the Wi-Fi: %q", got)
	}
	d.sched.cal = calState{heard: true, through: day.Add(time.Hour).UnixMilli(), events: []*pb.CalendarEvent{
		{Id: "m", StartUnixMs: day.Add(-time.Minute).UnixMilli(), EndUnixMs: day.Add(time.Minute).UnixMilli()},
	}}
	if got := d.conditionFails(&pb.Condition{Kind: pb.Condition_KIND_FREE}, day, macSample{}); got != "in a meeting" {
		t.Errorf("in a meeting: %q", got)
	}
	d.sched.cal.events[0].Declined = true
	if got := d.conditionFails(&pb.Condition{Kind: pb.Condition_KIND_FREE}, day, macSample{}); got != "" {
		t.Errorf("a declined meeting: %q", got)
	}
}

// Free time begins as a stretch of meetings ends, in working hours, with
// enough time before the next; meetings close together are one stretch;
// end triggers fire as events end.
func TestCalendarEndAndFreeTime(t *testing.T) {
	loc := time.Local
	day := time.Date(2026, 11, 26, 0, 0, 0, 0, loc) // a Thursday
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	ev := func(id string, h1, m1, h2, m2 int, mod func(*pb.CalendarEvent)) *pb.CalendarEvent {
		e := &pb.CalendarEvent{Id: id, Title: id, Calendar: "Work", StartUnixMs: at(h1, m1).UnixMilli(), EndUnixMs: at(h2, m2).UnixMilli()}
		if mod != nil {
			mod(e)
		}
		return e
	}
	events := []*pb.CalendarEvent{
		ev("early", 7, 0, 8, 0, nil),     // ends before working hours
		ev("standup", 9, 0, 9, 15, nil),  // then 10 minutes, then…
		ev("sync", 9, 25, 10, 0, nil),    // …one stretch, ending 10:00, free until 11:00
		ev("review", 11, 0, 11, 30, nil), // free 11:30-12:00: too short (30 min, needs 45)
		ev("lunch", 12, 0, 13, 0, func(e *pb.CalendarEvent) { e.Free = true }),
		ev("maybe", 12, 0, 12, 30, func(e *pb.CalendarEvent) { e.Tentative = true }),
		ev("nope", 12, 0, 12, 30, func(e *pb.CalendarEvent) { e.Declined = true }),
		ev("1:1", 11, 45, 12, 0, nil),  // with review: 11:00-12:00; free until 16:00
		ev("late", 16, 0, 17, 30, nil), // ends after working hours
	}
	wh, err := parseWorkHours("mon-fri 09:00-17:00")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range freeStarts(events, &pb.CalendarTrigger{When: pb.CalendarTrigger_WHEN_FREE, FreeMinutes: 45}, wh, loc) {
		got = append(got, f.at.Format("15:04")+"-"+f.until.Format("15:04"))
	}
	if strings.Join(got, " ") != "10:00-11:00 12:00-16:00" {
		t.Fatalf("free: %v", got)
	}
	if wh.in(at(10, 0).AddDate(0, 0, 2)) { // a Saturday
		t.Error("a Saturday is in working hours")
	}
	night, _ := parseWorkHours("22:00-06:00")
	if !night.in(at(23, 0)) || !night.in(at(5, 0)) || night.in(at(12, 0)) || !night.end(at(23, 0)).Equal(at(30, 0)) {
		t.Error("overnight working hours")
	}

	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	now := time.Now().Truncate(time.Minute)
	clock := useSchedClock(t, now)
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	id := eventSchedule(t, c, work, &pb.EventTrigger{Kind: &pb.EventTrigger_Calendar{Calendar: &pb.CalendarTrigger{When: pb.CalendarTrigger_WHEN_END, MinutesBefore: 5}}})
	if s := schedOf(d, id); s.Events[0].GetCalendar().MinutesBefore != 0 {
		t.Fatalf("an end trigger kept minutes before: %v", s.Events[0])
	}
	d.mu.Lock()
	d.calendarEvents(&pb.CalendarEvents{ThroughUnixMs: now.Add(72 * time.Hour).UnixMilli(), Events: []*pb.CalendarEvent{
		{Id: "a", Title: "Planning", StartUnixMs: now.Add(5 * time.Minute).UnixMilli(), EndUnixMs: now.Add(20 * time.Minute).UnixMilli()},
	}})
	d.mu.Unlock()
	tick(d, clock, now.Add(6*time.Minute))
	if n := len(schedOf(d, id).Runs); n != 0 {
		t.Fatal("fired as it started")
	}
	tick(d, clock, now.Add(20*time.Minute))
	if out := eventOutputs(t, work, 1); !strings.HasPrefix(out[0], "calendar|An event has ended|") {
		t.Fatalf("env: %q", out)
	}
}

// An automation with what an older Hammock doesn't know isn't sent to a
// linked computer that doesn't say it knows it.
func TestPeerCanSave(t *testing.T) {
	d := &Daemon{}
	d.comp.links = map[string]*pb.StoredLink{
		"us_old": {Record: &pb.Computer{Name: "Studio"}},
		"us_new": {Record: &pb.Computer{Name: "Mini", Features: []string{featureAutomations2}}},
	}
	plain := &pb.Schedule{Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_MERGED}}}}}
	onlyIf := &pb.Schedule{OnlyIf: []*pb.Condition{{Kind: pb.Condition_KIND_POWER}}}
	approved := &pb.Schedule{Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_APPROVED}}}}}
	ending := &pb.Schedule{Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Calendar{Calendar: &pb.CalendarTrigger{When: pb.CalendarTrigger_WHEN_END}}}}}
	if err := d.peerCanSave("us_old", plain); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*pb.Schedule{onlyIf, approved, ending, {Events: []*pb.EventTrigger{macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WAKE})}}} {
		err := d.peerCanSave("us_old", s)
		if err == nil || !strings.Contains(err.Error(), "Studio has an older Hammock") {
			t.Errorf("%v: %v", s, err)
		}
		if err := d.peerCanSave("us_new", s); err != nil {
			t.Errorf("a new one: %v", err)
		}
	}
	real := startDaemon(t, shortDir(t))
	defer real.Shutdown()
	real.mu.Lock()
	r := real.selfRecord(true)
	real.mu.Unlock()
	if !slices.Contains(r.Features, featureAutomations2) {
		t.Error("this computer doesn't say what it can do")
	}
}

// The newer GitHub events: approvals, new issues (not yours), a
// workflow's runs (by name or file), notifications.
func TestGitHubNewerEvents(t *testing.T) {
	var asked [][]string
	oldRun := ghRun
	ghRun = func(_ context.Context, args ...string) ([]byte, error) {
		asked = append(asked, args)
		switch {
		case strings.Contains(args[1], "actions/runs"):
			return []byte(`{"workflow_runs":[{"id":7,"name":"CI","path":".github/workflows/ci.yml","conclusion":"failure","head_branch":"main","html_url":"https://github.com/acme/widgets/actions/runs/7","run_number":12},
				{"id":8,"name":"Deploy","path":".github/workflows/deploy.yml","conclusion":"success"}]}`), nil
		case strings.Contains(args[1], "notifications"):
			return nil, errors.New("gh: Resource not accessible")
		}
		return []byte(`{"data":{}}`), nil
	}
	t.Cleanup(func() { ghRun = oldRun })
	wf := &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_WORKFLOW, Workflow: "ci.yml"}
	nt := &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_NOTIFICATION}
	j := &ghJob{triggers: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Github{Github: wf}}, {Kind: &pb.EventTrigger_Github{Github: nt}}}}
	r, err := ghAsk(context.Background(), "acme/widgets", "me", []*ghJob{j})
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %d times (only REST was needed): %v", len(asked), asked)
	}
	facts, ok := ghFacts(r, wf, "")
	if !ok || len(facts) != 1 || facts[0].key != "wf:7" || facts[0].ev.Summary != "A run of the “ci.yml” workflow finished: failure" {
		t.Fatalf("workflow: %v %+v", ok, facts)
	}
	if m := ghMissing(r, nt); !strings.Contains(m, "not accessible") {
		t.Fatalf("notifications' error: %q", m)
	}
	if _, ok := ghFacts(r, nt, ""); ok {
		t.Fatal("notifications said, though they failed")
	}

	r = &ghResult{repo: "acme/widgets", login: "me", asked: map[string]bool{"mine": true, "issues": true, "notifications": true}}
	pr := ghItem{Number: 4, Title: "Fix"}
	pr.Reviews.Nodes = append(pr.Reviews.Nodes, struct {
		ID     string `json:"id"`
		State  string `json:"state"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}{ID: "r1", State: "APPROVED"}, struct {
		ID     string `json:"id"`
		State  string `json:"state"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}{ID: "r2", State: "COMMENTED"})
	r.mine = []ghItem{pr}
	mineIssue := ghItem{Number: 9}
	mineIssue.Author.Login = "me"
	r.issues = []ghItem{{Number: 10, Title: "Broken"}, mineIssue}
	r.notes = []ghNotification{{ID: "n1", Reason: "review_requested", UpdatedAt: "2026-11-27T10:00:00Z"}}
	r.notes[0].Subject.URL = "https://api.github.com/repos/acme/widgets/pulls/4"
	if f, _ := ghFacts(r, &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_APPROVED}, ""); len(f) != 1 || f[0].key != "ap:r1" {
		t.Errorf("approved: %+v", f)
	}
	if f, _ := ghFacts(r, &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_ISSUE_OPENED}, ""); len(f) != 1 || f[0].key != "io:10" {
		t.Errorf("issues: %+v", f)
	}
	f, _ := ghFacts(r, nt, "")
	if len(f) != 1 || f[0].ev.Summary != "A GitHub notification about acme/widgets (review requested)" || f[0].ev.Fields[4][1] != "https://github.com/acme/widgets/pull/4" {
		t.Errorf("notifications: %+v", f)
	}
}

// A trigger that looks often notes each look at once, but is saved (and
// the apps told) for that alone only now and then.
func TestNoteCheckedNowAndThen(t *testing.T) {
	d := &Daemon{}
	tr := &pb.EventTrigger{Id: "tr_1"}
	now := time.Now()
	if !d.noteChecked(tr, now) {
		t.Fatal("the first look isn't saved")
	}
	if d.noteChecked(tr, now.Add(90*time.Second)) || tr.CheckedUnixMs != now.Add(90*time.Second).UnixMilli() {
		t.Fatal("saved again at once, or not noted")
	}
	if !d.noteChecked(tr, now.Add(checkedSaveEvery+time.Second)) {
		t.Fatal("never saved again")
	}
}

// What counts as a drive you connected.
func TestExternalDrive(t *testing.T) {
	usb := volumeInfo{Path: "/Volumes/Backup", Name: "Backup", Model: "SanDisk", Protocol: "USB", Removable: true, Ejectable: true, Local: true, Browsable: true}
	if !isExternalDrive(usb, false) {
		t.Error("a USB drive")
	}
	for name, v := range map[string]volumeInfo{
		"the startup disk": {Path: "/", Internal: true, Local: true, Browsable: true, Root: true},
		"a disk image":     {Path: "/Volumes/Installer", Model: "Disk Image", Ejectable: true, Local: true, Browsable: true},
		"a simulator's":    {Path: "/Library/Developer/CoreSimulator/Volumes/iOS_23A", Ejectable: true, Local: true, Browsable: true},
		"hidden":           {Path: "/Volumes/.timemachine/x", Ejectable: true, Local: true},
		"a network share":  {Path: "/Volumes/share", Ejectable: true, Browsable: true},
		"an internal disk": {Path: "/Volumes/Data", Internal: true, Local: true, Browsable: true},
	} {
		if isExternalDrive(v, false) {
			t.Errorf("%s counted", name)
		}
	}
	if isExternalDrive(usb, true) {
		t.Error("a Time Machine disk counted")
	}
}
