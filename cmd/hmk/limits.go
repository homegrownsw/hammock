package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// hmk limits: the agents that stopped at their usage limit and are sent
// "continue" when it resets (the daemon's limitresume.go), and cancelling
// that.
//
//	hmk limits                 list them
//	hmk limits cancel [PANE]   don't send PANE's agent "continue" (default: this pane's)
func runLimits(w *world, call func(*pb.ClientMessage) (*pb.ServerMessage, error), args []string) error {
	if len(args) == 0 || args[0] == "ls" || args[0] == "list" {
		ids := make([]string, 0, len(w.panes))
		for id, p := range w.panes {
			if p.GetAgent().GetLimitResume() != nil {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			fmt.Println("No agents are waiting for a usage limit to reset.")
			return nil
		}
		slices.SortFunc(ids, func(a, b string) int {
			return cmp.Compare(w.panes[a].Agent.LimitResume.AtUnixMs, w.panes[b].Agent.LimitResume.AtUnixMs)
		})
		now := time.Now()
		for _, id := range ids {
			a := w.panes[id].Agent
			printfSafe("%s  %s  %s  %s\n", id, cmp.Or(a.Harness, "agent"), limitResumeWords(a.LimitResume, now), a.Summary)
		}
		return nil
	}
	if args[0] != "cancel" {
		return errors.New("usage: hmk limits [cancel [PANE]]")
	}
	pid := paneEnv("PANE_ID")
	if len(args) > 1 {
		var err error
		if pid, err = w.resolve(args[1], "pane"); err != nil {
			return err
		}
	}
	if pid == "" {
		return errors.New("usage: hmk limits cancel PANE (or run it inside a Hammock pane)")
	}
	_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CancelLimitResume{CancelLimitResume: &pb.CancelLimitResume{PaneId: pid}}})
	return err
}

// limitResumeWords is client.LimitResumeWords, in a line of hmk's
// output: "continues at 15:01".
func limitResumeWords(lr *pb.LimitResume, now time.Time) string {
	w := client.LimitResumeWords(lr, now)
	return strings.ToLower(w[:1]) + w[1:]
}
