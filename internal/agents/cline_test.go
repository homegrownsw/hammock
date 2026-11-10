package agents

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/thread"
)

const clineFixtureDir = "../agentview/testdata/cline/1794050000000_k3x9q"

// Payloads as Cline CLI 3.0.74's file hooks serialize them
// (sdk/packages/core/src/hooks/hook-file-hooks.ts createPayloadBase and the
// run* functions there).
func TestClineHook(t *testing.T) {
	base := `"clineVersion":"3.0.74","timestamp":"2026-11-07T09:33:21.000Z","taskId":"conv_1794050000500_ab12cd3","sessionContext":{"rootSessionId":"1794050000000_k3x9q"},"workspaceRoots":["/work"],"userId":"alex","agent_id":"agent_1","parent_agent_id":null`
	sid := "1794050000000_k3x9q"
	cases := []struct {
		event, payload string
		want           Hook
	}{
		{"UserPromptSubmit", `{"hookName":"prompt_submit",` + base + `,"userPromptSubmit":{"prompt":"<user_input mode=\"act\">fix the tests</user_input>","attachments":[]}}`,
			Hook{Event: "UserPromptSubmit", SessionID: sid, Prompt: "fix the tests"}},
		// A message of tool results is a user message too.
		{"UserPromptSubmit", `{"hookName":"prompt_submit",` + base + `,"userPromptSubmit":{"prompt":"","attachments":[]}}`, Hook{SessionID: sid}},
		{"PreToolUse", `{"hookName":"tool_call",` + base + `,"iteration":1,"tool_call":{"id":"toolu_1","name":"run_commands","input":{"commands":["go test ./..."]}},"preToolUse":{"toolName":"run_commands","parameters":{"commands":"[\"go test ./...\"]"}}}`,
			Hook{Event: "PreToolUse", SessionID: sid, ToolName: "run_commands", ToolInput: []byte(`{"command":"go test ./..."}`)}},
		{"PostToolUse", `{"hookName":"tool_result",` + base + `,"iteration":1,"tool_result":{"id":"toolu_2","name":"editor","input":{"path":"/work/a.go","old_text":"a","new_text":"b"},"output":"Edited","durationMs":4},"postToolUse":{"toolName":"editor","success":true}}`,
			Hook{Event: "PostToolUse", SessionID: sid, ToolName: "editor", ToolInput: []byte(`{"file_path":"/work/a.go"}`)}},
		{"PreToolUse", `{"hookName":"tool_call",` + base + `,"tool_call":{"id":"toolu_3","name":"skills","input":{"skill":"release"}}}`,
			Hook{Event: "PreToolUse", SessionID: sid, ToolName: "skills", ToolInput: []byte(`{"skill":"release"}`)}},
		{"TaskComplete", `{"hookName":"agent_end",` + base + `,"iteration":3,"turn":{"outputText":"All green.","status":"completed"},"taskComplete":{"taskMetadata":{}}}`,
			Hook{Event: "Stop", SessionID: sid, LastMessage: "All green."}},
		{"TaskError", `{"hookName":"agent_error",` + base + `,"iteration":2,"error":{"name":"Error","message":"429 rate limited"}}`,
			Hook{Event: "StopFailure", SessionID: sid, ErrorDetails: "429 rate limited"}},
		{"TaskCancel", `{"hookName":"agent_abort",` + base + `,"reason":"aborted by user","taskCancel":{"taskMetadata":{}}}`, Hook{Event: "Stop", SessionID: sid}},
		{"TaskStart", `{"hookName":"agent_start",` + base + `,"taskStart":{"taskMetadata":{}}}`, Hook{SessionID: sid}},
		// Without the event's name, the payload's.
		{"", `{"hookName":"agent_end",` + base + `,"turn":{"outputText":"ok"}}`, Hook{Event: "Stop", SessionID: sid, LastMessage: "ok"}},
		// Not Cline's.
		{"PreToolUse", `{"hook_event_name":"PreToolUse","session_id":"x","tool_name":"Bash"}`, Hook{}},
	}
	for _, c := range cases {
		if got := Cline.ReadHook(c.event, []byte(c.payload)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s:\n got %+v\nwant %+v", c.event, c.payload, got, c.want)
		}
	}
}

func TestClineCommands(t *testing.T) {
	if got := Cline.StartCommand(""); got != "cline" {
		t.Errorf("start %q", got)
	}
	if got := Cline.StartCommand("it's broken"); got != `cline -i 'it'\''s broken'` {
		t.Errorf("start %q", got)
	}
	if got := Cline.Resume("1794050000000_k3x9q"); got != "cline --id '1794050000000_k3x9q'" {
		t.Errorf("resume %q", got)
	}
	for argv, want := range map[string]string{
		"cline --id 1794050000000_k3x9q":    "1794050000000_k3x9q",
		"cline -i --id=1794050000000_k3x9q": "1794050000000_k3x9q",
		"cline --id ../etc":                 "",
		"cline -i hello":                    "",
	} {
		if got := Cline.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q, want %q", argv, got, want)
		}
	}
	if !Cline.WorkingRE().MatchString("⠋ Thinking... (esc to cancel)") || Cline.WorkingRE().MatchString("↑/↓ navigate, Enter to select, Esc to cancel") {
		t.Error("working")
	}
	if !Cline.NeedsInputRE().MatchString("│ Cline needs permission │") || !Cline.NeedsInputRE().MatchString(`Approve tool "run_commands" with input {"commands":["ls"]}? [y/N]`) {
		t.Error("needs input")
	}
	if ByProcess("cline") != Cline {
		t.Error("process")
	}
}

// clineHome makes a home folder with the fixture session in Cline's
// sessions folder, and points every CLINE_* variable away.
func clineHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"CLINE_DIR", "CLINE_DATA_DIR", "CLINE_SESSION_DATA_DIR"} {
		t.Setenv(k, "")
	}
	dir := filepath.Join(home, ".cline", "data", "sessions", "1794050000000_k3x9q")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(clineFixtureDir, "*"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestClineSessions(t *testing.T) {
	home := clineHome(t)
	root := filepath.Join(home, ".cline", "data", "sessions")
	want := filepath.Join(root, "1794050000000_k3x9q", "1794050000000_k3x9q.messages.json")

	if got := clineRoots(); len(got) != 1 || got[0] != root {
		t.Errorf("roots %v", got)
	}
	if got := Cline.Sessions(Cline.Roots()); len(got) != 1 || got[0] != want {
		t.Errorf("sessions %v", got)
	}
	if p, ok := Cline.Locate("1794050000000_k3x9q"); !ok || p != want {
		t.Errorf("locate %q %v", p, ok)
	}
	if _, ok := Cline.Locate("../1794050000000_k3x9q"); ok {
		t.Error("located a path")
	}
	if p, ok := Cline.Newest("/work/"); !ok || p != want {
		t.Errorf("newest %q %v", p, ok)
	}
	if _, ok := Cline.Newest("/elsewhere"); ok {
		t.Error("newest elsewhere")
	}
	for p, own := range map[string]bool{
		want: true,
		filepath.Join(root, "1794050000000_k3x9q", "subagent_1.messages.json"): false,
		filepath.Join(root, "1794050000000_k3x9q", "1794050000000_k3x9q.json"): false,
	} {
		if Cline.OwnsFile(p) != own {
			t.Errorf("owns %s: %v", p, !own)
		}
	}

	// Moved by CLINE_DATA_DIR.
	moved := t.TempDir()
	t.Setenv("CLINE_DATA_DIR", moved)
	if got := clineRoots(); len(got) != 2 || got[0] != filepath.Join(moved, "sessions") {
		t.Errorf("roots %v", got)
	}
}

func TestClineOpenIndex(t *testing.T) {
	home := clineHome(t)
	path := filepath.Join(home, ".cline", "data", "sessions", "1794050000000_k3x9q", "1794050000000_k3x9q.messages.json")
	r, err := Cline.OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 13 || !strings.HasPrefix(lines[0], `{"$hammockSession":{"id":"1794050000000_k3x9q","cwd":"/work"`) {
		t.Fatalf("%d lines, first %s", len(lines), lines[0])
	}

	// The agent view reads them.
	p := agentview.NewCline("")
	for _, l := range lines {
		p.Feed([]byte(l))
	}
	if n := len(p.Blocks()); n != 12 {
		t.Errorf("%d blocks", n)
	}

	var th thread.Thread
	var recs []string
	if _, err := Cline.Index(strings.NewReader(string(b)), &th, func(r thread.Record) {
		recs = append(recs, string(r.Kind)+": "+r.Text)
	}); err != nil {
		t.Fatal(err)
	}
	if th.NativeID != "1794050000000_k3x9q" || th.Cwd != "/work" || th.Title != "Add a --top N flag to @tally.sh" ||
		!th.Created.Equal(time.Date(2026, 11, 7, 9, 33, 20, 0, time.UTC)) {
		t.Errorf("thread %+v", th)
	}
	want := []string{
		"user: Add a --top N flag to @tally.sh",
		"assistant: I'll read the script first.",
		"assistant: Added --top N: it prints the N biggest files.",
		"user: Now run it, and write a README",
		"assistant: It needs to be executable.",
	}
	if !reflect.DeepEqual(recs, want) {
		t.Errorf("records\n%s", strings.Join(recs, "\n"))
	}
}
