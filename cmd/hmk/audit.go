package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// runAudit prints recent activity (the audit log) in words, newest first:
// what agents, devices and guests did through Hammock, and permissions.
func runAudit(args []string) error {
	n := 30
	if len(args) > 0 {
		if v, err := strconv.Atoi(args[0]); err == nil {
			n = v
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ListAudit{ListAudit: &pb.ListAudit{Limit: 1000}}})
	if err != nil {
		return err
	}
	events := r.GetAuditLog().GetEvents()
	if len(events) > n {
		events = events[:n]
	}
	for _, e := range events {
		line := time.UnixMilli(e.TimeUnixMs).Format("2006-01-02 15:04") + "  " + e.Who + " " + e.Summary
		if e.Count > 1 {
			line += fmt.Sprintf(" (×%d since %s)", e.Count, time.UnixMilli(e.FirstUnixMs).Format("15:04"))
		}
		printlnSafe(line)
	}
	return nil
}
