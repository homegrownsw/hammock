package agents

import (
	"bytes"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Payloads as goose 1.58.0's HookContext serializes them
// (crates/goose/src/hooks/mod.rs).
func TestGooseHook(t *testing.T) {
	cases := []struct {
		event, payload string
		want           Hook
	}{
		{"SessionStart", `{"event":"SessionStart","session_id":"20261107_3","matcher_context":null}`, Hook{Event: "SessionStart", SessionID: "20261107_3"}},
		{"UserPromptSubmit", `{"event":"UserPromptSubmit","session_id":"20261107_3","matcher_context":"say hi","message":"say hi","working_dir":"/work"}`,
			Hook{Event: "UserPromptSubmit", SessionID: "20261107_3", Prompt: "say hi"}},
		{"PreToolUse", `{"event":"PreToolUse","session_id":"20261107_3","matcher_context":"shell","tool_call_id":"toolu_1","tool_name":"shell","tool_input":{"command":"ls"},"working_dir":"/work"}`,
			Hook{Event: "PreToolUse", SessionID: "20261107_3", ToolName: "shell", ToolInput: []byte(`{"command":"ls"}`)}},
		{"PostToolUseFailure", `{"event":"PostToolUseFailure","session_id":"20261107_3","matcher_context":"shell","tool_name":"shell","tool_input":null}`,
			Hook{Event: "PostToolUse", SessionID: "20261107_3", ToolName: "shell"}},
		{"Stop", `{"event":"Stop","session_id":"20261107_3","matcher_context":null,"last_assistant_message":"Done."}`, Hook{Event: "Stop", SessionID: "20261107_3", LastMessage: "Done."}},
		{"SessionEnd", `{"event":"SessionEnd","session_id":"20261107_3","matcher_context":null}`, Hook{Event: "SessionEnd", SessionID: "20261107_3"}},
		{"PreToolUseResult", `{"event":"PreToolUseResult","session_id":"20261107_3","decision":"allow"}`, Hook{SessionID: "20261107_3"}},
		// Another agent running Hammock's goose plugin: not goose's payload.
		{"PreToolUse", `{"hook_event_name":"PreToolUse","session_id":"x","tool_name":"Bash"}`, Hook{}},
		{"Stop", `{"session_id":"x"}`, Hook{}},
	}
	for _, c := range cases {
		if got := Goose.ReadHook(c.event, []byte(c.payload)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s:\n got %+v\nwant %+v", c.event, c.payload, got, c.want)
		}
	}
}

func TestGooseCommands(t *testing.T) {
	if got := Goose.StartCommand(""); got != "goose session" {
		t.Errorf("start %q", got)
	}
	if got := Goose.StartCommand("it's broken"); got != `goose run -s -t 'it'\''s broken'` {
		t.Errorf("start %q", got)
	}
	if got := Goose.Resume("20261107_3"); got != "goose session --resume --session-id '20261107_3'" {
		t.Errorf("resume %q", got)
	}
	for argv, want := range map[string]string{
		"goose session --resume --session-id 20261107_3":         "20261107_3",
		"goose s -r --id 20261107_4":                             "20261107_4",
		"goose session --resume --name=tally":                    "tally",
		"goose run --resume -n tally -t more":                    "tally",
		"goose session --resume --path /x/20250325_200615.jsonl": "20250325_200615",
		"goose session --name tally":                             "",
		"goose session --resume":                                 "",
		"goose run -t hi":                                        "",
	} {
		if got := Goose.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q, want %q", argv, got, want)
		}
	}
}

// gooseSchema is the part of goose's schema (session_manager.rs
// create_schema, schema version 18) Hammock reads.
const gooseSchema = `
CREATE TABLE sessions (
	id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '',
	user_set_name BOOLEAN DEFAULT FALSE, session_type TEXT NOT NULL DEFAULT 'user', working_dir TEXT NOT NULL,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	extension_data TEXT DEFAULT '{}', total_tokens INTEGER, input_tokens INTEGER, output_tokens INTEGER,
	accumulated_total_tokens INTEGER, schedule_id TEXT, recipe_json TEXT, provider_name TEXT, model_config_json TEXT,
	goose_mode TEXT NOT NULL DEFAULT 'auto', archived_at TIMESTAMP, project_id TEXT, parent_session_id TEXT);
CREATE TABLE messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT, session_id TEXT NOT NULL REFERENCES sessions(id),
	role TEXT NOT NULL, content_json TEXT NOT NULL, created_timestamp INTEGER NOT NULL,
	timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP, tokens INTEGER, metadata_json TEXT);
CREATE INDEX idx_messages_session ON messages(session_id);`

// gooseTestDB makes a goose database in a scratch GOOSE_PATH_ROOT, with
// Hammock's handles in a scratch folder, and returns a connection to write
// to it as goose would.
func gooseTestDB(t *testing.T) (*sql.DB, string) {
	root := t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	handles := filepath.Join(t.TempDir(), gooseHandleBase)
	old := gooseHandleDir
	gooseHandleDir = func() string { return handles }
	t.Cleanup(func() { gooseHandleDir = old })
	dir := filepath.Join(root, "data", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sessions.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(gooseSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if s, ok := gooseStores.Load(path); ok {
			if s := s.(*gooseStore); s.db != nil {
				s.db.Close()
			}
			gooseStores.Delete(path)
		}
	})
	return db, path
}

func gooseExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestGooseSessions(t *testing.T) {
	db, path := gooseTestDB(t)
	gooseExec(t, db, `INSERT INTO sessions (id, name, user_set_name, session_type, working_dir, created_at, updated_at, total_tokens, model_config_json)
		VALUES ('20261107_1', 'Top N flag for tally', 0, 'user', '/work', '2026-11-07 09:00:00', '2026-11-07 09:01:30', 18234, '{"model_name":"claude-sonnet-4-5","temperature":null}'),
		       ('20261107_2', 'CLI Session', 0, 'user', '/work', '2026-11-07 10:00:00', '2026-11-07 10:00:00', NULL, NULL),
		       ('20261107_3', 'subagent', 0, 'sub_agent', '/work', '2026-11-07 09:00:30', '2026-11-07 09:00:40', NULL, NULL)`)
	msg := func(sid, role, content string, ts int64, meta string) {
		gooseExec(t, db, `INSERT INTO messages (message_id, session_id, role, content_json, created_timestamp, metadata_json) VALUES ('m', ?, ?, ?, ?, ?)`, sid, role, content, ts, meta)
	}
	const shown = `{"userVisible":true,"agentVisible":true}`
	msg("20261107_1", "assistant", `[]`, 1794042000, `{"userVisible":false,"agentVisible":false}`)
	msg("20261107_1", "user", `[{"type":"text","text":"Add a --top N flag to tally.sh."}]`, 1794042001, shown)
	msg("20261107_1", "assistant", `[{"type":"text","text":"Done."},{"type":"toolRequest","id":"t1","toolCall":{"status":"success","value":{"name":"shell","arguments":{"command":"ls"}}}}]`, 1794042003, shown)
	msg("20261107_3", "user", `[{"type":"text","text":"sub"}]`, 1794042031, shown)

	// The finder: your sessions with messages in them, not subagents'.
	files := Goose.Sessions(gooseRoots())
	if len(files) != 1 || filepath.Base(files[0]) != "20261107_1" || !gooseOwnsFile(files[0]) {
		t.Fatalf("sessions %v", files)
	}
	r, err := Goose.OpenSession(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var th thread.Thread
	var recs []string
	if _, err := Goose.Index(r, &th, func(rec thread.Record) { recs = append(recs, string(rec.Kind)+" "+rec.Text+rec.NativeTool) }); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if th.NativeID != "20261107_1" || th.Cwd != "/work" || th.Title != "Top N flag for tally" || th.Named ||
		!th.Created.Equal(time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("thread %+v", th)
	}
	if want := []string{"user Add a --top N flag to tally.sh.", "assistant Done.", "tool_call shell"}; !reflect.DeepEqual(recs, want) {
		t.Errorf("records %q", recs)
	}

	// The agent view: Locate by id (or name), the newest in a folder.
	p, ok := Goose.Locate("20261107_1")
	if !ok || p != files[0] {
		t.Fatalf("locate %q %v", p, ok)
	}
	if p, ok := Goose.Locate("Top N flag for tally"); !ok || p != files[0] {
		t.Errorf("locate by name %q %v", p, ok)
	}
	if _, ok := Goose.Locate("20991231_9"); ok {
		t.Error("located a session that isn't there")
	}
	newest, ok := Goose.Newest("/work")
	if !ok || filepath.Base(newest) != "20261107_2" {
		t.Errorf("newest %q", newest)
	}
	before, _ := os.ReadFile(p)
	st, _ := os.Stat(p)
	parser := agentview.NewGoose("")
	r, _ = Goose.OpenSession(p)
	gooseFeed(parser, r)
	if n := len(parser.Blocks()); n != 3 {
		t.Fatalf("%d blocks", n)
	}
	if m, tok, _ := parser.Context(); m != "claude-sonnet-4-5" || tok != 18234 {
		t.Errorf("context %q %d", m, tok)
	}

	// Goose adds a message: the handle changes, so the view reads it again.
	time.Sleep(10 * time.Millisecond)
	msg("20261107_1", "user", `[{"type":"toolResponse","id":"t1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"tally.sh"}],"isError":false}}}]`, 1794042004, shown)
	if p2, _ := Goose.Locate("20261107_1"); p2 != p {
		t.Fatalf("moved to %q", p2)
	}
	after, _ := os.ReadFile(p)
	st2, _ := os.Stat(p)
	if bytes.Equal(before, after) || st2.ModTime().Equal(st.ModTime()) {
		t.Error("handle unchanged after a new message")
	}
	parser = agentview.NewGoose("")
	r, _ = Goose.OpenSession(p)
	gooseFeed(parser, r)
	if b := parser.Blocks()[2]; b.Pending || b.Output != "tally.sh" {
		t.Errorf("tool block %+v", b)
	}
	// Nothing new: the handle stays as it is.
	Goose.Locate("20261107_1")
	if st3, _ := os.Stat(p); !st3.ModTime().Equal(st2.ModTime()) {
		t.Error("handle rewritten with nothing new")
	}

	// A session goose deleted loses its handle; one only Locate wrote (no
	// messages yet) comes back when asked for.
	gooseExec(t, db, `DELETE FROM messages WHERE session_id = '20261107_1'`)
	gooseExec(t, db, `DELETE FROM sessions WHERE id = '20261107_1'`)
	if files := Goose.Sessions(gooseRoots()); len(files) != 0 {
		t.Errorf("sessions after delete %v", files)
	}
	if gooseExists(p) || gooseExists(newest) {
		t.Error("handles left behind")
	}
	if p, ok := Goose.Newest("/work"); !ok || p != newest || !gooseExists(p) {
		t.Errorf("newest again %q %v", p, ok)
	}

	// A handle is only trusted where Hammock keeps it for its database.
	forged := filepath.Join(filepath.Dir(newest), "20261107_9")
	os.WriteFile(forged, []byte(`{"db":"`+path+`","id":"20261107_2"}`), 0o600)
	if _, err := Goose.OpenSession(forged); err == nil {
		t.Error("opened a handle named for another session")
	}
}

// Goose's database read while goose has it open (its WAL growing), and
// after it's closed (no -wal or -shm files left).
func TestGooseReadOnly(t *testing.T) {
	db, path := gooseTestDB(t)
	gooseExec(t, db, `INSERT INTO sessions (id, name, working_dir) VALUES ('20261107_1', 'CLI Session', '/work')`)
	gooseExec(t, db, `INSERT INTO messages (session_id, role, content_json, created_timestamp) VALUES ('20261107_1', 'user', '[{"type":"text","text":"hi"}]', 1794042001)`)
	if files := Goose.Sessions(gooseRoots()); len(files) != 1 {
		t.Fatalf("sessions with goose running %v", files)
	}
	gooseStoreFor(path).db.Close()
	gooseStores.Delete(path)
	db.Close()
	for _, ext := range []string{"-wal", "-shm"} {
		if gooseExists(path + ext) {
			t.Fatalf("%s left", ext)
		}
	}
	before, _ := os.ReadFile(path)
	p, ok := Goose.Locate("20261107_1")
	if !ok {
		t.Fatal("not located with goose closed")
	}
	r, err := Goose.OpenSession(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	if !bytes.Contains(b, []byte(`"text":"hi"`)) {
		t.Errorf("lines %s", b)
	}
	// The database itself is as goose left it. (SQLite makes the -wal and
	// -shm files any reader of a WAL database needs, as goose's own
	// connections do.)
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Error("the database changed")
	}
}

func gooseFeed(p agentview.Parser, r io.ReadCloser) {
	defer r.Close()
	b, _ := io.ReadAll(r)
	for _, l := range bytes.Split(b, []byte("\n")) {
		if len(l) > 0 {
			p.Feed(l)
		}
	}
}

func TestGooseTime(t *testing.T) {
	want := time.Date(2026, 11, 7, 9, 1, 30, 0, time.UTC)
	for _, v := range []any{"2026-11-07 09:01:30", "2026-11-07T09:01:30Z", []byte("2026-11-07 09:01:30"), want} {
		if got := gooseTime(v); !got.Equal(want) {
			t.Errorf("%v: %v", v, got)
		}
	}
	if gooseMs(1794042001) != 1794042001000 || gooseMs(1794042001000) != 1794042001000 {
		t.Error("gooseMs")
	}
}
