package client

import (
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestLimitResumeWords(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.Local) // a Tuesday
	at := func(d time.Duration, known bool) *pb.LimitResume {
		return &pb.LimitResume{AtUnixMs: now.Add(d).UnixMilli(), ResetKnown: known}
	}
	for _, c := range []struct {
		lr   *pb.LimitResume
		want string
	}{
		{at(3*time.Hour+time.Minute, true), "Continues at 15:01"},
		{at(time.Hour, false), "Tries again at 13:00"},
		{at(22*time.Hour, true), "Continues tomorrow at 10:00"},
		{at(46*time.Hour, true), "Continues Thursday at 10:00"},
		{at(9*24*time.Hour, true), "Continues 3 Dec at 12:00"},
	} {
		if got := LimitResumeWords(c.lr, now); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
