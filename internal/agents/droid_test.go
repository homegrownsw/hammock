package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/thread"
)

func TestDroidCommands(t *testing.T) {
	if ByProcess("droid") != Droid || Parse("Factory Droid") != Droid {
		t.Fatal("not found by process or name")
	}
	id := "6f1c2a9e-4b7d-4c1e-9a3f-2d8b5e7c1a40"
	for argv, want := range map[string]string{
		"droid --resume " + id:   id,
		"droid -r " + id:         id,
		"droid --resume=" + id:   id,
		"droid --resume":         "", // the last session: unknown until its hooks say
		"droid exec -r high fix": "",
		"droid fix the tests":    "",
	} {
		if got := Droid.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q, want %q", argv, got, want)
		}
	}
	if got := Droid.Resume(id); got != "droid --resume '"+id+"'" {
		t.Errorf("resume: %s", got)
	}
	if got := Droid.StartCommand("fix it's tests"); got != `droid 'fix it'\''s tests'` {
		t.Errorf("start: %s", got)
	}
}

// Hook payloads as droid 1.6.0 sends them (its hooks reference: session_id,
// transcript_path, cwd, permission_mode, hook_event_name, and each event's
// own fields).
func TestDroidHooks(t *testing.T) {
	h := Droid.ReadHook("", []byte(`{"session_id":"s1","transcript_path":"/Users/me/.factory/sessions/-w/s1.jsonl","cwd":"/w","permission_mode":"off","hook_event_name":"Notification","message":"Droid needs your permission to run Execute","notification_type":"permission_prompt"}`))
	if h.Event != "Notification" || h.NotificationType != "permission_prompt" || h.SessionID != "s1" || !strings.HasSuffix(h.TranscriptPath, "s1.jsonl") {
		t.Errorf("notification: %+v", h)
	}
	h = Droid.ReadHook("", []byte(`{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"AskUser","tool_input":{"questionnaire":"1. Which?"}}`))
	if h.Event != "PermissionRequest" || h.ToolName != "" || h.SessionID != "s1" {
		t.Errorf("AskUser: %+v", h)
	}
	h = Droid.ReadHook("", []byte(`{"session_id":"s1","hook_event_name":"Notification","notification_type":"elicitation_dialog","message":"Linear needs input"}`))
	if h.Event != "PermissionRequest" || h.Message != "Linear needs input" {
		t.Errorf("elicitation: %+v", h)
	}
	h = Droid.ReadHook("", []byte(`{"session_id":"s1","hook_event_name":"PreToolUse","tool_name":"Execute","tool_input":{"command":"ls"}}`))
	if h.Event != "PreToolUse" || h.ToolName != "Execute" || string(h.ToolInput) != `{"command":"ls"}` {
		t.Errorf("tool: %+v", h)
	}
}

func TestDroidSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FACTORY_HOME_OVERRIDE", "")
	work := filepath.Join(home, "src", "my.app")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(work) // /var is /private/var on macOS
	proj := droidProject(work)
	if want := "-" + strings.ReplaceAll(strings.TrimPrefix(real, "/"), "/", "-"); proj != want || !strings.HasSuffix(proj, "-src-my.app") {
		t.Fatalf("project folder %q, want %q", proj, want)
	}
	dir := filepath.Join(home, ".factory", "sessions", proj)
	if err := os.MkdirAll(filepath.Join(home, ".factory", "sessions", "btw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../agentview/testdata/droid/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "6f1c2a9e-4b7d-4c1e-9a3f-2d8b5e7c1a40.jsonl")
	sub := filepath.Join(dir, "0a6c7c02-9d58-4bb2-8f6d-f0b6a1f3e2c1.jsonl")
	side := filepath.Join(home, ".factory", "sessions", "btw", "1b2c3d4e-0000-4000-8000-000000000000.jsonl")
	for p, data := range map[string]string{
		mine: string(fixture),
		sub:  `{"type":"session_start","id":"0a6c7c02-9d58-4bb2-8f6d-f0b6a1f3e2c1","title":"Run the tests","version":2,"cwd":"/work/tally","callingSessionId":"6f1c2a9e-4b7d-4c1e-9a3f-2d8b5e7c1a40","callingToolUseId":"toolu_8"}` + "\n",
		side: `{"type":"session_start","id":"1b2c3d4e-0000-4000-8000-000000000000","title":"btw","version":2,"cwd":"/w"}` + "\n",
		filepath.Join(dir, "6f1c2a9e-4b7d-4c1e-9a3f-2d8b5e7c1a40.settings.json"): `{"tags":[]}`,
	} {
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The subagent's session is newer, but isn't the one you're in.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(mine, old, old)
	if p, ok := Droid.Newest(work); !ok || p != mine {
		t.Errorf("newest: %s %v", p, ok)
	}
	if p, ok := Droid.Locate("6f1c2a9e-4b7d-4c1e-9a3f-2d8b5e7c1a40"); !ok || p != mine {
		t.Errorf("locate: %s %v", p, ok)
	}
	if _, ok := Droid.Locate("../x"); ok {
		t.Error("located a path")
	}
	files := Droid.Sessions(Droid.Roots())
	if len(files) != 2 || !Droid.OwnsFile(mine) || Droid.OwnsFile(strings.TrimSuffix(mine, ".jsonl")+".settings.json") {
		t.Errorf("sessions: %v", files)
	}

	var th thread.Thread
	var recs []thread.Record
	f, _ := os.Open(mine)
	defer f.Close()
	if _, err := Droid.Index(f, &th, func(r thread.Record) { recs = append(recs, r) }); err != nil {
		t.Fatal(err)
	}
	if th.NativeID != "6f1c2a9e-4b7d-4c1e-9a3f-2d8b5e7c1a40" || th.Cwd != "/work/tally" || th.Title != "Add a --top flag to tally.sh" || th.Created.IsZero() {
		t.Errorf("thread: %+v", th)
	}
	var got []string
	for _, r := range recs {
		got = append(got, string(r.Kind)+": "+r.Text)
	}
	want := []string{
		"user: Add a --top N flag to tally.sh.",
		"assistant: Let me look at the script.",
		"assistant: Added --top N: it prints the N biggest files.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("records:\n%s", strings.Join(got, "\n"))
	}
	// Droid's stand-in title isn't one.
	th = thread.Thread{}
	_, _ = Droid.Index(strings.NewReader(`{"type":"session_start","id":"x","title":"New Session","version":2,"cwd":"/w"}`+"\n"), &th, func(thread.Record) {})
	if th.Title != "" || th.NativeID != "x" {
		t.Errorf("stand-in title: %+v", th)
	}
	// A subagent's session isn't listed.
	recs = nil
	g, _ := os.Open(sub)
	defer g.Close()
	_, _ = Droid.Index(g, &thread.Thread{}, func(r thread.Record) { recs = append(recs, r) })
	if len(recs) != 0 {
		t.Errorf("subagent records: %v", recs)
	}

	// $FACTORY_HOME_OVERRIDE moves the home folder Droid's is in.
	t.Setenv("FACTORY_HOME_OVERRIDE", filepath.Join(home, "alt"))
	if r := Droid.Roots(); len(r) != 2 || r[1] != filepath.Join(home, "alt", ".factory", "sessions") {
		t.Errorf("roots: %v", r)
	}
}

func TestDroidSlashCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FACTORY_HOME_OVERRIDE", "")
	cwd := t.TempDir()
	for _, p := range []string{
		filepath.Join(home, ".factory", "commands", "Code Review.md"),
		filepath.Join(cwd, ".factory", "commands", "ops", "deploy.md"),
		filepath.Join(home, ".factory", "skills", "triage", "SKILL.md"),
	} {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("---\ndescription: x\n---\nDo it.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(Droid.SlashCommands(cwd), " ")
	for _, c := range []string{"/code-review", "/deploy", "/triage", "/compress", "/rename"} {
		if !strings.Contains(" "+got+" ", " "+c+" ") {
			t.Errorf("no %s in %s", c, got)
		}
	}
}
