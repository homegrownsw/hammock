package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

// runDueURLs fetches the URL checks due at the clock's time, one after
// another, and says how many.
func runDueURLs(d *Daemon) int {
	jobs := d.dueURLs(schedNow())
	for _, j := range jobs {
		d.runURL(j)
	}
	return len(jobs)
}

// fakeSite is a page whose status and body the test sets.
type fakeSite struct {
	mu     sync.Mutex
	status int
	body   string
}

func (f *fakeSite) set(status int, body string) {
	f.mu.Lock()
	f.status, f.body = status, body
	f.mu.Unlock()
}

func (f *fakeSite) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	status, body := f.status, f.body
	f.mu.Unlock()
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// A URL check: its first fetch only notes how things are; fail fires as it
// starts failing (once) and again only after it's passed; change fires as
// the page changes; contains as the page starts containing its text; one
// still going isn't started again; paused, it isn't fetched; its trigger
// says how its last fetch went.
func TestURLCheckTrigger(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	old := checkTick
	checkTick = time.Hour // (the test runs them)
	t.Cleanup(func() { checkTick = old })
	site := &fakeSite{status: 200, body: "all good"}
	srv := httptest.NewServer(site)
	defer srv.Close()
	work := t.TempDir()
	start := time.Now()
	clock := useSchedClock(t, start)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	// The test runs the checks: the watcher's never woken.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		d.mu.Lock()
		w := d.sched.checkWake
		d.sched.checkWake = nil
		d.mu.Unlock()
		if w != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no check watcher")
		}
	}
	c := dial(t, dir)
	fail := &pb.UrlCheckTrigger{Url: srv.URL + "/health", EveryMinutes: 1}
	change := &pb.UrlCheckTrigger{Url: srv.URL + "/page", EveryMinutes: 1, On: pb.UrlCheckTrigger_ON_CHANGE}
	contains := &pb.UrlCheckTrigger{Url: srv.URL + "/status", EveryMinutes: 1, On: pb.UrlCheckTrigger_ON_CONTAINS, Text: "DEPLOYED"}
	id := eventSchedule(t, c, work,
		&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: fail}},
		&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: change}},
		&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: contains}})
	s := schedOf(d, id)
	if !strings.Contains(schedtrig.Describe(s.Events[0], nil), srv.URL) || schedtrig.Spec(s.Events[2]) != "--url "+contains.Url+" --url-every 1 --url-on contains --url-text DEPLOYED" {
		t.Fatalf("saved: %v / %q", s.Events, schedtrig.Spec(s.Events[2]))
	}
	at := func(sec int) { clock.set(start.Add(time.Duration(sec) * time.Second)) }
	fired := func() []int64 {
		var out []int64
		for _, tr := range schedOf(d, id).Events {
			out = append(out, tr.FiredUnixMs)
		}
		return out
	}
	if n := runDueURLs(d); n != 3 {
		t.Fatalf("%d fetches at first", n)
	}
	if tr := schedOf(d, id).Events[0]; tr.CheckedUnixMs == 0 || tr.GetUrl().GetLastResult() != "200 OK" || tr.FiredUnixMs != 0 {
		t.Fatalf("after the first look: %v", tr)
	}
	if n := runDueURLs(d); n != 0 {
		t.Fatal("fetched again before it was due")
	}
	// One still going isn't started again.
	at(61)
	tid := schedOf(d, id).Events[0].Id
	d.mu.Lock()
	d.sched.checking[tid] = true
	d.mu.Unlock()
	// It fails: fail fires; change and contains don't (a failed fetch says
	// nothing of the page).
	site.set(503, "down for maintenance")
	jobs := d.dueURLs(schedNow())
	if len(jobs) != 2 {
		t.Fatalf("%d due while one was going", len(jobs))
	}
	for _, j := range jobs {
		d.runURL(j)
	}
	d.mu.Lock()
	delete(d.sched.checking, tid)
	d.sched.trig[tid].URL.Started = 0
	d.mu.Unlock()
	runDueURLs(d)
	f := fired()
	if f[0] == 0 || f[1] != 0 || f[2] != 0 {
		t.Fatalf("after failing: %v", f)
	}
	if tr := schedOf(d, id).Events[0]; tr.GetUrl().GetLastResult() != "Failed (503 Service Unavailable)" {
		t.Fatalf("result: %q", tr.GetUrl().GetLastResult())
	}
	if out := eventOutputs(t, work, 1); !strings.HasPrefix(out[0], "url|A URL check ") || !strings.Contains(out[0], "503 Service Unavailable") || !strings.HasSuffix(out[0], "down for maintenance") {
		t.Fatalf("run: %q", out)
	}
	// Still failing, the same page: nothing more fires.
	at(130)
	runDueURLs(d)
	if g := fired(); g[0] != f[0] || g[1] != f[1] || g[2] != 0 {
		t.Fatalf("still failing fired again: %v then %v", f, g)
	}
	// Back up with the text: contains and change fire; fail doesn't.
	site.set(200, "Release 4.2 deployed")
	at(200)
	runDueURLs(d)
	g := fired()
	if g[0] != f[0] || g[1] == f[1] || g[2] == 0 {
		t.Fatalf("recovered: %v then %v", f, g)
	}
	// The same page again: nothing.
	at(270)
	runDueURLs(d)
	if h := fired(); h[1] != g[1] || h[2] != g[2] {
		t.Fatalf("unchanged page fired: %v then %v", g, h)
	}
	// Unreachable is failing too, and fires fail again (it passed meanwhile).
	srv.Close()
	at(340)
	runDueURLs(d)
	if h := fired(); h[0] == f[0] {
		t.Fatalf("unreachable didn't fire: %v", h)
	}
	if r := schedOf(d, id).Events[0].GetUrl().GetLastResult(); !strings.HasPrefix(r, "Failed (unreachable") {
		t.Fatalf("unreachable: %q", r)
	}
	// Paused: not fetched.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: id, Paused: true}}})
	at(500)
	if n := runDueURLs(d); n != 0 {
		t.Fatalf("paused, it fetched %d", n)
	}

	// A fetch taking too long gives up, as failing.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(20 * time.Second):
		}
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if r := fetchURL(ctx, urlJob{url: slow.URL}); r.why != "timed out" || time.Since(began) > 5*time.Second {
		t.Fatalf("timeout: %+v after %v", r, time.Since(began))
	}
	// A big page is read only so far, and cut for the run.
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(maxURLBody)+1000)))
	}))
	defer big.Close()
	r := fetchURL(context.Background(), urlJob{url: big.URL})
	if int64(len(r.body)) != maxURLBody || !r.cut || r.why != "" {
		t.Fatalf("big: %d bytes, cut %v, %q", len(r.body), r.cut, r.why)
	}
	if kept := urlKept(r.body, r.cut); len(kept) > maxURLKept+20 || !strings.HasSuffix(kept, "[… cut]") {
		t.Fatalf("kept %d", len(kept))
	}

	// Bad ones aren't saved.
	for _, bad := range []*pb.UrlCheckTrigger{{Url: ""}, {Url: "ftp://example.com"}, {Url: "https://user:pw@example.com"}, {Url: "example.com"},
		{Url: "https://example.com", EveryMinutes: -1}, {Url: "https://example.com", On: pb.UrlCheckTrigger_ON_CONTAINS}, {Url: "https://example.com", TimeoutSeconds: 9999}} {
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
			Kind: "command", Prompt: "true", Cwd: work, Timing: pb.Schedule_TIMING_EVENTS,
			Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Url{Url: bad}}},
		}}}}); err == nil {
			t.Errorf("saved %v", bad)
		}
	}
}

// quietChecks stops the check watcher being woken (the test runs checks).
func quietChecks(t *testing.T, d *Daemon) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		d.mu.Lock()
		w := d.sched.checkWake
		d.sched.checkWake = nil
		d.mu.Unlock()
		if w != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no check watcher")
		}
	}
}

// A network blip (a failed fetch) neither counts as a change nor arms
// contains to fire again when the page is back as it was; checks wait a
// while after a wake, and a fetch that went on across one doesn't count.
func TestURLCheckBlipsAndWakes(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	old := checkTick
	checkTick = time.Hour
	t.Cleanup(func() { checkTick = old })
	site := &fakeSite{status: 200, body: "Release 4.2 deployed"}
	srv := httptest.NewServer(site)
	defer srv.Close()
	work := t.TempDir()
	start := time.Now()
	clock := useSchedClock(t, start)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	quietChecks(t, d)
	c := dial(t, dir)
	id := eventSchedule(t, c, work,
		&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{Url: srv.URL + "/a", EveryMinutes: 1, On: pb.UrlCheckTrigger_ON_CHANGE}}},
		&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{Url: srv.URL + "/b", EveryMinutes: 1, On: pb.UrlCheckTrigger_ON_CONTAINS, Text: "deployed"}}})
	at := func(sec int) { clock.set(start.Add(time.Duration(sec) * time.Second)) }
	runDueURLs(d) // the first look
	for i, st := range []struct {
		status int
		body   string
	}{{503, "oops"}, {200, "Release 4.2 deployed"}, {0, ""}, {200, "Release 4.2 deployed"}} {
		at(61 * (i + 1))
		if st.status == 0 {
			evs := schedOf(d, id).Events
			d.mu.Lock()
			for _, tr := range evs {
				d.urlDone(urlJob{schedule: id, trigger: tr.Id, key: urlKey(tr.GetUrl())}, urlResult{why: "unreachable: no route to host"})
				d.sched.trig[tr.Id].URL.Started = schedNow().UnixMilli()
			}
			d.mu.Unlock()
			continue
		}
		site.set(st.status, st.body)
		runDueURLs(d)
	}
	if g := firedAt(d, id); g[0]+g[1] != 0 {
		t.Fatalf("a blip fired: %v", g)
	}

	// Just woken: nothing's fetched for a while.
	at(600)
	d.noteChecksWake(schedNow(), true)
	if n := runDueURLs(d); n != 0 {
		t.Fatalf("%d fetched just after a wake", n)
	}
	at(600 + int(checkWakeGrace/time.Second) + 1)
	jobs := d.dueURLs(schedNow())
	if len(jobs) != 2 {
		t.Fatalf("%d due after the wake's grace", len(jobs))
	}
	// One that went on across a sleep doesn't count, and is fetched again.
	site.set(200, "something else")
	before := schedOf(d, id).Events[0].GetUrl().GetLastResult()
	d.noteChecksWake(schedNow().Add(time.Second), true)
	d.runURL(jobs[0])
	if g := firedAt(d, id); g[0] != 0 || schedOf(d, id).Events[0].GetUrl().GetLastResult() != before {
		t.Fatalf("a fetch across a sleep counted: %v", g)
	}
	d.mu.Lock()
	again := d.sched.trig[jobs[0].trigger].URL.Started == 0 && !d.sched.checking[jobs[0].trigger]
	d.mu.Unlock()
	if !again {
		t.Fatal("not fetched again after the wake")
	}
}
