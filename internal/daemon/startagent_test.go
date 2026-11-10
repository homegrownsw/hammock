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
	// And the pane it starts is its own, not yours: made by it, as one it
	// opened with CreateTab would be.
	r, err := agent.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{
		Harness: "claude", Cwd: work, GroupId: gid, Prompt: "help",
	}}})
	if err != nil {
		t.Fatalf("an agent couldn't start one: %v", err)
	}
	d.mu.Lock()
	np := d.tabs[client.CreatedID(r)].FocusedPaneId
	made, by := d.agentMade[np], d.createdBy(np)
	mine := d.agentMade[pane]
	d.mu.Unlock()
	if !made || by != pane {
		t.Errorf("an agent's start: agent-made %v, made by %q, want %q", made, by, pane)
	}
	if mine {
		t.Error("your own start counted as an agent's")
	}
}

// A prompt is the agent's first message and nothing else: control
// characters (^U to clear the line, Return to run what's left) and quotes
// in it never reach a shell as keystrokes or code. The agent is the pane's
// command, and a shell stays once it exits.
func TestStartAgentPromptIsInert(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf 'STARTED[%s]\\n' \"$@\"\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	plainShell(t)
	t.Setenv("ENV", "")
	work := t.TempDir()
	pwned := filepath.Join(work, "pwned")

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	prompt := "\x15touch " + pwned + "\r" + "'; touch " + pwned + "; '\\'$(touch " + pwned + ")\x1b[201~"
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{
		Harness: "claude", Cwd: work, GroupId: gid, Prompt: prompt,
	}}}))
	d.mu.Lock()
	pane := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pane}}})
	// The fake claude prints its argument and exits; the shell after it
	// echoes a marker, so all of the start has run by then.
	waitOutput(t, c, pane, "STARTED[touch "+pwned)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("echo SHELL-AFTER\r")}}})
	waitOutput(t, c, pane, "SHELL-AFTER")
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("the prompt ran a command")
	}
}

// Started with `-l -c`, an agent the login shell doesn't find (on a PATH
// only ~/.zshrc sets) is started from where it's installed instead; one
// it finds is started by name.
func TestAgentCommandFallsBackToInstalled(t *testing.T) {
	plainShell(t)
	t.Setenv("ENV", "")
	inst := t.TempDir()
	os.WriteFile(filepath.Join(inst, "claude"), []byte("#!/bin/sh\n"), 0o755)
	oldDirs := agentInstallDirs
	agentInstallDirs = []string{inst}
	t.Cleanup(func() { agentInstallDirs = oldDirs; agentsCache.at = time.Time{} })
	claude := mustAgent(t, "claude")

	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")
	agentsCache.at = time.Time{}
	if got, want := agentCommand(claude, "claude 'go'"), shellQuote(filepath.Join(inst, "claude"))+" 'go'"; got != want {
		t.Errorf("not on PATH: %q, want %q", got, want)
	}
	if got := agentCommand(claude, "claudette 'go'"); got != "claudette 'go'" {
		t.Errorf("another command: %q", got)
	}

	t.Setenv("PATH", inst+":/usr/bin:/bin")
	agentsCache.at = time.Time{}
	if got := agentCommand(claude, "claude 'go'"); got != "claude 'go'" {
		t.Errorf("on PATH: %q", got)
	}
}
