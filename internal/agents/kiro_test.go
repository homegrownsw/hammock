package agents

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homegrownsw/hammock/internal/thread"
)

// Payloads as Kiro CLI 3.1.0 builds them (acp-server.js buildHookPayload):
// session_id, hook_event_name and cwd, and each event's own fields.
func TestKiroHook(t *testing.T) {
	const sid = "sess_0f8e3c2a-1b2c-4d5e-8f90-123456789abc"
	common := `"session_id":"` + sid + `","cwd":"/Users/a/w",`
	for _, tc := range []struct{ arg, payload, event, tool, input, prompt string }{
		{"SessionStart", `"hook_event_name":"SessionStart"`, "SessionStart", "", "", ""},
		{"UserPromptSubmit", `"hook_event_name":"UserPromptSubmit","prompt":"fix it"`, "UserPromptSubmit", "", "", "fix it"},
		{"PreToolUse", `"hook_event_name":"PreToolUse","tool_name":"execute_bash","tool_input":{"command":"ls"}`, "PreToolUse", "execute_bash", `{"command":"ls"}`, ""},
		{"PostToolUse", `"hook_event_name":"PostToolUse","tool_name":"execute_bash","tool_input":{"command":"ls"},"tool_response":{"success":true}`, "PostToolUse", "execute_bash", `{"command":"ls"}`, ""},
		{"Stop", `"hook_event_name":"Stop"`, "Stop", "", "", ""},
		{"SessionEnd", `"hook_event_name":"SessionEnd","reason":"exit"`, "SessionEnd", "", "", ""},
		{"", `"hook_event_name":"PreCompact","compaction_id":"c1","transcript_path":"/tmp/x"`, "", "", "", ""},
		// 2.x agent-config hooks, camelCase.
		{"", `"hook_event_name":"agentSpawn"`, "SessionStart", "", "", ""},
		{"", `"hook_event_name":"stop","assistant_response":"done"`, "Stop", "", "", ""},
	} {
		h := Kiro.ReadHook(tc.arg, []byte("{"+common+tc.payload+"}"))
		if h.Event != tc.event || h.ToolName != tc.tool || string(h.ToolInput) != tc.input || h.Prompt != tc.prompt || h.SessionID != sid {
			t.Errorf("%s: %+v", tc.payload, h)
		}
	}
}

func TestKiroCommands(t *testing.T) {
	const id = "sess_0f8e3c2a-1b2c-4d5e-8f90-123456789abc"
	if got := Kiro.StartCommand("it's"); got != `kiro-cli chat 'it'\''s'` {
		t.Errorf("start %q", got)
	}
	if got := Kiro.StartCommand(""); got != "kiro-cli chat" {
		t.Errorf("bare start %q", got)
	}
	if got := Kiro.Resume(id); got != "kiro-cli chat --v3 --resume-id '"+id+"'" {
		t.Errorf("resume %q", got)
	}
	for argv, want := range map[string]string{
		"kiro-cli chat --resume-id " + id:       id,
		"kiro-cli chat --v3 --resume-id=" + id:  id,
		"kiro-cli --resume-id " + id + " hello": id,
		"kiro-cli chat --resume":                "",
		"kiro-cli chat " + id:                   "",
	} {
		if got := Kiro.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q", argv, got)
		}
	}
	if ByProcess("kiro") != Kiro || ByProcess("kiro-cli") != Kiro || Parse("Kiro") != Kiro {
		t.Error("not found by its names")
	}
}

// The screen as the 3.1.0 TUI draws it (tui.js).
func TestKiroScreen(t *testing.T) {
	working := []string{"⠋ Thinking... (esc to cancel)", "Still thinking... (ctrl+c to cancel)", "Thinking... (esc to cancel · ctrl+o to view)"}
	asking := []string{"execute_bash · go test requires approval", "[y] allow once · [n] deny · [tab] add note · [esc] interrupt",
		"❯ Yes, single permission", "Allow this action? Use 't' to trust (always allow) this tool for the session. [y/n/t]:"}
	for _, s := range working {
		if !Kiro.WorkingRE().MatchString(s) {
			t.Errorf("not working: %q", s)
		}
	}
	for _, s := range asking {
		if !Kiro.NeedsInputRE().MatchString(s) {
			t.Errorf("not asking: %q", s)
		}
	}
	// The approval card's own hint isn't working.
	if Kiro.WorkingRE().MatchString("str_replace · calc.go requires approval        esc to cancel") {
		t.Error("the approval card reads as working")
	}
}

// Kiro names a workspace's folder of sessions by the SHA-256 of its path
// (JustSessions saw /private/tmp/kiro-v3-probe as 7b13d0ff170a04dc).
func TestKiroWorkspaceHash(t *testing.T) {
	if got := kiroWorkspaceHashes("/private/tmp/kiro-v3-probe/")[0]; got != "7b13d0ff170a04dc" {
		t.Errorf("hash %q", got)
	}
}

func TestKiroSessions(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("KIRO_HOME", "")
	cwd := filepath.Join(h, "w")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(h, ".kiro", "sessions")
	ws := kiroWorkspaceHashes(cwd)[len(kiroWorkspaceHashes(cwd))-1]
	write := func(id, meta, messages string) string {
		dir := filepath.Join(root, ws, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "messages.jsonl")
		if err := os.WriteFile(p, []byte(messages), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const id = "sess_11111111-2222-4333-8444-555555555555"
	main := write(id,
		`{"schemaVersion":"1.0.0","id":"`+id+`","title":"Fix the build","agentMode":"vibe","workspacePaths":["`+cwd+`"],"createdAt":"2026-11-06T10:00:00.000Z","lastModifiedAt":"2026-11-06T10:01:00.000Z","modelId":"auto"}`,
		`{"id":"u1","timestamp":"2026-11-06T10:00:01.000Z","payload":{"type":"user","content":"Fix the build","source":"chat"}}
{"id":"h1","timestamp":"2026-11-06T10:00:01.100Z","payload":{"type":"user","content":"git: clean","source":"hook"}}
{"id":"a1-say","timestamp":"2026-11-06T10:00:02.000Z","payload":{"type":"assistant","content":"Looking","operationType":"Say","_meta":{"kiro":{"streaming":true}}}}
{"id":"a1-say","timestamp":"2026-11-06T10:00:02.000Z","payload":{"type":"assistant","content":"Looking into it.","operationType":"Say"}}
{"id":"a2-say","timestamp":"2026-11-06T10:00:03.000Z","payload":{"type":"assistant","content":"...","operationType":"Reasoning"}}
{"id":"a3-call","timestamp":"2026-11-06T10:00:04.000Z","payload":{"type":"tool_call","toolCallId":"a3","toolName":"execute_bash","args":{"command":"make"},"status":"completed"}}
`)
	sub := write("sess_99999999-2222-4333-8444-555555555555",
		`{"id":"sess_99999999-2222-4333-8444-555555555555","title":"sub","workspacePaths":["`+cwd+`"],"createdAt":"2026-11-06T10:00:00.000Z","lastModifiedAt":"2026-11-06T10:00:00.000Z","createdReason":"subagent"}`,
		`{"id":"x","timestamp":"2026-11-06T10:00:01.000Z","payload":{"type":"user","content":"review","source":"chat"}}`+"\n")

	if p, ok := Kiro.Locate(id); !ok || p != main {
		t.Errorf("locate %q %v", p, ok)
	}
	if _, ok := Kiro.Locate("../x"); ok {
		t.Error("located a path")
	}
	if p, ok := Kiro.Newest(cwd); !ok || p != main {
		t.Errorf("newest %q %v", p, ok)
	}
	if got := Kiro.Sessions(Kiro.Roots()); len(got) != 1 || got[0] != main {
		t.Errorf("sessions %v", got)
	}
	if !Kiro.OwnsFile(main) || !Kiro.OwnsFile(sub) || Kiro.OwnsFile(filepath.Join(filepath.Dir(main), "session.json")) {
		t.Error("owns")
	}

	r, err := Kiro.OpenSession(main)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var th thread.Thread
	var recs []string
	if _, err := Kiro.Index(r, &th, func(rec thread.Record) { recs = append(recs, string(rec.Kind)+": "+rec.Text) }); err != nil {
		t.Fatal(err)
	}
	if th.NativeID != id || th.Cwd != cwd || th.Title != "Fix the build" || th.Created.IsZero() {
		t.Errorf("thread %+v", th)
	}
	if strings.Join(recs, "\n") != string(thread.KindUser)+": Fix the build\n"+string(thread.KindAssistant)+": Looking into it." {
		t.Errorf("records %q", recs)
	}
	// The agent view reads the file itself; the line Open adds is skipped.
	r2, _ := Kiro.OpenSession(main)
	b, _ := io.ReadAll(r2)
	r2.Close()
	if !strings.HasPrefix(string(b), `{"created":`) || !strings.Contains(strings.SplitN(string(b), "\n", 2)[0], `"type":"hammock_session"`) {
		t.Errorf("open: %.200s", b)
	}
}
