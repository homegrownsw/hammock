package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// testdata/grok/updates.jsonl is a hand-made Grok CLI session
// (~/.grok/sessions/<cwd>/<id>/updates.jsonl): ACP session/update lines,
// {timestamp, method, params: {sessionId, update, _meta}}, and Grok's own
// "_x.ai/session/update" ones (turn_completed, model_changed), as Grok
// 1.0.41 sent them over ACP in t3code's recordings (run_terminal_command
// with its rawOutput, chunks a few words each, user chunks with their
// promptIndex), with ACP's tool call content for a read and an edit (its
// diff) and a plan, and an MCP tool (server__tool) that failed.
func TestGrokBlocks(t *testing.T) {
	f, err := os.Open("testdata/grok/updates.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g := NewGrok("/work/app")
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	var pendingSeen bool
	for sc.Scan() {
		g.Feed(sc.Bytes())
		if p := g.Pending(); p != nil && p.Id == "call-24294da9-5ce1-46a9-bc23-926eca6c5df7-0" && p.Output == "--- FAIL: TestAdd" {
			pendingSeen = true
		}
	}
	if !pendingSeen {
		t.Error("a running command isn't pending with its output so far")
	}
	var got []string
	for _, b := range g.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Input != "" && b.Kind == 4 && b.Title == "Updated the plan" {
			s += " | " + strings.ReplaceAll(b.Input, "\n", " / ")
		}
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
		"USER Fix the failing test in @math.go",
		"THINKING The user wants the test fixed.",
		"TEXT I'll run the tests first.",
		"TOOL Ran `go test ./...` → --- FAIL: TestAdd FAIL (error)",
		"TOOL Read math.go → package app func Add(a, b int) int { return a - b }",
		"TOOL Edited math.go | -func Add(a, b int) int { return a - b } / +func Add(a, b int) int { return a + b }",
		"TOOL Updated the plan | - [x] Fix Add / - [~] Rerun the tests",
		"TOOL Used get_issue (linear) → issue not found (error)",
		"TEXT Fixed: `Add` subtracted.",
		"USER Now run it in the background",
		"TOOL Ran `go test ./... -count=100` in the background → Cancelled (error)",
		"NOTE Interrupted",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if model, tokens, _ := g.Context(); model != "grok-4.7" || tokens != 24100 {
		t.Errorf("context: %q %d", model, tokens)
	}
	if !g.Referenced("/work/app/math.go") {
		t.Error("the file it read and edited isn't referenced")
	}
	// The second message starts its own turn.
	bs := g.Blocks()
	if last := bs[len(bs)-2]; last.TurnId != bs[9].Id || bs[3].TurnId != bs[0].Id {
		t.Errorf("turns: %q %q", last.TurnId, bs[3].TurnId)
	}
}

// A restored session's tool call, its arguments only on its first update,
// and a write's diff from a new file (no oldText).
func TestGrokToolDetails(t *testing.T) {
	g := NewGrok("/w")
	for _, l := range []string{
		`{"timestamp":1,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}`,
		`{"timestamp":2,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"a","title":"write","_meta":{"x.ai/tool":{"name":"write","label":"Write File"}}}}}`,
		`{"timestamp":3,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"a","rawInput":{"file_path":"/w/new.txt","content":"one\ntwo"}}}}`,
		`{"timestamp":4,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"a","status":"completed","content":[{"type":"diff","path":"/w/new.txt","oldText":null,"newText":"one\ntwo"}]}}}`,
		`{"timestamp":5,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"b","title":"frobnicate","rawInput":{"x":1}}}}`,
		`{"timestamp":6,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"tool_call_update","toolCallId":"b","status":"completed","title":"Frobnicated the widget","rawOutput":{"type":"Text","text":"done"}}}}`,
		`{"timestamp":7,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"usage_update","used":5000,"size":500000}}}`,
	} {
		g.Feed([]byte(l))
	}
	bs := g.Blocks()
	if len(bs) != 3 {
		t.Fatalf("blocks: %v", bs)
	}
	if w := bs[1]; w.Title != "Wrote new.txt" || w.Diff != "+one\n+two\n" || w.Added != 2 || w.Pending || w.Output != "" {
		t.Errorf("write: %v", w)
	}
	if o := bs[2]; o.Title != "Frobnicated the widget" || o.Output != "done" || o.Input != `{"x":1}` {
		t.Errorf("other tool: %v", o)
	}
	if _, tokens, window := g.Context(); tokens != 5000 || window != 500000 {
		t.Errorf("context: %d %d", tokens, window)
	}
}
