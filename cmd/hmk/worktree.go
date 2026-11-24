package main

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// runWorktree: hmk worktree remove [DIR] [--force] removes a linked git
// worktree (by default the one this folder's in) the repository's own way,
// then closes the tabs in it. A new one comes with hmk agent --worktree.
func runWorktree(args []string) error {
	usage := fmt.Errorf("usage: hmk worktree remove [DIR] [--force]   (a new one: hmk agent --worktree NAME)")
	if len(args) == 0 || args[0] != "remove" && args[0] != "rm" {
		return usage
	}
	req := &pb.RemoveWorktree{}
	for _, a := range args[1:] {
		switch {
		case a == "--force" || a == "-f":
			req.Force = true
		case len(a) > 0 && a[0] == '-':
			return usage
		case req.Path == "":
			req.Path = absDir(a)
		default:
			return usage
		}
	}
	if req.Path == "" {
		req.Path, _ = os.Getwd()
	}
	// The repository's recipe may take a while.
	ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RemoveWorktree{RemoveWorktree: req}}); err != nil {
		return err
	}
	fmt.Println("removed", req.Path)
	return nil
}
