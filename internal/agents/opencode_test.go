package agents

import (
	"bufio"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjsp/hammock/internal/agentview"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/sessions"
	"github.com/alexjsp/hammock/internal/thread"
)

// openCodeFixture builds a database from one of agentview's SQL fixtures at
// dir/name, in WAL mode as OpenCode keeps it, and returns a connection
// that can write to it (OpenCode's own, as it were).
func openCodeFixture(t *testing.T, fixture, path string) *sql.DB {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "agentview", "testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(string(src)); err != nil {
		t.Fatal(err)
	}
	return db
}

// openCodeBlocks reads a session as the agent view does.
func openCodeBlocks(t *testing.T, a *Agent, path string) *agentview.OpenCode {
	t.Helper()
	r, err := a.OpenSession(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := a.NewParser("", false).(*agentview.OpenCode)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		p.Feed(sc.Bytes())
	}
	return p
}

func openCodeSummary(blocks []*pb.AgentBlock) string {
	var b strings.Builder
	for _, bl := range blocks {
		line := strings.TrimPrefix(bl.Kind.String(), "KIND_") + " " + openCodeFirstLine(bl.Text+bl.Title)
		if bl.Pending {
			line += " (pending)"
		}
		if bl.Error {
			line += " (error)"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func openCodeFirstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

const openCodeMain = "ses_7f3a9c2e1d4bAbCdEfGhIjKl"

func TestOpenCodeV2Session(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("OPENCODE_DB", "")
	dbPath := filepath.Join(data, "opencode", "opencode.db")
	w := openCodeFixture(t, "opencode/v2.sql", dbPath)

	roots := OpenCode.Roots()
	got := OpenCode.Sessions(roots)
	if len(got) != 2 {
		t.Fatalf("sessions = %v, want the two top-level ones", got)
	}
	for _, p := range got {
		if strings.Contains(p, "Subagent") || strings.Contains(p, "ses_0ld1x") {
			t.Errorf("listed %s: a subagent's, or a 1.x one V2 has imported", p)
		}
	}
	path, ok := OpenCode.Locate(openCodeMain)
	if !ok || path != filepath.Join(dbPath, openCodeMain) {
		t.Fatalf("Locate = %q %v", path, ok)
	}
	if !OpenCode.OwnsFile(path) || OpenCode.OwnsFile(filepath.Join(data, "opencode", "other.db", openCodeMain)) || Kilo.OwnsFile(path) {
		t.Error("OwnsFile")
	}
	if p, ok := OpenCode.Newest("/Users/me/proj"); !ok || p != path {
		t.Errorf("Newest = %q %v", p, ok)
	}
	if _, ok := OpenCode.Locate("ses_missing"); ok {
		t.Error("located a session that isn't there")
	}

	p := openCodeBlocks(t, OpenCode, path)
	want := `USER The login check in /Users/me/proj/auth.go is inverted. Fix it?
THINKING The condition uses != where it means ==.
TOOL Read auth.go
TOOL Edited auth.go
TOOL Ran ` + "`go vet ./...`" + ` (error)
TOOL Edited auth.go
TEXT Fixed: ` + "`ok`" + ` now compares with ` + "`==`." + `
TOOL Ran ` + "`git status --short`" + `
USER Now run the tests
TEXT Running them now.
TOOL Run the tests (pending)
NOTE Interrupted
`
	if s := openCodeSummary(p.Blocks()); s != want {
		t.Errorf("blocks:\n%s\nwant:\n%s", s, want)
	}
	blocks := p.Blocks()
	if b := blocks[3]; b.Diff == "" || b.Added != 1 || b.Removed != 1 {
		t.Errorf("edit = %+v, want its diff", b)
	}
	if b := blocks[4]; !strings.Contains(b.Output, "unreachable code") {
		t.Errorf("failed shell's output = %q", b.Output)
	}
	if b := blocks[5]; !strings.Contains(b.Diff, "+// ok compares") || b.Input != "auth.go" {
		t.Errorf("patch = %+v", b)
	}
	if b := blocks[7]; b.Output != "M auth.go" && b.Output != " M auth.go" {
		t.Errorf("shell output = %q", b.Output)
	}
	if pend := p.Pending(); pend == nil || pend.Input != "go test ./..." {
		t.Errorf("Pending = %v", pend)
	}
	if !p.Referenced("/Users/me/proj/auth.go") {
		t.Error("auth.go not referenced")
	}
	if m, tok, _ := p.Context(); m != "claude-sonnet-4-5" || tok != 1500+20+6200 {
		t.Errorf("Context = %q %d", m, tok)
	}

	// The session finder's reading.
	r, _ := OpenCode.OpenSession(path)
	var th thread.Thread
	var recs []thread.Record
	if _, err := OpenCode.Index(r, &th, func(rec thread.Record) { recs = append(recs, rec) }); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if th.NativeID != openCodeMain || th.Cwd != "/Users/me/proj" || th.Title != "Fix the login check" || th.Created.UnixMilli() != 1762500000000 {
		t.Errorf("thread = %+v", th)
	}
	if len(recs) != 4 || recs[0].Kind != thread.KindUser || recs[1].Kind != thread.KindAssistant || recs[1].Text != "Fixed: `ok` now compares with `==`." {
		t.Errorf("records = %+v", recs)
	}

	// Its "file" changes as the session does.
	st1, err := OpenCode.StatSession(path)
	if err != nil || !st1.Mode().IsRegular() {
		t.Fatalf("Stat = %v %v", st1, err)
	}
	if _, err := w.Exec(`INSERT INTO session_part VALUES ('msg_06', ?, 2, 'text', NULL, 1762500060000, 1762500060000, '{"type":"text","text":"All pass."}')`, openCodeMain); err != nil {
		t.Fatal(err)
	}
	st2, _ := OpenCode.StatSession(path)
	if st2.Size() == st1.Size() && st2.ModTime().Equal(st1.ModTime()) {
		t.Error("Stat didn't change with the session")
	}
	if !strings.Contains(openCodeSummary(openCodeBlocks(t, OpenCode, path).Blocks()), "TEXT All pass.") {
		t.Error("new part not read")
	}
	if _, err := OpenCode.StatSession(filepath.Join(dbPath, "ses_missing")); err == nil {
		t.Error("Stat of a missing session")
	}
}

func TestKiloV1Session(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("KILO_DB", "")
	dbPath := filepath.Join(data, "kilo", "kilo.db")
	openCodeFixture(t, "kilo/v1.sql", dbPath).Close()
	// Closed, its -wal and -shm are gone: read only still reads it.
	if _, err := os.Stat(dbPath + "-shm"); err == nil {
		t.Fatal("-shm still there")
	}
	openCodeConns.Lock()
	delete(openCodeConns.m, dbPath)
	openCodeConns.Unlock()

	const id = "ses_5a1b2c3d4e5fKiloSession1"
	path, ok := Kilo.Locate(id)
	if !ok {
		t.Fatal("Locate")
	}
	if got := Kilo.Sessions(Kilo.Roots()); len(got) != 1 || got[0] != path {
		t.Errorf("sessions = %v", got)
	}
	p := openCodeBlocks(t, Kilo, path)
	want := `USER Add a footer to index.html
THINKING Look at the page first.
TOOL List the site's files
TOOL Wrote footer.html
QUESTION Which year should the footer show?
TEXT Added footer.html.
USER Now style it
TOOL Edited a file (pending)
NOTE Interrupted
`
	if s := openCodeSummary(p.Blocks()); s != want {
		t.Errorf("blocks:\n%s\nwant:\n%s", s, want)
	}
	blocks := p.Blocks()
	if b := blocks[2]; b.Input != "ls" || !strings.HasPrefix(b.Output, "index.html") {
		t.Errorf("bash = %+v", b)
	}
	if b := blocks[3]; b.Added != 3 || !strings.Contains(b.Diff, "+<footer>") {
		t.Errorf("write = %+v", b)
	}
	if q := blocks[4].Questions; len(q) != 1 || q[0].Header != "Year" || len(q[0].Options) != 2 || q[0].Options[1].Label != "Auto" {
		t.Errorf("questions = %v", q)
	}
	if m, tok, _ := p.Context(); m != "anthropic/claude-sonnet-4.5" || tok != 900+120+3000+100 {
		t.Errorf("Context = %q %d", m, tok)
	}

	r, _ := Kilo.OpenSession(path)
	var th thread.Thread
	n := 0
	if _, err := Kilo.Index(r, &th, func(thread.Record) { n++ }); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if th.Title != "" || th.Cwd != "/Users/me/site" || n != 3 {
		t.Errorf("thread = %+v, %d records (a placeholder title isn't one)", th, n)
	}
}

func TestOpenCodeCommandLines(t *testing.T) {
	if got := OpenCode.StartCommand("fix it's bug"); got != `opencode --prompt 'fix it'\''s bug'` {
		t.Errorf("Start = %s", got)
	}
	if got := Kilo.StartCommand(""); got != "kilo" {
		t.Errorf("Start = %s", got)
	}
	if got := OpenCode.Resume(openCodeMain); got != "opencode --session '"+openCodeMain+"'" {
		t.Errorf("Resume = %s", got)
	}
	for argv, want := range map[string]string{
		"opencode --session " + openCodeMain:  openCodeMain,
		"kilo -s " + openCodeMain + " --fork": openCodeMain,
		"opencode --session=" + openCodeMain:  openCodeMain,
		"opencode -c":                         "",
		"opencode --session ../../etc/x":      "",
		"node /x/bin/opencode.cjs -s nope_":   "",
	} {
		if got := openCodeResumeID(strings.Fields(argv)); got != want {
			t.Errorf("ResumeID(%s) = %q, want %q", argv, got, want)
		}
	}
	for _, name := range []string{"opencode", "opencode.cjs", ".opencode"} {
		if ByProcess(name) != OpenCode {
			t.Errorf("ByProcess(%s)", name)
		}
	}
	for _, name := range []string{"kilo", "kilocode", ".kilo"} {
		if ByProcess(name) != Kilo {
			t.Errorf("ByProcess(%s)", name)
		}
	}
	if !OpenCode.WorkingRE().MatchString("  esc interrupt") || !Kilo.WorkingRE().MatchString("esc again to interrupt") {
		t.Error("working screen")
	}
	if !OpenCode.NeedsInputRE().MatchString("△ Permission required") {
		t.Error("permission screen")
	}
}

// What Hammock's plugins send (cmd/hmk/hooks_opencode.go) reads as Claude's
// hooks do.
func TestOpenCodePluginPayloads(t *testing.T) {
	h := OpenCode.ReadHook("PreToolUse", []byte(`{"session_id":"`+openCodeMain+`","tool_name":"edit","tool_input":{"path":"a.go","file_path":"a.go"}}`))
	if h.Event != "PreToolUse" || h.SessionID != openCodeMain || h.ToolName != "edit" || !strings.Contains(string(h.ToolInput), `"file_path":"a.go"`) {
		t.Errorf("PreToolUse = %+v", h)
	}
	h = Kilo.ReadHook("Stop", []byte(`{"session_id":"`+openCodeMain+`","last_assistant_message":"Done."}`))
	if h.Event != "Stop" || h.LastMessage != "Done." {
		t.Errorf("Stop = %+v", h)
	}
	h = OpenCode.ReadHook("StopFailure", []byte(`{"session_id":"`+openCodeMain+`","error":"rate_limit","error_details":"Too many requests"}`))
	if h.Error != "rate_limit" || h.ErrorDetails != "Too many requests" {
		t.Errorf("StopFailure = %+v", h)
	}
	if h = OpenCode.ReadHook("SessionStart", []byte(`{}`)); h.Event != "SessionStart" || h.SessionID != "" {
		t.Errorf("SessionStart = %+v", h)
	}
}

// The session finder lists, reads and searches sessions kept in a
// database, through their Stat and Open.
func TestOpenCodeSessionFinder(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("OPENCODE_DB", "")
	w := openCodeFixture(t, "opencode/v2.sql", filepath.Join(data, "opencode", "opencode.db"))
	a := OpenCode
	x, err := sessions.Open(filepath.Join(t.TempDir(), "s.db"), sessions.Sources{{
		Harness: a.ID, Read: a.Index, Open: a.Open, Stat: a.Stat,
		Files: func() []string { return a.Sessions(a.Roots()) },
	}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := x.Refresh(); err != nil || n != 2 {
		t.Fatalf("Refresh = %d %v", n, err)
	}
	r, err := x.Search("inverted", 5)
	if err != nil || len(r) != 1 || r[0].ID != openCodeMain || r[0].Title != "Fix the login check" || r[0].Cwd != "/Users/me/proj" {
		t.Fatalf("Search = %+v %v", r, err)
	}
	if n, _ := x.Refresh(); n != 0 {
		t.Errorf("unchanged sessions read again: %d", n)
	}
	if _, err := w.Exec(`INSERT INTO session_message VALUES ('msg_10', ?, 'user', 8, 1762500200000, 1762500200000, '{"time":{"created":1762500200000},"text":"Also add a regression test","files":[],"agents":[],"skills":[]}')`, openCodeMain); err != nil {
		t.Fatal(err)
	}
	if n, _ := x.Refresh(); n != 1 {
		t.Errorf("changed session not read again: %d", n)
	}
	if r, _ := x.Search("regression", 5); len(r) != 1 {
		t.Errorf("new message not found: %+v", r)
	}
}
