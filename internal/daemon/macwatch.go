package daemon

// This Mac triggers (pb.MacTrigger; schedtriggers.go). The daemon looks at
// the Mac itself every macTick while a trigger or Only If condition needs
// it (so these work with the app closed): its disks' free space (as the
// Finder counts it, purgeable space included), its battery and power,
// waking from sleep, you being away from it (push.go's awayRule) and
// coming back, and external drives being connected. The Mac app tells it
// what it can't see itself (MacAppState): the Wi-Fi network, the apps
// open, the displays connected and which of its Places the Mac is at (by
// name only), which fire only while the app is open.
// Each trigger fires on a change: what's already so when it's made (or
// the daemon starts) doesn't, and a disk or battery trigger fires once as
// it falls below its mark, then again only after it's been back above it.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

var (
	// macTick is how often the Mac is looked at; macWakeGap a gap between
	// looks (while fully awake) long enough to have been asleep.
	macTick    = 15 * time.Second
	macWakeGap = 90 * time.Second
	// macFresh is how old a look may be for an Only If condition; older,
	// it looks again.
	macFresh = 45 * time.Second
	// macAwayStale is how old the watcher's look at you being away may be
	// for an Only If condition when it can't look again (macFreshSample).
	macAwayStale = 90 * time.Second
	// What the looks use (tests replace them).
	macVolumesNow = hostVolumes
	macPowerNow   = hostPower
)

// macVolume is a mounted volume. External: a drive you connected (not
// the startup disk, a disk image, a Time Machine backup or a simulator's).
type macVolume struct {
	Path, Name   string
	Total, Avail int64 // bytes; Avail as the Finder counts it
	External     bool
}

// macPower is where the Mac's power comes from.
type macPower struct {
	Known, OnAC, HasBattery bool
	Percent                 int
}

// macSample is one look at the Mac: only what was needed is looked at.
type macSample struct {
	at                         time.Time
	lookedPower, lookedVolumes bool
	lookedAway                 bool
	power                      macPower
	volumes                    []macVolume
	away                       bool
	idle                       time.Duration
}

// macState is what the daemon knows of the Mac. Guarded by d.mu.
type macState struct {
	wake        chan struct{}
	sample      macSample
	awaySince   time.Time // you've been away since (zero: you're here, or it isn't watched)
	lastPresent time.Time // the last look that found you here
	lastAwake   time.Time // the last look while fully awake
	// The Mac app's view (nil: it isn't open, or hasn't said since it
	// connected), and its connection.
	app     *pb.MacAppState
	appConn *conn
	// Each place's last known state (in: true) from the app's connection:
	// one it can't tell for now keeps the last, so in, unknown, then out
	// is still leaving.
	places map[string]bool
}

// macTrigState is what a This Mac trigger has seen (in its trigState).
type macTrigState struct {
	Primed  bool     `json:"primed"`             // it's had a first look
	Below   bool     `json:"below,omitempty"`    // disk, battery: below its mark (fired)
	OnAC    bool     `json:"ac,omitempty"`       // power: plugged in
	Drives  []string `json:"drives,omitempty"`   // external drives connected
	AwayFor int64    `json:"away_for,omitempty"` // away: the stretch (its start, ms) it fired for
}

// volumeInfo is what's known of a volume, to tell an external drive.
type volumeInfo struct {
	Path, Name, Model, Protocol                            string
	Internal, Removable, Ejectable, Local, Browsable, Root bool
}

// externalDrive: a volume is a drive you connected: local, shown in the
// Finder, external or removable, and not a disk image, a Time Machine
// backup or snapshot, or a simulator runtime's volume.
func externalDrive(path, name, model, protocol string, internal, removable, ejectable, local, browsable, root bool) bool {
	v := volumeInfo{path, name, model, protocol, internal, removable, ejectable, local, browsable, root}
	return isExternalDrive(v, timeMachineVolume(path))
}

func isExternalDrive(v volumeInfo, timeMachine bool) bool {
	switch {
	case v.Root, !v.Local, !v.Browsable, v.Internal && !v.Removable && !v.Ejectable, timeMachine:
		return false
	case strings.Contains(strings.ToLower(v.Model), "disk image"), strings.EqualFold(v.Protocol, "disk image"), strings.EqualFold(v.Protocol, "virtual interface"):
		return false
	case strings.HasPrefix(v.Path, "/Library/Developer/"), strings.Contains(v.Path, "/CoreSimulator/"),
		strings.HasPrefix(v.Path, "/Volumes/."), strings.HasPrefix(v.Path, "/System/"), strings.HasPrefix(v.Path, "/private/"):
		return false
	case strings.Contains(strings.ToLower(v.Path), "timemachine"), strings.Contains(strings.ToLower(v.Path), "com.apple.timemachine"):
		return false
	}
	return true
}

// timeMachineVolume: path is a Time Machine backup disk.
var timeMachineVolume = func(path string) bool {
	for _, f := range []string{"Backups.backupdb", ".com.apple.backupd.mvlist.plist", ".com.apple.TimeMachine.SnapshotHistory.plist"} {
		if exists(filepath.Join(path, f)) {
			return true
		}
	}
	return false
}

// watchMac looks at the Mac every macTick, until the daemon stops.
func (d *Daemon) watchMac() {
	wake := make(chan struct{}, 1)
	d.mu.Lock()
	d.sched.mac.wake = wake
	d.mu.Unlock()
	t := time.NewTicker(macTick)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		case <-wake:
		}
		d.lookAtMac()
	}
}

// macNeed is what of the Mac is worth looking at.
type macNeed struct{ power, volumes, away bool }

func (n macNeed) any() bool { return n.power || n.volumes || n.away }

// macNeeds is what the triggers and conditions that can act now need.
// Callers hold d.mu.
func (d *Daemon) macNeeds() macNeed {
	var n macNeed
	for _, s := range d.sched.list {
		if s.Paused || d.sched.off {
			continue
		}
		for _, t := range s.Events {
			switch t.GetMac().GetEvent() {
			case pb.MacTrigger_EVENT_DISK_LOW, pb.MacTrigger_EVENT_DRIVE:
				n.volumes = true
			case pb.MacTrigger_EVENT_BATTERY_LOW, pb.MacTrigger_EVENT_PLUGGED_IN, pb.MacTrigger_EVENT_UNPLUGGED:
				n.power = true
			case pb.MacTrigger_EVENT_AWAY, pb.MacTrigger_EVENT_BACK:
				n.away = true
			}
		}
		for _, c := range s.OnlyIf {
			switch c.Kind {
			case pb.Condition_KIND_POWER:
				n.power = true
			case pb.Condition_KIND_AWAY:
				n.away = true
			case pb.Condition_KIND_DISK:
				n.volumes = true
			}
		}
	}
	return n
}

// lookAtMac has one look at what's needed, and fires what changed.
func (d *Daemon) lookAtMac() {
	now := schedNow()
	awake := schedAwake()
	d.mu.Lock()
	need := d.macNeeds()
	d.mu.Unlock()
	sm := d.sampleMac(now, need)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.finishSample(&sm, need)
	d.macLooked(now, awake, sm)
}

// sampleMac looks at what need says, but you being away (finishSample).
// Off d.mu: it asks IOKit and runs ioreg.
func (d *Daemon) sampleMac(now time.Time, need macNeed) macSample {
	sm := macSample{at: now}
	if need.power {
		sm.power, sm.lookedPower = macPowerNow(), true
	}
	if need.volumes {
		sm.volumes, sm.lookedVolumes = macVolumesNow(), true
	}
	if need.away {
		sm.idle, sm.away, sm.lookedAway = idleTime(), screenLocked(), true // (away: locked, for now)
	}
	return sm
}

// finishSample works out whether you're away (push.go's rule: locked, or
// untouched a couple of minutes, or using Hammock on your phone or at
// another of your computers since). Callers hold d.mu.
func (d *Daemon) finishSample(sm *macSample, need macNeed) {
	if need.away {
		locked := sm.away
		sm.away = awayRule(d.usingPhone(sm.idle), d.inUseElsewhere(), true, locked, sm.idle)
	}
}

// macLooked takes a look at the Mac: notes it, and fires the triggers it
// sets off. Callers hold d.mu.
func (d *Daemon) macLooked(now time.Time, awake bool, sm macSample) {
	st := &d.sched.mac
	st.sample = sm
	// Waking: a look while fully awake, long after the last such look.
	var slept time.Duration
	if awake {
		if !st.lastAwake.IsZero() && now.Sub(st.lastAwake) > macWakeGap {
			slept = now.Sub(st.lastAwake)
		}
		st.lastAwake = now
	}
	// Away and back.
	var back time.Duration
	switch {
	case !sm.lookedAway:
		st.awaySince, st.lastPresent = time.Time{}, time.Time{}
	case sm.away && st.awaySince.IsZero():
		since := now.Add(-min(sm.idle, 24*time.Hour))
		if since.Before(st.lastPresent) {
			since = st.lastPresent
		}
		st.awaySince = since
	case !sm.away:
		if !st.awaySince.IsZero() {
			back = now.Sub(st.awaySince)
		}
		st.awaySince, st.lastPresent = time.Time{}, now
	}
	for _, s := range d.sched.list {
		for _, t := range d.eventsOf(s) {
			if m := t.GetMac(); m != nil && !schedtrig.MacAppWatched(m.Event) {
				d.macTrigger(s, t, m, now, sm, slept, back)
			}
		}
	}
}

// macTrigger checks one of the daemon's own This Mac triggers against a
// look. Callers hold d.mu.
func (d *Daemon) macTrigger(s *pb.Schedule, t *pb.EventTrigger, m *pb.MacTrigger, now time.Time, sm macSample, slept, back time.Duration) {
	ts := d.trigStateFor(t.Id, "mac:"+schedtrig.Spec(t))
	if ts.Mac == nil {
		ts.Mac = &macTrigState{}
	}
	ms := ts.Mac
	fire := func(summary string, fields ...[2]string) {
		_, _, _ = d.fireEvent(s, &schedEvent{Trigger: t.Id, Kind: "mac", Summary: summary, Source: "this Mac", Fields: fields})
	}
	// edge fires as what's watched falls below its mark, and re-arms once
	// it's back above (a little over it, so hovering there fires once).
	edge := func(below, above bool, summary string, fields ...[2]string) {
		switch {
		case !ms.Primed:
			ms.Primed, ms.Below = true, below
		case below && !ms.Below:
			ms.Below = true
			fire(summary, fields...)
		case ms.Below && above:
			ms.Below = false
		default:
			return
		}
		d.saveTrigState()
	}
	switch m.Event {
	case pb.MacTrigger_EVENT_DISK_LOW:
		if !sm.lookedVolumes {
			return
		}
		v := findVolume(sm.volumes, m.Volume)
		if v == nil {
			d.setTriggerError(s, t, "There's no disk at "+cmpOr(m.Volume, "/")+" now")
			return
		}
		d.setTriggerError(s, t, "")
		below, above := diskBelow(m, *v)
		edge(below, above, schedtrig.DiskWords(m.Volume)+" is low on space",
			[2]string{"volume", v.Path}, [2]string{"free", diskFreeWords(*v)}, [2]string{"free_bytes", fmt.Sprint(v.Avail)}, [2]string{"total_bytes", fmt.Sprint(v.Total)})
	case pb.MacTrigger_EVENT_BATTERY_LOW:
		if !sm.lookedPower || !sm.power.Known {
			return
		}
		if !sm.power.HasBattery {
			d.setTriggerError(s, t, "This Mac has no battery")
			return
		}
		d.setTriggerError(s, t, "")
		mark := int(m.Percent)
		if mark <= 0 {
			mark = schedtrig.MacBatteryPercentDefault
		}
		edge(sm.power.Percent < mark, sm.power.Percent > mark, fmt.Sprintf("The battery is down to %d%%", sm.power.Percent),
			[2]string{"battery", fmt.Sprintf("%d%%", sm.power.Percent)}, [2]string{"plugged_in", fmt.Sprint(sm.power.OnAC)})
	case pb.MacTrigger_EVENT_PLUGGED_IN, pb.MacTrigger_EVENT_UNPLUGGED:
		if !sm.lookedPower || !sm.power.Known {
			return
		}
		was, is := ms.OnAC, sm.power.OnAC
		if ms.Primed && was == is {
			return
		}
		primed := ms.Primed
		ms.Primed, ms.OnAC = true, is
		d.saveTrigState()
		if !primed {
			return
		}
		fields := [][2]string{}
		if sm.power.HasBattery {
			fields = append(fields, [2]string{"battery", fmt.Sprintf("%d%%", sm.power.Percent)})
		}
		switch {
		case is && m.Event == pb.MacTrigger_EVENT_PLUGGED_IN:
			fire("The Mac was plugged in", fields...)
		case !is && m.Event == pb.MacTrigger_EVENT_UNPLUGGED:
			fire("The Mac was unplugged", fields...)
		}
	case pb.MacTrigger_EVENT_WAKE:
		if slept > 0 && slept >= time.Duration(m.Minutes)*time.Minute {
			fire("The Mac woke after sleeping "+humanMinutes(slept), [2]string{"slept_minutes", fmt.Sprint(int(slept.Minutes()))})
		}
	case pb.MacTrigger_EVENT_AWAY:
		st := &d.sched.mac
		if !sm.lookedAway || !sm.away || st.awaySince.IsZero() {
			return
		}
		need := time.Duration(max(m.Minutes, 1)) * time.Minute
		since := st.awaySince.UnixMilli()
		if now.Sub(st.awaySince) < need || ms.AwayFor == since {
			return
		}
		ms.AwayFor = since
		d.saveTrigState()
		if st.awaySince.Add(need).UnixMilli() < ts.Armed {
			return // away already when it was made
		}
		fire("You've been away from the Mac for "+humanMinutes(now.Sub(st.awaySince)), [2]string{"away_since", st.awaySince.In(schedZone()).Format(time.RFC3339)})
	case pb.MacTrigger_EVENT_BACK:
		if back > 0 && back >= time.Duration(schedtrig.BackMinutes(m))*time.Minute {
			fire("You're back at the Mac after "+humanMinutes(back)+" away", [2]string{"away_minutes", fmt.Sprint(int(back.Minutes()))})
		}
	case pb.MacTrigger_EVENT_DRIVE:
		if !sm.lookedVolumes {
			return
		}
		var cur []string
		byKey := map[string]macVolume{}
		for _, v := range sm.volumes {
			if v.External {
				k := v.Path + "\x00" + v.Name
				cur = append(cur, k)
				byKey[k] = v
			}
		}
		slices.Sort(cur)
		if ms.Primed && slices.Equal(cur, ms.Drives) {
			return
		}
		primed, prev := ms.Primed, ms.Drives
		ms.Primed, ms.Drives = true, cur
		d.saveTrigState()
		if !primed {
			return
		}
		for _, k := range cur {
			v := byKey[k]
			if slices.Contains(prev, k) || m.Name != "" && !strings.Contains(strings.ToLower(v.Name), strings.ToLower(m.Name)) {
				continue
			}
			summary := "A drive was connected"
			if m.Name != "" {
				summary = "A drive named “" + m.Name + "” was connected"
			}
			fire(summary, [2]string{"drive", v.Name}, [2]string{"path", v.Path}, [2]string{"free", diskFreeWords(v)})
		}
	}
}

// findVolume is the volume mounted at path (a name: /Volumes/NAME; "": the
// startup disk).
func findVolume(vols []macVolume, path string) *macVolume {
	if path == "" {
		path = "/"
	}
	for i, v := range vols {
		if v.Path == path {
			return &vols[i]
		}
	}
	return nil
}

// diskBelow: v's free space is below m's mark, or (above) back over it, a
// little (1% or 1 GB) over.
func diskBelow(m *pb.MacTrigger, v macVolume) (below, above bool) {
	if m.Gigabytes > 0 {
		mark := int64(m.Gigabytes) * 1e9
		return v.Avail < mark, v.Avail >= mark+1e9
	}
	if v.Total <= 0 {
		return false, false
	}
	mark := float64(m.Percent)
	if mark <= 0 {
		mark = schedtrig.MacDiskPercentDefault
	}
	free := float64(v.Avail) * 100 / float64(v.Total)
	return free < mark, free >= mark+1
}

// diskFreeWords says how much of v is free: "42 GB (8%)".
func diskFreeWords(v macVolume) string {
	pct := 0
	if v.Total > 0 {
		pct = int(v.Avail * 100 / v.Total)
	}
	return fmt.Sprintf("%.0f GB (%d%%)", float64(v.Avail)/1e9, pct)
}

// macFreshSample is a look at the Mac no older than macFresh with what
// need says, for Only If conditions: the watcher's last, else a new one.
// Callers hold d.mu, so a new look asks IOKit for power and volumes (in
// this process) but never runs ioreg for you being away: that's the
// watcher's last look, if it's no older than macAwayStale (the watcher
// looks every macTick while a condition needs it, so an older one is rare:
// just after a wake, or the condition being added), else not known
// (lookedAway false), and the watcher's asked to look now.
func (d *Daemon) macFreshSample(now time.Time, need macNeed) macSample {
	last := d.sched.mac.sample
	if now.Sub(last.at) <= macFresh && (!need.power || last.lookedPower) && (!need.volumes || last.lookedVolumes) && (!need.away || last.lookedAway) {
		return last
	}
	sm := d.sampleMac(now, macNeed{power: need.power, volumes: need.volumes})
	if need.away {
		if last.lookedAway && now.Sub(last.at) <= macAwayStale {
			sm.idle, sm.away, sm.lookedAway = last.idle, last.away, true
		} else {
			d.wakeMacWatcher()
		}
	}
	return sm
}

// wakeMacWatcher has the watcher look again now (a trigger or condition
// was added). Callers hold d.mu.
func (d *Daemon) wakeMacWatcher() {
	if c := d.sched.mac.wake; c != nil {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

// ---------- the Mac app's view ----------

// macAppState takes what the Mac app sees: fires the Wi-Fi, app and
// display triggers what changed since it last said sets off (nothing
// when it's only just connected). Callers hold d.mu.
func (d *Daemon) macAppState(c *conn, m *pb.MacAppState) {
	if len(m.Apps) > 500 {
		m.Apps = m.Apps[:500]
	}
	if len(m.Displays) > 32 {
		m.Displays = m.Displays[:32]
	}
	m.Wifi = oneLine(m.Wifi, 64)
	for _, a := range m.Apps {
		a.Name, a.BundleId = oneLine(a.Name, 200), oneLine(a.BundleId, 200)
	}
	for _, x := range m.Displays {
		x.Id, x.Name = oneLine(x.Id, 200), oneLine(x.Name, 200)
	}
	for _, l := range []*[]string{&m.PlacesIn, &m.PlacesOut, &m.Places} {
		if len(*l) > 100 {
			*l = (*l)[:100]
		}
		for i, n := range *l {
			(*l)[i] = oneLine(n, schedtrig.MaxPlaceName)
		}
	}
	prev := d.sched.mac.app
	if d.sched.mac.appConn != c {
		prev = nil // another connection: news only from what this one says next
		d.sched.mac.places = nil
	}
	if m.Idle {
		// It's stopped watching: what changes meanwhile isn't seen, so what
		// it says when it starts again is a first look, not news.
		d.sched.mac.app, d.sched.mac.appConn, d.sched.mac.places = nil, c, nil
		d.macAppErrors()
		return
	}
	if len(m.Places) == 0 {
		d.sched.mac.places = nil // places aren't watched now: likewise
	}
	arrived, left, places := placeEdges(d.sched.mac.places, m)
	d.sched.mac.app, d.sched.mac.appConn, d.sched.mac.places = m, c, places
	d.macAppErrors()
	if prev == nil {
		return
	}
	for _, s := range d.sched.list {
		for _, t := range d.eventsOf(s) {
			if mt := t.GetMac(); mt != nil && schedtrig.MacAppWatched(mt.Event) {
				evs := macAppEvents(mt, prev, m)
				if schedtrig.IsPlace(mt.Event) {
					evs = placeEvents(mt, arrived, left)
				}
				for _, ev := range evs {
					ev.Trigger = t.Id
					_, _, _ = d.fireEvent(s, ev)
				}
			}
		}
	}
}

// macAppEvents are the events a change from prev to cur sets off for m.
func macAppEvents(m *pb.MacTrigger, prev, cur *pb.MacAppState) []*schedEvent {
	var out []*schedEvent
	ev := func(summary string, fields ...[2]string) {
		out = append(out, &schedEvent{Kind: "mac", Summary: summary, Source: "this Mac", Fields: fields})
	}
	nameIs := func(name string) bool {
		return m.Name == "" || strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(m.Name))
	}
	switch m.Event {
	case pb.MacTrigger_EVENT_WIFI_JOINED, pb.MacTrigger_EVENT_WIFI_LEFT:
		if !prev.WifiKnown || !cur.WifiKnown || prev.Wifi == cur.Wifi {
			return nil
		}
		if m.Event == pb.MacTrigger_EVENT_WIFI_LEFT && prev.Wifi != "" && nameIs(prev.Wifi) {
			ev(cmpOr(quoted(m.Name, "The Mac left the Wi-Fi "), "The Mac left a Wi-Fi network"), [2]string{"network", prev.Wifi}, [2]string{"now", cur.Wifi})
		}
		if m.Event == pb.MacTrigger_EVENT_WIFI_JOINED && cur.Wifi != "" && nameIs(cur.Wifi) {
			ev(cmpOr(quoted(m.Name, "The Mac joined the Wi-Fi "), "The Mac joined a Wi-Fi network"), [2]string{"network", cur.Wifi}, [2]string{"before", prev.Wifi})
		}
	case pb.MacTrigger_EVENT_APP_OPENED, pb.MacTrigger_EVENT_APP_QUIT:
		key := func(a *pb.MacApp) string { return cmpOr(a.BundleId, a.Name) }
		was, is := map[string]bool{}, map[string]bool{}
		for _, a := range prev.Apps {
			was[key(a)] = true
		}
		for _, a := range cur.Apps {
			is[key(a)] = true
		}
		match := func(a *pb.MacApp) bool {
			return nameIs(a.Name) || strings.EqualFold(a.BundleId, strings.TrimSpace(m.Name))
		}
		if m.Event == pb.MacTrigger_EVENT_APP_OPENED {
			for _, a := range cur.Apps {
				if !was[key(a)] && match(a) {
					ev("“"+m.Name+"” opened", [2]string{"app", a.Name}, [2]string{"bundle_id", a.BundleId})
				}
			}
		} else {
			for _, a := range prev.Apps {
				if !is[key(a)] && match(a) {
					ev("“"+m.Name+"” quit", [2]string{"app", a.Name}, [2]string{"bundle_id", a.BundleId})
				}
			}
		}
	case pb.MacTrigger_EVENT_DISPLAY:
		was := map[string]bool{}
		for _, x := range prev.Displays {
			was[x.Id] = true
		}
		for _, x := range cur.Displays {
			if was[x.Id] || m.Name != "" && !strings.Contains(strings.ToLower(x.Name), strings.ToLower(m.Name)) {
				continue
			}
			ev(cmpOr(quoted(m.Name, "A display named "), "A display was connected"), [2]string{"display", x.Name})
		}
	}
	return out
}

// placeEdges works out the places arrived at and left, from each place's
// last known state (last; nil: none yet, as the app has just connected)
// and what the app says now: a place goes from out to in (arrived) or in
// to out (left). One the app can't tell now (in neither list) keeps its
// last state; one first known now is no news. next is the states now.
// (The app settles what it says after a wake, so a Mac waking where it
// slept says nothing new.)
func placeEdges(last map[string]bool, cur *pb.MacAppState) (arrived, left []string, next map[string]bool) {
	next = map[string]bool{}
	for k, v := range last {
		next[k] = v
	}
	note := func(names []string, in bool) {
		for _, n := range names {
			key := strings.ToLower(strings.TrimSpace(n))
			was, known := last[key]
			next[key] = in
			switch {
			case !known || was == in:
			case in:
				arrived = append(arrived, n)
			default:
				left = append(left, n)
			}
		}
	}
	note(cur.PlacesIn, true)
	note(cur.PlacesOut, false)
	return arrived, left, next
}

// placeEvents are the events arriving at and leaving places set off for m.
func placeEvents(m *pb.MacTrigger, arrived, left []string) []*schedEvent {
	names := arrived
	summary := "The Mac arrived at “" + m.Name + "”"
	if m.Event == pb.MacTrigger_EVENT_PLACE_LEAVE {
		names, summary = left, "The Mac left “"+m.Name+"”"
	}
	for _, n := range names {
		if strings.EqualFold(strings.TrimSpace(n), strings.TrimSpace(m.Name)) {
			return []*schedEvent{{Kind: "place", Summary: summary, Source: "this Mac", Fields: [][2]string{{"place", n}}}}
		}
	}
	return nil
}

// placeIn says whether the Mac is at the place called name, as the Mac
// app last said (known: it's said). Callers hold d.mu.
func (d *Daemon) placeIn(name string) (in, known bool) {
	in, known = d.sched.mac.places[strings.ToLower(strings.TrimSpace(name))]
	return in, known
}

// quoted is prefix and “name” ("" when name is).
func quoted(name, prefix string) string {
	if name == "" {
		return ""
	}
	return prefix + "“" + name + "”"
}

// macAppClosed: a connection closed; if it was the Mac app's, what it
// watched can't fire now. Callers hold d.mu.
func (d *Daemon) macAppClosed(c *conn) {
	if d.sched.mac.appConn != c {
		return
	}
	d.sched.mac.app, d.sched.mac.appConn, d.sched.mac.places = nil, nil, nil
	d.macAppErrors()
}

// macAppErrors says on each trigger the Mac app watches for why it can't
// fire now ("Needs Hammock open"), or nothing. Callers hold d.mu.
func (d *Daemon) macAppErrors() {
	app := d.sched.mac.app
	for _, s := range d.sched.list {
		for _, t := range s.Events {
			m := t.GetMac()
			if m == nil || !schedtrig.MacAppWatched(m.Event) {
				continue
			}
			msg := ""
			switch {
			case app == nil && d.sched.mac.appConn == nil:
				msg = "Needs Hammock open"
			case app == nil:
				// Open, but not watching (its automations were off): it
				// starts as one comes on.
			case (m.Event == pb.MacTrigger_EVENT_WIFI_JOINED || m.Event == pb.MacTrigger_EVENT_WIFI_LEFT) && (app.LocationDenied || !app.WifiKnown):
				msg = "No Location access: Hammock needs it to see the Wi-Fi network's name (System Settings ▸ Privacy & Security ▸ Location Services)"
			case schedtrig.IsPlace(m.Event) && app.LocationDenied:
				msg = "No Location access: Hammock needs it to tell where the Mac is (System Settings ▸ Privacy & Security ▸ Location Services)"
			case schedtrig.IsPlace(m.Event) && !slices.ContainsFunc(app.Places, func(n string) bool { return strings.EqualFold(n, m.Name) }):
				msg = "There's no place called “" + m.Name + "” in this Mac's Places"
			}
			d.setTriggerError(s, t, msg)
		}
	}
}
