package main

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A log pane is a terminal following a log source: hmk ls --json and
// list_panes call it a "log", not a "terminal".
func TestKindJSON(t *testing.T) {
	for _, c := range []struct {
		pane *pb.Pane
		want string
	}{
		{&pb.Pane{}, "terminal"},
		{&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL}, "terminal"},
		{&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL, Log: &pb.LogSource{Kind: "file", Path: "/tmp/app.log"}}, "log"},
		{&pb.Pane{Log: &pb.LogSource{Kind: "macos"}}, "log"},
		{&pb.Pane{Kind: pb.PaneKind_PANE_KIND_BROWSER}, "browser"},
		{&pb.Pane{Kind: pb.PaneKind_PANE_KIND_SIMULATOR}, "simulator"},
		{&pb.Pane{Kind: pb.PaneKind_PANE_KIND_EDITOR}, "editor"},
		{&pb.Pane{Kind: pb.PaneKind_PANE_KIND_DIFF}, "diff"},
	} {
		if got := kindJSON(c.pane); got != c.want {
			t.Errorf("kindJSON(%v) = %q, want %q", c.pane, got, c.want)
		}
	}
}
