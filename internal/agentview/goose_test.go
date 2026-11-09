package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// A session as agents.Goose's Open writes it: Hammock's session line, then
// one line per row of goose's messages table, its content_json and
// metadata_json as goose 1.58.0 serializes them (MessageContent,
// ToolResult's {status, value|error}, rmcp's CallToolResult; the hidden
// SessionStart message and a turn-context message only the model sees).
// Hand-made.
func TestGooseBlocks(t *testing.T) {
	f, err := os.Open("testdata/goose/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g := NewGoose("")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		g.Feed(sc.Bytes())
	}
	var got []string
	for _, b := range g.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Output != "" {
			s += " > " + strings.ReplaceAll(b.Output, "\n", " / ")
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
		"TEXT I'll look at the script first.",
		"TOOL Ran `cat tally.sh` > #!/bin/sh / wc -w *.md",
		"THINKING Take the count from $2.",
		`TOOL Edited tally.sh | -wc -w *.md / +top="$2" / +wc -w *.md | head -n "$top"`,
		"TOOL Updated the plan > Updated",
		"TEXT Added --top N: it prints the N biggest files.",
		"USER Now run it.",
		"TOOL Ran `./tally.sh --top 2` > sh: ./tally.sh: Permission denied /  / Command exited with code 126 (error)",
		"TOOL Used search_issues (github) > -32603: Tool not found (error)",
		"NOTE Context compacted to fit the model's window",
		"TOOL Edited tally.sh | -#!/bin/sh / +#!/bin/bash (pending)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	turn := ""
	seen := map[string]bool{}
	for _, b := range g.Blocks() {
		if seen[b.Id] {
			t.Errorf("id %s twice", b.Id)
		}
		seen[b.Id] = true
		if b.Kind.String() == "KIND_USER" {
			turn = b.Id
		}
		if b.TurnId != turn {
			t.Errorf("%s: turn %q, want %q", b.Title+b.Text, b.TurnId, turn)
		}
	}
	if p := g.Pending(); p == nil || p.Title != "Edited tally.sh" {
		t.Errorf("pending: %v", p)
	}
	if !g.Referenced("/work/tally.sh") {
		t.Error("tally.sh not referenced")
	}
	if m, tok, _ := g.Context(); m != "claude-sonnet-4-5-20250929" || tok != 18234 {
		t.Errorf("context %q %d", m, tok)
	}
	if g.Blocks()[0].TimeUnixMs != 1794050001000 {
		t.Errorf("time %d", g.Blocks()[0].TimeUnixMs)
	}
}

func TestGooseTools(t *testing.T) {
	g := NewGoose("/work")
	for _, c := range []struct {
		name string
		in   map[string]any
		want string
	}{
		{"write", map[string]any{"path": "/work/a.md", "content": "hi"}, "Wrote a.md"},
		{"tree", map[string]any{"path": "/work/src", "depth": 2}, "Listed src"},
		{"read_image", map[string]any{"source": "https://x.test/a.png"}, "Looked at https://x.test/a.png"},
		{"developer__text_editor", map[string]any{"command": "view", "path": "/work/b.go"}, "Read b.go"},
		{"developer__shell", map[string]any{"command": "ls\nwc"}, "Ran `ls …`"},
		{"delegate", map[string]any{"instructions": "Review the diff"}, "Started an agent: Review the diff"},
		{"load", map[string]any{"source": "x"}, "Used load"},
	} {
		if got := g.tool(c.name, c.in).Title; got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
