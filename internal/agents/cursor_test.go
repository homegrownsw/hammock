package agents

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjsp/hammock/internal/thread"
)

// Payloads as cursor-agent 2026.11.05 sends them (its hook executor adds
// hook_event_name, cursor_version, workspace_roots, transcript_path to what
// each event carries).
func TestCursorHook(t *testing.T) {
	const tp = "/Users/a/.cursor/projects/Users-a-w/agent-transcripts/c1/c1.jsonl"
	common := `"conversation_id":"c1","generation_id":"g1","session_id":"c1","cursor_version":"2026.11.05","workspace_roots":["/Users/a/w"],"transcript_path":"` + tp + `",`
	for _, tc := range []struct{ payload, event, tool, input string }{
		{`"hook_event_name":"sessionStart","is_background_agent":false,"composer_mode":"agent"`, "SessionStart", "", ""},
		{`"hook_event_name":"beforeSubmitPrompt","prompt":"fix it","attachments":[]`, "UserPromptSubmit", "", ""},
		{`"hook_event_name":"beforeShellExecution","command":"go test ./...","cwd":"/Users/a/w"`, "PreToolUse", "Shell", `{"command":"go test ./..."}`},
		{`"hook_event_name":"afterShellExecution","command":"go test ./...","output":"ok","duration":1200`, "PostToolUse", "Shell", `{"command":"go test ./..."}`},
		{`"hook_event_name":"beforeMCPExecution","tool_name":"search_issues","tool_input":"{\"q\":\"x\"}","url":"https://x"`, "PreToolUse", "search_issues", `{"q":"x"}`},
		{`"hook_event_name":"afterFileEdit","file_path":"/Users/a/w/main.go","edits":[{"old_string":"a","new_string":"b"}]`, "PostToolUse", "Edit", `{"file_path":"/Users/a/w/main.go"}`},
		{`"hook_event_name":"preToolUse","tool_name":"Read","tool_input":{"path":"x"},"tool_use_id":"t1"`, "PreToolUse", "Read", `{"path":"x"}`},
		{`"hook_event_name":"stop","status":"completed","loop_count":0`, "Stop", "", ""},
		{`"hook_event_name":"stop","status":"aborted","loop_count":0`, "Stop", "", ""},
		{`"hook_event_name":"stop","status":"error","loop_count":0`, "StopFailure", "", ""},
		{`"hook_event_name":"sessionEnd","reason":"user_close","duration_ms":5`, "SessionEnd", "", ""},
		{`"hook_event_name":"afterAgentResponse","text":"done"`, "", "", ""},
	} {
		h := Cursor.ReadHook("", []byte("{"+common+tc.payload+"}"))
		if h.Event != tc.event || h.ToolName != tc.tool || string(h.ToolInput) != tc.input && tc.input != "" {
			t.Errorf("%s: got %q %q %s", tc.payload, h.Event, h.ToolName, h.ToolInput)
		}
		if h.SessionID != "c1" || h.TranscriptPath != tp {
			t.Errorf("%s: session %q transcript %q", tc.payload, h.SessionID, h.TranscriptPath)
		}
	}
	if h := Cursor.ReadHook("", []byte(`{`+common+`"hook_event_name":"beforeSubmitPrompt","prompt":"fix it"}`)); h.Prompt != "fix it" {
		t.Errorf("prompt %q", h.Prompt)
	}
}

func TestCursorResumeID(t *testing.T) {
	const id = "0f8e3c2a-1b2c-4d5e-8f90-123456789abc"
	for argv, want := range map[string]string{
		"/u/.local/bin/cursor-agent --use-system-ca /v/index.js --resume " + id: id,
		"cursor-agent --resume=" + id:                                           id,
		"cursor-agent --resume fix the bug":                                     "",
		"cursor-agent " + id:                                                    "",
	} {
		if got := Cursor.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q", argv, got)
		}
	}
	if Cursor.Resume(id) != "cursor-agent --resume '"+id+"'" || Cursor.StartCommand("it's") != `cursor-agent 'it'\''s'` {
		t.Error(Cursor.Resume(id), Cursor.StartCommand("it's"))
	}
}

func TestCursorSlugAndCwd(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/me/Developer/hammock":             "Users-me-Developer-hammock",
		"/Users/a/.claude/worktrees/agent-cursor": "Users-a-claude-worktrees-agent-cursor",
		"/Users/a/my app (v2)/":                   "Users-a-my-app-v2",
		"/Users/a/café":                           "Users-a-caf",
	} {
		if got := CursorSlug(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	root := t.TempDir()
	for _, d := range []string{"a/my.app/src", "a/my-app/src", "a/my/app/src", "b"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := cursorFolders(root, "a-my-app-src")
	if len(got) != 3 {
		t.Fatalf("found %v", got)
	}
	// The one with the chat in Cursor's store wins.
	t.Setenv("HOME", t.TempDir())
	cfg := t.TempDir()
	t.Setenv("CURSOR_CONFIG_DIR", cfg)
	want := filepath.Join(root, "a/my-app/src")
	sum := md5.Sum([]byte(want))
	if err := os.MkdirAll(filepath.Join(cfg, "chats", hex.EncodeToString(sum[:]), "c1"), 0o755); err != nil {
		t.Fatal(err)
	}
	cursorCwds.Store("x-"+CursorSlug(root)+"-a-my-app-src", got)
	if got := CursorCwd("x-"+CursorSlug(root)+"-a-my-app-src", "c1"); got != want {
		t.Errorf("cwd %q, want %q", got, want)
	}
}

// The session finder: transcripts under ~/.cursor/projects, read with the
// chat's id and folder in front.
func TestCursorSessions(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("CURSOR_DATA_DIR", "")
	work := filepath.Join(h, "src", "tally")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	work, _ = filepath.EvalSymlinks(work)
	const id = "0f8e3c2a-1b2c-4d5e-8f90-123456789abc"
	dir := filepath.Join(h, ".cursor", "projects", CursorSlug(work), "agent-transcripts", id)
	if err := os.MkdirAll(filepath.Join(dir, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../agentview/testdata/cursor/transcript.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	for _, p := range []string{path, filepath.Join(dir, "subagents", "s1.jsonl")} {
		if err := os.WriteFile(p, fixture, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := Cursor.Sessions(Cursor.Roots())
	if len(files) != 1 || files[0] != path || !Cursor.OwnsFile(path) {
		t.Fatalf("sessions %v", files)
	}
	if p, ok := Cursor.Locate(id); !ok || p != path {
		t.Errorf("locate %q", p)
	}
	if _, ok := Cursor.Locate("../x"); ok {
		t.Error("located ../x")
	}
	if p, ok := Cursor.Newest(work); !ok || p != path {
		t.Errorf("newest %q", p)
	}

	r, err := Cursor.OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var th thread.Thread
	var recs []thread.Record
	if _, err := Cursor.Index(r, &th, func(rec thread.Record) { recs = append(recs, rec) }); err != nil {
		t.Fatal(err)
	}
	if th.NativeID != id || th.Cwd != work || th.Created.IsZero() {
		t.Errorf("thread %+v", th)
	}
	var users []string
	for _, rec := range recs {
		if rec.Kind == thread.KindUser {
			users = append(users, rec.Text)
		}
	}
	if strings.Join(users, "|") != "Add a --top N flag to tally.sh.|Now run it." {
		t.Errorf("user messages %q", users)
	}
	_, _ = io.Copy(io.Discard, r)
}
