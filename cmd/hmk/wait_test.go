package main

import (
	"context"
	"errors"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fastWait makes --wait's polling and redialling instant for a test.
func fastWait(t *testing.T, within time.Duration) {
	t.Helper()
	poll, redial, back := waitPoll, waitRedial, waitBackWithin
	waitPoll, waitRedial, waitBackWithin = time.Millisecond, time.Millisecond, within
	t.Cleanup(func() { waitPoll, waitRedial, waitBackWithin = poll, redial, back })
}

// The daemon restarting while a --wait command runs (an update): hmk
// connects again and still exits with the command's own status.
func TestWaitSurvivesDaemonRestart(t *testing.T) {
	fastWait(t, time.Minute)
	dropped := errors.New("failed to get reader: EOF")
	calls, up := 0, true
	fetch := func(context.Context) (*pb.Pane, error) {
		calls++
		switch {
		case !up:
			return nil, dropped
		case calls == 2:
			up = false // the daemon goes away
			return nil, dropped
		case calls < 2:
			return &pb.Pane{Id: "pn_1"}, nil
		default:
			return &pb.Pane{Id: "pn_1", Exited: true, ExitStatus: 3}, nil
		}
	}
	tries := 0
	redial := func(context.Context) error {
		tries++
		if tries < 3 {
			return errors.New("connection refused") // still restarting
		}
		up = true
		return nil
	}
	var status exitStatus
	if err := waitPane(context.Background(), "pn_1", fetch, redial); !errors.As(err, &status) || status != 3 {
		t.Fatalf("got %v, want the command's exit status 3", err)
	}
}

// A daemon that doesn't come back ends the wait, saying so.
func TestWaitGivesUpWhenDaemonStaysAway(t *testing.T) {
	fastWait(t, 20*time.Millisecond)
	fetch := func(context.Context) (*pb.Pane, error) { return nil, errors.New("EOF") }
	redial := func(context.Context) error { return errors.New("connection refused") }
	if err := waitPane(context.Background(), "pn_1", fetch, redial); err == nil {
		t.Fatal("waited forever for a daemon that's gone")
	}
}

// An error straight after connecting again is the daemon's answer: not
// retried forever.
func TestWaitDoesNotRetryTheDaemonsError(t *testing.T) {
	fastWait(t, time.Minute)
	redials := 0
	fetch := func(context.Context) (*pb.Pane, error) { return nil, errors.New("refused") }
	redial := func(context.Context) error { redials++; return nil }
	if err := waitPane(context.Background(), "pn_1", fetch, redial); err == nil || redials != 1 {
		t.Fatalf("got %v after %d redials, want an error after one", err, redials)
	}
}
