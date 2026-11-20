package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fakeNode is sleep run as node: an agent run by node (Claude Code
// installed with npm), which nothing knows as an agent by its name. (A
// link: macOS won't run a copy of its own sleep.)
func fakeNode(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node")
	if err := os.Symlink("/bin/sleep", path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProcAncestors(t *testing.T) {
	if got := procAncestors(os.Getpid()); len(got) < 2 || got[0] != os.Getpid() || got[1] != os.Getppid() {
		t.Fatalf("procAncestors: %v", got)
	}
}

// hookFrom is hook, sent by a process whose ancestors are from.
func hookFrom(d *Daemon, pane, event string, payload map[string]any, from []int) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["hook_event_name"] = event
	b, _ := json.Marshal(payload)
	d.mu.Lock()
	d.agentHub.handle(HookEvent{PaneID: pane, Harness: "claude", Payload: b, from: from})
	d.mu.Unlock()
}

// paneChild waits for the pane's shell to run name (as its command line says).
func paneChild(t *testing.T, d *Daemon, paneID, name string) (shell, child int) {
	t.Helper()
	waitFor(t, name+" under the pane's shell", func() bool {
		d.mu.Lock()
		p := d.panes[paneID]
		d.mu.Unlock()
		p.mu.Lock()
		shell = p.pid
		p.mu.Unlock()
		for _, c := range procinfo.Children(shell) {
			if args := procinfo.Args(c); len(args) > 0 && filepath.Base(args[0]) == name {
				child = c
				return true
			}
		}
		return false
	})
	return shell, child
}

func agentOf(d *Daemon, paneID string) (*pb.Pane, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, item := d.attention["at_"+paneID]
	return d.panes[paneID].snapshotInfo(), item
}

// detect runs the agents' poll as often as it takes to see an agent gone.
func detect(d *Daemon) {
	for range goneTicks {
		d.mu.Lock()
		d.detectAgents()
		d.mu.Unlock()
	}
}

func waitExited(t *testing.T, pid int) {
	t.Helper()
	waitFor(t, "the process to go", func() bool {
		_, err := procinfo.StartTime(pid)
		return err != nil
	})
}

// A hooked agent whose process dies without its SessionEnd (killed, crashed)
// ends: not working or waiting for you, its Needs You item gone, its session
// not resumed after a restart. Stopped (^Z), it hasn't ended.
func TestHookedAgentDiesUnannounced(t *testing.T) {
	node := fakeNode(t)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", node + " 600; exec /bin/sh"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	shell, agent := paneChild(t, d, paneID, "node")

	// Its hooks come from a process under it (sh -c, then hmk).
	from := []int{99999, agent, shell, 1}
	hookFrom(d, paneID, "SessionStart", map[string]any{"session_id": "s1"}, from)
	hookFrom(d, paneID, "UserPromptSubmit", map[string]any{"session_id": "s1", "prompt": "go"}, from)
	hookFrom(d, paneID, "Notification", map[string]any{"notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"}, from)
	info, item := agentOf(d, paneID)
	if info.Agent.GetState() != pb.AgentState_AGENT_STATE_NEEDS_INPUT || !item || d.agentHub.resumeCommand(info) == nil {
		t.Fatalf("before: %v, item %v", info.Agent, item)
	}

	syscall.Kill(agent, syscall.SIGSTOP)
	detect(d)
	detect(d)
	if info, item := agentOf(d, paneID); info.Agent.GetState() != pb.AgentState_AGENT_STATE_NEEDS_INPUT || !item {
		t.Fatalf("stopped: %v, item %v", info.Agent, item)
	}
	syscall.Kill(agent, syscall.SIGCONT)

	syscall.Kill(agent, syscall.SIGKILL)
	waitExited(t, agent)
	detect(d)
	info, item = agentOf(d, paneID)
	if info.Agent.GetState() != pb.AgentState_AGENT_STATE_EXITED || item {
		t.Fatalf("killed: %v, item %v", info.Agent, item)
	}
	if cmd := d.agentHub.resumeCommand(info); cmd != nil {
		t.Fatalf("resumed after a restart: %v", cmd)
	}
	if info.Agent.Summary != "" {
		t.Fatalf("still says it's asking: %q", info.Agent.Summary)
	}
}

// Before its process is known (the daemon restarted since its last hook),
// its shell back at its prompt with nothing under it says it's gone; a
// process still under it (stopped, or running) says it isn't.
func TestHookedAgentGoneShellQuiet(t *testing.T) {
	old := quietShellAge
	quietShellAge = 0
	t.Cleanup(func() { quietShellAge = old })
	node := fakeNode(t)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", node + " 600; exec /bin/sh"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	_, agent := paneChild(t, d, paneID, "node")

	hookFrom(d, paneID, "UserPromptSubmit", map[string]any{"session_id": "s1", "prompt": "go"}, nil)
	syscall.Kill(agent, syscall.SIGSTOP)
	detect(d)
	detect(d)
	if info, _ := agentOf(d, paneID); info.Agent.GetState() != pb.AgentState_AGENT_STATE_WORKING {
		t.Fatalf("stopped: %v", info.Agent)
	}
	syscall.Kill(agent, syscall.SIGKILL)
	waitExited(t, agent)
	waitFor(t, "the agent's end", func() bool {
		detect(d)
		info, _ := agentOf(d, paneID)
		return info.Agent.GetState() == pb.AgentState_AGENT_STATE_EXITED
	})
}

// A scheduled run of an agent run by node that dies without its SessionEnd
// is over, so the next isn't skipped as still going (#34).
func TestScheduledRunAgentDies(t *testing.T) {
	node := fakeNode(t)
	bin := filepath.Dir(node)
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexec \""+node+"\" 600\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	plainShell(t)
	t.Setenv("ENV", "")
	clock := useSchedClock(t, time.Date(2026, 11, 4, 8, 29, 0, 0, time.Local))
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Assistant"}}}))
	s := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Name: "Briefing", Kind: "claude", Prompt: "/briefing", Cwd: t.TempDir(), GroupId: gid, MinuteOfDay: 8*60 + 30,
	}}}}).GetScheduleReply().GetSchedule()
	tick(d, clock, time.Date(2026, 11, 4, 8, 30, 0, 0, time.Local))
	run := schedOf(d, s.Id).Runs[0]
	shell, agent := paneChild(t, d, run.PaneId, "node")

	from := []int{agent, shell}
	hookFrom(d, run.PaneId, "SessionStart", map[string]any{"session_id": "s1"}, from)
	hookFrom(d, run.PaneId, "UserPromptSubmit", map[string]any{"session_id": "s1", "prompt": "/briefing"}, from)
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_RUNNING {
		t.Fatalf("working: %v", r)
	}
	syscall.Kill(agent, syscall.SIGKILL)
	waitExited(t, agent)
	detect(d)
	if r := schedOf(d, s.Id).Runs[0]; r.Status == pb.ScheduleRun_STATUS_RUNNING || r.EndedUnixMs == 0 {
		t.Fatalf("its agent died, the run still going: %v", r)
	}
}
