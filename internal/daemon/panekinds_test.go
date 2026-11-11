package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A kind of pane turned off in Settings is refused to agents, whatever
// tools their session has, and only to them.
func TestPaneKindOffRefusedToAgents(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("diff-enabled = false\nbrowser-enabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffTab := &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{}}}}}
	if err := paneKindCheck(diffTab); err == nil || !strings.Contains(err.Error(), "diff viewer is turned off") {
		t.Fatalf("a diff pane: %v", err)
	}
	review := &pb.ClientMessage{Msg: &pb.ClientMessage_ReviewCommand{ReviewCommand: &pb.ReviewCommand{}}}
	if err := paneKindCheck(review); err == nil {
		t.Fatal("review threads go with diff panes")
	}
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Url: "http://localhost:3000"}}}},
		{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{Spec: &pb.PaneSpec{Command: []string{"git", "diff"}}}}},
		{Msg: &pb.ClientMessage_ShowOutput{ShowOutput: &pb.ShowOutput{}}},
		{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{Op: "devices"}}},
	} {
		if err := paneKindCheck(m); err != nil {
			t.Errorf("%T: %v", m.Msg, err)
		}
	}

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	person := &conn{d: d, caller: &caller{pane: "p1", direct: true}}
	if err := person.agentCheck(diffTab); err != nil {
		t.Fatalf("a person may still open one: %v", err)
	}
	agent := &conn{d: d, caller: &caller{pane: "p1", agent: true, direct: true}}
	if err := agent.agentCheck(diffTab); err == nil {
		t.Fatal("an agent opened a diff pane turned off")
	}
}
