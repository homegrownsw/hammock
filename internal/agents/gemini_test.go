package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/thread"
)

func TestGeminiAndQwenCommands(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{Gemini.StartCommand(""), "gemini"},
		{Gemini.StartCommand("fix it's tests"), `gemini -i 'fix it'\''s tests'`},
		{Gemini.Resume("5f1c2a7e-3b4d-4e8f-9a01-2c3d4e5f6a7b"), "gemini --resume '5f1c2a7e-3b4d-4e8f-9a01-2c3d4e5f6a7b'"},
		{Qwen.StartCommand("hi"), "qwen -i 'hi'"},
		{Qwen.Resume("abc"), "qwen --resume 'abc'"},
		{Gemini.ResumeID([]string{"node", "/opt/homebrew/bin/gemini", "--resume", "5f1c2a7e-3b4d-4e8f-9a01-2c3d4e5f6a7b"}), "5f1c2a7e-3b4d-4e8f-9a01-2c3d4e5f6a7b"},
		{Gemini.ResumeID([]string{"gemini", "-r", "latest"}), ""},
		{Gemini.ResumeID([]string{"gemini", "--resume=3"}), ""},
		{Gemini.ResumeID([]string{"gemini", "--session-id", "my-run"}), "my-run"},
		{Qwen.ResumeID([]string{"qwen", "--resume", "3e9a1c7b-5d2f-4a8e-b6c0-7f1e2d3c4b5a"}), "3e9a1c7b-5d2f-4a8e-b6c0-7f1e2d3c4b5a"},
		{Qwen.ResumeID([]string{"qwen", "--resume", "-i", "x"}), ""},
		{qwenProject("/Users/a/my proj.v2"), "-Users-a-my-proj-v2"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if Get("gemini") != Gemini || Get("qwen") != Qwen || ByProcess("gemini") != Gemini || ByProcess("qwen") != Qwen {
		t.Error("not registered")
	}
}

// Payloads as Gemini CLI's hookEventHandler.ts builds them (0.69).
func TestGeminiHook(t *testing.T) {
	base := `"session_id":"5f1c","transcript_path":"/h/.gemini/tmp/tally/chats/session-x.jsonl","cwd":"/w","timestamp":"2026-11-07T09:00:00Z"`
	for _, c := range []struct{ payload, event, detail string }{
		{`{` + base + `,"hook_event_name":"BeforeAgent","prompt":"add a flag"}`, "UserPromptSubmit", "add a flag"},
		{`{` + base + `,"hook_event_name":"BeforeTool","tool_name":"run_shell_command","tool_input":{"command":"ls"}}`, "PreToolUse", "run_shell_command"},
		{`{` + base + `,"hook_event_name":"AfterTool","tool_name":"read_file","tool_input":{"file_path":"a"},"tool_response":{"llmContent":"x"}}`, "PostToolUse", "read_file"},
		{`{` + base + `,"hook_event_name":"AfterAgent","prompt":"add a flag","prompt_response":"Done: added it.","stop_hook_active":false}`, "Stop", "add a flagDone: added it."},
		{`{` + base + `,"hook_event_name":"Notification","notification_type":"ToolPermission","message":"Tool Gemini wants to run: ls","details":{"type":"exec","title":"Confirm Shell Command"}}`, "PermissionRequest", "Tool Gemini wants to run: ls"},
		{`{` + base + `,"hook_event_name":"SessionStart","source":"startup"}`, "SessionStart", ""},
		{`{` + base + `,"hook_event_name":"SessionEnd","reason":"exit"}`, "SessionEnd", ""},
		{`{` + base + `,"hook_event_name":"AfterModel","llm_request":{},"llm_response":{}}`, "", ""},
	} {
		h := Gemini.ReadHook("", []byte(c.payload))
		if h.Event != c.event || h.SessionID != "5f1c" || !strings.HasSuffix(h.TranscriptPath, "session-x.jsonl") {
			t.Errorf("%s: %+v", c.payload, h)
		}
		if got := h.Prompt + h.ToolName + h.LastMessage + h.Message; got != c.detail {
			t.Errorf("%s: detail %q, want %q", c.event, got, c.detail)
		}
	}
	// Qwen Code's are Claude Code's.
	h := Qwen.ReadHook("", []byte(`{"session_id":"q1","transcript_path":"/t.jsonl","hook_event_name":"PostToolUseFailure","tool_name":"edit","tool_input":{}}`))
	if h.Event != "PostToolUse" || h.SessionID != "q1" || h.ToolName != "edit" {
		t.Errorf("qwen: %+v", h)
	}
	h = Qwen.ReadHook("", []byte(`{"session_id":"q1","hook_event_name":"Stop","last_assistant_message":"Done."}`))
	if h.Event != "Stop" || h.LastMessage != "Done." {
		t.Errorf("qwen stop: %+v", h)
	}
}

func writeFile(t *testing.T, path, content string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		_ = os.Chtimes(path, mod, mod)
	}
}

func TestGeminiFindsSessions(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("GEMINI_CLI_HOME", "")
	tmp := filepath.Join(h, ".gemini", "tmp")
	fixture, err := os.ReadFile("../agentview/testdata/gemini/session-2026-11-07T09-12-5f1c2a7e.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// The current layout: a short name from projects.json, with its marker.
	writeFile(t, filepath.Join(h, ".gemini", "projects.json"), `{"projects":{"/work/tally":"tally"}}`, time.Time{})
	writeFile(t, filepath.Join(tmp, "tally", ".project_root"), "/work/tally", time.Time{})
	main := filepath.Join(tmp, "tally", "chats", "session-2026-11-07T09-12-5f1c2a7e.jsonl")
	writeFile(t, main, string(fixture), now.Add(-time.Hour))
	// A subagent's transcript isn't a session of its own.
	writeFile(t, filepath.Join(tmp, "tally", "chats", "5f1c2a7e-3b4d-4e8f-9a01-2c3d4e5f6a7b", "agent-1.jsonl"), `{"sessionId":"agent-1","projectHash":"x","kind":"subagent"}`, now)
	// An older version's folder, named by the path's hash, for another project.
	sum := sha256.Sum256([]byte("/work/old"))
	old := filepath.Join(tmp, hex.EncodeToString(sum[:]), "chats", "session-2026-01-02T03-04-aaaabbbb.jsonl")
	writeFile(t, old, `{"sessionId":"aaaabbbb-0000-4000-8000-000000000000","projectHash":"h","startTime":"2026-01-02T03:04:00Z"}`+"\n", now)

	if p, ok := Gemini.Locate("5f1c2a7e-3b4d-4e8f-9a01-2c3d4e5f6a7b"); !ok || p != main {
		t.Errorf("locate: %q %v", p, ok)
	}
	if _, ok := Gemini.Locate("5f1c2a7e-0000-0000-0000-000000000000"); ok {
		t.Error("located a session by its id's first 8 characters alone")
	}
	if p, ok := Gemini.Newest("/work/tally"); !ok || p != main {
		t.Errorf("newest: %q %v", p, ok)
	}
	if p, ok := Gemini.Newest("/work/old"); !ok || p != old {
		t.Errorf("newest by hash: %q %v", p, ok)
	}
	if _, ok := Gemini.Newest("/work/none"); ok {
		t.Error("newest for a folder with no sessions")
	}
	files := Gemini.Sessions(Gemini.Roots())
	if len(files) != 2 {
		t.Errorf("sessions: %v", files)
	}
	for _, f := range files {
		if !Gemini.OwnsFile(f) {
			t.Errorf("doesn't own %s", f)
		}
	}

	// The finder reads it with its project's folder, which the file lacks.
	r, err := Gemini.OpenSession(main)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var th thread.Thread
	var recs []string
	if _, err := Gemini.Index(r, &th, func(rec thread.Record) { recs = append(recs, string(rec.Kind)+": "+rec.Text) }); err != nil {
		t.Fatal(err)
	}
	if th.NativeID != "5f1c2a7e-3b4d-4e8f-9a01-2c3d4e5f6a7b" || th.Cwd != "/work/tally" || th.Title != "Add --top flag to tally.sh" || th.Created.IsZero() {
		t.Errorf("thread: %+v", th)
	}
	want := []string{
		"user: Add a --top N flag to tally.sh",
		"assistant: I'll read the script first.",
		"assistant: Added --top N: it prints the N biggest files, then the total.",
	}
	if strings.Join(recs, "\n") != strings.Join(want, "\n") {
		t.Errorf("records:\n%s", strings.Join(recs, "\n"))
	}
}

func TestQwenFindsSessions(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("QWEN_HOME", "")
	t.Setenv("QWEN_RUNTIME_DIR", "")
	fixture, err := os.ReadFile("../agentview/testdata/qwen/3e9a1c7b-5d2f-4a8e-b6c0-7f1e2d3c4b5a.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h, ".qwen", "projects", "-work-tally", "chats", "3e9a1c7b-5d2f-4a8e-b6c0-7f1e2d3c4b5a.jsonl")
	writeFile(t, path, string(fixture), time.Time{})
	if p, ok := Qwen.Locate("3e9a1c7b-5d2f-4a8e-b6c0-7f1e2d3c4b5a"); !ok || p != path {
		t.Errorf("locate: %q %v", p, ok)
	}
	if p, ok := Qwen.Newest("/work/tally"); !ok || p != path {
		t.Errorf("newest: %q %v", p, ok)
	}
	if s := Qwen.Sessions(Qwen.Roots()); len(s) != 1 || !Qwen.OwnsFile(s[0]) {
		t.Errorf("sessions: %v", s)
	}
	var th thread.Thread
	var recs []string
	n, _ := Qwen.Index(strings.NewReader(string(fixture)), &th, func(rec thread.Record) { recs = append(recs, string(rec.Kind)+": "+rec.Text) })
	if n != int64(len(fixture)) {
		t.Errorf("read %d of %d bytes", n, len(fixture))
	}
	if th.NativeID != "3e9a1c7b-5d2f-4a8e-b6c0-7f1e2d3c4b5a" || th.Cwd != "/work/tally" || th.Title != "Top N flag for tally" || th.Named {
		t.Errorf("thread: %+v", th)
	}
	if len(recs) != 6 || recs[0] != "user: Add a --top N flag to tally.sh" || recs[1] != "assistant: I'll read the script first." {
		t.Errorf("records:\n%s", strings.Join(recs, "\n"))
	}
	// A title you gave it stays when an automatic one comes after.
	qwenLine([]byte(`{"type":"system","subtype":"custom_title","systemPayload":{"customTitle":"Mine","titleSource":"manual"}}`), &th, func(thread.Record) {})
	qwenLine([]byte(`{"type":"system","subtype":"custom_title","systemPayload":{"customTitle":"Auto","titleSource":"auto"}}`), &th, func(thread.Record) {})
	if th.Title != "Mine" || !th.Named {
		t.Errorf("title: %q named %v", th.Title, th.Named)
	}
}
