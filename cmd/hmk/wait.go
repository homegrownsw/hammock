package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexjsp/hammock/internal/client"
)

// exitStatus ends hmk with a waited-for command's own exit status.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// waitForExit blocks until pane id's command ends, for --wait: a launcher
// (a diff viewer's, say) opens a tab, waits, and carries on. A clean exit
// closes the pane, so gone counts as 0 (as does the person closing it); a
// failure leaves the pane open, with its status.
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
