package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// A transcript as cursor-agent 2026.11.05 writes them (its agent-transcript
// package's TranscriptStore: role/message lines, tool_use without results,
// turn_ended lines; the <timestamp> and <user_query> wrapping as posted at
// forum.cursor.com/t/166592), hand-made.
func TestCursorBlocks(t *testing.T) {
	f, err := os.Open("testdata/cursor/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := NewCursor("/work")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c.Feed(sc.Bytes())
	}
	var got []string
	for _, b := range c.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		got = append(got, s)
	}
	want := []string{
		"USER Add a --top N flag to tally.sh.",
		"TEXT I'll look at the script first.",
		"TOOL Read tally.sh",
		"TOOL Ran `ls *.md`",
		`TOOL Edited tally.sh | -wc -w *.md / +top="$2" / +wc -w *.md | head -n "$top"`,
		"TOOL Found files matching **/*.md",
		"TOOL Updated the plan",
		"TEXT Added --top N: it prints the N biggest files.",
		"USER Now run it.",
		"TOOL Used search_issues (github)",
		"NOTE Interrupted",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Ids are unique; each turn's blocks belong to your message.
	seen := map[string]bool{}
	turn := ""
	for _, b := range c.Blocks() {
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
	if !c.Referenced("/work/tally.sh") || c.Pending() != nil {
		t.Error("tally.sh not referenced, or a block pending")
	}
	at := time.UnixMilli(c.Blocks()[0].TimeUnixMs).UTC()
	if want := time.Date(2026, 7, 23, 21, 20, 0, 0, time.UTC); !at.Equal(want) {
		t.Errorf("time %v, want %v", at, want)
	}
}

func TestCursorUserText(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                             "plain",
		"<user_query>\n  hi\n</user_query>": "hi",
		"<timestamp>x</timestamp>\n<user_query>a\nb</user_query>": "a\nb",
	} {
		if got, _ := CursorUserText(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if _, at := CursorUserText("<timestamp>Monday, Nov 2, 2026, 9:05 AM (UTC-5:30)</timestamp>hi"); !at.Equal(time.Date(2026, 11, 2, 14, 35, 0, 0, time.UTC)) {
		t.Errorf("offset: %v", at.UTC())
	}
}
