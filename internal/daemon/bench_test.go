package daemon

import (
	"bytes"
	"context"
	"sort"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// benchPane starts a daemon with one pane running cmd and an attached client.
func benchPane(b *testing.B, cmd ...string) (*client.Client, string) {
	b.Helper()
	dir := shortDirB(b)
	d := startDaemonB(b, dir)
	b.Cleanup(d.Shutdown)
	ctx := context.Background()
	c, err := client.Dial(ctx, SocketPath(dir), "bench", "bench")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { c.Close() })
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "bench"}}})
	if err != nil {
		b.Fatal(err)
	}
	r, err = c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: client.CreatedID(r), Pane: &pb.PaneSpec{Command: cmd, Cwd: "/tmp"},
	}}})
	if err != nil {
		b.Fatal(err)
	}
	d.mu.Lock()
	paneID := d.tabs[client.CreatedID(r)].FocusedPaneId
	d.mu.Unlock()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}}); err != nil {
		b.Fatal(err)
	}
	return c, paneID
}

// BenchmarkInputEcho measures the local round trip a keystroke takes through
// client → daemon → holder → PTY → (raw-mode cat echoes) → holder → daemon →
// client. It reports p50/p99 latency.
func BenchmarkInputEcho(b *testing.B) {
	c, pane := benchPane(b, "/bin/sh", "-c", "stty raw -echo; exec cat")
	ctx := context.Background()
	time.Sleep(200 * time.Millisecond)
	drain(c)
	lat := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte{'x'}}}})
		for {
			ev := <-c.Events
			if o := ev.GetPaneOutput(); o != nil && bytes.IndexByte(o.Data, 'x') >= 0 {
				break
			}
		}
		lat = append(lat, time.Since(start))
	}
	b.StopTimer()
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	b.ReportMetric(float64(lat[len(lat)/2].Microseconds()), "p50-µs")
	b.ReportMetric(float64(lat[len(lat)*99/100].Microseconds()), "p99-µs")
}

// BenchmarkOutputThroughput measures bytes/s delivered to an attached client
// while the pane floods output (32 MiB per iteration).
func BenchmarkOutputThroughput(b *testing.B) {
	const size = 32 << 20
	c, pane := benchPane(b, "/bin/sh", "-c", "read x; head -c 33554432 /dev/zero | tr '\\0' 'a'; sleep 60")
	ctx := context.Background()
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		if i > 0 {
			b.Skip("one flood per run")
		}
		time.Sleep(300 * time.Millisecond)
		drain(c)
		b.ResetTimer()
		_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("\r")}}})
		got := 0
		for got < size {
			ev := <-c.Events
			if o := ev.GetPaneOutput(); o != nil {
				got += bytes.Count(o.Data, []byte{'a'})
			}
		}
	}
}

func drain(c *client.Client) {
	for {
		select {
		case <-c.Events:
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}
