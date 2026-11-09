package agentview

import (
	"encoding/json"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// The database fixtures beside this (testdata/opencode/v2.sql,
// testdata/kilo/v1.sql) are read through agents' Open in
// internal/agents/opencode_test.go; this feeds the lines it writes directly.
func TestOpenCodeLines(t *testing.T) {
	p := NewOpenCode("/Users/me/proj")
	feed := func(l OpenCodeLine) []*pb.AgentBlock {
		b, _ := json.Marshal(l)
		return p.Feed(b)
	}
	feed(OpenCodeLine{Session: &OpenCodeSession{ID: "ses_1", Cwd: "/elsewhere"}})
	feed(OpenCodeLine{ID: "m1", Role: "user", Time: 1, Text: "Look it up"})
	got := feed(OpenCodeLine{ID: "m2", Role: "assistant", Time: 2, Parts: []OpenCodePart{
		{ID: "c1", Type: "tool", Tool: "websearch", Status: "completed", Input: json.RawMessage(`{"query":"sqlite wal"}`), Output: "results"},
		{ID: "c2", Type: "tool", Tool: "github_create_issue", Status: "running", Input: json.RawMessage(`{"title":"x"}`)},
	}})
	if len(got) != 2 || got[0].Title != "Searched the web for `sqlite wal`" || got[0].TurnId != "m1" {
		t.Fatalf("blocks = %v", got)
	}
	if got[1].Title != "Used github_create_issue" || !got[1].Pending || p.Pending() != got[1] {
		t.Errorf("MCP tool = %v", got[1])
	}
	// The same message again, finished: its blocks change in place.
	feed(OpenCodeLine{ID: "m2", Role: "assistant", Time: 2, Parts: []OpenCodePart{
		{ID: "c1", Type: "tool", Tool: "websearch", Status: "completed", Input: json.RawMessage(`{"query":"sqlite wal"}`)},
		{ID: "c2", Type: "tool", Tool: "github_create_issue", Status: "error", Error: "401"},
	}})
	if n := len(p.Blocks()); n != 3 || p.Pending() != nil || !p.Blocks()[2].Error || p.Blocks()[2].Output != "401" {
		t.Errorf("after: %v", p.Blocks())
	}
	if p.cx.Cwd != "/Users/me/proj" {
		t.Error("the pane's folder gave way to the session's")
	}
}
