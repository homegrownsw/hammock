package daemon

import (
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

// Arriving at and leaving places: out to in is arriving, in to out
// leaving; a place first known (the app just connected, or Location just
// found where the Mac is) is no news; one not known for a while keeps its
// last state, so in, unknown, out is still leaving, and in, unknown, in
// (a wake where it slept) is nothing.
func TestPlaceEdges(t *testing.T) {
	type step struct {
		in, out       []string
		arrived, left []string
	}
	for _, tc := range []struct {
		name  string
		steps []step
	}{
		{"first look is no news", []step{{in: []string{"Office"}, out: []string{"Studio"}}}},
		{"arrive then leave", []step{
			{out: []string{"Office"}},
			{in: []string{"Office"}, arrived: []string{"Office"}},
			{in: []string{"Office"}},
			{out: []string{"Office"}, left: []string{"Office"}},
		}},
		{"unknown in between keeps the last", []step{
			{in: []string{"Office"}},
			{},
			{out: []string{"Office"}, left: []string{"Office"}},
		}},
		{"a wake where it slept", []step{
			{in: []string{"Studio"}},
			{},
			{in: []string{"Studio"}},
		}},
		{"known only later", []step{
			{},
			{in: []string{"Studio"}},
			{out: []string{"Studio"}, left: []string{"Studio"}},
		}},
		{"names are matched in any case", []step{
			{out: []string{"office"}},
			{in: []string{"Office"}, arrived: []string{"Office"}},
		}},
	} {
		var last map[string]bool
		for i, s := range tc.steps {
			arrived, left, next := placeEdges(last, &pb.MacAppState{PlacesIn: s.in, PlacesOut: s.out})
			if !slices.Equal(arrived, s.arrived) || !slices.Equal(left, s.left) {
				t.Errorf("%s, step %d: arrived %v left %v, want %v %v", tc.name, i, arrived, left, s.arrived, s.left)
			}
			last = next
		}
	}
	ev := placeEvents(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_LEAVE, Name: "office"}, []string{"Studio"}, []string{"Office"})
	if len(ev) != 1 || ev[0].Summary != "The Mac left “office”" {
		t.Fatalf("leave: %v", ev)
	}
	if ev := placeEvents(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_ARRIVE, Name: "Office"}, []string{"Studio"}, []string{"Office"}); ev != nil {
		t.Fatalf("arrived at another: %v", ev)
	}
}

// Place triggers and Only If, from what the Mac app says: they say why
// they can't fire (the app closed, no Location access, no such place),
// fire on arriving and leaving (never for where the Mac was as the app
// connected), and an Only If place holds only while it's known the Mac is
// there.
func TestPlaceTriggers(t *testing.T) {
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
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_ARRIVE, Name: "Office"}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_LEAVE, Name: "Office"}),
		macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_ARRIVE, Name: "Moon"}),
	)
	if got := schedtrig.Spec(schedOf(d, id).Events[1]); got != "place:leave=Office" {
		t.Fatalf("spec: %q", got)
	}
	if e := schedOf(d, id).Events[0].Error; e != "Needs Hammock open" {
		t.Fatalf("with the app closed: %q", e)
	}
	app := &conn{}
	places := []string{"Office", "Studio"}
	send := func(min int, m *pb.MacAppState) {
		clock.set(now.Add(time.Duration(min) * time.Minute))
		m.Places = places
		d.mu.Lock()
		d.macAppState(app, m)
		d.mu.Unlock()
	}
	cond := &pb.Condition{Kind: pb.Condition_KIND_PLACE, Place: "office"}
	holds := func() string {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.conditionFails(cond, schedNow(), macSample{})
	}
	if w := holds(); !strings.Contains(w, "isn't open") {
		t.Fatalf("Only If with the app closed: %q", w)
	}
	send(1, &pb.MacAppState{PlacesIn: []string{"Office"}, PlacesOut: []string{"Studio"}})
	if g := firedAt(d, id); g[0]+g[1]+g[2] != 0 {
		t.Fatalf("fired for where it was as it connected: %v", g)
	}
	tr := schedOf(d, id).Events
	if tr[0].Error != "" || !strings.Contains(tr[2].Error, "no place called “Moon”") {
		t.Fatalf("errors: %q, %q", tr[0].Error, tr[2].Error)
	}
	if w := holds(); w != "" {
		t.Fatalf("Only If at the office: %q", w)
	}
	// Location loses track (asleep), then finds it's left.
	send(2, &pb.MacAppState{})
	if g := firedAt(d, id); g[0]+g[1] != 0 {
		t.Fatalf("unknown fired: %v", g)
	}
	send(3, &pb.MacAppState{PlacesOut: []string{"Office", "Studio"}})
	if g := firedAt(d, id); g[0] != 0 || g[1] != now.Add(3*time.Minute).UnixMilli() {
		t.Fatalf("left: %v", g)
	}
	if w := holds(); w != "not at “office”" {
		t.Fatalf("Only If away from the office: %q", w)
	}
	send(4, &pb.MacAppState{PlacesIn: []string{"Office"}, PlacesOut: []string{"Studio"}})
	if g := firedAt(d, id); g[0] != now.Add(4*time.Minute).UnixMilli() {
		t.Fatalf("arrived: %v", g)
	}
	// No Location access: the triggers and Only If say so.
	send(5, &pb.MacAppState{LocationDenied: true})
	if e := schedOf(d, id).Events[0].Error; !strings.HasPrefix(e, "No Location access") {
		t.Fatalf("no Location access: %q", e)
	}
	if w := holds(); !strings.Contains(w, "no Location access") {
		t.Fatalf("Only If without Location: %q", w)
	}
	// A new connection starts afresh: what it says first is no news.
	d.mu.Lock()
	d.macAppClosed(app)
	d.mu.Unlock()
	app = &conn{}
	send(6, &pb.MacAppState{PlacesOut: []string{"Office"}})
	if g := firedAt(d, id); g[1] != now.Add(3*time.Minute).UnixMilli() {
		t.Fatalf("a new connection fired: %v", g)
	}

	// An older linked computer is refused places and URL checks.
	d2 := &Daemon{}
	d2.comp.links = map[string]*pb.StoredLink{"us_old": {Record: &pb.Computer{Name: "Studio", Features: []string{featureAutomations2, featureSessions}}}}
	for _, s := range []*pb.Schedule{
		{OnlyIf: []*pb.Condition{cond}},
		{Events: []*pb.EventTrigger{macTrig(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_ARRIVE, Name: "Office"})}},
		{Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{Url: "https://example.com"}}}}},
	} {
		if err := d2.peerCanSave("us_old", s); err == nil || !strings.Contains(err.Error(), "older Hammock") {
			t.Errorf("%v: %v", s, err)
		}
	}
}
