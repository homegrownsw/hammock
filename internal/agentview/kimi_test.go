package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// testdata/kimi/wire.jsonl is a hand-made Kimi Code journal in the format
// kimi-code 2.5.2 writes (wire protocol 1.5, packages/agent-core-v2's
// docs/wire-manifest.d.ts and contextMemory/loopEventFold.ts): your
// messages as context.append_message with their origin (a hook's addition
// beside the text, a reminder, a "!" command and its output, a slash
// skill), the replies as loop events (thinking, text, tool calls with
// Kimi's own input names, results as a string or parts), an interrupted
// turn, /undo, a compaction and a failed turn; and the request records the
// parser skips.
func TestKimiBlocks(t *testing.T) {
	f, err := os.Open("testdata/kimi/wire.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	k := NewKimi("", false)
	sc := bufio.NewScanner(f)
	rewound := false
	for sc.Scan() {
		k.Feed(sc.Bytes())
		rewound = k.Rewound() || rewound
	}
	if !rewound {
		t.Error("/undo didn't rewind")
	}
	line := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var got []string
	for _, b := range k.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Output != "" {
			s += " → " + line(b.Output)
		}
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Pending {
			s += " (pending)"
		}
		if b.Error {
			s += " (error)"
		}
		got = append(got, s)
	}
	want := []string{
		"USER Add a test for parseConfig",
		"THINKING Find the parser first.",
		"TEXT I'll read the config parser first.",
		"TOOL Read config.go → package main func parseConfig() {}",
		"TOOL Edited config.go → Replaced 1 occurrence in config.go | -func parseConfig() {} / +func parseConfig() error { / +\treturn nil / +}",
		"TOOL Run the tests → ok app 0.2s",
		"TEXT Added the test; go test passes.",
		"TOOL Ran `ls && pwd` → config.go /Users/me/app",
		"USER /skill:commit fix login",
		"TOOL Updated the to-do list → Todo list updated",
		"QUESTION Asked you (error)",
		"NOTE Interrupted",
		"NOTE Conversation compacted → The user asked for a parseConfig test; it was added.",
		"NOTE Rate limit reached … (error)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	byTitle := map[string]*pb.AgentBlock{}
	for _, b := range k.Blocks() {
		byTitle[b.Title] = b
	}
	if k.Cwd != "/Users/me/app" || !k.Referenced("/Users/me/app/config.go") {
		t.Errorf("cwd %q, referenced config.go: %v", k.Cwd, k.Referenced("/Users/me/app/config.go"))
	}
	if b := byTitle["Updated the to-do list"]; b == nil || b.Input != "- [x] Stage\n- [~] Commit\n" {
		t.Errorf("todos: %+v", b)
	}
	if b := byTitle["Asked you"]; b == nil || len(b.Questions) != 1 || b.Questions[0].Question != "Which message style?" || len(b.Questions[0].Options) != 2 {
		t.Errorf("question: %+v", b)
	}
	if k.Pending() != nil {
		t.Errorf("pending after the turn ended: %+v", k.Pending())
	}
	if model, tokens, _ := k.Context(); model != "kimi-for-coding" || tokens != 4600 {
		t.Errorf("context: %s %d", model, tokens)
	}
}

// A call still running is pending, until its result.
func TestKimiPending(t *testing.T) {
	k := NewKimi("/w", false)
	k.Feed([]byte(`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"go"}],"toolCalls":[]},"time":1}`))
	k.Feed([]byte(`{"type":"context.append_loop_event","event":{"type":"tool.call","uuid":"t1","stepUuid":"s1","toolCallId":"c1","name":"Write","args":{"path":"/w/a.txt","content":"hi\n"}},"time":2}`))
	p := k.Pending()
	if p == nil || p.Title != "Wrote a.txt" || p.Added != 2 {
		t.Fatalf("pending: %+v", p)
	}
	k.Feed([]byte(`{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"c1","result":{"output":"Wrote 3 bytes","isError":false}},"time":3}`))
	if k.Pending() != nil {
		t.Fatal("still pending")
	}
}

// Older and migrated journals keep whole assistant and tool messages.
func TestKimiWholeMessages(t *testing.T) {
	k := NewKimi("/w", false)
	for _, l := range []string{
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"hi"}],"toolCalls":[]},"time":1}`,
		`{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"Looking."}],"toolCalls":[{"type":"function","id":"c1","name":"Grep","arguments":"{\"pattern\":\"TODO\"}"}]},"time":2}`,
		`{"type":"context.append_message","message":{"role":"tool","content":[{"type":"text","text":"a.go:1: TODO"}],"toolCalls":[],"toolCallId":"c1"},"time":3}`,
		`{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"Blocked by a hook."}],"toolCalls":[],"origin":{"kind":"hook_result","event":"UserPromptSubmit","blocked":true}},"time":4}`,
	} {
		k.Feed([]byte(l))
	}
	var got []string
	for _, b := range k.Blocks() {
		got = append(got, strings.TrimPrefix(b.Kind.String(), "KIND_")+" "+b.Title+b.Text+" "+b.Output)
	}
	want := "USER hi |TEXT Looking. |TOOL Searched for `TODO` a.go:1: TODO|NOTE Blocked by a hook. "
	if strings.Join(got, "|") != want {
		t.Fatalf("got %q", strings.Join(got, "|"))
	}
}

func TestKimiToolInput(t *testing.T) {
	got := string(KimiToolInput("Edit", []byte(`{"path":"a.go","old_string":"x","new_string":"y"}`)))
	if got != `{"file_path":"a.go","new_string":"y","old_string":"x"}` {
		t.Errorf("Edit: %s", got)
	}
	got = string(KimiToolInput("AskUserQuestion", []byte(`{"questions":[{"question":"Q?","multi_select":true}]}`)))
	if qs := Questions([]byte(got)); len(qs) != 1 || !qs[0].MultiSelect {
		t.Errorf("AskUserQuestion: %s", got)
	}
}
