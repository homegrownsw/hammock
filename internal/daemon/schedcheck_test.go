package daemon

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

// runDueChecks runs the checks due at the clock's time, one after another,
// and says how many.
func runDueChecks(d *Daemon) int {
	jobs := d.dueChecks(schedNow())
	for _, j := range jobs {
		d.runCheck(j)
	}
	return len(jobs)
}

// A command check: its first look only notes how things are; it fires as
// the command starts failing (once, not on every failing check), and as it
// passes again; one still going isn't started again; its trigger says when
// it last looked and fired, and how its last check went.
func TestCommandCheckTrigger(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	old := checkTick
	checkTick = time.Hour // (the test runs them)
	t.Cleanup(func() { checkTick = old })
	work := t.TempDir()
	flag := filepath.Join(work, "flag")
	set := func(v string) {
		if err := os.WriteFile(flag, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set("ok")
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
	check := &pb.CommandCheckTrigger{Command: `cat flag; test "$(cat flag)" = ok`, EveryMinutes: 1}
	id := eventSchedule(t, c, work, &pb.EventTrigger{Kind: &pb.EventTrigger_Check{Check: check}})
	if s := schedOf(d, id); s.Events[0].GetCheck().GetEveryMinutes() != 1 || !strings.Contains(schedtrig.Describe(s.Events[0], nil), check.Command) {
		t.Fatalf("saved: %v", s.Events[0])
	}
	if n := runDueChecks(d); n != 1 {
		t.Fatalf("%d checks at first", n)
	}
	tr := schedOf(d, id).Events[0]
	if tr.CheckedUnixMs == 0 || tr.GetCheck().GetLastResult() != "Passed" || tr.FiredUnixMs != 0 {
		t.Fatalf("after the first look: %v", tr)
	}
	if n := runDueChecks(d); n != 0 {
		t.Fatal("checked again before it was due")
	}
	// One still going isn't started again.
	clock.set(start.Add(61 * time.Second))
	d.mu.Lock()
	d.sched.checking[tr.Id] = true
	d.mu.Unlock()
	if jobs := d.dueChecks(schedNow()); len(jobs) != 0 {
		t.Fatal("started while one was going")
	}
	d.mu.Lock()
	delete(d.sched.checking, tr.Id)
	d.mu.Unlock()

	// It starts failing: one run, with its output.
	set("broken")
	runDueChecks(d)
	if out := eventOutputs(t, work, 1); out[0] != "check|A command check started failing (exit 1)|broken" {
		t.Fatalf("run: %q", out[0])
	}
	tr = schedOf(d, id).Events[0]
	if tr.FiredUnixMs == 0 || tr.GetCheck().GetLastResult() != "Failed (exit 1)" {
		t.Fatalf("after failing: %v", tr)
	}
	if schedtrig.Status(tr, schedNow()) == "" {
		t.Error("no status line")
	}
	// Still failing: nothing more.
	clock.set(start.Add(130 * time.Second))
	runDueChecks(d)
	d.mu.Lock()
	waiting := len(d.sched.pending[id])
	d.mu.Unlock()
	if n := len(schedOf(d, id).Runs); n != 1 || waiting != 0 {
		t.Fatalf("still failing fired again: %d runs, %d waiting", n, waiting)
	}

	// Recover: the same check (what it's seen is kept), firing as it passes.
	s := schedOf(d, id)
	s.Events[0].GetCheck().On = pb.CommandCheckTrigger_ON_RECOVER
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}})
	if got := schedOf(d, id).Events[0]; got.Id != tr.Id || got.FiredUnixMs != tr.FiredUnixMs {
		t.Fatalf("edited: %v", got)
	}
	set("ok")
	clock.set(start.Add(200 * time.Second))
	runDueChecks(d)
	out := eventOutputs(t, work, 2)
	if !strings.Contains(strings.Join(out, "\n"), "check|A command check passes again|ok") {
		t.Fatalf("recovered: %q", out)
	}

	// A command that runs too long is stopped, and counts as failing.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	code, why := runCheckCommand(ctx, checkJob{command: "sleep 20", dir: work}, &checkOutput{n: 100, sum: nopHash{}})
	if code != -1 || why != "timed out" || time.Since(began) > 5*time.Second {
		t.Fatalf("timeout: %d %q after %v", code, why, time.Since(began))
	}

	// One that passes, leaving something running that holds its output,
	// passes; what it left is ended with it.
	pidFile := filepath.Join(work, "bg.pid")
	code, why = runCheckCommand(context.Background(), checkJob{command: "sleep 30 & echo $! > bg.pid; echo done", dir: work}, &checkOutput{n: 100, sum: nopHash{}})
	if code != 0 || why != "" {
		t.Fatalf("a check leaving a child: %d %q", code, why)
	}
	if b, err := os.ReadFile(pidFile); err != nil {
		t.Fatal(err)
	} else if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid <= 0 {
		t.Fatalf("pid %q", b)
	} else {
		waitFor(t, "its child to be ended", func() bool { return syscall.Kill(pid, 0) != nil })
	}

	// Bad ones aren't saved.
	for _, bad := range []*pb.CommandCheckTrigger{{Command: ""}, {Command: "a\nb"}, {Command: "x", EveryMinutes: -1}, {Command: "x", TimeoutSeconds: 99999}} {
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
			Kind: "command", Prompt: "true", Cwd: work, Timing: pb.Schedule_TIMING_EVENTS,
			Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Check{Check: bad}}},
		}}}}); err == nil {
			t.Errorf("saved %v", bad)
		}
	}
}

type nopHash struct{}

func (nopHash) Write(p []byte) (int, error) { return len(p), nil }
func (nopHash) Sum(b []byte) []byte         { return b }
func (nopHash) Reset()                      {}
func (nopHash) Size() int                   { return 0 }
func (nopHash) BlockSize() int              { return 1 }

// A trigger from Shortcuts says whether it started a run, or waits and
// why; what it sends is cut at 64 KB (and it's told); turned off or
// paused, it's refused, saying which.
func TestTriggerReplyStatus(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	work := t.TempDir()
	start := time.Now()
	clock := useSchedClock(t, start)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	trig := &pb.EventTrigger{Kind: &pb.EventTrigger_Webhook{Webhook: &pb.WebhookTrigger{}}}
	if runtime.GOOS == "darwin" {
		trig = &pb.EventTrigger{Kind: &pb.EventTrigger_Shortcut{Shortcut: &pb.ShortcutTrigger{}}}
	}
	id := eventSchedule(t, c, work, trig)
	run := func(data string) (*pb.TriggerReply, error) {
		r, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{
			ScheduleId: id, Trigger: true, Source: "shortcuts", Label: "A message from Sam", Data: data, StatusReply: true}}})
		return r.GetTriggerReply(), err
	}
	r, err := run(strings.Repeat("x", maxHookBody+100))
	if err != nil || r.Status != pb.TriggerReply_STATUS_STARTED || r.TabId == "" || !r.Cut {
		t.Fatalf("first: %v %v", r, err)
	}
	if out := eventOutputs(t, work, 1); !strings.HasPrefix(out[0], "shortcuts|From Shortcuts|xxx") || !strings.HasSuffix(out[0], "[… cut]") {
		t.Fatalf("env: %.80q…", out[0])
	}
	if s := schedOf(d, id); s.Runs[0].Trigger != "From Shortcuts" {
		t.Fatalf("run: %v", s.Runs[0])
	}
	// Within its cooldown: queued, saying until when, on the trigger too.
	r, err = run("again")
	if err != nil || r.Status != pb.TriggerReply_STATUS_QUEUED || !strings.Contains(r.Reason, "cooldown, until") {
		t.Fatalf("second: %v %v", r, err)
	}
	if runtime.GOOS == "darwin" {
		if tr := schedOf(d, id).Events[0]; tr.Held != r.Reason || tr.FiredUnixMs == 0 {
			t.Fatalf("trigger: %v", tr)
		}
	}
	// Once it runs, the trigger's no longer held.
	for deadline := time.Now().Add(10 * time.Second); runActive(schedOf(d, id).Runs[0]); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the first run never ended")
		}
	}
	tick(d, clock, start.Add(31*time.Second))
	eventOutputs(t, work, 2)
	if tr := schedOf(d, id).Events[0]; tr.Held != "" {
		t.Fatalf("still held: %v", tr)
	}
	// Hammock starting: queued, saying so.
	d.mu.Lock()
	d.sched.ready = false
	d.mu.Unlock()
	clock.set(start.Add(5 * time.Minute))
	if r, err = run("starting"); err != nil || r.Reason != "waiting for Hammock to start" {
		t.Fatalf("starting: %v %v", r, err)
	}
	d.mu.Lock()
	d.sched.ready = true
	// Turned off, and paused: refused, in their own words.
	d.sched.off = true
	d.mu.Unlock()
	if _, err = run("off"); err == nil || !strings.Contains(err.Error(), "Automations are off") {
		t.Fatalf("off: %v", err)
	}
	d.mu.Lock()
	d.sched.off = false
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: id, Paused: true}}})
	if _, err = run("paused"); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("paused: %v", err)
	}
	// Without status_reply (hmk trigger as it was): Created or Ok.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: id, Paused: false}}})
	if r, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: id, Trigger: true}}}); err != nil || r.GetTriggerReply() != nil {
		t.Fatalf("plain: %v %v", r, err)
	}
	// An Only If that doesn't hold: skipped, saying why (not started).
	for deadline := time.Now().Add(10 * time.Second); slices.ContainsFunc(schedOf(d, id).Runs, runActive); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the plain run never ended")
		}
	}
	clock.set(start.Add(30 * time.Minute))
	s := schedOf(d, id)
	other := int32((schedTime().Weekday() + 1) % 7)
	s.OnlyIf = []*pb.Condition{{Kind: pb.Condition_KIND_DAYS, Weekdays: []int32{other}}}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}})
	if r, err = run("skipped"); err != nil || r.Status != pb.TriggerReply_STATUS_SKIPPED || !strings.HasPrefix(r.Reason, "Only if: not ") || r.TabId != "" {
		t.Fatalf("skipped: %v %v", r, err)
	}
	_ = proto.Int32
}
