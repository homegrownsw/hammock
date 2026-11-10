package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homegrownsw/hammock/internal/thread"
)

// Payloads as Copilot sends them to hooks configured with Claude Code's
// event names (docs.github.com, "Hooks configuration", PascalCase payloads;
// Copilot 1.0.107).
func TestCopilotHooks(t *testing.T) {
	cases := []struct {
		event, payload string
		want           Hook
	}{
		{"SessionStart", `{"hook_event_name":"SessionStart","session_id":"s1","timestamp":"2026-11-07T10:00:00Z","cwd":"/p","source":"startup"}`,
			Hook{Event: "SessionStart", SessionID: "s1"}},
		{"UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","session_id":"s1","cwd":"/p","prompt":"fix it"}`,
			Hook{Event: "UserPromptSubmit", SessionID: "s1", Prompt: "fix it"}},
		{"PreToolUse", `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"}}`,
			Hook{Event: "PreToolUse", SessionID: "s1", ToolName: "Bash", ToolInput: []byte(`{"command":"ls"}`)}},
		{"PostToolUseFailure", `{"hook_event_name":"PostToolUseFailure","session_id":"s1","tool_name":"Bash","tool_input":{"command":"false"},"error":"exit 1"}`,
			Hook{Event: "PostToolUse", SessionID: "s1", ToolName: "Bash", ToolInput: []byte(`{"command":"false"}`), Error: "exit 1"}},
		// The camelCase shape (a hook configured as preToolUse), toolArgs a JSON string.
		{"preToolUse", `{"sessionId":"s1","toolName":"bash","toolArgs":"{\"command\":\"ls\"}"}`,
			Hook{Event: "preToolUse", SessionID: "s1", ToolName: "bash", ToolInput: []byte(`{"command":"ls"}`)}},
		{"Stop", `{"hook_event_name":"Stop","session_id":"s1","transcript_path":"/h/.copilot/session-state/s1/events.jsonl","stop_reason":"end_turn","stop_hook_active":false}`,
			Hook{Event: "Stop", SessionID: "s1", TranscriptPath: "/h/.copilot/session-state/s1/events.jsonl"}},
		{"Notification", `{"hook_event_name":"Notification","sessionId":"s1","message":"Copilot needs your permission","notification_type":"permission_prompt"}`,
			Hook{Event: "Notification", SessionID: "s1", Message: "Copilot needs your permission", NotificationType: "permission_prompt"}},
		{"Notification", `{"hook_event_name":"Notification","sessionId":"s1","message":"Question","notification_type":"elicitation_dialog"}`,
			Hook{Event: "Notification", SessionID: "s1", Message: "Question", NotificationType: "permission_prompt"}},
		{"Notification", `{"hook_event_name":"Notification","sessionId":"s1","message":"Shell finished","notification_type":"shell_completed"}`,
			Hook{Event: "", SessionID: "s1", Message: "Shell finished", NotificationType: "shell_completed"}},
		{"ErrorOccurred", `{"hook_event_name":"ErrorOccurred","session_id":"s1","error":{"message":"429 Too Many Requests","name":"RateLimitError"},"error_context":"model_call","recoverable":false}`,
			Hook{Event: "StopFailure", SessionID: "s1", Error: "RateLimitError", ErrorDetails: "429 Too Many Requests"}},
		{"ErrorOccurred", `{"hook_event_name":"ErrorOccurred","session_id":"s1","error":{"message":"retrying","name":"NetworkError"},"error_context":"model_call","recoverable":true}`,
			Hook{Event: "", SessionID: "s1"}},
		{"ErrorOccurred", `{"hook_event_name":"ErrorOccurred","session_id":"s1","error":{"message":"bad args","name":"ToolError"},"error_context":"tool_execution","recoverable":false}`,
			Hook{Event: "", SessionID: "s1"}},
		{"SubagentStop", `{"hook_event_name":"SubagentStop","session_id":"s1"}`, Hook{Event: "", SessionID: "s1"}},
		{"SessionEnd", `{"hook_event_name":"SessionEnd","session_id":"s1","reason":"user_exit"}`, Hook{Event: "SessionEnd", SessionID: "s1"}},
	}
	for _, c := range cases {
		got := Copilot.ReadHook(c.event, []byte(c.payload))
		if got.Event != c.want.Event || got.SessionID != c.want.SessionID || got.Prompt != c.want.Prompt || got.ToolName != c.want.ToolName ||
			string(got.ToolInput) != string(c.want.ToolInput) || got.TranscriptPath != c.want.TranscriptPath || got.Message != c.want.Message ||
			got.NotificationType != c.want.NotificationType || (c.want.Event == "StopFailure" && (got.Error != c.want.Error || got.ErrorDetails != c.want.ErrorDetails)) ||
			(c.want.Error != "" && got.Error != c.want.Error) {
			t.Errorf("%s %s:\n got %+v\nwant %+v", c.event, c.payload, got, c.want)
		}
	}
}

func TestCopilotCommands(t *testing.T) {
	if got := Copilot.StartCommand("fix the bug's cause"); got != `copilot -i 'fix the bug'\''s cause'` {
		t.Errorf("start: %s", got)
	}
	if got := Copilot.StartCommand(""); got != "copilot" {
		t.Errorf("start: %s", got)
	}
	if got := Copilot.Resume("0cb916db-26aa-40f2-86b5-1ba81b225fd2"); got != "copilot --resume='0cb916db-26aa-40f2-86b5-1ba81b225fd2'" {
		t.Errorf("resume: %s", got)
	}
	id := "0cb916db-26aa-40f2-86b5-1ba81b225fd2"
	for argv, want := range map[string]string{
		"copilot --resume=" + id:                 id,
		"copilot --resume " + id:                 id,
		"copilot --allow-all --session-id=" + id: id,
		"copilot --session-id " + id:             id,
		"copilot --resume=0cb916d":               "", // a prefix: Locate can't find it
		"copilot --resume my-feature":            "",
		"copilot --resume":                       "",
		"copilot --continue":                     "",
		"copilot -i hello":                       "",
	} {
		if got := Copilot.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("ResumeID(%s) = %q, want %q", argv, got, want)
		}
	}
	if !Copilot.NeedsInputRE().MatchString("│ Do you want to run this command?\n│ ❯ 1. Yes") ||
		!Copilot.NeedsInputRE().MatchString("Do you want to allow this tool call?") ||
		!Copilot.WorkingRE().MatchString("◉ Thinking (Esc to cancel)") {
		t.Error("screen patterns")
	}
	if ByProcess("copilot") != Copilot || Parse("GitHub Copilot CLI") != Copilot || Parse("copilot") != Copilot {
		t.Error("lookup")
	}
}

func TestCopilotSessions(t *testing.T) {
	root := t.TempDir()
	id := "0cb916db-26aa-40f2-86b5-1ba81b225fd2"
	other := "11111111-2222-4333-8444-555555555555"
	old := "22222222-3333-4444-8555-666666666666"
	write := func(rel, s string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fixture, err := os.ReadFile("../agentview/testdata/copilot/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	main := write(filepath.Join(id, "events.jsonl"), string(fixture))
	write(filepath.Join(id, "workspace.yaml"), "id: "+id+"\ncwd: /Users/a/proj\nname: \"Add a top flag: tally\"\nuser_named: false\nsummary_count: 0\ncreated_at: 2026-11-07T10:00:00.000Z\n")
	otherPath := write(filepath.Join(other, "events.jsonl"), `{"type":"session.start","data":{"sessionId":"`+other+`","startTime":"2026-11-06T10:00:00.000Z","context":{"cwd":"/Users/a/elsewhere"}},"id":"x","timestamp":"2026-11-06T10:00:00.000Z","parentId":null}`+"\n")
	write(filepath.Join(other, "workspace.yaml"), "id: "+other+"\nname: 'It''s mine'\nuser_named: true\n")
	flat := write(old+".jsonl", `{"type":"session.start","data":{"sessionId":"`+old+`"},"id":"y","timestamp":"2026-01-01T00:00:00Z"}`+"\n")
	write(filepath.Join(id, "checkpoints", "index.md"), "x")
	write("diagnostics.jsonl", "{}\n")

	roots := []string{root}
	got := copilotSessions(roots)
	if len(got) != 3 {
		t.Errorf("sessions: %v", got)
	}
	for _, p := range got {
		if !copilotOwnsFile(p) {
			t.Errorf("doesn't own %s", p)
		}
	}
	if copilotOwnsFile(filepath.Join(root, "diagnostics.jsonl")) {
		t.Error("owns diagnostics.jsonl")
	}
	if p, ok := copilotLocate(roots, id); !ok || p != main {
		t.Errorf("locate: %s %v", p, ok)
	}
	if p, ok := copilotLocate(roots, old); !ok || p != flat {
		t.Errorf("locate flat: %s %v", p, ok)
	}
	if _, ok := copilotLocate(roots, "../x"); ok {
		t.Error("located a path")
	}
	if p, ok := copilotNewest(roots, "/Users/a/elsewhere"); !ok || p != otherPath {
		t.Errorf("newest: %s %v", p, ok)
	}
	if _, ok := copilotNewest(roots, "/nowhere"); ok {
		t.Error("newest in a folder with none")
	}
	names := copilotNames(roots)
	if names[id] != "Add a top flag: tally" || names[other] != "It's mine" {
		t.Errorf("names: %v", names)
	}

	f, _ := os.Open(main)
	defer f.Close()
	var th thread.Thread
	var recs []thread.Record
	n, err := Copilot.Index(f, &th, func(r thread.Record) { recs = append(recs, r) })
	if err != nil || n != int64(len(fixture)) {
		t.Fatalf("index read %d of %d: %v", n, len(fixture), err)
	}
	if th.NativeID != id || th.Cwd != "/Users/a/proj" || th.Created.IsZero() {
		t.Errorf("thread: %+v", th)
	}
	var texts []string
	for _, r := range recs {
		texts = append(texts, string(r.Kind)+": "+r.Text)
	}
	want := "user: Add a --top N flag to tally.sh.|assistant: I'll add the flag.|assistant: Added `--top N` (default 10).|user: Now run the tests"
	if strings.Join(texts, "|") != want {
		t.Errorf("records: %s", strings.Join(texts, "|"))
	}
}
