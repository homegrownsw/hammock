package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// feedGemini feeds a fixture to p, line by line.
func feedGemini(t *testing.T, p Parser, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		p.Feed(sc.Bytes())
	}
}

// gemSummary is each block in a line, as the Codex test writes them.
func gemSummary(blocks []*pb.AgentBlock) string {
	var got []string
	for _, b := range blocks {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Output != "" {
			s += " → " + oneLine(b.Output)
		}
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Error {
			s += " (failed)"
		}
		if b.Pending {
			s += " (pending)"
		}
		got = append(got, s)
	}
	return strings.Join(got, "\n")
}

// A Gemini CLI session as its ChatRecordingService (0.69) writes one: the
// same message written again as it changes, tool responses as user
// messages, then a prompt that was cancelled (rewound away).
func TestGeminiBlocks(t *testing.T) {
	g := NewGemini("/work/tally")
	feedGemini(t, g, "testdata/gemini/session-2026-11-07T09-12-5f1c2a7e.jsonl")
	want := strings.Join([]string{
		"USER Add a --top N flag to tally.sh",
		"THINKING **Reading the script**\nI need to see how tally.sh sorts its output.",
		"TEXT I'll read the script first.",
		"TOOL Show the script and the notes → #!/usr/bin/env bash wc -w *.md | sort -rn notes.md rules.md",
		`TOOL Edited tally.sh | #!/usr/bin/env bash / +top="$2" /  wc -w *.md | sort -rn`,
		"TEXT Added --top N: it prints the N biggest files, then the total.",
		"NOTE Interrupted",
	}, "\n")
	if got := gemSummary(g.Blocks()); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if !g.Rewound() || g.Rewound() {
		t.Error("the rewind should be reported once")
	}
	for _, b := range g.Blocks() {
		if b.TurnId != g.Blocks()[0].Id {
			t.Errorf("%s: turn %q", b.Title, b.TurnId)
		}
	}
	if b := g.Blocks()[4]; b.Added != 1 || b.Removed != 0 || b.Input != "tally.sh" {
		t.Errorf("edit: +%d -%d %q", b.Added, b.Removed, b.Input)
	}
	if m, tok, win := g.Context(); m != "gemini-3-pro-preview" || tok != 8650 || win != 1_048_576 {
		t.Errorf("context: %s %d %d", m, tok, win)
	}
	if !g.Referenced("/work/tally/tally.sh") {
		t.Error("the edited file should be referenced")
	}
}

// A compression records its summary, then the turns it kept again as new
// messages, then a $patch: the view shows a note, not the turns twice.
func TestGeminiCompression(t *testing.T) {
	g := NewGemini("/w")
	for _, l := range []string{
		`{"id":"u1","timestamp":"2026-11-07T09:00:00Z","type":"user","content":[{"text":"first"}]}`,
		`{"id":"g1","timestamp":"2026-11-07T09:00:05Z","type":"gemini","content":"reply one","model":"gemini-2.5-pro"}`,
		`{"id":"s1","timestamp":"2026-11-07T09:10:00.000Z","type":"user","content":[{"text":"<state_snapshot>\n<overall_goal>x</overall_goal>\n</state_snapshot>"}]}`,
		`{"id":"s2","timestamp":"2026-11-07T09:10:00.001Z","type":"gemini","content":[{"text":"Got it. Thanks for the additional context!"}]}`,
		`{"id":"k1","timestamp":"2026-11-07T09:10:00.002Z","type":"gemini","content":[{"text":"reply one"}]}`,
		`{"$patch":{"removeIds":["u1","g1"],"orderIds":["s1","s2","k1"]}}`,
		`{"id":"u2","timestamp":"2026-11-07T09:10:00.400Z","type":"user","content":[{"text":"second"}]}`,
		`{"id":"g2","timestamp":"2026-11-07T09:10:03Z","type":"gemini","content":"reply two","toolCalls":[{"id":"c1","name":"run_shell_command","args":{"command":"go test ./..."},"status":"error","resultDisplay":"FAIL ./x"}]}`,
		`{"id":"e1","timestamp":"2026-11-07T09:10:04Z","type":"error","content":"[API Error: quota exceeded]"}`,
	} {
		g.Feed([]byte(l))
	}
	want := strings.Join([]string{
		"USER first",
		"TEXT reply one",
		"NOTE Conversation compacted → <state_snapshot> <overall_goal>x</overall_goal> </state_snapshot>",
		"USER second",
		"TEXT reply two",
		"TOOL Ran `go test ./...` → FAIL ./x (failed)",
		"NOTE [API Error: quota exceeded]",
	}, "\n")
	if got := gemSummary(g.Blocks()); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if g.Rewound() {
		t.Error("a compression isn't a rewind")
	}
}

// A Qwen Code session as its ChatRecordingService (0.26) writes one: a
// turn with a command and an edit, a second turn rewound away, a slash
// command and a compression.
func TestQwenBlocks(t *testing.T) {
	q := NewQwen("/work/tally")
	feedGemini(t, q, "testdata/qwen/3e9a1c7b-5d2f-4a8e-b6c0-7f1e2d3c4b5a.jsonl")
	want := strings.Join([]string{
		"USER Add a --top N flag to tally.sh",
		"THINKING The script sorts with wc; I should look at it.",
		"TEXT I'll read the script first.",
		"TOOL Ran `cat tally.sh && ls *.md` → #!/usr/bin/env bash wc -w *.md | sort -rn notes.md rules.md",
		`TOOL Edited tally.sh | #!/usr/bin/env bash / +top="$2" /  wc -w *.md | sort -rn`,
		"TEXT Added --top N: it prints the N biggest files, then the total.",
		"USER /compress",
		"NOTE Conversation compacted",
		"USER Make --top default to 5",
	}, "\n")
	if got := gemSummary(q.Blocks()); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if !q.Rewound() {
		t.Error("the rewind should be reported")
	}
	if m, tok, win := q.Context(); m != "qwen3-coder-plus" || tok != 12500 || win != 1_000_000 {
		t.Errorf("context: %s %d %d", m, tok, win)
	}

	// A call still running is pending; a failed command shows as failed.
	q2 := NewQwen("/w")
	q2.Feed([]byte(`{"uuid":"u","parentUuid":null,"timestamp":"2026-11-07T10:00:00Z","type":"user","message":{"role":"user","parts":[{"text":"test it"}]}}`))
	q2.Feed([]byte(`{"uuid":"a","parentUuid":"u","timestamp":"2026-11-07T10:00:01Z","type":"assistant","message":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"run_shell_command","args":{"command":"go test ./...","description":"Run the tests"}}}]}}`))
	if p := q2.Pending(); p == nil || p.Title != "Run the tests" {
		t.Fatalf("pending: %v", p)
	}
	q2.Feed([]byte(`{"uuid":"t","parentUuid":"a","timestamp":"2026-11-07T10:00:09Z","type":"tool_result","message":{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"run_shell_command","response":{"output":"FAIL"}}}]},"toolCallResult":{"callId":"c1","status":"success","resultDisplay":{"type":"shell_result","version":1,"text":"FAIL ./x","output":"FAIL ./x","directory":"/w","exitCode":1,"signal":null,"pid":1,"error":null,"outcome":"completed","notices":[],"truncated":false,"outputFiles":[]}}}`))
	if p := q2.Pending(); p != nil {
		t.Fatalf("still pending: %v", p)
	}
	if b := q2.Blocks()[1]; !b.Error || b.Output != "FAIL ./x" {
		t.Fatalf("result: error %v output %q", b.Error, b.Output)
	}
}
