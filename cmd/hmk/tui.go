package main

import (
	"context"
	"os"
	"time"

	"github.com/alexjsp/hammock/internal/tui"
)

func runTUI() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c, err := connect(ctx)
	cancel()
	if err != nil {
		return err
	}
	defer c.Close()
	return tui.Run(c, os.Stdin, os.Stdout)
}
