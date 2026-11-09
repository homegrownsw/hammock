package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Starting an agent opens a tab in the folder, in the Group asked for, and
// runs it there with the prompt; the folders list suggests the Group's
// and browses others.
func TestStartAgent(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho STARTED \"$@\"\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin") // nothing real called claude
	plainShell(t)
	t.Setenv("ENV", "")
	agentsCache.at = time.Time{}
	oldDirs := agentInstallDirs
	agentInstallDirs = nil // nor where they're usually installed
	t.Cleanup(func() { agentInstallDirs = oldDirs })
	work := t.TempDir()
	os.Mkdir(filepath.Join(work, "app"), 0o755)
	os.Mkdir(filepath.Join(work, ".hidden"), 0o755)

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))

	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{
		Harness: "claude", Cwd: work, GroupId: gid, Prompt: "fix the 'build'", TabId: "tb_strtagnt",
	}}}))
	if tid != "tb_strtagnt" {
		t.Fatalf("tab %q", tid)
	}
	d.mu.Lock()
	tab := d.tabs[tid]
	pane := tab.FocusedPaneId
	info := d.panes[pane].snapshotInfo()
	d.mu.Unlock()
	if tab.GroupId != gid || info.Cwd != work || info.Agent.GetHarness() != "claude" {
		t.Fatalf("tab %v, pane %v", tab, info)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pane}}})
	waitOutput(t, c, pane, "STARTED fix the 'build'")

	// The folders: this Group's first, and work's subfolders (not hidden ones).
	d.mu.Lock()
	d.panes[pane].mu.Lock()
	d.panes[pane].info.Cwd = work // as the shell would report it
	d.panes[pane].mu.Unlock()
	d.mu.Unlock()
	fl := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListFolders{ListFolders: &pb.ListFolders{GroupId: gid, Path: work}}}).GetFolderList()
	if len(fl.Suggestions) == 0 || fl.Suggestions[0].Path != work || fl.Suggestions[0].Why != "This Group" {
		t.Fatalf("suggestions: %v", fl.Suggestions)
	}
	if !slices.Equal(fl.Folders, []string{"app"}) || fl.Path != work || fl.Parent != filepath.Dir(work) {
		t.Fatalf("listing: %v", fl)
	}
	if !slices.Contains(fl.Agents, "claude") || slices.Contains(fl.Agents, "codex") {
		t.Fatalf("agents: %v", fl.Agents)
	}

	// Refused: another agent, a folder that isn't there, a relative one.
	for _, bad := range []*pb.StartAgent{
		{Harness: "no-such-agent", Cwd: work, GroupId: gid},
		{Harness: "claude", Cwd: filepath.Join(work, "nope"), GroupId: gid},
		{Harness: "claude", Cwd: "rel", GroupId: gid},
	} {
		if _, err := c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: bad}}); err == nil {
			t.Errorf("%v: started", bad)
		}
	}

	// An agent may start one too, in your Groups (as it could run one in a
	// pane it opens).
	agent := dialAs(t, dir, &caller{pane: pane, agent: true}, "mcp")
	if _, err := agent.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_ListFolders{ListFolders: &pb.ListFolders{GroupId: gid}}}); err != nil {
		t.Errorf("an agent couldn't list folders: %v", err)
	}
}
