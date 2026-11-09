package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// testdata/kiro/messages.jsonl is a Kiro CLI V3 session's messages.jsonl,
// its rows shaped as Kiro CLI 3.1.0's agent writes them (@kiro/agent
// 0.66.91, acp-server.js: the persisted-message schema and
// actionToMessages, which writes a row again under its id as it changes;
// the agent_note, approval and session_start rows as Kiro 2.34.0 wrote
// them on a real run, per JustSessions' fixture): a message with an image,
// a hook's context, encrypted reasoning, a command and its failure, a
// reply as it streamed and when done, an edit asked for and approved, a
// write, a subagent (whose own rows are skipped), a denied command, and a
// second turn cancelled mid-command.
func TestKiroBlocks(t *testing.T) {
	k := NewKiro("/Users/a/w")
	f, err := os.Open("testdata/kiro/messages.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var pendingSeen bool
	for sc.Scan() {
		for _, b := range k.Feed(sc.Bytes()) {
			if b.Title == "Edited calc.go" && b.Pending {
				pendingSeen = true
				if p := k.Pending(); p == nil || p.Id != b.Id {
					t.Errorf("pending while asked: %v", p)
				}
			}
		}
	}
	if !pendingSeen {
		t.Error("the edit was never pending while it waited for approval")
	}
	var got []string
	for _, b := range k.Blocks() {
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
		if len(b.Images) > 0 {
			s += " [image]"
		}
		got = append(got, s)
	}
	want := []string{
		"USER Fix the failing test in /Users/a/w/calc.go [image]",
		"TOOL Ran `go test ./...` → --- FAIL: TestAdd FAIL (failed)",
		"TEXT The test fails because Add subtracts. Fixing it.",
		"TOOL Edited calc.go → Replaced 1 occurrence | -return a - b / +return a + b",
		"TOOL Wrote notes/fix.md → Wrote notes/fix.md | +Fixed Add. / +Tests pass.",
		"SUBAGENT Started an agent: reviewer → Looks good.",
		"TOOL Ran `rm -rf build` → Denied (failed)",
		"TEXT Done: `go test` passes.",
		"USER Now run the linter",
		"TOOL Ran `golangci-lint run`",
		"NOTE Interrupted",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if k.Pending() != nil {
		t.Errorf("pending after the turn ended: %v", k.Pending())
	}
	if !k.Referenced("/Users/a/w/calc.go") || !k.Referenced("/Users/a/w/notes/fix.md") {
		t.Error("files not referenced")
	}
	b := k.Blocks()[0]
	if img, ok := k.Image(b.Images[0].Ref); !ok || string(img.Data) != "hello" || img.MediaType != "image/png" {
		t.Errorf("image: %v %v", img, ok)
	}
	for _, b := range k.Blocks()[1:] {
		if b.TurnId != k.Blocks()[0].Id && b.Kind != 1 && b.Text != "Interrupted" && !strings.Contains(b.Title, "golangci") {
			t.Errorf("%s: turn %q", b.Title+b.Text, b.TurnId)
		}
	}
}
