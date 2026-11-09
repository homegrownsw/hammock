package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// A transcript as Antigravity CLI writes them (agy 1.5.x: the step format
// its bundled docs describe, with the wrapping of your messages, the
// JSON-in-a-string arguments, the RUNNING step written again and the
// result types seen in real ones, github.com/ZingerLittleBee/Heeler/
// issues/644), hand-made.
func TestAntigravityBlocks(t *testing.T) {
	a := NewAntigravity("/work")
	antigravityFeedFile(t, a, "testdata/antigravity/transcript_full.jsonl")
	var got []string
	for _, b := range a.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Output != "" {
			s += " => " + strings.ReplaceAll(b.Output, "\n", " / ")
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
		"THINKING Reading the script first.",
		"TEXT I'll look at the script first.",
		"TOOL Read tally.sh => Showing lines 1-2 of 2 / 1: #!/bin/sh / 2: wc -w *.md",
		"TOOL Ran `ls *.md` => a.md / b.md",
		`TOOL Edited tally.sh | -wc -w *.md / +top="$2" / +wc -w *.md | head -n "$top" => The file /work/tally.sh has been updated.`,
		`TOOL Searched the web for "head -n portable" => search failed (error)`,
		"TEXT Added --top N: it prints the N biggest files.",
		"USER Now run it.\n\n1 attachment",
		"TOOL Ran `./tally.sh --top 1` => Interrupted",
		"USER Stop, use mcp instead.",
		`TOOL Used mcp_github_search_issues => Interrupted`,
		"NOTE Error: RESOURCE_EXHAUSTED: quota reached …",
		"NOTE Compacted the conversation",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	seen := map[string]bool{}
	turn := ""
	for _, b := range a.Blocks() {
		if seen[b.Id] {
			t.Errorf("id %s twice", b.Id)
		}
		seen[b.Id] = true
		if b.Kind == pb.AgentBlock_KIND_USER {
			turn = b.Id
		}
		if b.TurnId != turn {
			t.Errorf("%s: turn %q, want %q", b.Id, b.TurnId, turn)
		}
	}
	if _, tokens, _ := a.Context(); tokens != 1700 {
		t.Errorf("tokens %d", tokens)
	}
	if !a.Referenced("/work/tally.sh") || a.Referenced("/etc/passwd") {
		t.Error("referenced files")
	}
	if a.Rewound() {
		t.Error("rewound without a rewind")
	}
}

// A command still running is pending until its step is written again;
// /rewind (a step_index written again with another type) drops what came
// after it.
func TestAntigravityPendingAndRewind(t *testing.T) {
	a := NewAntigravity("/work")
	feed := func(s string) []*pb.AgentBlock { return a.Feed([]byte(s)) }
	feed(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nbuild it\n</USER_REQUEST>"}`)
	feed(`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"make\""}}]}`)
	if p := a.Pending(); p == nil || p.Title != "Ran `make`" {
		t.Fatalf("pending %v", p)
	}
	feed(`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"RUNNING","content":"cc main.c"}`)
	if p := a.Pending(); p == nil || p.Output != "cc main.c" {
		t.Fatalf("running %v", p)
	}
	changed := feed(`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","content":"cc main.c\nok"}`)
	if len(changed) != 1 || changed[0].Pending || changed[0].Output != "cc main.c\nok" || a.Pending() != nil {
		t.Fatalf("done %v", changed)
	}
	feed(`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Built."}`)
	if a.Rewound() {
		t.Fatal("rewound")
	}
	feed(`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"Sure."}`)
	// The same type at step 1: it's said again, as it is now.
	var texts []string
	for _, b := range a.Blocks() {
		texts = append(texts, b.Title+b.Text)
	}
	if got := strings.Join(texts, "|"); got != "build it|Built.|Sure." {
		t.Fatalf("rewritten: %s", got)
	}
	feed(`{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<USER_REQUEST>\nno, test it\n</USER_REQUEST>"}`)
	if !a.Rewound() {
		t.Fatal("not rewound")
	}
	texts = nil
	for _, b := range a.Blocks() {
		texts = append(texts, b.Title+b.Text)
	}
	if got := strings.Join(texts, "|"); got != "build it|no, test it" {
		t.Fatalf("after rewind: %s", got)
	}
}

func TestAntigravityArgsAndText(t *testing.T) {
	in := AntigravityArgs([]byte(`{"CommandLine":"\"go test\"","StartLine":"3","Overwrite":"true","Plain":"npm test","Nested":"[{\"a\":1}]"}`))
	if in["CommandLine"] != "go test" || in["StartLine"] != float64(3) || in["Overwrite"] != true || in["Plain"] != "npm test" || len(asAny(in["Nested"])) != 1 {
		t.Fatalf("%#v", in)
	}
	if s := AntigravityUserText("<USER_REQUEST>\nhi\nthere\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nx\n</ADDITIONAL_METADATA>"); s != "hi\nthere" {
		t.Fatalf("%q", s)
	}
	if s := AntigravityUserText("plain\n<ADDITIONAL_METADATA>\nx\n</ADDITIONAL_METADATA>"); s != "plain" {
		t.Fatalf("%q", s)
	}
}

func antigravityFeedFile(t *testing.T, p Parser, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		p.Feed(sc.Bytes())
	}
}
