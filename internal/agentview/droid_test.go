package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// testdata/droid/session.jsonl is a hand-made Droid session in the format
// droid 1.6.0 writes (its session_start and message events, the message
// content as its R1e serializer saves it: Anthropic blocks, tool_use_id in
// snake case; its tools' input schemas; system notices with visibility
// "user_only", model-only text "llm_only", hook records with hookEventName).
func TestDroidBlocks(t *testing.T) {
	f, err := os.Open("testdata/droid/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDroid("", false)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		d.Feed(sc.Bytes())
	}
	var got []string
	for _, b := range d.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Output != "" {
			s += " → " + oneLine(b.Output)
		}
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Pending {
			s += " (pending)"
		}
		got = append(got, s)
	}
	want := []string{
		"USER Add a --top N flag to tally.sh.",
		"THINKING Look at the script first.",
		"TEXT Let me look at the script.",
		"TOOL Show tally.sh → #!/usr/bin/env bash wc -w *.md | sort -rn",
		`TOOL Edited tally.sh → File edited successfully. | -wc -w *.md | sort -rn / +top="${2:-0}" / +wc -w *.md | sort -rn`,
		"TOOL Wrote README.md → File created successfully | +# tally",
		"TOOL Edited notes.md → Patch applied | @@ notes.md / -old line / +new line",
		"TOOL Listed the folder → README.md notes.md tally.sh",
		"TOOL Updated the to-do list → Todo list updated",
		"TOOL Found files matching `*.md, *.sh` → README.md notes.md tally.sh",
		"NOTE Model switched to GPT-6.1",
		"SUBAGENT Run the testsworker → All 4 tests pass.",
		"TEXT Added --top N: it prints the N biggest files.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	byTitle := map[string]*pb.AgentBlock{}
	for _, b := range d.Blocks() {
		byTitle[b.Title] = b
	}
	// Droid's tools show as Claude Code's that do the same (the apps' icons).
	if b := byTitle["Show tally.sh"]; b.Tool != "Bash" || b.Input != "cat tally.sh" {
		t.Errorf("command: %v", b)
	}
	if b := byTitle["Edited notes.md"]; b.Tool != "Edit" || b.Added != 1 || b.Removed != 1 {
		t.Errorf("patch: %v", b)
	}
	if b := byTitle["Updated the to-do list"]; !strings.Contains(b.Input, "[in_progress] Test it") {
		t.Errorf("todos: %q", b.Input)
	}
	if !d.Referenced("/work/tally/notes.md") || !d.Referenced("/work/tally/tally.sh") {
		t.Error("edited files not referenced")
	}
	if m, tok, win := d.Context(); m != "gpt-6.1-sol" || tok != 0 || win != 0 {
		t.Errorf("context: %s %d %d", m, tok, win)
	}
	// Every block belongs to your message's turn.
	for _, b := range d.Blocks() {
		if b.TurnId != "m1" {
			t.Errorf("%s: turn %q", b.Title, b.TurnId)
		}
	}
	if d.Cwd != "/work/tally" {
		t.Errorf("cwd from session_start: %q", d.Cwd)
	}
}

// A call still running is pending; its result arriving ends it. A message
// you send again from the same point replaces the branch (a rewind).
func TestDroidPendingAndRewind(t *testing.T) {
	d := NewDroid("/w", false)
	d.Feed([]byte(`{"type":"message","id":"u1","timestamp":"2026-11-01T08:00:00Z","message":{"role":"user","content":[{"type":"text","text":"run the tests"}]}}`))
	d.Feed([]byte(`{"type":"message","id":"a1","parentId":"u1","timestamp":"2026-11-01T08:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Execute","input":{"command":"go test ./..."}}]}}`))
	if p := d.Pending(); p == nil || p.Title != "Ran `go test ./...`" {
		t.Fatalf("pending: %v", p)
	}
	d.Feed([]byte(`{"type":"message","id":"r1","parentId":"a1","timestamp":"2026-11-01T08:00:09Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"FAIL ./x"}]}}`))
	if p := d.Pending(); p != nil {
		t.Fatalf("still pending: %v", p)
	}
	if b := d.Blocks()[1]; !b.Error || b.Output != "FAIL ./x" {
		t.Fatalf("result: %v", b)
	}
	d.Feed([]byte(`{"type":"message","id":"u2","parentId":"a1","timestamp":"2026-11-01T08:00:10Z","message":{"role":"user","content":[{"type":"text","text":"never mind"}]}}`))
	d.Feed([]byte(`{"type":"message","id":"u3","timestamp":"2026-11-01T08:00:11Z","message":{"role":"user","content":[{"type":"text","text":"try again"}]}}`))
	if n := len(d.Blocks()); n != 4 {
		t.Fatalf("blocks: %d", n)
	}
	// Not Droid's: nothing.
	if out := d.Feed([]byte(`{"type":"agent_turn_outcome","turnId":"u3","reason":"completed"}`)); out != nil {
		t.Fatalf("outcome: %v", out)
	}
}
