package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// exitStatus ends hmk with a waited-for command's own exit status.
type exitStatus int

// startFailedStatus is hmk's exit status when --wait's pane never started
// its command (its terminal didn't start), as env and docker run use 125
// for their own failure: so a caller can't take it for the command's.
const startFailedStatus = 125

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// waitBackWithin is how long --wait waits for the daemon to come back when
// its connection drops: the daemon restarting (an update, a new build) leaves
// panes running, so the command is still going and still worth waiting for.
var waitBackWithin = 5 * time.Minute

// waitPoll is how often --wait looks at the pane, and waitRedial how often it
// tries the daemon again while it's away.
var waitPoll, waitRedial = 300 * time.Millisecond, time.Second

// waitForExit blocks until pane id's command ends, for --wait: a launcher
// (a diff viewer's, say) opens a tab, waits, and carries on. A clean exit
// closes the pane, so gone counts as 0 (as does the person closing it); a
// failure leaves the pane open, with its status. A pane whose terminal never
// started ends hmk with startFailedStatus, saying why.
func waitForExit(c *client.Client, id string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fetch := func(ctx context.Context) (*pb.Pane, error) {
		w, err := fetchWorld(ctx, c)
		if err != nil {
			return nil, err
		}
		return w.panes[id], nil
	}
	redial := func(ctx context.Context) error {
		n, err := connect(ctx)
		if err != nil {
			return err
		}
		_ = c.Close()
		c = n
		return nil
	}
	return waitPane(ctx, id, fetch, redial)
}

// waitPane is waitForExit's loop: fetch says how the pane is (nil: gone),
// and redial connects again when fetching fails (the daemon restarting).
func waitPane(ctx context.Context, id string, fetch func(context.Context) (*pb.Pane, error), redial func(context.Context) error) error {
	redialed := false
	for {
		p, err := fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Failing straight after connecting again: the daemon's answer, not a dropped connection.
			if redialed {
				return err
			}
			if rerr := waitBack(ctx, id, redial); rerr != nil {
				return fmt.Errorf("lost Hammock while waiting for pane %s (%v): %w", id, err, rerr)
			}
			redialed = true
			continue
		}
		redialed = false
		if p == nil {
			return nil
		}
		if p.Exited {
			if p.StartError != "" {
				fmt.Fprintf(os.Stderr, "hammock: pane %s couldn't start its command: %s\n", id, p.StartError)
				return exitStatus(startFailedStatus)
			}
			if p.ExitStatus != 0 {
				return exitStatus(p.ExitStatus)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitPoll):
		}
	}
}

// waitBack connects again, for up to waitBackWithin.
func waitBack(ctx context.Context, id string, redial func(context.Context) error) error {
	fmt.Fprintf(os.Stderr, "hammock: lost the connection while waiting for pane %s; waiting for Hammock to come back…\n", id)
	deadline := time.Now().Add(waitBackWithin)
	for {
		err := redial(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitRedial):
		}
	}
}
