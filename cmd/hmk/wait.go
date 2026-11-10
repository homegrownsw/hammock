package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
)

// exitStatus ends hmk with a waited-for command's own exit status.
type exitStatus int

// startFailedStatus is hmk's exit status when --wait's pane never started
// its command (its terminal didn't start), as env and docker run use 125
// for their own failure: so a caller can't take it for the command's.
const startFailedStatus = 125

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// waitForExit blocks until pane id's command ends, for --wait: a launcher
// (a diff viewer's, say) opens a tab, waits, and carries on. A clean exit
// closes the pane, so gone counts as 0 (as does the person closing it); a
// failure leaves the pane open, with its status. A pane whose terminal never
// started ends hmk with startFailedStatus, saying why.
func waitForExit(c *client.Client, id string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		w, err := fetchWorld(ctx, c)
		if err != nil {
			return err
		}
		p := w.panes[id]
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
		case <-time.After(300 * time.Millisecond):
		}
	}
}
