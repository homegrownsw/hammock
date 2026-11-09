package agents

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Goose is Block's open-source agent (github.com/block/goose, now
// aaif-goose under the Agentic AI Foundation), a Rust CLI. What Hammock relies
// on was read from its source at 1.58.0 (4facf56, 2026-11-06):
//
//   - `goose session` starts it interactively; `goose run -s -t "prompt"`
//     starts it with a first message and stays interactive (-s,
//     --interactive). `goose session --resume --session-id ID` resumes a
//     session (--name NAME by its name; --resume alone the latest).
//   - Hooks are the Open Plugins kind: a plugin folder under
//     ~/.agents/plugins (<GOOSE_PATH_ROOT>/.agents/plugins) whose
//     hooks/hooks.json maps events to commands, run with `sh -c`, the event's
//     JSON on stdin ({"event", "session_id", "tool_name", "tool_input",
//     "message" (your prompt), "last_assistant_message", "working_dir"};
//     crates/goose/src/hooks/mod.rs HookContext). No event says it's asking
//     to run a tool: its approval comes before PreToolUse, so the screen says.
//   - Sessions are rows in one SQLite database, <data>/sessions/sessions.db,
//     <data> being $GOOSE_PATH_ROOT/data, else $XDG_DATA_HOME/goose, else
//     ~/.local/share/goose (etcetera's XDG strategy, on macOS too); before
//     1.10 they were JSONL files there, which it imports and then ignores.
//   - Its limits are its providers' (API keys, or another agent's
//     subscription): nothing on disk says them.
//
// Hammock's session files are one per file, and goose's are all in one
// database, so Hammock keeps a small handle for each session it reads, in its
// own cache folder (gooseHandleDir): the database's path and the session's
// id, rewritten whenever the session changes, so the agent view and the
// session finder see it change. Open reads the session from goose's
// database (read-only) as lines (agentview.GooseLine).
var Goose = &Agent{
	ID: "goose", Order: 140, Name: "Goose", Short: "Goose", Command: "goose",
	Install:   "curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh | bash",
	Namesake:  gooseMigrations,
	Hooks:     true,
	ParseHook: gooseHook,
	Start: func(prompt string) string {
		if p := strings.TrimSpace(prompt); p != "" {
			return "goose run -s -t " + Quote(p)
		}
		return "goose session"
	},
	Resume:      func(id string) string { return "goose session --resume --session-id " + Quote(id) },
	ResumeID:    gooseResumeID,
	Schedulable: true,
	// Its approval prompt comes before its PreToolUse hook, and no hook says it.
	ScreenAsks: true,
	Screen: Screen{
		// Its spinner: "<a thinking message>...  (Ctrl+C to interrupt)"
		// (goose-cli session/output.rs ThinkingIndicator).
		Working: regexp.MustCompile(`(?i)\(ctrl\+c to interrupt\)`),
		// prompt_tool_confirmation (goose-cli session/mod.rs).
		NeedsInput: regexp.MustCompile(`Goose would like to call the above tool, do you allow\?|Do you allow this tool call\?`),
		// Its input's hint after Ctrl+C (session/completion.rs).
		Interrupted: regexp.MustCompile(`Interrupted, what should goose work on instead\?`),
	},

	Roots:   gooseRoots,
	RootEnv: map[string]string{"GOOSE_PATH_ROOT": filepath.Join("data", "sessions"), "XDG_DATA_HOME": filepath.Join("goose", "sessions")},
	Locate: func(id string) (string, bool) {
		for _, db := range gooseDatabases(gooseRoots()) {
			if p, ok := gooseStoreFor(db).locate(id); ok {
				return p, true
			}
		}
		return "", false
	},
	Newest: func(cwd string) (string, bool) {
		var best string
		var at int64
		for _, db := range gooseDatabases(gooseRoots()) {
			if p, t, ok := gooseStoreFor(db).newest(cwd); ok && (best == "" || t > at) {
				best, at = p, t
			}
		}
		return best, best != ""
	},
	Sessions: func(roots []string) []string {
		var out []string
		for _, db := range gooseDatabases(roots) {
			out = append(out, gooseStoreFor(db).sessions()...)
		}
		return out
	},
	Index:    gooseIndex,
	OwnsFile: gooseOwnsFile,

	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewGoose(cwd) },
	Whole:         true,
	Open:          gooseOpen,
	SlashCommands: func(string) []string { return gooseSlashCommands },
}

func init() { Register(Goose) }

// gooseSlashCommands are its CLI's (goose-cli session/input.rs, 1.58.0).
var gooseSlashCommands = []string{"/builtin", "/clear", "/compact", "/edit", "/exit", "/extension", "/help", "/mode", "/model",
	"/new", "/prompt", "/prompts", "/quit", "/r", "/skills", "/t"}

// gooseHook reads one of its hook events (the command names the event,
// which its payload's "event" says too).
func gooseHook(event string, payload []byte) Hook {
	var p struct {
		Event         *string         `json:"event"`
		HookEventName string          `json:"hook_event_name"`
		SessionID     string          `json:"session_id"`
		ToolName      string          `json:"tool_name"`
		ToolInput     json.RawMessage `json:"tool_input"`
		Message       string          `json:"message"`
		LastMessage   string          `json:"last_assistant_message"`
	}
	if json.Unmarshal(payload, &p) != nil || p.Event == nil || p.HookEventName != "" {
		// Not goose's: another agent that reads ~/.agents/plugins too ran
		// Hammock's goose plugin.
		return Hook{}
	}
	h := Hook{SessionID: p.SessionID, ToolName: p.ToolName, ToolInput: rawOr(p.ToolInput)}
	switch cmp.Or(event, *p.Event) {
	case "SessionStart":
		h.Event = "SessionStart"
	case "SessionEnd":
		h.Event = "SessionEnd"
	case "UserPromptSubmit":
		h.Event, h.Prompt = "UserPromptSubmit", p.Message
	case "PreToolUse":
		h.Event = "PreToolUse"
	case "PostToolUse", "PostToolUseFailure":
		h.Event = "PostToolUse"
	case "Stop":
		h.Event, h.LastMessage = "Stop", p.LastMessage
	}
	return h
}

// gooseIDRE is a session id goose accepts: 1-128 ASCII letters, digits,
// "_" and "-" (it makes them <yyyymmdd>_<n>).
var gooseIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// gooseResumeID is the session `goose session|run --resume` resumes: its
// --session-id (--id), else its --name (Locate finds a session by name
// too), else a legacy --path's file name.
func gooseResumeID(argv []string) string {
	sub, resume := false, false
	for _, a := range argv[min(1, len(argv)):] {
		switch a {
		case "session", "s", "run":
			sub = true
		case "--resume", "-r":
			resume = true
		}
	}
	if !sub || !resume {
		return ""
	}
	if id := argAfter(argv, "--session-id", "--id"); id != "" {
		return id
	}
	if name := argAfter(argv, "--name", "-n"); name != "" {
		return name
	}
	if p := argAfter(argv, "--path"); p != "" {
		return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	}
	return ""
}

// gooseDBDirs are the folders goose's sessions.db may be in.
func gooseDBDirs() []string {
	var out []string
	if v := os.Getenv("GOOSE_PATH_ROOT"); filepath.IsAbs(v) {
		out = append(out, filepath.Join(v, "data", "sessions"))
	}
	out = append(out, filepath.Join(xdg("XDG_DATA_HOME", ".local", "share"), "goose", "sessions"))
	if d := home(".local", "share", "goose", "sessions"); !slices.Contains(out, d) {
		out = append(out, d)
	}
	return out
}

// gooseRoots: Hammock's handles, then goose's database folders.
func gooseRoots() []string { return append([]string{gooseHandleDir()}, gooseDBDirs()...) }

// gooseHandleBase is the name of the folder Hammock keeps its handles in.
const gooseHandleBase = "goose-sessions"

// gooseHandleDir is where Hammock keeps its handles to goose's sessions: a
// cache (~/Library/Caches/hammock/goose-sessions), shared by every copy of
// Hammock, each handle in a folder for its database (gooseDBKey).
var gooseHandleDir = func() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "hammock", gooseHandleBase)
	}
	return home(".cache", "hammock", gooseHandleBase)
}

// gooseOwnsFile: path is one of Hammock's handles to a goose session
// (<handles>/<database key>/<session id>).
func gooseOwnsFile(path string) bool {
	dir := filepath.Dir(path)
	return filepath.Base(filepath.Dir(dir)) == gooseHandleBase && gooseKeyRE.MatchString(filepath.Base(dir)) && gooseIDRE.MatchString(filepath.Base(path))
}

var gooseKeyRE = regexp.MustCompile(`^[0-9a-f]{16}$`)

// gooseDBKey names a database's folder of handles.
func gooseDBKey(db string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(db)))
	return hex.EncodeToString(sum[:8])
}

// gooseDatabases are the sessions.db files in roots.
func gooseDatabases(roots []string) []string {
	var out []string
	for _, r := range roots {
		p := filepath.Join(r, "sessions.db")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// gooseHandle is the content of a handle file.
type gooseHandle struct {
	Hammock string `json:"hammock"`
	DB      string `json:"db"`
	ID      string `json:"id"`
	Stamp   string `json:"stamp"` // what changes when the session does
}

const gooseHandleNote = "A handle Hammock keeps to a goose session, which is in goose's own database"

// gooseStore reads one of goose's databases.
type gooseStore struct {
	path string

	mu      sync.Mutex
	db      *sql.DB
	file    os.FileInfo
	located map[string]gooseCached // by session id (or name)
	newestC map[string]gooseCached // by cwd
	listSig string
	list    []string
}

type gooseCached struct {
	sig, path string
	at        int64
}

var gooseStores sync.Map // database path -> *gooseStore

func gooseStoreFor(path string) *gooseStore {
	if s, ok := gooseStores.Load(path); ok {
		return s.(*gooseStore)
	}
	s, _ := gooseStores.LoadOrStore(path, &gooseStore{path: path, located: map[string]gooseCached{}, newestC: map[string]gooseCached{}})
	return s.(*gooseStore)
}

// signature changes whenever goose writes the database (its WAL, or the
// database itself when it checkpoints). Callers hold s.mu.
func (s *gooseStore) signature() string {
	var b strings.Builder
	for _, p := range []string{s.path, s.path + "-wal"} {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%d.%d/", st.Size(), st.ModTime().UnixNano())
		} else {
			b.WriteString("-/")
		}
	}
	return b.String()
}

// open is the database, opened read-only (again, if it was replaced).
// Callers hold s.mu.
func (s *gooseStore) open() (*sql.DB, error) {
	st, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	if s.db != nil && os.SameFile(st, s.file) {
		return s.db, nil
	}
	if s.db != nil {
		s.db.Close()
		s.located, s.newestC, s.listSig = map[string]gooseCached{}, map[string]gooseCached{}, ""
	}
	u := url.URL{Scheme: "file", Path: s.path}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	s.db, s.file = db, st
	return db, nil
}

// locate is the handle to session id (or the newest one named id).
func (s *gooseStore) locate(id string) (string, bool) {
	if id == "" || len(id) > 512 {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sig := s.signature()
	if c, ok := s.located[id]; ok && c.sig == sig && (c.path == "" || gooseExists(c.path)) {
		return c.path, c.path != ""
	}
	db, err := s.open()
	if err != nil {
		return "", false
	}
	path := ""
	for _, q := range []string{`SELECT id FROM sessions WHERE id = ?`, `SELECT id FROM sessions WHERE name = ? ORDER BY updated_at DESC LIMIT 1`} {
		var real string
		if db.QueryRow(q, id).Scan(&real) == nil && gooseIDRE.MatchString(real) {
			path, _ = s.handle(db, real)
			break
		}
	}
	if len(s.located) > 1000 {
		clear(s.located)
	}
	s.located[id] = gooseCached{sig: sig, path: path}
	return path, path != ""
}

// newest is the handle to the session last active in cwd, and when.
func (s *gooseStore) newest(cwd string) (string, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sig := s.signature()
	if c, ok := s.newestC[cwd]; ok && c.sig == sig && (c.path == "" || gooseExists(c.path)) {
		return c.path, c.at, c.path != ""
	}
	db, err := s.open()
	if err != nil {
		return "", 0, false
	}
	var id string
	var updated any
	path, at := "", int64(0)
	err = db.QueryRow(`SELECT id, updated_at FROM sessions WHERE working_dir = ? AND session_type IN ('user', 'scheduled')
		ORDER BY updated_at DESC, created_at DESC LIMIT 1`, filepath.Clean(cwd)).Scan(&id, &updated)
	if err == nil && gooseIDRE.MatchString(id) {
		path, _ = s.handle(db, id)
		at = gooseTime(updated).UnixMilli()
	}
	if len(s.newestC) > 1000 {
		clear(s.newestC)
	}
	s.newestC[cwd] = gooseCached{sig: sig, path: path, at: at}
	return path, at, path != ""
}

// handle writes (if it changed) and returns session id's handle. Callers
// hold s.mu.
func (s *gooseStore) handle(db *sql.DB, id string) (string, error) {
	var name string
	var updated any
	var tokens, n, last sql.NullInt64
	err := db.QueryRow(`SELECT s.name, s.updated_at, s.total_tokens,
		(SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id),
		(SELECT MAX(m.id) FROM messages m WHERE m.session_id = s.id)
		FROM sessions s WHERE s.id = ?`, id).Scan(&name, &updated, &tokens, &n, &last)
	if err != nil {
		return "", err
	}
	return s.writeHandle(id, gooseStamp(n.Int64, last.Int64, updated, name, tokens.Int64))
}

func gooseStamp(n, last int64, updated any, name string, tokens int64) string {
	return fmt.Sprintf("%d.%d.%d.%d.%x", n, last, gooseTime(updated).UnixNano(), tokens, sha256.Sum256([]byte(name)))
}

// writeHandle writes session id's handle, unless it already says stamp.
func (s *gooseStore) writeHandle(id, stamp string) (string, error) {
	path := filepath.Join(gooseHandleDir(), gooseDBKey(s.path), id)
	content, _ := json.Marshal(gooseHandle{Hammock: gooseHandleNote, DB: s.path, ID: id, Stamp: stamp})
	content = append(content, '\n')
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, content) {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".handle-*")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(content)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return "", werr
	}
	return path, nil
}

// sessions are the handles to every session of yours in the database (not
// subagents' or goose's own hidden ones) with anything in it, written
// afresh; handles to sessions it no longer has are removed.
func (s *gooseStore) sessions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sig := s.signature()
	if sig == s.listSig {
		return s.list
	}
	db, err := s.open()
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT s.id, s.name, s.updated_at, s.total_tokens, c.n, c.last
		FROM sessions s JOIN (SELECT session_id, COUNT(*) AS n, MAX(id) AS last FROM messages GROUP BY session_id) c ON c.session_id = s.id
		WHERE s.session_type IN ('user', 'scheduled')`)
	if err != nil {
		return nil
	}
	var out []string
	keep := map[string]bool{}
	for rows.Next() {
		var id, name string
		var updated any
		var tokens, n, last sql.NullInt64
		if rows.Scan(&id, &name, &updated, &tokens, &n, &last) != nil || !gooseIDRE.MatchString(id) {
			continue
		}
		if p, err := s.writeHandle(id, gooseStamp(n.Int64, last.Int64, updated, name, tokens.Int64)); err == nil {
			out = append(out, p)
			keep[id] = true
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return nil
	}
	rows.Close()
	dir := filepath.Join(gooseHandleDir(), gooseDBKey(s.path))
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if !keep[e.Name()] && gooseIDRE.MatchString(e.Name()) {
				// A session goose deleted, or one only Locate wrote (a
				// subagent's): written again when it's asked for.
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	clear(s.located)
	clear(s.newestC)
	s.listSig, s.list = sig, out
	return out
}

// gooseOpen reads the session a handle names from goose's database, as
// lines: the session (agentview.GooseLine "session"), then its messages in
// goose's order.
func gooseOpen(path string) (io.ReadCloser, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var h gooseHandle
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	// The handle must be where Hammock put it for its database and session.
	if !filepath.IsAbs(h.DB) || filepath.Base(h.DB) != "sessions.db" || filepath.Base(path) != h.ID ||
		filepath.Base(filepath.Dir(path)) != gooseDBKey(h.DB) || !gooseIDRE.MatchString(h.ID) {
		return nil, errors.New("not a goose session handle")
	}
	s := gooseStoreFor(h.DB)
	s.mu.Lock()
	db, err := s.open()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := gooseSessionLines(db, h.ID, &buf); err != nil {
		return nil, err
	}
	return io.NopCloser(&buf), nil
}

// gooseSessionLines writes session id's lines to w.
func gooseSessionLines(db *sql.DB, id string, w *bytes.Buffer) error {
	var cwd, name string
	var named sql.NullBool
	var created, updated any
	var tokens sql.NullInt64
	var model sql.NullString
	err := db.QueryRow(`SELECT working_dir, name, user_set_name, created_at, updated_at, total_tokens, model_config_json
		FROM sessions WHERE id = ?`, id).Scan(&cwd, &name, &named, &created, &updated, &tokens, &model)
	if err != nil {
		return err
	}
	var mc struct {
		ModelName string `json:"model_name"`
	}
	_ = json.Unmarshal([]byte(model.String), &mc)
	head := agentview.GooseLine{Type: "session", ID: id, Cwd: cwd, Name: name, Named: named.Bool,
		CreatedMs: gooseTime(created).UnixMilli(), UpdatedMs: gooseTime(updated).UnixMilli(), Model: mc.ModelName, Tokens: tokens.Int64}
	line, _ := json.Marshal(head)
	w.Write(append(line, '\n'))

	rows, err := db.Query(`SELECT id, role, content_json, created_timestamp, metadata_json FROM messages
		WHERE session_id = ? ORDER BY created_timestamp, id`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row, ts int64
		var role, content string
		var meta sql.NullString
		if rows.Scan(&row, &role, &content, &ts, &meta) != nil || !json.Valid([]byte(content)) {
			continue
		}
		l := agentview.GooseLine{Type: "message", Row: row, Role: role, TimeMs: gooseMs(ts), Content: json.RawMessage(content)}
		if meta.Valid && json.Valid([]byte(meta.String)) {
			l.Metadata = json.RawMessage(meta.String)
		}
		line, _ := json.Marshal(l)
		w.Write(append(line, '\n'))
	}
	return rows.Err()
}

// gooseMs is a message's created_timestamp in milliseconds: goose writes
// seconds, and some messages milliseconds (its MILLISECOND_TIMESTAMP_THRESHOLD).
func gooseMs(ts int64) int64 {
	if ts >= 10_000_000_000 {
		return ts
	}
	return ts * 1000
}

// gooseTime reads one of its TIMESTAMP columns: SQLite's "2006-01-02
// 15:04:05" (UTC), or RFC 3339, as the driver hands it over.
func gooseTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case []byte:
		return gooseTime(string(t))
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999"} {
			if at, err := time.Parse(layout, t); err == nil {
				return at
			}
		}
	case int64:
		return time.UnixMilli(gooseMs(t))
	}
	return time.Time{}
}

// gooseIndex reads a session's lines (gooseOpen's) for the session finder.
func gooseIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			gooseRecord(line, t, emit)
		}
		if err != nil {
			return 0, nil
		}
	}
}

func gooseRecord(line []byte, t *thread.Thread, emit func(thread.Record)) {
	l, parts, meta, ok := agentview.ParseGooseLine(line)
	if !ok {
		return
	}
	at := time.Time{}
	if l.TimeMs > 0 {
		at = time.UnixMilli(l.TimeMs)
	}
	switch {
	case l.Type == "session":
		t.NativeID, t.Cwd = cmp.Or(t.NativeID, l.ID), cmp.Or(t.Cwd, l.Cwd)
		// "CLI Session" is what goose calls one until it names it.
		if l.Name != "" && l.Name != "CLI Session" {
			t.Title, t.Named = l.Name, l.Named
		}
		if l.CreatedMs > 0 {
			t.Created = time.UnixMilli(l.CreatedMs)
		}
	case l.Type != "message" || !meta.Shown() || meta.CompactionSummary:
	case l.Role == "user":
		if text := agentview.GooseUserText(parts); text != "" {
			if t.Created.IsZero() {
				t.Created = at
			}
			emit(thread.Record{Kind: thread.KindUser, Time: at, Text: text})
		}
	case l.Role == "assistant":
		for _, p := range parts {
			switch p.Type {
			case "text":
				if s := strings.TrimSpace(p.Text); s != "" {
					emit(thread.Record{Kind: thread.KindAssistant, Time: at, Text: s})
				}
			case "toolRequest":
				if name, args := p.ToolCall.Call(); name != "" {
					in, _ := json.Marshal(args)
					emit(thread.Record{Kind: thread.KindToolCall, Time: at, CallID: p.ID, NativeTool: name, Input: in})
				}
			}
		}
	}
}

func gooseExists(path string) bool { _, err := os.Stat(path); return err == nil }

// gooseMigrations: the process is pressly/goose, the database migration
// tool, also "goose": its first argument (after its flags) is a database
// driver or a migration command, none of which Goose the agent takes.
func gooseMigrations(_ string, args []string) bool {
	for i := 1; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			// Its flags that take a value as the next argument.
			if !strings.Contains(a, "=") && (a == "-dir" || a == "--dir" || a == "-table" || a == "--table" || a == "-certfile" || a == "-ssl-cert" || a == "-ssl-key") {
				i++
			}
			continue
		}
		return gooseMigrationWords[strings.ToLower(a)]
	}
	return false
}

var gooseMigrationWords = map[string]bool{
	"up": true, "up-by-one": true, "up-to": true, "down": true, "down-to": true, "redo": true, "reset": true,
	"status": true, "create": true, "fix": true, "validate": true,
	"postgres": true, "mysql": true, "sqlite3": true, "sqlite": true, "mssql": true, "redshift": true, "tidb": true,
	"clickhouse": true, "vertica": true, "ydb": true, "turso": true, "starrocks": true,
}
