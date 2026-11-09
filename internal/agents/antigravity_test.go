package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjsp/hammock/internal/thread"
)

// Payloads as its hooks.json docs (agy 1.5.3, bundled in the binary) give
// them: the common fields, then each event's own, camelCase.
func TestAntigravityHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	const id = "ec33ebf9-0cba-4100-8142-c61503f6c587"
	logs := filepath.Join(home, ".gemini", "antigravity-cli", "brain", id, ".system_generated", "logs")
	tp := filepath.Join(logs, "transcript.jsonl")
	common := `"conversationId":"` + id + `","workspacePaths":["/w"],"transcriptPath":"` + tp + `","artifactDirectoryPath":"/a","modelName":"auto","lastUserInput":"Fix the failing unit test",`
	for _, tc := range []struct{ event, payload, want, tool, input, prompt string }{
		{"SessionStart", ``, "SessionStart", "", "", ""},
		{"PreInvocation", `"invocationNum":1,"initialNumSteps":0`, "UserPromptSubmit", "", "", "Fix the failing unit test"},
		{"PreInvocation", `"invocationNum":0,"initialNumSteps":0`, "UserPromptSubmit", "", "", "Fix the failing unit test"},
		{"PreInvocation", `"invocationNum":3,"initialNumSteps":10`, "PostToolUse", "", "", ""},
		{"PostInvocation", `"invocationNum":3,"initialNumSteps":10`, "", "", "", ""},
		{"PreToolUse", `"toolCall":{"name":"run_command","args":{"CommandLine":"npm test"}},"stepIdx":19`, "PreToolUse", "run_command", `{"command":"npm test"}`, ""},
		{"PostToolUse", `"toolCall":{"name":"view_file","args":{"AbsolutePath":"\"/w/a.go\""}},"stepIdx":5`, "PostToolUse", "view_file", `{"file_path":"/w/a.go"}`, ""},
		{"Stop", `"executionNum":1,"terminationReason":"NO_TOOL_CALL","fullyIdle":true,"finalModelOutput":"Done."`, "Stop", "", "", ""},
		{"Stop", `"executionNum":1,"terminationReason":"USER_CANCELED","fullyIdle":true`, "Stop", "", "", ""},
		{"Stop", `"executionNum":1,"terminationReason":"ERROR","error":"quota exceeded","fullyIdle":true`, "StopFailure", "", "", ""},
	} {
		h := Antigravity.ReadHook(tc.event, []byte("{"+common+strings.TrimSuffix(tc.payload, ",")+"}"))
		if tc.payload == "" {
			h = Antigravity.ReadHook(tc.event, []byte("{"+strings.TrimSuffix(common, ",")+"}"))
		}
		if h.Event != tc.want || h.ToolName != tc.tool || tc.input != "" && string(h.ToolInput) != tc.input || h.Prompt != tc.prompt {
			t.Errorf("%s %s: got %q %q %s %q", tc.event, tc.payload, h.Event, h.ToolName, h.ToolInput, h.Prompt)
		}
		if h.SessionID != id || h.TranscriptPath != tp {
			t.Errorf("%s: session %q transcript %q", tc.event, h.SessionID, h.TranscriptPath)
		}
	}
	if h := Antigravity.ReadHook("Stop", []byte(`{"conversationId":"c","terminationReason":"NO_TOOL_CALL","finalModelOutput":"Done."}`)); h.LastMessage != "Done." {
		t.Errorf("last message %q", h.LastMessage)
	}
	if h := Antigravity.ReadHook("Stop", []byte(`{"conversationId":"c","terminationReason":"ERROR","error":"quota exceeded"}`)); h.ErrorDetails != "quota exceeded" {
		t.Errorf("error %q", h.ErrorDetails)
	}

	// With the whole transcript there, hooks point at it.
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(logs, "transcript_full.jsonl")
	if err := os.WriteFile(full, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if h := Antigravity.ReadHook("SessionStart", []byte("{"+strings.TrimSuffix(common, ",")+"}")); h.TranscriptPath != full {
		t.Errorf("transcript %q", h.TranscriptPath)
	}

	// A subagent's hooks keep the pane's conversation and don't end its turn.
	sub := `{"conversationId":"11111111-2222-4333-8444-555555555555","parentConversationId":"` + id + `","transcriptPath":"/x/transcript.jsonl",`
	for ev, want := range map[string]string{"SessionStart": "", "Stop": "", "PreInvocation": "PostToolUse", "PostToolUse": "PostToolUse"} {
		h := Antigravity.ReadHook(ev, []byte(sub+`"invocationNum":1,"terminationReason":"NO_TOOL_CALL"}`))
		if h.Event != want || h.SessionID != "" || h.TranscriptPath != "" || h.Prompt != "" {
			t.Errorf("subagent %s: %+v", ev, h)
		}
	}
}

func TestAntigravityCommands(t *testing.T) {
	const id = "ec33ebf9-0cba-4100-8142-c61503f6c587"
	for argv, want := range map[string]string{
		"agy --conversation=" + id:               id,
		"/u/.local/bin/agy --conversation " + id: id,
		"agy -c":                                 "",
		"agy --conversation=latest":              "",
		"agy " + id:                              "",
	} {
		if got := Antigravity.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q", argv, got)
		}
	}
	if Antigravity.Resume(id) != "agy --conversation='"+id+"'" || Antigravity.StartCommand("it's") != `agy -i 'it'\''s'` || Antigravity.StartCommand(" ") != "agy" {
		t.Error(Antigravity.Resume(id), Antigravity.StartCommand("it's"))
	}
	if ByProcess("agy") != Antigravity || Parse("antigravity") != Antigravity || Parse("Antigravity CLI") != Antigravity {
		t.Error("not found by name")
	}
	for _, s := range []string{"Run this command?", "Allow access to this file?", "Allow calling this tool?", "Do you want to proceed?"} {
		if !Antigravity.NeedsInputRE().MatchString(s) {
			t.Errorf("%q not a prompt", s)
		}
	}
}

// Its data folder as agy 1.5 leaves it: conversations' transcripts under
// brain/, your messages in history.jsonl (a subagent's conversation isn't
// in it).
func TestAntigravitySessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := filepath.Join(home, ".gemini", "antigravity-cli")
	const mine, other, subagent = "0f8e3c2a-1b2c-4d5e-8f90-123456789abc", "1f8e3c2a-1b2c-4d5e-8f90-123456789abc", "2f8e3c2a-1b2c-4d5e-8f90-123456789abc"
	write := func(rel, s string) string {
		p := filepath.Join(app, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	steps := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-06T07:00:00Z","content":"<USER_REQUEST>\nFix the test\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-06T07:00:00Z.\n</ADDITIONAL_METADATA>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-06T07:00:01Z","content":"Looking.","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"go test ./...\""}}]}
{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-06T07:00:02Z","content":"ok"}
{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-06T07:00:03Z","content":"Fixed."}
`
	full := write("brain/"+mine+"/.system_generated/logs/transcript_full.jsonl", steps)
	write("brain/"+mine+"/.system_generated/logs/transcript.jsonl", steps)
	clipped := write("brain/"+other+"/.system_generated/logs/transcript.jsonl", steps)
	write("brain/"+subagent+"/.system_generated/logs/transcript_full.jsonl", steps)
	write("brain/"+mine+"/task.md", "# Task")
	write("history.jsonl", `{"display":"Fix the test","timestamp":1793949228828,"workspace":"/w/old","conversationId":"`+mine+`"}
{"display":"again","timestamp":1793949228900,"workspace":"/w/app","conversationId":"`+mine+`"}
{"display":"other","timestamp":1793949228999,"workspace":"/w/other","conversationId":"`+other+`"}
`)

	got := Antigravity.Sessions(Antigravity.Roots())
	if len(got) != 2 || !antigravityContains(got, full) || !antigravityContains(got, clipped) {
		t.Fatalf("sessions %v", got)
	}
	if p, ok := Antigravity.Locate(mine); !ok || p != full {
		t.Errorf("locate %q", p)
	}
	if _, ok := Antigravity.Locate("../x"); ok {
		t.Error("located a path")
	}
	if p, ok := Antigravity.Newest("/w/app"); !ok || p != full {
		t.Errorf("newest %q", p)
	}
	if _, ok := Antigravity.Newest("/w/old"); ok {
		t.Error("newest in a folder it left")
	}
	for p, want := range map[string]bool{full: true, clipped: true, filepath.Join(app, "brain", mine, "task.md"): false, filepath.Join(app, "history.jsonl"): false} {
		if Antigravity.OwnsFile(p) != want {
			t.Errorf("owns %s: %v", p, !want)
		}
	}

	r, err := Antigravity.OpenSession(full)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var th thread.Thread
	var recs []thread.Record
	if _, err := Antigravity.Index(r, &th, func(rec thread.Record) { recs = append(recs, rec) }); err != nil {
		t.Fatal(err)
	}
	if th.NativeID != mine || th.Cwd != "/w/app" || th.Created.IsZero() {
		t.Errorf("thread %+v", th)
	}
	var kinds []string
	for _, rec := range recs {
		kinds = append(kinds, string(rec.Kind)+":"+rec.Text+rec.NativeTool)
	}
	if got := strings.Join(kinds, "|"); got != "user:Fix the test|assistant:Looking.|tool_call:run_command|assistant:Fixed." {
		t.Errorf("records %s", got)
	}
	var in map[string]string
	_ = json.Unmarshal(recs[2].Input, &in)
	if in["CommandLine"] != "go test ./..." {
		t.Errorf("input %s", recs[2].Input)
	}

	// Without a history, every conversation is listed.
	os.Remove(filepath.Join(app, "history.jsonl"))
	if got := Antigravity.Sessions(Antigravity.Roots()); len(got) != 3 {
		t.Errorf("without history %v", got)
	}
}

func antigravityContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
