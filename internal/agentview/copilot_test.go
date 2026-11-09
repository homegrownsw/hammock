package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// testdata/copilot/events.jsonl is a hand-made GitHub Copilot CLI session
// log (~/.copilot/session-state/<id>/events.jsonl), written to the event
// schema @github/copilot 1.0.107 ships (schemas/session-events.schema.json,
// and its SDK's generated/session-events.d.ts): the envelope {type, data,
// id, timestamp, parentId, agentId?} and the events written to disk, with
// Copilot's own tools and arguments (view {path}, edit {path, old_str,
// new_str}, bash {command}, apply_patch given the patch as a string, MCP
// tools with mcpServerName/mcpToolName). A subagent's event (agentId) and a
// skill's injected message are left out.
func TestCopilotBlocks(t *testing.T) {
	f, err := os.Open("testdata/copilot/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := NewCopilot("")
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	var pendingSeen bool
	for sc.Scan() {
		c.Feed(sc.Bytes())
		if p := c.Pending(); p != nil && p.Id == "call_bash" {
			pendingSeen = true
		}
	}
	if !pendingSeen {
		t.Error("a requested tool call isn't pending before it runs")
	}
	var got []string
	for _, b := range c.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Output != "" {
			s += " → " + oneLine(b.Output)
		}
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Error {
			s += " (error)"
		}
		if b.Pending {
			s += " (pending)"
		}
		got = append(got, s)
	}
	want := []string{
		"USER Add a --top N flag to tally.sh.",
		"THINKING Look at the script first.",
		"TOOL Read tally.sh → 1. #!/bin/sh 2. sort | uniq -c",
		"TEXT I'll add the flag.",
		`TOOL Edited tally.sh | -sort | uniq -c / +sort | uniq -c | sort -rn | head -n "$TOP"`,
		"TOOL Ran `sh tally.sh --top 3 < words.txt` → tally.sh: line 3: TOP: unbound variable <exited with exit code 1> (error)",
		"TOOL Edited tally.sh | @@ tally.sh / -#!/bin/sh / +#!/bin/sh / +TOP=${2:-10}",
		"TOOL Used get_issue (github-mcp-server) → Issue not found (error)",
		"TEXT Added `--top N` (default 10).",
		"USER Now run the tests",
		"TOOL Ran `make test` → Cancelled (error)",
		"NOTE Interrupted",
		"NOTE Conversation compacted",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if c.cx.Cwd != "/Users/a/proj" || !c.Referenced("/Users/a/proj/tally.sh") {
		t.Errorf("cwd %q, tally.sh referenced %v", c.cx.Cwd, c.Referenced("/Users/a/proj/tally.sh"))
	}
	if model, tokens, window := c.Context(); model != "gpt-5.6-sol" || tokens != 12000 || window != 128000 {
		t.Errorf("context: %s %d %d", model, tokens, window)
	}
	for _, b := range c.Blocks() {
		if b.Kind.String() != "KIND_USER" && b.TurnId == "" {
			t.Errorf("%s has no turn", b.Id)
		}
	}
}
