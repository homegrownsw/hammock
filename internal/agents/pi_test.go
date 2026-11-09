package agents

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/thread"
)

// The payloads Hammock's Pi extension sends (cmd/hmk/hooks_pi.go), as pi
// 1.5.0 produced them in a pane.
func TestParsePiHook(t *testing.T) {
	const sess = `"session_id":"01a1b576-52a3-73e2-880f-3eb0a9d18ee8","transcript_path":"/Users/me/.pi/agent/sessions/--Users-me-proj--/2026-11-07T10-03-12-932Z_01a1b576-52a3-73e2-880f-3eb0a9d18ee8.jsonl"`
	cases := []struct {
		event, payload string
		want           Hook
	}{
		{"session_start", `{` + sess + `,"reason":"startup"}`, Hook{Event: "SessionStart"}},
		{"before_agent_start", `{` + sess + `,"prompt":"say hi"}`, Hook{Event: "UserPromptSubmit", Prompt: "say hi"}},
		{"tool_execution_start", `{` + sess + `,"tool_name":"bash","tool_input":{"command":"echo hi"}}`, Hook{Event: "PreToolUse", ToolName: "bash"}},
		{"tool_execution_end", `{` + sess + `,"tool_name":"bash"}`, Hook{Event: "PostToolUse", ToolName: "bash"}},
		{"ui_prompt_start", `{` + sess + `,"title":"Allow rm -rf build?"}`, Hook{Event: "PermissionRequest", Message: "Allow rm -rf build?"}},
		{"ui_prompt_start", `{` + sess + `}`, Hook{Event: "PermissionRequest", Message: "Waiting for you"}},
		{"ui_prompt_end", `{` + sess + `,"idle":false}`, Hook{Event: "PostToolUse"}},
		{"ui_prompt_end", `{` + sess + `,"idle":true}`, Hook{Event: "Notification", NotificationType: "idle_prompt"}},
		{"agent_settled", `{` + sess + `,"aborted":false,"last_assistant_message":"Done: it printed hi.","error":""}`, Hook{Event: "Stop", LastMessage: "Done: it printed hi."}},
		{"agent_settled", `{` + sess + `,"aborted":true,"last_assistant_message":"","error":"aborted"}`, Hook{Event: "Stop", Error: "aborted"}},
		{"agent_settled", `{` + sess + `,"aborted":false,"last_assistant_message":"","error":"429 rate limit exceeded"}`,
			Hook{Event: "StopFailure", Error: "429 rate limit exceeded", ErrorDetails: "429 rate limit exceeded", LastMessage: "429 rate limit exceeded"}},
		{"session_shutdown", `{` + sess + `,"reason":"quit"}`, Hook{Event: "SessionEnd"}},
		{"session_shutdown", `{` + sess + `,"reason":"resume"}`, Hook{Event: ""}},
		{"turn_start", `{` + sess + `}`, Hook{Event: ""}},
	}
	for _, c := range cases {
		h := Pi.ReadHook(c.event, []byte(c.payload))
		if h.SessionID != "01a1b576-52a3-73e2-880f-3eb0a9d18ee8" || !strings.HasSuffix(h.TranscriptPath, "_01a1b576-52a3-73e2-880f-3eb0a9d18ee8.jsonl") {
			t.Errorf("%s: session %q %q", c.event, h.SessionID, h.TranscriptPath)
		}
		got := h
		got.SessionID, got.TranscriptPath, got.ToolInput = "", "", nil
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s:\n got %+v\nwant %+v", c.event, c.payload, got, c.want)
		}
	}
	// Pi's "path" is given as file_path too, for Hammock's summaries.
	h := Pi.ReadHook("tool_execution_start", []byte(`{"tool_name":"edit","tool_input":{"path":"src/x.go"}}`))
	if !strings.Contains(string(h.ToolInput), `"file_path":"src/x.go"`) {
		t.Errorf("tool input %s", h.ToolInput)
	}
}

func TestPiCommands(t *testing.T) {
	for _, c := range []struct{ prompt, want string }{
		{"", "pi"},
		{"fix the tests", "pi 'fix the tests'"},
		{"it's broken", `pi 'it'\''s broken'`},
		{"@README.md says what?", "pi ' @README.md says what?'"},
		{"--help me", "pi ' --help me'"},
	} {
		if got := Pi.StartCommand(c.prompt); got != c.want {
			t.Errorf("start %q: %s, want %s", c.prompt, got, c.want)
		}
	}
	if got := Pi.Resume("01a1b576-52a3-73e2-880f-3eb0a9d18ee8"); got != "pi --session '01a1b576-52a3-73e2-880f-3eb0a9d18ee8'" {
		t.Errorf("resume: %s", got)
	}
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"node", "/opt/homebrew/bin/pi", "--session", "01a1b576-52a3-73e2-880f-3eb0a9d18ee8"}, "01a1b576-52a3-73e2-880f-3eb0a9d18ee8"},
		{[]string{"pi", "--session=01a1b5"}, "01a1b5"},
		{[]string{"pi", "--session", "/Users/me/.pi/agent/sessions/--Users-me-p--/2026-11-07T10-03-12-932Z_01a1b576-52a3-73e2-880f-3eb0a9d18ee8.jsonl"}, "01a1b576-52a3-73e2-880f-3eb0a9d18ee8"},
		{[]string{"pi", "--session-id", "my-task.1"}, "my-task.1"},
		{[]string{"pi", "--session", "../../etc/passwd"}, ""},
		{[]string{"pi", "-c"}, ""},
		{[]string{"pi", "fix it"}, ""},
	} {
		if got := Pi.ResumeID(c.argv); got != c.want {
			t.Errorf("resume id %v: %q, want %q", c.argv, got, c.want)
		}
	}
}

// Pi 1.5.0's screen while working, and idle (its startup hints).
func TestPiScreen(t *testing.T) {
	if !Pi.WorkingRE().MatchString("say hi\n\n── ⠋ Working ─────────────────\n2.1%/100k (auto)    fake-1") {
		t.Error("working screen not seen")
	}
	if Pi.WorkingRE().MatchString("█▀ █ escape interrupt · ctrl+c/ctrl+d clear/exit · / commands\n────────────────\n0.0%/100k (auto)") {
		t.Error("idle screen seen as working")
	}
}

func TestPiSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	cwd := "/private/tmp/claude-501/-Users-me-Hammock/proj2"
	// As pi 1.5.0 named the folder for this cwd.
	if got := piCwdDir(cwd); got != "--private-tmp-claude-501--Users-me-Hammock-proj2--" {
		t.Fatalf("cwd folder %s", got)
	}
	dir := filepath.Join(home, ".pi", "agent", "sessions", piCwdDir(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(dir, "2026-11-06T09-00-00-000Z_01a1b500-0000-7000-8000-000000000001.jsonl")
	newer := filepath.Join(dir, "2026-11-07T10-05-00-853Z_01a1b577-f833-7289-8cf5-ab39191246a3.jsonl")
	for i, f := range []string{older, newer} {
		if err := os.WriteFile(f, []byte(`{"type":"session","version":3,"id":"x","timestamp":"2026-11-07T10:05:00.853Z","cwd":"`+cwd+`"}`+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(time.Duration(i-2) * time.Hour)
		_ = os.Chtimes(f, at, at)
	}
	_ = os.WriteFile(filepath.Join(dir, "notes.jsonl"), []byte("{}\n"), 0o644)

	if p, ok := Pi.Newest(cwd); !ok || p != newer {
		t.Errorf("newest: %s %v", p, ok)
	}
	if p, ok := Pi.Locate("01a1b577-f833-7289-8cf5-ab39191246a3"); !ok || p != newer {
		t.Errorf("locate: %s %v", p, ok)
	}
	if _, ok := Pi.Locate("*"); ok {
		t.Error("located a pattern")
	}
	if got := Pi.Sessions(Pi.Roots()); len(got) != 2 {
		t.Errorf("sessions: %v", got)
	}
	if Pi.OwnsFile(filepath.Join(dir, "notes.jsonl")) || !Pi.OwnsFile(newer) {
		t.Error("owns the wrong files")
	}

	// A custom session folder (settings.json's sessionDir) holds every
	// session flat; the newest for a cwd is found by its header.
	custom := filepath.Join(home, "pi-sessions")
	_ = os.MkdirAll(custom, 0o755)
	_ = os.WriteFile(filepath.Join(home, ".pi", "agent", "settings.json"), []byte(`{"sessionDir":"~/pi-sessions"}`), 0o644)
	flat := filepath.Join(custom, "2026-11-07T11-00-00-000Z_01a1b600-0000-7000-8000-000000000002.jsonl")
	other := filepath.Join(custom, "2026-11-07T12-00-00-000Z_01a1b600-0000-7000-8000-000000000003.jsonl")
	_ = os.WriteFile(flat, []byte(`{"type":"session","version":3,"id":"y","timestamp":"2026-11-07T11:00:00.000Z","cwd":"`+cwd+`"}`+"\n"), 0o644)
	_ = os.WriteFile(other, []byte(`{"type":"session","version":3,"id":"z","timestamp":"2026-11-07T12:00:00.000Z","cwd":"/elsewhere"}`+"\n"), 0o644)
	if p, ok := Pi.Newest(cwd); !ok || p != flat {
		t.Errorf("newest with a custom folder: %s %v", p, ok)
	}
	if got := Pi.Sessions(Pi.Roots()); len(got) != 4 {
		t.Errorf("sessions with a custom folder: %v", got)
	}

	// $PI_CODING_AGENT_DIR moves the config folder (and its sessions).
	moved := filepath.Join(home, "elsewhere")
	t.Setenv("PI_CODING_AGENT_DIR", moved)
	roots := Pi.Roots()
	if roots[1] != filepath.Join(moved, "sessions") {
		t.Errorf("roots %v", roots)
	}
}

func TestPiIndex(t *testing.T) {
	f, err := os.Open("../agentview/testdata/pi/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var th thread.Thread
	var recs []thread.Record
	n, err := Pi.Index(f, &th, func(r thread.Record) { recs = append(recs, r) })
	if err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	if n != st.Size() {
		t.Errorf("read %d of %d bytes", n, st.Size())
	}
	if th.NativeID != "01a1b577-f833-7289-8cf5-ab39191246a3" || th.Cwd != "/Users/me/proj" || th.Title != "Notes work" || !th.Named ||
		th.Created.Format(time.RFC3339) != "2026-11-07T10:05:00Z" {
		t.Errorf("thread %+v", th)
	}
	var users []string
	for _, r := range recs {
		if r.Kind == thread.KindUser {
			users = append(users, r.Text)
		}
	}
	// Every branch is searchable.
	if strings.Join(users, "|") != "make notes|again please|try another way" {
		t.Errorf("users %q", users)
	}
}

func TestPiSlashCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_CODING_AGENT_DIR", "")
	_ = os.MkdirAll(filepath.Join(home, ".pi", "agent", "prompts"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".pi", "agent", "prompts", "review.md"), []byte("Review"), 0o644)
	_ = os.MkdirAll(filepath.Join(home, ".agents", "skills", "pdf"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".agents", "skills", "pdf", "SKILL.md"), []byte("---\nname: pdf-tools\n---\n"), 0o644)
	got := strings.Join(piSlashCommands(""), " ")
	for _, want := range []string{"/tree", "/resume", "/review", "/skill:pdf-tools"} {
		if !strings.Contains(" "+got+" ", " "+want+" ") {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}
