package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A failed command runs again in the same pane (Run Again): its last
// screen stays above the new run's, and a pane still running, or a
// scheduled run's, is refused.
func TestRestartPane(t *testing.T) {
	t.Parallel()
	dir := shortDir(t)
	ran := filepath.Join(dir, "ran")
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "echo x >> " + ran + "; echo run-$(wc -l < " + ran + " | tr -d ' '); sleep 0.3; exit 3"}}}}}))
	d.mu.Lock()
	pid := d.tabs[tab].FocusedPaneId
	p := d.panes[pid]
	d.mu.Unlock()
	restart := &pb.ClientMessage{Msg: &pb.ClientMessage_RestartPane{RestartPane: &pb.RestartPane{PaneId: pid}}}

	if _, err := callErr(t, c, restart); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("restarting a running pane: %v", err)
	}
	waitFor(t, "the command to fail", func() bool { return p.snapshotInfo().Exited })
	if s := p.snapshotInfo().ExitStatus; s != 3 {
		t.Fatalf("exit status %d", s)
	}

	call(t, c, restart)
	waitFor(t, "it to run again", func() bool {
		b, _ := os.ReadFile(ran)
		return strings.Count(string(b), "x") == 2
	})
	waitFor(t, "its screen to show both runs", func() bool {
		s := p.text(false)
		return strings.Contains(s, "run-1") && strings.Contains(s, "started again") && strings.Contains(s, "run-2")
	})
	waitFor(t, "it to fail again", func() bool { return p.snapshotInfo().Exited })
	if info := p.snapshotInfo(); info.ExitStatus != 3 {
		t.Fatalf("second run's exit status %d", info.ExitStatus)
	}

	p.mu.Lock()
	p.info.ScheduleRunId = "r"
	p.mu.Unlock()
	if _, err := callErr(t, c, restart); err == nil || !strings.Contains(err.Error(), "scheduled") {
		t.Fatalf("restarting a scheduled run: %v", err)
	}
}

// An agent's pane resumes its session; one with nothing said in it, or no
// agent, runs its own command again.
func TestRestartCommand(t *testing.T) {
	transcript := claudeSession(t, "s1")
	d := startDaemon(t, shortDir(t))
	defer d.Shutdown()
	cmd, resumes := d.restartCommand("p1", &pb.Agent{Harness: "claude", SessionId: "s1", TranscriptPath: transcript})
	if !resumes || !strings.Contains(strings.Join(cmd, " "), "claude --resume 's1'") {
		t.Errorf("an agent's pane: %q (resumes %v)", cmd, resumes)
	}
	for name, a := range map[string]*pb.Agent{
		"no agent":     nil,
		"no session":   {Harness: "claude"},
		"nothing said": {Harness: "claude", SessionId: "never"},
		"no resume":    {Harness: "not-an-agent", SessionId: "s1", TranscriptPath: transcript},
	} {
		if cmd, resumes := d.restartCommand("p1", a); cmd != nil || resumes {
			t.Errorf("%s: %q", name, cmd)
		}
	}
}
