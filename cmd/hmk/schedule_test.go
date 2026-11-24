package main

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestScheduleDaysNeedsAt(t *testing.T) {
	for _, c := range []struct {
		args   []string
		adding bool
		cron   string
		ok     bool
	}{
		{[]string{"--days", "weekdays", "--", "hi"}, true, "", false},
		{[]string{"--cron", "0 9 * * *", "--days", "weekdays", "--", "hi"}, true, "", false},
		{[]string{"--days", "weekdays", "--at", "08:30", "--", "hi"}, true, "", true},
		{[]string{"--at", "08:30", "--days", "weekdays", "--", "hi"}, true, "", true},
		{[]string{"--cron", "0 9 * * *", "--", "hi"}, true, "", true},
		// Editing one that already runs at a time: --days alone keeps it.
		{[]string{"--days", "weekends"}, false, "", true},
		// …but one that ran by cron has no time to keep.
		{[]string{"--days", "weekends"}, false, "0 9 * * *", false},
	} {
		s := &pb.Schedule{Cron: c.cron, MinuteOfDay: 9 * 60}
		err := scheduleFlags(&world{}, s, c.args, c.adding)
		if (err == nil) != c.ok {
			t.Errorf("scheduleFlags(%q, adding %v, cron %q) = %v; want ok %v", c.args, c.adding, c.cron, err, c.ok)
		}
	}
}

func TestScheduleCommandWords(t *testing.T) {
	for _, c := range []struct {
		kind string
		args []string
		want string
	}{
		{"command", []string{"sh", "-c", "echo a; date"}, `sh -c 'echo a; date'`},
		{"command", []string{"echo a; date"}, "echo a; date"},
		{"command", []string{"echo", "it's", ""}, `echo 'it'\''s' ''`},
		{"command", []string{"make", "test"}, "make test"},
		{"claude", []string{"tidy", "the", "README; then stop"}, "tidy the README; then stop"},
	} {
		s := &pb.Schedule{Kind: c.kind}
		if err := scheduleFlags(&world{}, s, append([]string{"--"}, c.args...), false); err != nil {
			t.Fatal(err)
		}
		if s.Prompt != c.want {
			t.Errorf("%s -- %q: prompt %q, want %q", c.kind, c.args, s.Prompt, c.want)
		}
	}
}

// Triggers alone make a task that runs only on them; with a time, both.
func TestScheduleTriggers(t *testing.T) {
	s := &pb.Schedule{}
	if err := scheduleFlags(&world{}, s, []string{"--trigger", "github:ci-failed", "--trigger", "webhook", "--cooldown", "120", "--", "fix it"}, true); err != nil {
		t.Fatal(err)
	}
	if s.Timing != pb.Schedule_TIMING_EVENTS || len(s.Events) != 2 || s.GetCooldownSeconds() != 120 {
		t.Fatalf("events only: %v", s)
	}
	s = &pb.Schedule{}
	if err := scheduleFlags(&world{}, s, []string{"--at", "08:30", "--trigger", "github:merged", "--", "x"}, true); err != nil {
		t.Fatal(err)
	}
	if s.Timing != pb.Schedule_TIMING_REPEAT || len(s.Events) != 1 {
		t.Fatalf("both: %v", s)
	}
	s = &pb.Schedule{}
	if err := scheduleFlags(&world{}, s, []string{"--once", "2026-12-23 17:00", "--", "x"}, true); err != nil {
		t.Fatal(err)
	}
	if s.Timing != pb.Schedule_TIMING_ONCE || s.OnceUnixMs == 0 {
		t.Fatalf("once: %v", s)
	}
	if err := scheduleFlags(&world{}, &pb.Schedule{}, []string{"--trigger", "log:x", "--", "x"}, true); err == nil {
		t.Fatal("a log trigger with no pane")
	}
	if err := scheduleFlags(&world{}, &pb.Schedule{}, []string{"--once", "tomorrow", "--", "x"}, true); err == nil {
		t.Fatal("--once tomorrow")
	}
}
