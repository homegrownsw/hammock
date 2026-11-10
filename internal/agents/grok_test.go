package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/thread"
)

// Payloads as Grok 1.0.77's hooks documentation gives them: camelCase,
// with Claude Code's snake_case names alongside (hook_event_name holds the
// PascalCase name, hookEventName Grok's own snake_case one).
func TestGrokHooks(t *testing.T) {
	const tp = "/h/.grok/sessions/%2Fp/019f-s1/updates.jsonl"
	cases := []struct {
		event, payload string
		want           Hook
	}{
		{"SessionStart", `{"hookEventName":"session_start","hook_event_name":"SessionStart","sessionId":"s1","session_id":"s1","cwd":"/p","workspaceRoot":"/p","transcriptPath":"` + tp + `","transcript_path":"` + tp + `","source":"new"}`,
			Hook{Event: "SessionStart", SessionID: "s1", TranscriptPath: tp}},
		{"UserPromptSubmit", `{"hookEventName":"user_prompt_submit","hook_event_name":"UserPromptSubmit","sessionId":"s1","prompt":"fix it","promptId":"p1"}`,
			Hook{Event: "UserPromptSubmit", SessionID: "s1", Prompt: "fix it"}},
		{"PreToolUse", `{"hookEventName":"pre_tool_use","hook_event_name":"PreToolUse","sessionId":"s1","toolName":"run_terminal_command","tool_name":"run_terminal_command","toolInput":{"command":"npm test"},"tool_input":{"command":"npm test"},"toolUseId":"c1","toolInputTruncated":false}`,
			Hook{Event: "PreToolUse", SessionID: "s1", ToolName: "run_terminal_command", ToolInput: []byte(`{"command":"npm test"}`)}},
		{"PostToolUseFailure", `{"hook_event_name":"PostToolUseFailure","sessionId":"s1","toolName":"github__search","toolInput":{}}`,
			Hook{Event: "PostToolUse", SessionID: "s1", ToolName: "github__search", ToolInput: []byte(`{}`)}},
		{"Stop", `{"hook_event_name":"Stop","sessionId":"s1","reason":"end_turn","stopHookActive":false,"lastAssistantMessage":"Done.","last_assistant_message":"Done."}`,
			Hook{Event: "Stop", SessionID: "s1", LastMessage: "Done."}},
		// The Stop fired as the session closes.
		{"Stop", `{"hook_event_name":"Stop","sessionId":"s1","reason":"shutdown","lastAssistantMessage":"Done."}`, Hook{Event: "", SessionID: "s1"}},
		{"StopCancelled", `{"hook_event_name":"StopCancelled","sessionId":"s1","reason":"user_interrupt","lastAssistantMessage":"I was"}`,
			Hook{Event: "Stop", SessionID: "s1"}},
		{"StopFailure", `{"hook_event_name":"StopFailure","sessionId":"s1","error":"rate_limit","errorDetails":"429 slow down","lastAssistantMessage":"API error: 429"}`,
			Hook{Event: "StopFailure", SessionID: "s1", Error: "rate_limit", ErrorDetails: "429 slow down"}},
		{"StopFailure", `{"hook_event_name":"StopFailure","sessionId":"c1","error":"server_error","subagentType":"explore"}`, Hook{Event: "", SessionID: "c1", Error: "server_error"}},
		{"Notification", `{"hook_event_name":"Notification","sessionId":"s1","notificationType":"permission_prompt","notification_type":"permission_prompt","message":"Tool permission requested"}`,
			Hook{Event: "Notification", SessionID: "s1", Message: "Tool permission requested", NotificationType: "permission_prompt"}},
		{"Notification", `{"hook_event_name":"Notification","sessionId":"s1","notificationType":"elicitation_dialog","message":"User question requested"}`,
			Hook{Event: "Notification", SessionID: "s1", Message: "User question requested", NotificationType: "permission_prompt"}},
		{"Notification", `{"hook_event_name":"Notification","sessionId":"s1","notificationType":"idle_prompt","message":"Waiting for your next prompt"}`,
			Hook{Event: "Notification", SessionID: "s1", Message: "Waiting for your next prompt", NotificationType: "idle_prompt"}},
		{"Notification", `{"hook_event_name":"Notification","sessionId":"s1","notificationType":"task_complete"}`, Hook{Event: "", SessionID: "s1", NotificationType: "task_complete"}},
		{"SessionEnd", `{"hook_event_name":"SessionEnd","sessionId":"s1","reason":"exit"}`, Hook{Event: "SessionEnd", SessionID: "s1"}},
		{"SessionEnd", `{"hook_event_name":"SessionEnd","sessionId":"c1","subagentType":"explore"}`, Hook{Event: "", SessionID: "c1"}},
	}
	for _, c := range cases {
		got := Grok.ReadHook(c.event, []byte(c.payload))
		if got.Event != c.want.Event || got.SessionID != c.want.SessionID || got.Prompt != c.want.Prompt || got.ToolName != c.want.ToolName ||
			string(got.ToolInput) != string(c.want.ToolInput) || got.TranscriptPath != c.want.TranscriptPath || got.Message != c.want.Message ||
			got.NotificationType != c.want.NotificationType || got.LastMessage != c.want.LastMessage ||
			got.Error != c.want.Error || got.ErrorDetails != c.want.ErrorDetails {
			t.Errorf("%s %s:\n got %+v\nwant %+v", c.event, c.payload, got, c.want)
		}
	}
	// Without one in the payload, the session is its environment's.
	t.Setenv("GROK_SESSION_ID", "env-id")
	if got := Grok.ReadHook("SessionStart", []byte(`{}`)); got.SessionID != "env-id" {
		t.Errorf("session from the environment: %+v", got)
	}
}

func TestGrokCommands(t *testing.T) {
	if got := Grok.StartCommand("fix the bug's cause"); got != `grok 'fix the bug'\''s cause'` {
		t.Errorf("start: %s", got)
	}
	if got := Grok.StartCommand(""); got != "grok" {
		t.Errorf("start: %s", got)
	}
	const id = "019fec1a-12f7-72f2-9b1f-7778a00aea3c"
	if got := Grok.Resume(id); got != "grok --resume '"+id+"'" {
		t.Errorf("resume: %s", got)
	}
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"grok", "--resume", id}, id},
		{[]string{"grok", "--resume=" + strings.ToUpper(id)}, id},
		{[]string{"grok", "-p", "go on", "-r", id}, id},
		{[]string{"grok", "-s", id}, id},
		{[]string{"grok", "--resume", "fix-login-bug"}, ""}, // a title
		{[]string{"grok", "--resume"}, ""},
		{[]string{"grok", "-c"}, ""},
		{[]string{"grok", "-r", id, "--fork-session"}, ""},
	} {
		if got := grokResumeID(c.argv); got != c.want {
			t.Errorf("%v: %q, want %q", c.argv, got, c.want)
		}
	}
	if a := ByProcess("grok"); a != Grok {
		t.Errorf("grok is %v", a)
	}
	if a := ByProcess("grok-native"); a != Grok {
		t.Errorf("grok-native is %v", a)
	}
}

// A sessions folder laid out as Grok's Session Management documentation
// says: ~/.grok/sessions/<cwd URL-encoded>/<id>/{summary.json,
// updates.jsonl, subagents/<id>/meta.json}, a long folder's group named
// by a hash with its path in .cwd.
func TestGrokSessions(t *testing.T) {
	root := t.TempDir()
	const main, child, other, long = "019f0000-0000-7000-8000-000000000001", "019f0000-0000-7000-8000-000000000002", "019f0000-0000-7000-8000-000000000003", "019f0000-0000-7000-8000-000000000004"
	write := func(rel, content string, mod time.Time) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if !mod.IsZero() {
			_ = os.Chtimes(p, mod, mod)
		}
		return p
	}
	now := time.Now()
	mainFile := write("%2Fwork%2Fapp/"+main+"/updates.jsonl", "{}\n", now.Add(-time.Hour))
	write("%2Fwork%2Fapp/"+main+"/summary.json", `{"info":{"id":"`+main+`","cwd":"/work/app"},"generated_title":"Fix the math test","session_summary":"x","title_is_manual":false}`, time.Time{})
	write("%2Fwork%2Fapp/"+main+"/subagents/"+child+"/meta.json", `{"subagentId":"`+child+`","childSessionId":"`+child+`","parentPromptId":"p1"}`, time.Time{})
	// The subagent's own session, newer, beside it.
	write("%2Fwork%2Fapp/"+child+"/updates.jsonl", "{}\n", now)
	otherFile := write("%2Fwork%2Fother/"+other+"/updates.jsonl", "{}\n", now)
	longFile := write("deep-folder-1a2b3c/"+long+"/updates.jsonl", "{}\n", now)
	write("deep-folder-1a2b3c/.cwd", "/very/deep/folder\n", time.Time{})
	write("%2Fwork%2Fapp/permission.toml", "", time.Time{})

	roots := []string{root, filepath.Join(root, "missing")}
	got := grokSessions(roots)
	if len(got) != 3 || !grokContains(got, mainFile) || !grokContains(got, otherFile) || !grokContains(got, longFile) {
		t.Errorf("sessions: %v", got)
	}
	if p, ok := grokNewest(roots, "/work/app/"); !ok || p != mainFile {
		t.Errorf("newest in /work/app: %s %v", p, ok)
	}
	if p, ok := grokNewest(roots, "/very/deep/folder"); !ok || p != longFile {
		t.Errorf("newest in a long folder: %s %v", p, ok)
	}
	if _, ok := grokNewest(roots, "/work"); ok {
		t.Error("a session in a folder with none")
	}
	if p, ok := grokLocate(roots, other); !ok || p != otherFile {
		t.Errorf("locate: %s %v", p, ok)
	}
	if _, ok := grokLocate(roots, "../"+other); ok {
		t.Error("locate outside the root")
	}
	if names := grokNames(roots); names[main] != "Fix the math test" || len(names) != 1 {
		t.Errorf("names: %v", names)
	}
	if !grokOwnsFile(mainFile) || grokOwnsFile(filepath.Join(filepath.Dir(mainFile), "chat_history.jsonl")) {
		t.Error("owns file")
	}
}

func grokContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// The session finder reads the agent view's fixture: your messages and its
// replies, each joined from its chunks.
func TestGrokIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "%2Fwork%2Fapp", "019fec1a-12f7-72f2-9b1f-7778a00aea3c")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../agentview/testdata/grok/updates.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "updates.jsonl")
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var th thread.Thread
	var got []string
	n, err := grokIndex(f, &th, func(r thread.Record) { got = append(got, string(r.Kind)+": "+r.Text) })
	if err != nil || n != int64(len(doc)) {
		t.Fatalf("read %d of %d: %v", n, len(doc), err)
	}
	want := []string{
		"user: Fix the failing test in @math.go",
		"assistant: I'll run the tests first.",
		"assistant: Fixed: `Add` subtracted.",
		"user: Now run it in the background",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if th.NativeID != "019fec1a-12f7-72f2-9b1f-7778a00aea3c" || th.Cwd != "/work/app" || th.Title != "Fix failing math test" ||
		!th.Created.Equal(time.UnixMilli(1790405167000)) {
		t.Errorf("thread: %+v", th)
	}

	// A reply still streaming at the end is read next time, whole.
	part := `{"timestamp":1,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}}}` + "\n" +
		`{"timestamp":2,"method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hel"}}}}` + "\n"
	got = nil
	n, _ = grokIndex(strings.NewReader(part), &thread.Thread{}, func(r thread.Record) { got = append(got, string(r.Kind)+": "+r.Text) })
	if first := strings.Index(part, "\n") + 1; n != int64(first) || len(got) != 1 || got[0] != "user: hi" {
		t.Errorf("partial: %d %v", n, got)
	}
}
