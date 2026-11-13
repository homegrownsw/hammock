package daemon

import (
	"image"
	"image/color"
	"image/png"

	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/agentview"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/vt"
)

func TestWindowStartCaps(t *testing.T) {
	turn := func(tools int, out string) []*pb.AgentBlock {
		bs := []*pb.AgentBlock{{Kind: pb.AgentBlock_KIND_USER}}
		for range tools {
			bs = append(bs, &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Output: out})
		}
		return bs
	}
	// Short turns: 40 of them.
	var blocks []*pb.AgentBlock
	for range 60 {
		blocks = append(blocks, turn(1, "ok")...)
	}
	if got := len(blocks) - windowStart(blocks, len(blocks)); got != 80 {
		t.Fatalf("short turns: page of %d blocks, want 80", got)
	}
	// One huge turn: capped by blocks.
	blocks = turn(1000, "ok")
	if got := len(blocks) - windowStart(blocks, len(blocks)); got != agentBlocks {
		t.Fatalf("long turn: page of %d blocks, want %d", got, agentBlocks)
	}
	// Big outputs: capped by bytes.
	blocks = turn(100, strings.Repeat("x", 16<<10))
	if got := len(blocks) - windowStart(blocks, len(blocks)); got > 17 {
		t.Fatalf("big outputs: page of %d blocks", got)
	}
}

func TestEscInterruptMarksClaudeIdle(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		// Like Claude: a keystroke (Esc) stops it, and it says so.
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", `stty -icanon -echo min 1; dd bs=1 count=1 >/dev/null 2>&1; echo "  ⎿  Interrupted · What should Claude do instead?"; exec cat`}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"go"}`)})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && p.GetAgent().GetState() == pb.AgentState_AGENT_STATE_WORKING {
				return true
			}
		}
		return false
	})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("\x1b")}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && p.GetAgent().GetState() == pb.AgentState_AGENT_STATE_IDLE && p.GetAgent().GetSeen() {
				return true
			}
		}
		return false
	})
}

func TestClaudeStopSummaryIsItsReply(t *testing.T) {
	_, summary, _ := classify(agents.Hook{Event: "Stop", LastMessage: "Fixed the map judder.\nMore detail."})
	if summary != "Fixed the map judder." {
		t.Fatalf("summary %q", summary)
	}
}

// TestSubagents: the main view lists the session's subagents, and watching
// one shows its own transcript.
func TestSubagents(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()

	// A session file, and one subagent beside it.
	sess := allowTranscripts(t)
	main := filepath.Join(sess, "s.jsonl")
	write := func(path, s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(main, `{"type":"user","uuid":"u1","timestamp":"2026-10-31T10:00:00Z","message":{"role":"user","content":"review it"}}`+"\n")
	subs := filepath.Join(sess, "s", "subagents")
	if err := os.MkdirAll(subs, 0o700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(subs, "agent-abc123.meta.json"), `{"agentType":"general-purpose","description":"Review the diff","finished":false}`)
	write(filepath.Join(subs, "agent-abc123.jsonl"),
		`{"type":"user","isSidechain":true,"uuid":"s1","timestamp":"2026-10-31T10:00:01Z","message":{"role":"user","content":"Look at the diff"}}`+"\n"+
			`{"type":"assistant","isSidechain":true,"uuid":"s2","timestamp":"2026-10-31T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"It looks fine."}]}}`+"\n")
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"go","transcript_path":"` + main + `"}`)})

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		v := ev.GetAgentView()
		return v != nil && v.SubagentId == "" && len(v.Subagents) == 1 && v.Subagents[0].Id == "abc123" &&
			v.Subagents[0].Description == "Review the diff" && !v.Subagents[0].Finished
	})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true, SubagentId: "abc123"}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		v := ev.GetAgentView()
		if v == nil || v.SubagentId != "abc123" || !v.Working {
			return false
		}
		for _, b := range v.Blocks {
			if b.Kind == pb.AgentBlock_KIND_TEXT && b.Text == "It looks fine." {
				return true
			}
		}
		return false
	})
	// A path out of the folder isn't a subagent.
	if r, err := c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true, SubagentId: "../x"}}}); err == nil && r.GetError() == nil {
		t.Fatal("../x accepted as a subagent id")
	}
}

// Needs You's Allow types only while the pane shows a prompt: with the
// prompt gone, nothing is typed and the reply says so.
func TestDecisionNeedsAPromptOnScreen(t *testing.T) {
	if !vt.Emulated {
		t.Skip("needs libghostty-vt (-tags ghostty): the fallback mirror doesn't see the screen cleared")
	}
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	prompt, _ := os.ReadFile(filepath.Join("..", "agentview", "testdata", "permission.txt"))
	script := filepath.Join(t.TempDir(), "prompt.txt")
	os.WriteFile(script, prompt, 0o600)
	// Shows the prompt, reports the first key it gets, clears the screen,
	// then reports anything typed after.
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", `stty -icanon -icrnl -echo min 1; cat "$0"; k=$(dd bs=1 count=1 2>/dev/null | od -An -c | tr -d ' '); printf '\033[2J\033[Hgot[%s]\n' "$k"; exec cat -v`, script}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitOutput(t, c, paneID, "Esc to cancel")
	allow := &pb.ClientMessage{Msg: &pb.ClientMessage_AgentAnswer{AgentAnswer: &pb.AgentAnswer{PaneId: paneID, Decision: pb.AgentAnswer_DECISION_ALLOW}}}
	call(t, c, allow)
	if out := waitOutput(t, c, paneID, "got["); !strings.Contains(out, `got[\r]`) {
		t.Fatalf("allow typed: %q", out)
	}
	// The prompt's gone: refused, and nothing reaches the pane.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, allow); err == nil || !strings.Contains(err.Error(), "isn't asking") {
		t.Fatalf("allow with no prompt: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("z")}}})
	out := waitOutput(t, c, paneID, "z")
	if strings.Contains(out, "^M") || strings.Contains(out, "^[") {
		t.Fatalf("typed into the pane: %q", out)
	}
}

// TestRunningSubagents: a subagent counts while its record isn't finished
// and its transcript is being written; a finished or long-quiet one doesn't.
func TestRunningSubagents(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("agent-a.meta.json", `{"finished":false}`)
	write("agent-a.jsonl", "{}\n")
	write("agent-b.meta.json", `{"finished":true}`)
	write("agent-b.jsonl", "{}\n")
	write("agent-c.meta.json", `{"finished":false}`) // a killed session's
	write("agent-c.jsonl", "{}\n")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "agent-c.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}
	if n := runningSubagents(dir); n != 1 {
		t.Fatalf("running %d, want 1", n)
	}
}

// A Codex pane has an agent view too: its rollout (as its hooks name it)
// read into blocks, and what's appended to it sent as it comes.
func TestCodexAgentView(t *testing.T) {
	roots := allowTranscripts(t)
	fixture, err := os.ReadFile("../thread/testdata/golden/codex-0.166.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(roots, "rollout-2026-11-01T08-04-22-019c0000-0000-7000-8000-00000000c0de.jsonl")
	lines := strings.SplitAfter(string(fixture), "\n")
	os.WriteFile(path, []byte(strings.Join(lines[:10], "")), 0o600) // up to the reasoning

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "codex", Payload: json.RawMessage(
		fmt.Sprintf(`{"hook_event_name":"UserPromptSubmit","session_id":"019c0000-0000-7000-8000-00000000c0de","transcript_path":%q,"prompt":"Add a --top N flag"}`, path))})

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true}}})
	first := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetAgentView() != nil && len(ev.GetAgentView().Blocks) > 0 }).GetAgentView()
	if len(first.Blocks) != 1 || first.Blocks[0].Text != "Add a --top N flag to tally.sh." {
		t.Fatalf("first view: %v", first.Blocks)
	}
	// Codex's own slash commands, not Claude's.
	if !slices.Contains(first.Commands, "/model") || !slices.Contains(first.Commands, "/review") || slices.Contains(first.Commands, "/rewind") {
		t.Fatalf("commands: %v", first.Commands)
	}
	// Its built-ins only run at a message's start; its saved prompts aren't among them.
	if !slices.Contains(first.StartCommands, "/model") || slices.ContainsFunc(first.StartCommands, func(c string) bool {
		return !slices.Contains(first.Commands, c) || strings.HasPrefix(c, "/prompts:")
	}) {
		t.Fatalf("start commands: %v", first.StartCommands)
	}
	// Codex carries on: its command, edit and reply arrive.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(strings.Join(lines[10:], ""))
	f.Close()
	var titles []string
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, b := range ev.GetAgentView().GetBlocks() {
			titles = append(titles, b.Title+b.Text)
		}
		return slices.Contains(titles, "Added --top N: it prints the N biggest files, then the total.")
	})
	if !slices.Contains(titles, "Ran `cat tally.sh && ls *.md`") || !slices.Contains(titles, "Edited /work/tally/tally.sh") && !slices.Contains(titles, "Edited tally.sh") {
		t.Fatalf("blocks: %q", titles)
	}
}

// A pasted image's original PNG is served only while it's the same paste
// (paste numbers are reused, overwriting it) and has transparency.
func TestPastedOriginal(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, model color.Model) string {
		p := filepath.Join(dir, name)
		var img image.Image
		if model == color.RGBAModel {
			img = image.NewNRGBA(image.Rect(0, 0, 4, 4))
		} else {
			img = image.NewGray(image.Rect(0, 0, 4, 4))
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		return p
	}
	clear := write("1.png", color.RGBAModel)
	opaque := write("2.png", color.GrayModel)
	now := time.Now()
	if pastedOriginal(agentview.Image{Original: clear, Sent: now}) == nil {
		t.Fatal("transparent PNG not served")
	}
	if pastedOriginal(agentview.Image{Original: opaque, Sent: now}) != nil {
		t.Fatal("opaque PNG served (the JPEG is smaller)")
	}
	if pastedOriginal(agentview.Image{Original: clear, Sent: now.Add(-time.Hour)}) != nil {
		t.Fatal("PNG written after the message served: a later paste")
	}
	link := filepath.Join(dir, "3.png")
	if err := os.Symlink(clear, link); err != nil {
		t.Fatal(err)
	}
	if pastedOriginal(agentview.Image{Original: link, Sent: now}) != nil {
		t.Fatal("symlink followed")
	}
	if pastedOriginal(agentview.Image{Original: filepath.Join(dir, "gone.png"), Sent: now}) != nil {
		t.Fatal("missing file served")
	}
}

// An agent whose hooks never say it's asking (Cursor's) waits for you once
// its screen shows an approval prompt, though its hooks last said working.
func TestScreenAsksWhileHooksFlow(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", `sleep 1; echo "  Run this command?"; echo "  ls -la"; exec cat`}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "cursor", Payload: json.RawMessage(`{"hook_event_name":"beforeSubmitPrompt","conversation_id":"c1","prompt":"go"}`)})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && p.GetAgent().GetState() == pb.AgentState_AGENT_STATE_NEEDS_INPUT {
				return true
			}
		}
		return false
	})
}

func TestSendParts(t *testing.T) {
	show := func(ps []sendPart) string {
		var s []string
		for _, p := range ps {
			if p.path != "" {
				s = append(s, "<"+p.path+">")
			} else {
				s = append(s, p.text)
			}
		}
		return strings.Join(s, "|")
	}
	for _, tc := range []struct {
		name, text string
		paths      []string
		inline     bool
		want       string
	}{
		{"paths first", "  hello ", []string{"a", "b"}, false, "<a>|<b>|hello"},
		{"inline", "compare ￼ with ￼ please", []string{"a", "b"}, true, "compare |<a>| with |<b>| please"},
		{"inline at start", "￼ what's this?", []string{"a"}, true, "<a>| what's this?"},
		{"inline, only attachments", " ￼ ￼ ", []string{"a", "b"}, true, "<a>| |<b>"},
		{"more paths than marks", "see ￼", []string{"a", "b"}, true, "see |<a>|<b>"},
		{"more marks than paths", "￼ and ￼", []string{"a"}, true, "<a>| and "},
		{"nothing", "  ￼ ", nil, true, ""},
	} {
		if got := show(sendParts(tc.text, tc.paths, tc.inline)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
