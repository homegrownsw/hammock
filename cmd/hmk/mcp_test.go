package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// rerun_pane says why a pane can't run again before asking the user to
// allow it, in the daemon's terms: only a terminal whose program ended.
func TestWhyNotRerun(t *testing.T) {
	for name, c := range map[string]struct {
		p    *pb.Pane
		want string
	}{
		"exited":         {&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL, Exited: true, ExitStatus: 1}, ""},
		"exited, legacy": {&pb.Pane{Exited: true}, ""},
		"running":        {&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL}, "still running"},
		"browser":        {&pb.Pane{Kind: pb.PaneKind_PANE_KIND_BROWSER, Exited: true}, "only a terminal"},
		"editor":         {&pb.Pane{Kind: pb.PaneKind_PANE_KIND_EDITOR}, "only a terminal"},
		"asleep":         {&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL, Exited: true, Asleep: true}, "asleep"},
		"scheduled run":  {&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL, Exited: true, ScheduleRunId: "r"}, "automation_run"},
	} {
		got := whyNotRerun(c.p)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// rerun_pane is one of the pane tools, taking the pane to run again.
func TestRerunPaneTool(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "hammock"}, nil)
	(&mcpServer{}).register(server)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if tool.Name != "rerun_pane" {
			continue
		}
		if s, ok := tool.InputSchema.(map[string]any); !ok || !strings.Contains(strings.Join(toStrings(s["required"]), ","), "pane_id") {
			t.Errorf("pane_id isn't required: %#v", tool.InputSchema)
		}
		return
	}
	t.Fatal("no rerun_pane tool")
}

func toStrings(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// Renamed tools still answer to their old names, unadvertised in the docs.
func TestAutomationToolsKeepOldNames(t *testing.T) {
	ctx := t.Context()
	s := &mcpServer{kindTools: map[string][]string{}}
	server := mcp.NewServer(&mcp.Implementation{Name: "hammock"}, nil)
	s.registerKind(server, "schedules")
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	names := map[string]bool{}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names[tool.Name] = true
	}
	for n, old := range toolOldNames {
		if !names[n] || !names[old] || !oldToolName(old) {
			t.Errorf("%s / %s: %v", n, old, names)
		}
	}
}
