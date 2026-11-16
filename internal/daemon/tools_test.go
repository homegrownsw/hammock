package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A tool turned off in Settings is refused to agents, whatever MCP tools
// their session has, and only to them.
func TestToolOffRefusedToAgents(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("diff-enabled = false\nbrowser-enabled = true\nschedules-enabled = false\nsessions-enabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diffTab := &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{}}}}}
	if err := toolCheck(diffTab); err == nil || !strings.Contains(err.Error(), "diff viewer is turned off") {
		t.Fatalf("a diff pane: %v", err)
	}
	review := &pb.ClientMessage{Msg: &pb.ClientMessage_ReviewCommand{ReviewCommand: &pb.ReviewCommand{}}}
	if err := toolCheck(review); err == nil {
		t.Fatal("review threads go with diff panes")
	}
	for _, c := range []struct {
		m    *pb.ClientMessage
		name string
	}{
		{&pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{}}}, "Scheduled Tasks"},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{}}}, "Scheduled Tasks"},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: &pb.SessionExcerpts{}}}, "Session Search"},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{}}}, "Session Search"},
	} {
		if err := toolCheck(c.m); err == nil || !strings.Contains(err.Error(), c.name+" is turned off") {
			t.Errorf("%T: %v", c.m.Msg, err)
		}
	}
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Url: "http://localhost:3000"}}}},
		{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{Spec: &pb.PaneSpec{Command: []string{"git", "diff"}}}}},
		{Msg: &pb.ClientMessage_ShowOutput{ShowOutput: &pb.ShowOutput{}}},
		{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{Op: "devices"}}},
	} {
		if err := toolCheck(m); err != nil {
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
