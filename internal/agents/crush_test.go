package agents

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/thread"
)

// crushSchema is Crush's database schema at v0.98.1 (its
// internal/db/migrations, applied in order), the columns Hammock reads and
// the ones around them.
const crushSchema = `
CREATE TABLE sessions (
    id TEXT PRIMARY KEY, parent_session_id TEXT, title TEXT NOT NULL,
    message_count INTEGER NOT NULL DEFAULT 0, prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0, cost REAL NOT NULL DEFAULT 0.0,
    updated_at INTEGER NOT NULL, created_at INTEGER NOT NULL,
    summary_message_id TEXT, todos TEXT, channel TEXT);
CREATE TABLE messages (
    id TEXT PRIMARY KEY, session_id TEXT NOT NULL, role TEXT NOT NULL,
    parts TEXT NOT NULL default '[]', model TEXT,
    created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, finished_at INTEGER,
    provider TEXT, is_summary_message INTEGER DEFAULT 0 NOT NULL,
    prism_model_id TEXT, prism_model_name TEXT, prism_hypercredit_savings REAL, prism_dollar_savings REAL,
    FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE);
CREATE INDEX idx_messages_session_id ON messages (session_id);
CREATE TRIGGER update_session_message_count_on_insert AFTER INSERT ON messages
BEGIN UPDATE sessions SET message_count = message_count + 1 WHERE id = new.session_id; END;
`

const crushFixture = "../agentview/testdata/crush/session.jsonl"

// crushTestDB makes a project, its .crush/crush.db holding the fixture's
// session (in WAL mode, as Crush keeps it, open for writing as while Crush
// runs), and Crush's projects.json naming it, all under a scratch home.
func crushTestDB(t *testing.T) (project, dataDir string, w *sql.DB) {
	t.Helper()
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CRUSH_GLOBAL_DATA", "")
	project = filepath.Join(h, "app")
	dataDir = filepath.Join(project, ".crush")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	w, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "crush.db")+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	t.Cleanup(func() { w.Close() })
	if _, err := w.Exec(crushSchema); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(crushFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		var s CrushMeta
		var m CrushMessage
		if err := json.Unmarshal(line, &s); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(line, &m)
		if s.Type == "hammock_session" {
			_, err = w.Exec(`INSERT INTO sessions (id, title, prompt_tokens, completion_tokens, updated_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				s.ID, s.Title, s.PromptTokens, s.CompletionTokens, s.Updated, s.Created)
		} else {
			var fin any
			if m.Finished != 0 {
				fin = m.Finished
			}
			_, err = w.Exec(`INSERT INTO messages (id, session_id, role, parts, model, provider, created_at, updated_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				m.ID, "6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20", m.Role, string(m.Parts), nullIfEmpty(m.Model), nullIfEmpty(m.Provider),
				m.Created, m.Updated, fin)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	// An older session, a subagent's and a title's (not listed).
	for _, q := range []string{
		`INSERT INTO sessions (id, title, updated_at, created_at) VALUES ('0a0a0a0a-0000-4000-8000-000000000001', 'Older', 1762400000, 1762400000)`,
		`INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES ('o1', '0a0a0a0a-0000-4000-8000-000000000001', 'user', '[{"type":"text","data":{"text":"old one"}}]', 1762400000, 1762400000)`,
		`INSERT INTO sessions (id, parent_session_id, title, updated_at, created_at) VALUES ('m2$$call_1', '6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20', 'Task', 1762600000, 1762600000)`,
		`INSERT INTO sessions (id, parent_session_id, title, updated_at, created_at) VALUES ('title-6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20', '6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20', 'Generate a title', 1762600000, 1762600000)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	pj := filepath.Join(h, ".local", "share", "crush", "projects.json")
	_ = os.MkdirAll(filepath.Dir(pj), 0o700)
	list, _ := json.Marshal(map[string]any{"projects": []map[string]any{{"path": project, "data_dir": dataDir, "last_accessed": time.Now()}}})
	if err := os.WriteFile(pj, list, 0o600); err != nil {
		t.Fatal(err)
	}
	return project, dataDir, w
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestCrushSessions(t *testing.T) {
	project, dataDir, w := crushTestDB(t)
	const id = "6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20"
	path := filepath.Join(dataDir, "crush.db", id)

	if got := Crush.Roots(); !reflect.DeepEqual(got, []string{dataDir}) {
		t.Errorf("roots = %v", got)
	}
	if got := Crush.Sessions(Crush.Roots()); !reflect.DeepEqual(got, []string{path, filepath.Join(dataDir, "crush.db", "0a0a0a0a-0000-4000-8000-000000000001")}) {
		t.Errorf("sessions = %v", got)
	}
	if p, ok := Crush.Locate(id); !ok || p != path {
		t.Errorf("locate = %q, %v", p, ok)
	}
	if _, ok := Crush.Locate("title-" + id); ok {
		t.Error("located a title session")
	}
	if p, ok := Crush.Newest(filepath.Join(project)); !ok || p != path {
		t.Errorf("newest = %q, %v", p, ok)
	}
	if !Crush.OwnsFile(path) || Crush.OwnsFile(filepath.Join(dataDir, "crush.db")) || Crush.OwnsFile(filepath.Join(dataDir, "x.jsonl")) {
		t.Error("OwnsFile")
	}

	// Open gives the fixture back: the session, then its messages.
	r, err := Crush.OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	want, _ := os.ReadFile(crushFixture)
	gl, wl := bytes.Split(bytes.TrimSpace(got), []byte("\n")), bytes.Split(bytes.TrimSpace(want), []byte("\n"))
	if len(gl) != len(wl) {
		t.Fatalf("%d lines, want %d:\n%s", len(gl), len(wl), got)
	}
	for i := range wl {
		var g, w any
		_ = json.Unmarshal(gl[i], &g)
		_ = json.Unmarshal(wl[i], &w)
		if i == 0 {
			w.(map[string]any)["cwd"] = project // the folder projects.json names
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("line %d:\n got %s\nwant %s", i, gl[i], wl[i])
		}
	}

	// Stat: changes as a message streams, from the -wal file while Crush
	// has the database open.
	st, err := Crush.StatSession(path)
	if err != nil || !st.Mode().IsRegular() || st.ModTime().Unix() != 1762500060 {
		t.Fatalf("stat = %v, %v", st, err)
	}
	if _, err := Crush.StatSession(filepath.Join(dataDir, "crush.db", "nope")); !os.IsNotExist(err) {
		t.Errorf("stat of a missing session: %v", err)
	}
	if _, err := w.Exec(`UPDATE messages SET parts = ? WHERE id = 'm8'`, `[{"type":"text","data":{"text":"Fixed: parse was off by one, and more."}}]`); err != nil {
		t.Fatal(err)
	}
	st2, err := Crush.StatSession(path)
	if err != nil || st2.Size() == st.Size() {
		t.Errorf("stat after an update = %v (was %v), %v", st2.Size(), st.Size(), err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "crush.db-wal")); err != nil {
		t.Errorf("no -wal file: %v", err)
	}
}

// Read when nothing has the database open (no -wal file): immutable, so
// no files are made beside it.
func TestCrushClosedDB(t *testing.T) {
	_, dataDir, w := crushTestDB(t)
	w.Close()
	if _, err := os.Stat(filepath.Join(dataDir, "crush.db-wal")); err == nil {
		t.Skip("the -wal file outlived its last connection")
	}
	path := filepath.Join(dataDir, "crush.db", "6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20")
	if _, err := Crush.StatSession(path); err != nil {
		t.Fatal(err)
	}
	r, err := Crush.OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	ents, _ := os.ReadDir(dataDir)
	for _, e := range ents {
		if e.Name() != "crush.db" {
			t.Errorf("reading it made %s", e.Name())
		}
	}
}

func TestCrushIndex(t *testing.T) {
	_, dataDir, _ := crushTestDB(t)
	r, err := Crush.OpenSession(filepath.Join(dataDir, "crush.db", "6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var th thread.Thread
	var recs []string
	if _, err := Crush.Index(r, &th, func(rec thread.Record) {
		if rec.Kind == thread.KindUser || rec.Kind == thread.KindAssistant {
			recs = append(recs, string(rec.Kind)+": "+rec.Text)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if th.NativeID != "6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20" || th.Cwd != filepath.Dir(dataDir) || th.Title != "Fix the failing test" || th.Created.Unix() != 1762500000 {
		t.Errorf("thread = %+v", th)
	}
	want := []string{
		"user: The test in parse_test.go fails, fix it",
		"assistant: I'll run the tests.",
		"assistant: Fixed: `parse` was off by one.",
		"user: Now run them all",
	}
	if !reflect.DeepEqual(recs, want) {
		t.Errorf("records:\n%s", strings.Join(recs, "\n"))
	}
}

func TestCrushDataDir(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("CRUSH_GLOBAL_DATA", "")
	mk := func(p ...string) string {
		d := filepath.Join(append([]string{h}, p...)...)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		return d
	}
	repo := mk("repo")
	mk("repo", ".git")
	mk("repo", ".crush")
	sub := mk("repo", "a", "b")
	plain := mk("plain", "x")
	mk("plain", ".crush")
	mk(".crush") // in the home folder: never used
	for _, c := range []struct{ cwd, want string }{
		{sub, filepath.Join(repo, ".crush")},               // up to the git top
		{plain, filepath.Join(plain, ".crush")},            // no git: only its own
		{mk("other"), filepath.Join(h, "other", ".crush")}, // not ~/.crush
	} {
		if got := CrushDataDir(c.cwd); got != c.want {
			t.Errorf("CrushDataDir(%s) = %s, want %s", c.cwd, got, c.want)
		}
	}
}

func TestCrushCommands(t *testing.T) {
	if got := Crush.StartCommand("fix it"); got != "crush" {
		t.Errorf("start = %q", got)
	}
	if got := Crush.Resume("6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20"); got != "crush --session '6f1c2a9e-4b7d-4c1e-9a52-0d3e8b7f1a20'" {
		t.Errorf("resume = %q", got)
	}
	for argv, want := range map[string]string{
		"crush --session abc":   "abc",
		"crush -s abc":          "abc",
		"crush --session=abc":   "abc",
		"crush -C":              "",
		"crush --session ../x":  "",
		"crush --yolo -s 1234f": "1234f",
	} {
		if got := Crush.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("ResumeID(%s) = %q, want %q", argv, got, want)
		}
	}
	if ByProcess("crush") != Crush {
		t.Error("not found by its process")
	}
	for screen, want := range map[string]bool{
		" esc cancel • tab focus editor • shift+tab mode • / or ctrl+p commands": true,
		"esc press again to cancel • tab focus chat":                             true,
		"↑↓ choose • enter confirm • esc cancel":                                 false,
		"tab focus editor • shift+tab mode • / or ctrl+p commands • ctrl+c quit": false,
	} {
		if got := Crush.WorkingRE().MatchString(screen); got != want {
			t.Errorf("working %q = %v", screen, got)
		}
	}
	if !Crush.NeedsInputRE().MatchString("Permission Required ////\n\nTool bash\n Allow   Allow for Session   Deny") {
		t.Error("its permission dialog isn't asking")
	}
}
