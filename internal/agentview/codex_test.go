package agentview

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The hand-made Codex 0.166 session (internal/thread's golden fixture) as
// the agent view shows it.
func TestCodexBlocks(t *testing.T) {
	f, err := os.Open("../thread/testdata/golden/codex-0.166.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := NewCodex("")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c.Feed(sc.Bytes())
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
		if b.Pending {
			s += " (pending)"
		}
		got = append(got, s)
	}
	want := []string{
		"USER Add a --top N flag to tally.sh.",
		"TOOL Ran `cat tally.sh && ls *.md` → #!/usr/bin/env bash wc -w *.md | sort -rn notes.md rules.md",
		`TOOL Edited tally.sh | @@ tally.sh / +top="$2"`,
		"TEXT Added --top N: it prints the N biggest files, then the total.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The AGENTS.md and environment messages aren't yours; every turn's
	// blocks belong to your message.
	for _, b := range c.Blocks() {
		if b.TurnId != c.Blocks()[0].Id {
			t.Errorf("%s: turn %q", b.Title, b.TurnId)
		}
	}
	if b := c.Blocks()[1]; b.Kind != pb.AgentBlock_KIND_TOOL || b.Input != "cat tally.sh && ls *.md" {
		t.Errorf("command block: %v", b)
	}
	if m, _, win := c.Context(); win != 0 && m != "" {
		t.Logf("model %s window %d", m, win)
	}

	// A call still running shows as pending; a failed command as failed.
	c2 := NewCodex("/work")
	c2.Feed([]byte(`{"timestamp":"2026-11-01T08:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"run the tests"}]}}`))
	c2.Feed([]byte(`{"timestamp":"2026-11-01T08:00:01Z","type":"response_item","payload":{"type":"function_call","id":"fc1","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\"}","call_id":"c1"}}`))
	if p := c2.Pending(); p == nil || p.Title != "Ran `go test ./...`" {
		t.Fatalf("pending: %v", p)
	}
	c2.Feed([]byte(`{"timestamp":"2026-11-01T08:00:09Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Chunk ID: 1\nWall time: 8.0 seconds\nProcess exited with code 1\nOutput:\nFAIL ./internal/x\n"}}`))
	if p := c2.Pending(); p != nil {
		t.Fatalf("still pending: %v", p)
	}
	if b := c2.Blocks()[1]; !b.Error || b.Output != "FAIL ./internal/x" {
		t.Fatalf("result: error %v output %q", b.Error, b.Output)
	}
	c2.Feed([]byte(`{"timestamp":"2026-11-01T08:00:10Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":51234},"model_context_window":258400}}}`))
	c2.Feed([]byte(`{"timestamp":"2026-11-01T08:00:10Z","type":"turn_context","payload":{"model":"gpt-6-luna","cwd":"/work"}}`))
	if m, tok, win := c2.Context(); m != "gpt-6-luna" || tok != 51234 || win != 258400 {
		t.Fatalf("context: %s %d %d", m, tok, win)
	}
}

// A "!" command typed at Codex's prompt, recorded once it's done as a user
// message: shown as the command it ran, not dropped as injected context.
func TestCodexUserShellCommand(t *testing.T) {
	c := NewCodex("/tmp")
	rec := func(text string) []byte {
		b, _ := json.Marshal(map[string]any{"timestamp": "2026-11-09T12:00:00Z", "type": "response_item",
			"payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": text}}}})
		return b
	}
	c.Feed(rec("<user_shell_command>\n<command>\nls -1\n</command>\n<result>\nExit code: 0\nDuration: 0.0123 seconds\nOutput:\na.txt\nb.txt\n</result>\n</user_shell_command>"))
	c.Feed(rec("<user_shell_command>\n<command>\nfalse\n</command>\n<result>\nExit code: 1\nDuration: 0.0010 seconds\nOutput:\n\n</result>\n</user_shell_command>"))
	bs := c.Blocks()
	if len(bs) != 2 || bs[0].Kind != pb.AgentBlock_KIND_TOOL || bs[0].Title != "Ran `ls -1`" || bs[0].Output != "a.txt\nb.txt" || bs[0].Error || bs[0].Pending {
		t.Fatalf("blocks %v", bs)
	}
	if !bs[1].Error || bs[1].Title != "Ran `false`" {
		t.Fatalf("failed command %v", bs[1])
	}
}

// A turn's end, with how long it worked (task_complete).
func TestCodexTaskCompleteEndsTheTurn(t *testing.T) {
	c := NewCodex("")
	c.Feed([]byte(`{"timestamp":"2026-11-01T09:54:40.437Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","duration_ms":6101}}`))
	c.Feed([]byte(`{"timestamp":"2026-11-01T09:55:40.437Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t2"}}`)) // no duration: nothing
	bs := c.Blocks()
	if len(bs) != 1 || bs[0].Kind != pb.AgentBlock_KIND_DONE || bs[0].DurationMs != 6101 ||
		bs[0].TimeUnixMs != time.Date(2026, 11, 1, 9, 54, 40, 437e6, time.UTC).UnixMilli() {
		t.Fatalf("got %v", bs)
	}
}
