package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/agents"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestLiveUsage(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	calls := 0
	var answer *pb.AgentUsage
	var fail error
	u := agentUsage{id: "claude", fetch: func(context.Context) (*pb.AgentUsage, time.Duration, error) {
		calls++
		return answer, 0, fail
	}}
	r := usageReader{readers: []agentUsage{u}}
	now := time.Now()

	// Off unless turned on: nothing is asked.
	if a := r.read(); len(a.Agents) != 0 || calls != 0 {
		t.Fatal("asked while off")
	}
	// usage-live turns it on for the agents in usage-live-agents (every one
	// while that's unset); its old name, for Claude's alone, only for Claude.
	for _, c := range []struct {
		config      string
		claude, cdx bool
	}{
		{"", false, false},
		{"usage-live = true\n", true, true},
		{"usage-live = true\nusage-live-agents = codex, cursor\n", false, true},
		{"usage-live = true\nusage-live-agents =\n", false, false},
		{"claude-usage-live = true\n", true, false},
		{"claude-usage-live = true\nusage-live-agents = codex\n", true, false},
		{"usage-live = false\nclaude-usage-live = true\n", true, false},
	} {
		if err := os.WriteFile(filepath.Join(cfg, "config"), []byte(c.config), 0o644); err != nil {
			t.Fatal(err)
		}
		if usageLive("claude") != c.claude || usageLive("codex") != c.cdx {
			t.Errorf("%q: claude %v codex %v", c.config, usageLive("claude"), usageLive("codex"))
		}
	}
	answer = agents.ParseClaudeWindows([]byte(`{"five_hour":{"utilization":67,"resets_at":"2026-11-07T12:09:59+00:00"},
		"seven_day":{"utilization":22,"resets_at":"2026-11-12T11:59:59+00:00"}}`), now.UnixMilli())
	x := &r.readers[0]
	ask := func(at time.Time) *pb.AgentUsage {
		if x.due(true, at) {
			x.askLive(at)
		}
		return x.live
	}
	if a := ask(now); a == nil || a.Windows[1].UsedPercent != 22 || calls != 1 {
		t.Fatalf("%v (%d calls)", a, calls)
	}
	// Not asked again until liveEvery has passed; a failure keeps the last answer.
	ask(now.Add(time.Minute))
	fail = errors.New("429")
	if a := ask(now.Add(liveEvery)); a == nil || a.Windows[1].UsedPercent != 22 || calls != 2 {
		t.Fatalf("%v (%d calls)", a, calls)
	}
	if got := r.read(); len(got.Agents) != 1 || got.Agents[0].Harness != "claude" {
		t.Fatalf("read: %v", got)
	}
	// Turned off, it's forgotten.
	if x.due(false, now); x.live != nil {
		t.Error("kept while off")
	}

	// Failing (signed out), the last answer goes once it's liveStale old;
	// throttled, it stays.
	fail, answer = nil, &pb.AgentUsage{FetchedUnixMs: now.UnixMilli(), Windows: answer.Windows}
	ask(now)
	fail = errors.New("401")
	if a := ask(now.Add(liveEvery)); a == nil {
		t.Fatal("dropped while fresh")
	}
	if a := ask(now.Add(liveStale)); a != nil {
		t.Fatal("stale answer kept")
	}
	fail = nil
	ask(now.Add(liveStale + liveEvery))
	throttled := func(context.Context) (*pb.AgentUsage, time.Duration, error) { return nil, time.Hour, errors.New("429") }
	x.fetch = throttled
	if a := ask(now.Add(liveStale + 4*liveEvery)); a == nil {
		t.Fatal("dropped while throttled")
	}

	// Only the agents asked for are asked.
	asked := 0
	r2 := usageReader{readers: []agentUsage{
		{id: "claude", fetch: func(context.Context) (*pb.AgentUsage, time.Duration, error) { asked++; return answer, 0, nil }},
		{id: "codex", fetch: func(context.Context) (*pb.AgentUsage, time.Duration, error) {
			t.Error("codex asked")
			return nil, 0, nil
		}},
	}, liveFor: func(id string) bool { return id == "claude" }}
	if got := r2.read(); asked != 1 || len(got.Agents) != 1 || got.Agents[0].Harness != "claude" {
		t.Fatalf("asked %d: %v", asked, got)
	}

	// What's on disk wins only when it's newer.
	cached := &pb.AgentUsage{Harness: "claude", FetchedUnixMs: now.Add(-2 * time.Hour).UnixMilli()}
	if newerUsage(cached, answer) != answer || newerUsage(cached, nil) != cached || newerUsage(nil, answer) != answer {
		t.Error("older cache shown")
	}
	cached.FetchedUnixMs = now.Add(time.Minute).UnixMilli()
	if newerUsage(cached, answer) != cached {
		t.Error("newer cache not shown")
	}
}
