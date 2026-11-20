package main

import (
	"context"
	"os"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/tui"
)

func runTUI() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, err := connect(ctx)
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()
	c.OnApproval = nil // the TUI's screen is its own (it shows the request in Needs You)
	return tui.Run(c, func(ctx context.Context) (*client.Client, error) {
		c, err := connect(ctx)
		if err == nil {
			c.OnApproval = nil
		}
		return c, err
	}, os.Stdin, os.Stdout)
}
