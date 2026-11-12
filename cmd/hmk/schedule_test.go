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
