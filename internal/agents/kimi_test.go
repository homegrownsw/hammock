package agents

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/thread"
)

// Kimi Code's hook payloads (features/externalHooks: camelCase input made
// snake_case, with hook_event_name, session_id, cwd and session_title).
func TestKimiHook(t *testing.T) {
	for _, c := range []struct {
		payload string
		want    Hook
	}{
		{`{"hook_event_name":"UserPromptSubmit","session_id":"session_1","cwd":"/w","prompt":[{"type":"text","text":"fix it"},{"type":"image_url","imageUrl":{"url":"x"}}],"is_steer":false}`,
			Hook{Event: "UserPromptSubmit", SessionID: "session_1", Prompt: "fix it"}},
		{`{"hook_event_name":"PreToolUse","session_id":"session_1","tool_name":"Edit","tool_input":{"path":"a.go","old_string":"x","new_string":"y"},"tool_call_id":"c1"}`,
			Hook{Event: "PreToolUse", SessionID: "session_1", ToolName: "Edit", ToolInput: json.RawMessage(`{"file_path":"a.go","new_string":"y","old_string":"x"}`)}},
		{`{"hook_event_name":"PostToolUseFailure","session_id":"session_1","tool_name":"Bash","tool_input":{"command":"false"},"error":{"code":"x","message":"exit 1"}}`,
			Hook{Event: "PostToolUse", SessionID: "session_1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"false"}`)}},
		{`{"hook_event_name":"PermissionRequest","session_id":"session_1","agent_id":"main","tool_name":"Bash","action":"run","tool_input":{"command":"rm x"}}`,
			Hook{Event: "PermissionRequest", SessionID: "session_1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"rm x"}`)}},
		{`{"hook_event_name":"Interrupt","session_id":"session_1","turn_id":3,"reason":"cancelled"}`,
			Hook{Event: "Stop", SessionID: "session_1"}},
		{`{"hook_event_name":"StopFailure","session_id":"session_1","error_type":"RateLimitError","error_message":"Rate limit reached"}`,
			Hook{Event: "StopFailure", SessionID: "session_1", Error: "RateLimitError", ErrorDetails: "Rate limit reached"}},
		{`{"hook_event_name":"Notification","session_id":"session_1","notification_type":"task.completed"}`,
			Hook{Event: "", SessionID: "session_1", NotificationType: "task.completed"}},
	} {
		got := Kimi.ReadHook("", []byte(c.payload))
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(c.want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("%s:\ngot  %s\nwant %s", c.payload, gotJSON, wantJSON)
		}
	}
}

func TestKimiResumeID(t *testing.T) {
	for argv, want := range map[string]string{
		"kimi --session session_ab12":   "session_ab12",
		"kimi -S session_ab12 --yolo":   "session_ab12",
		"kimi -r session_ab12":          "session_ab12",
		"kimi --resume=session_ab12":    "session_ab12",
		"kimi --session --yolo":         "",
		"kimi -c":                       "",
		"kimi --session ../../etc":      "",
		"node /x/bin/kimi -S session_9": "session_9",
	} {
		if got := Kimi.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q, want %q", argv, got, want)
		}
	}
	if got := Kimi.Resume("session_ab12"); got != "kimi --session 'session_ab12'" {
		t.Errorf("Resume: %s", got)
	}
	if got := Kimi.StartCommand("hello"); got != "kimi" {
		t.Errorf("Start: %s", got)
	}
}

// Workspace folders as kimi-code's encodeWorkDirKey names them (values from
// running its algorithm in node).
func TestKimiWorkspace(t *testing.T) {
	for cwd, want := range map[string]string{
		"/Users/me/My App": "wd_my-app_ca799e010ccb",
		"/Users/me/app/":   "wd_app_844551840eba",
		"/":                "wd_workspace_e3b0c44298fc",
	} {
		if got := kimiWorkspace(cwd); got != want {
			t.Errorf("%s: %s, want %s", cwd, got, want)
		}
	}
}

// A Kimi data folder: two sessions in a folder's workspace, one in a
// managed project's that only session_index.jsonl ties to its folder.
func TestKimiSessions(t *testing.T) {
	data := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", data)
	work := t.TempDir()
	work, _ = filepath.EvalSymlinks(work)
	sessions := filepath.Join(data, "sessions")
	journal := func(ws, id, wire, state string, mod time.Time) string {
		dir := filepath.Join(sessions, ws, id)
		p := filepath.Join(dir, "agents", "main", "wire.jsonl")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(wire), 0o644); err != nil {
			t.Fatal(err)
		}
		if state != "" {
			_ = os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644)
		}
		_ = os.Chtimes(p, mod, mod)
		return p
	}
	now := time.Now()
	wire := `{"type":"metadata","protocol_version":"1.5","created_at":1794045600000}
{"type":"config.update","environmentDisclosure":{"cwd":"` + work + `"},"time":1794045600010}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"Add a test"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1794045601000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>x</system-reminder>"}],"toolCalls":[],"origin":{"kind":"injection","variant":"v"}},"time":1794045601500}
{"type":"context.append_loop_event","event":{"type":"content.part","stepUuid":"s1","part":{"type":"text","text":"Added it."}},"time":1794045602000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"Commit. ARGUMENTS: now"}],"toolCalls":[],"origin":{"kind":"skill_activation","skillName":"commit","skillArgs":"now","trigger":"user-slash"}},"time":1794045603000}
`
	ws := kimiWorkspace(work)
	older := journal(ws, "session_old", wire, "", now.Add(-time.Hour))
	newer := journal(ws, "session_new", wire, `{"id":"session_new","title":"Parser tests","titleKind":"custom","isCustomTitle":true,"cwd":"`+work+`","createdAt":1794045600000,"updatedAt":1794045603000,"archived":false}`, now.Add(-time.Minute))
	managed := journal("wm_1234", "session_managed", wire, "", now.Add(-2*time.Hour))
	_ = os.WriteFile(filepath.Join(data, "subagent-not-a-session.txt"), nil, 0o644)

	if p, ok := Kimi.Locate("session_old"); !ok || p != older {
		t.Errorf("Locate: %s %v", p, ok)
	}
	if _, ok := Kimi.Locate("../session_old"); ok {
		t.Error("Locate took a path")
	}
	if p, ok := Kimi.Newest(work); !ok || p != newer {
		t.Errorf("Newest: %s %v", p, ok)
	}
	// A managed project: found through session_index.jsonl.
	proj := t.TempDir()
	proj, _ = filepath.EvalSymlinks(proj)
	idx := `{"sessionId":"session_managed","sessionDir":"` + filepath.Dir(filepath.Dir(filepath.Dir(managed))) + `","workDir":"` + proj + `"}` + "\n"
	_ = os.WriteFile(filepath.Join(data, "session_index.jsonl"), []byte(idx), 0o644)
	if p, ok := Kimi.Newest(proj); !ok || p != managed {
		t.Errorf("Newest (managed): %s %v", p, ok)
	}
	_ = os.WriteFile(filepath.Join(data, "session_index.jsonl"), []byte(idx+`{"sessionId":"session_managed","deleted":true}`+"\n"), 0o644)
	if p, ok := Kimi.Newest(proj); ok {
		t.Errorf("Newest found a deleted session: %s", p)
	}

	if got := Kimi.Sessions([]string{sessions}); len(got) != 3 {
		t.Errorf("Sessions: %v", got)
	}
	if !Kimi.OwnsFile(newer) || Kimi.OwnsFile(filepath.Join(filepath.Dir(newer), "plans", "p.md")) || Kimi.OwnsFile(filepath.Join(sessions, ws, "session_new", "state.json")) {
		t.Error("OwnsFile")
	}

	index := func(p string) (thread.Thread, []thread.Record) {
		r, err := Kimi.OpenSession(p)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		var th thread.Thread
		var recs []thread.Record
		if _, err := Kimi.Index(r, &th, func(rec thread.Record) { recs = append(recs, rec) }); err != nil {
			t.Fatal(err)
		}
		return th, recs
	}
	th, recs := index(newer)
	if th.NativeID != "session_new" || th.Cwd != work || th.Title != "Parser tests" || !th.Named || th.Created.UnixMilli() != 1794045600000 {
		t.Errorf("thread: %+v", th)
	}
	var got []string
	for _, r := range recs {
		got = append(got, string(r.Kind)+": "+r.Text)
	}
	if strings.Join(got, "|") != "user: Add a test|assistant: Added it.|user: /skill:commit now" {
		t.Errorf("records: %q", got)
	}
	// No state.json: the folder's name, the journal's folder and first message.
	th, _ = index(older)
	if th.NativeID != "session_old" || th.Cwd != work || th.Title != "" || th.Created.UnixMilli() != 1794045601000 {
		t.Errorf("thread without state.json: %+v", th)
	}
	if names := Kimi.Names([]string{sessions}); len(names) != 1 || names["session_new"] != "Parser tests" {
		t.Errorf("Names: %v", names)
	}
	// The file kimiOpen gives the parser's lines unchanged after its own.
	r, _ := Kimi.OpenSession(newer)
	all, _ := io.ReadAll(r)
	r.Close()
	if !strings.HasSuffix(string(all), wire) || !strings.HasPrefix(string(all), `{"type":"hammock.kimi.state","id":"session_new"`) {
		t.Errorf("open: %s", all)
	}
}

func TestKimiProcess(t *testing.T) {
	if ByProcess("kimi") != Kimi || ByProcess("kimi-code") != Kimi || Parse("Kimi Code") != Kimi {
		t.Error("not found by its names")
	}
	if !Kimi.WorkingRE().MatchString("⠹ Working…") || Kimi.WorkingRE().MatchString("Enter to submit  ·  Esc to cancel") {
		t.Error("working")
	}
	if !Kimi.NeedsInputRE().MatchString("Run this command?") || !Kimi.NeedsInputRE().MatchString("Approve mcp__hammock__close_pane?") {
		t.Error("needs input")
	}
}
