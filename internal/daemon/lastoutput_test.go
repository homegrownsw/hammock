package daemon

import (
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A pane's last output is published the first time, then only once it's
// outputNews behind, so a busy pane doesn't update every client per write.
func TestNoteOutputThrottles(t *testing.T) {
	info := &pb.Pane{}
	start := time.UnixMilli(1_800_000_000_000)
	if !noteOutput(info, start) {
		t.Fatal("first output: want news")
	}
	if info.LastOutputUnixMs != start.UnixMilli() {
		t.Fatalf("recorded %d, want %d", info.LastOutputUnixMs, start.UnixMilli())
	}
	if noteOutput(info, start.Add(outputNews-time.Second)) {
		t.Fatal("output within outputNews: want no news")
	}
	if info.LastOutputUnixMs != start.UnixMilli() {
		t.Fatal("output within outputNews changed the published time")
	}
	later := start.Add(outputNews)
	if !noteOutput(info, later) || info.LastOutputUnixMs != later.UnixMilli() {
		t.Fatalf("output outputNews later: want news and %d, got %d", later.UnixMilli(), info.LastOutputUnixMs)
	}
}
