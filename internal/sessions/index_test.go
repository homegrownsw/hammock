package sessions

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/agents"
	"github.com/alexjsp/hammock/internal/thread"
	"github.com/klauspost/compress/zstd"
)

func ccLine(typ, text string) string {
	switch typ {
	case "user", "assistant":
		return fmt.Sprintf(`{"type":%q,"sessionId":"s1","cwd":"/work/app","timestamp":"2026-11-01T08:00:00Z","message":{"role":%q,"content":%q}}`+"\n", typ, typ, text)
	case "ai-title":
		return fmt.Sprintf(`{"type":"ai-title","aiTitle":%q,"sessionId":"s1"}`+"\n", text)
	case "custom-title":
		return fmt.Sprintf(`{"type":"custom-title","customTitle":%q,"sessionId":"s1"}`+"\n", text)
	}
	panic(typ)
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(s)
	f.Close()
}

func setup(t *testing.T) (*Index, string, string) {
	claude, codex := t.TempDir(), t.TempDir()
	x, err := Open(filepath.Join(t.TempDir(), "s.db"), claudeAndCodex(claude, codex))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { x.Close() })
	return x, claude, codex
}

func TestClaudeSessionsIndexAsTheyGrow(t *testing.T) {
	x, claude, _ := setup(t)
	dir := filepath.Join(claude, "projects", "-work-app")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "s1.jsonl")
	appendTo(t, path, ccLine("user", "<command-name>/clear</command-name>")+
		ccLine("user", "Fix the flaky login test")+
		ccLine("assistant", "The retry loop races the token refresh.")+
		ccLine("ai-title", "Flaky login test"))
	if n, err := x.Refresh(); err != nil || n != 1 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	s, err := x.Get("claude", "s1")
	if err != nil || s.Title != "Flaky login test" || s.FirstPrompt != "Fix the flaky login test" || s.Messages != 2 || s.Cwd != "/work/app" {
		t.Fatalf("%+v %v", s, err)
	}

	// It grows: a /rename, more said, and a later generated title, which
	// doesn't replace the name.
	appendTo(t, path, ccLine("custom-title", "Login fix")+ccLine("user", "Now add a regression test for zeppelin tokens")+ccLine("ai-title", "Token refresh race"))
	if n, _ := x.Refresh(); n != 1 {
		t.Fatalf("grown session not re-read: %d", n)
	}
	s, _ = x.Get("claude", "s1")
	if s.Title != "Login fix" || s.LastPrompt != "Now add a regression test for zeppelin tokens" || s.Messages != 3 || s.FirstPrompt != "Fix the flaky login test" {
		t.Fatalf("after growing: %+v", s)
	}
	// Both the first part and what was added are searchable, by prefix.
	for _, q := range []string{"retry loop", "zepp", "LOGIN fix", "work app"} {
		if r, err := x.Search(q, 5); err != nil || len(r) != 1 {
			t.Errorf("search %q: %d %v", q, len(r), err)
		}
	}
	if r, _ := x.Search("retry", 5); len(r) == 1 && !strings.Contains(r[0].Snippet, "«retry»") {
		t.Errorf("snippet %q", r[0].Snippet)
	}
	if r, _ := x.Search("nothing-like-this", 5); len(r) != 0 {
		t.Errorf("matched nothing-like-this: %v", r)
	}
	// Nothing changed: nothing read.
	if n, _ := x.Refresh(); n != 0 {
		t.Fatalf("unchanged refresh read %d", n)
	}
	// Gone from disk: gone from the index.
	os.Remove(path)
	x.Refresh()
	if r, _ := x.Search("", 5); len(r) != 0 {
		t.Fatalf("deleted session still listed: %v", r)
	}
}

func TestOnlyCommandsIsntListed(t *testing.T) {
	x, claude, _ := setup(t)
	dir := filepath.Join(claude, "projects", "-work-app")
	os.MkdirAll(dir, 0o755)
	appendTo(t, filepath.Join(dir, "s1.jsonl"), ccLine("user", "<command-name>/model</command-name>")+ccLine("user", "<local-command-stdout>Set model</local-command-stdout>"))
	x.Refresh()
	if r, _ := x.Search("", 5); len(r) != 0 {
		t.Fatalf("listed: %+v", r)
	}
}

func TestCodexSessions(t *testing.T) {
	x, _, codex := setup(t)
	dir := filepath.Join(codex, "sessions", "2026", "11", "01")
	os.MkdirAll(dir, 0o755)
	id := "01a19623-5ee1-73d5-95d1-a3f769975038"
	rollout := `{"timestamp":"2026-11-01T08:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"/work/tally"}}
{"timestamp":"2026-11-01T08:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work/tally\n<INSTRUCTIONS>…</INSTRUCTIONS>"}]}}
{"timestamp":"2026-11-01T08:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"add a --top flag to tally.sh"}]}}
{"timestamp":"2026-11-01T08:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Added it."}]}}
`
	f, _ := os.Create(filepath.Join(dir, "rollout-2026-11-01T08-00-00-"+id+".jsonl.zst"))
	w, _ := zstd.NewWriter(f)
	w.Write([]byte(rollout))
	w.Close()
	f.Close()
	appendTo(t, filepath.Join(codex, "session_index.jsonl"), `{"id":"`+id+`","thread_name":"Top flag"}`+"\n")
	x.Refresh()
	s, err := x.Get("codex", id)
	if err != nil || s.Title != "Top flag" || s.FirstPrompt != "add a --top flag to tally.sh" || s.Messages != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	// Renamed in Codex: picked up without the session changing.
	appendTo(t, filepath.Join(codex, "session_index.jsonl"), `{"id":"`+id+`","thread_name":"Tally --top"}`+"\n")
	x.Refresh()
	if s, _ := x.Get("codex", id); s.Title != "Tally --top" {
		t.Fatalf("rename: %q", s.Title)
	}
	if r, _ := x.Search("tally top", 5); len(r) != 1 {
		t.Fatalf("search: %v", r)
	}
}

func TestSearchInAFolder(t *testing.T) {
	x, claude, _ := setup(t)
	line := func(sid, cwd, typ, text string) string {
		return fmt.Sprintf(`{"type":%q,"sessionId":%q,"cwd":%q,"timestamp":"2026-11-01T08:00:00Z","message":{"role":%q,"content":%q}}`+"\n", typ, sid, cwd, typ, text)
	}
	for _, s := range []struct{ sid, cwd string }{{"a", "/work/app"}, {"b", "/work/app/web"}, {"c", "/work/application"}, {"d", "/elsewhere"}} {
		dir := filepath.Join(claude, "projects", strings.ReplaceAll(s.cwd, "/", "-"))
		os.MkdirAll(dir, 0o755)
		appendTo(t, filepath.Join(dir, s.sid+".jsonl"), line(s.sid, s.cwd, "user", "decide the cache policy")+line(s.sid, s.cwd, "assistant", "Two layers."))
	}
	if _, err := x.Refresh(); err != nil {
		t.Fatal(err)
	}
	ids := func(ss []Session) string {
		var out []string
		for _, s := range ss {
			out = append(out, s.ID)
		}
		slices.Sort(out)
		return strings.Join(out, ",")
	}
	for _, q := range []string{"", "cache policy"} {
		in, err := x.SearchIn(q, "/work/app/", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Inside it (b) counts; a sibling sharing its prefix (c) doesn't.
		if got := ids(in); got != "a,b" {
			t.Errorf("%q in /work/app: %s, want a,b", q, got)
		}
		all, _ := x.SearchIn(q, "", nil, 0)
		if got := ids(all); got != "a,b,c,d" {
			t.Errorf("%q everywhere: %s", q, got)
		}
		// Among several folders (a Group's): any of them, and folder too.
		among, _ := x.SearchIn(q, "", []string{"/work/app/web", "/elsewhere/"}, 0)
		if got := ids(among); got != "b,d" {
			t.Errorf("%q among web, elsewhere: %s, want b,d", q, got)
		}
		both, _ := x.SearchIn(q, "/work", []string{"/work/app/web", "/elsewhere"}, 0)
		if got := ids(both); got != "b" {
			t.Errorf("%q in /work among web, elsewhere: %s, want b", q, got)
		}
		if none, _ := x.SearchIn(q, "", []string{}, 0); len(none) != 0 {
			t.Errorf("%q among no folders: %s, want none", q, ids(none))
		}
	}
}

// statInfo is a session that isn't a file of its own, as a Source's Stat
// gives it.
type statInfo struct{ size int64 }

func (s statInfo) Name() string       { return "s1" }
func (s statInfo) Size() int64        { return s.size }
func (s statInfo) Mode() os.FileMode  { return 0o444 }
func (s statInfo) ModTime() time.Time { return time.Unix(1762500000, 0) }
func (s statInfo) IsDir() bool        { return false }
func (s statInfo) Sys() any           { return nil }

// A source whose sessions are rows somewhere (Crush's): its paths name no
// file, and its Stat says when one changed.
func TestSourceWithStat(t *testing.T) {
	size, reads := int64(1), 0
	src := Source{
		Harness: "rows",
		Files:   func() []string { return []string{"/nowhere/db/s1"} },
		Stat:    func(string) (os.FileInfo, error) { return statInfo{size}, nil },
		Open: func(string) (io.ReadCloser, error) {
			reads++
			return io.NopCloser(strings.NewReader("hello rows\n")), nil
		},
		Read: func(r io.Reader, th *thread.Thread, emit func(thread.Record)) (int64, error) {
			b, _ := io.ReadAll(r)
			th.NativeID, th.Cwd = "s1", "/work/rows"
			emit(thread.Record{Kind: thread.KindUser, Text: strings.TrimSpace(string(b))})
			return 0, nil
		},
	}
	x, err := Open(filepath.Join(t.TempDir(), "s.db"), Sources{src})
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	x.Refresh()
	if s, err := x.Get("rows", "s1"); err != nil || s.FirstPrompt != "hello rows" || s.Path != "/nowhere/db/s1" {
		t.Fatalf("%+v %v", s, err)
	}
	x.Refresh()
	size++
	x.Refresh()
	if reads != 2 {
		t.Errorf("read %d times, want 2 (once more after it changed)", reads)
	}
}

// A folder listed twice (CLAUDE_CONFIG_DIR naming ~/.claude) doesn't count
// what a session adds twice.
func TestSameFolderTwice(t *testing.T) {
	claude := t.TempDir()
	roots := []string{filepath.Join(claude, "projects"), filepath.Join(claude, "projects")}
	src := Sources{{Harness: "claude", Appends: true, Read: agents.Claude.Index, Files: func() []string { return agents.Claude.Sessions(roots) }}}
	x, err := Open(filepath.Join(t.TempDir(), "s.db"), src)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	dir := filepath.Join(claude, "projects", "-work-app")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "s1.jsonl")
	appendTo(t, path, ccLine("user", "First")+ccLine("assistant", "Done."))
	x.Refresh()
	appendTo(t, path, ccLine("user", "Second")+ccLine("assistant", "Done again."))
	x.Refresh()
	if s, err := x.Get("claude", "s1"); err != nil || s.Messages != 4 {
		t.Fatalf("messages %d (want 4) %v", s.Messages, err)
	}
}
