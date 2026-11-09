package agents

import (
	"bufio"
	"bytes"
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
	_ "modernc.org/sqlite"
)

// Crush is Charm's coding agent (github.com/charmbracelet/crush), a Go
// binary. What Hammock relies on was read from its source at v0.98.1:
//
//   - Its state is per project: a SQLite database (WAL mode) at
//     <data dir>/crush.db, the data dir being options.data_directory or
//     --data-dir, else the closest .crush folder from the working folder up
//     to its git work tree's top (not one in your home folder), else
//     <cwd>/.crush (internal/config/load.go setDefaults). Each start records
//     {path, data_dir} in projects.json, in $CRUSH_GLOBAL_DATA, else
//     $XDG_DATA_HOME/crush, else ~/.local/share/crush (internal/projects).
//   - Sessions are rows of its sessions table (id a UUID; a subagent's or
//     title generation's has a parent_session_id), messages rows of
//     messages (role user, assistant or tool; parts a JSON array of
//     {"type", "data"}: text, reasoning, tool_call, tool_result, finish,
//     binary, image_url, shell_command), each rewritten as it streams
//     (internal/db/migrations, internal/message/content.go).
//
// Hammock's sessions are files, so a Crush session is named by a path inside
// its database, <data dir>/crush.db/<session id> (crush.db is a file, so
// no real path looks like that): Stat says when that session changed, and
// Open reads it as JSON lines, one for the session and one per message, for
// the parser and the session finder.
//
// Its hooks (crush.json "hooks") have one event, PreToolUse: with no event
// for a turn ending or a permission prompt, Hammock would think it worked
// forever, so it reads Crush's screen instead (Hooks false). It reports
// its state to herdr over herdr's own socket, and to terminals with the
// Program Status Protocol (OSC 7501), neither of which Hammock reads yet.
//
// `crush --session <id>` (-s) resumes a session of the folder it's run in;
// `crush --continue` (-C) the latest. It takes no first message when
// started interactively (`crush "…"` is an unknown command), and
// `crush run "…"` answers non-interactively, approving every permission
// itself, so it can't be scheduled as the other agents are. Its usage
// limits aren't kept on disk (the database has each session's tokens and
// cost only).
var Crush = &Agent{
	ID: "crush", Order: 130, Name: "Crush", Short: "Crush", Command: "crush",
	Install: "brew install charmbracelet/tap/crush",
	// No first message: see above.
	// No first message on its command line: typed in once it is up.
	TypePrompt:  true,
	Schedulable: true,
	Resume:      func(id string) string { return "crush --session " + Quote(id) },
	ResumeID: func(argv []string) string {
		if id := argAfter(argv, "--session", "-s"); safeSegment(id) {
			return id
		}
		return ""
	},
	Screen: Screen{
		// Its status bar while it works: "esc cancel • tab focus editor …"
		// ("press again to cancel" after one Esc, "clear queue" with
		// prompts queued). Dialogs say "esc cancel" too, but not before
		// "tab focus".
		Working: regexp.MustCompile(`esc (?:cancel|press again to cancel|clear queue)\W{1,5}tab focus (?:chat|editor)`),
		// Its permission dialog: "Permission Required", with Allow,
		// "Allow for Session" and Deny.
		NeedsInput: regexp.MustCompile(`Permission Required|Allow for Session`),
	},

	Roots:    crushDataDirs,
	Locate:   crushLocate,
	Newest:   crushNewest,
	Sessions: crushSessionPaths,
	Stat:     crushStat,
	Open:     crushOpen,
	Whole:    true,
	Index: func(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
		br := bufio.NewReaderSize(r, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				crushRecord(line, t, emit)
			}
			if err != nil {
				return 0, nil
			}
		}
	},
	OwnsFile: func(path string) bool { _, _, ok := crushSplit(path); return ok },

	NewParser: func(cwd string, _ bool) agentview.Parser { return agentview.NewCrush(cwd) },
}

func init() { Register(Crush) }

// crushSplit reads a session path: its database and session id.
func crushSplit(path string) (db, id string, ok bool) {
	db, id = filepath.Dir(path), filepath.Base(path)
	return db, id, filepath.IsAbs(path) && filepath.Base(db) == "crush.db" && safeSegment(id)
}

// crushPath is the path naming session id in the database in dataDir.
func crushPath(dataDir, id string) string { return filepath.Join(dataDir, "crush.db", id) }

// crushProject is one entry of its projects.json.
type crushProject struct {
	Path    string `json:"path"`
	DataDir string `json:"data_dir"`
}

var crushProjectsCache struct {
	sync.Mutex
	files map[string]crushProjectsFile
}

type crushProjectsFile struct {
	size     int64
	mod      time.Time
	projects []crushProject
}

// crushProjects are the projects Crush has run in, most recent first, from
// each place its projects.json may be (Hammock's environment may not be the
// agent's, so each is tried).
func crushProjects() []crushProject {
	var files []string
	if d := envOrEmpty("CRUSH_GLOBAL_DATA"); d != "" {
		files = append(files, filepath.Join(d, "projects.json"))
	}
	files = append(files, filepath.Join(xdg("XDG_DATA_HOME", ".local", "share"), "crush", "projects.json"),
		home(".local", "share", "crush", "projects.json"))
	crushProjectsCache.Lock()
	defer crushProjectsCache.Unlock()
	if crushProjectsCache.files == nil {
		crushProjectsCache.files = map[string]crushProjectsFile{}
	}
	var out []crushProject
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f] {
			continue
		}
		seen[f] = true
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		c, ok := crushProjectsCache.files[f]
		if !ok || c.size != st.Size() || !c.mod.Equal(st.ModTime()) {
			c = crushProjectsFile{size: st.Size(), mod: st.ModTime()}
			if b, err := os.ReadFile(f); err == nil {
				var l struct {
					Projects []crushProject `json:"projects"`
				}
				_ = json.Unmarshal(b, &l)
				for _, p := range l.Projects {
					if filepath.IsAbs(p.Path) && filepath.IsAbs(p.DataDir) {
						p.Path, p.DataDir = filepath.Clean(p.Path), filepath.Clean(p.DataDir)
						c.projects = append(c.projects, p)
					}
				}
			}
			crushProjectsCache.files[f] = c
		}
		out = append(out, c.projects...)
	}
	return out
}

// crushDataDirs are the data folders of the projects Crush has run in that
// have a database.
func crushDataDirs() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range crushProjects() {
		if seen[p.DataDir] {
			continue
		}
		seen[p.DataDir] = true
		if st, err := os.Stat(filepath.Join(p.DataDir, "crush.db")); err == nil && st.Mode().IsRegular() {
			out = append(out, p.DataDir)
		}
	}
	return out
}

// CrushDataDir is the data folder Crush started in cwd uses: the one
// projects.json says it used there, else the closest .crush folder up to
// the top of cwd's git work tree (not one in the home folder), else
// cwd/.crush.
func CrushDataDir(cwd string) string {
	cwd = filepath.Clean(cwd)
	for _, p := range crushProjects() {
		if p.Path == cwd {
			return p.DataDir
		}
	}
	h, _ := os.UserHomeDir()
	top := crushGitTop(cwd)
	for d := cwd; ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, ".crush")); err == nil && st.IsDir() {
			if d == h {
				break // its lookup stops there, finding nothing
			}
			return filepath.Join(d, ".crush")
		}
		if d == top || filepath.Dir(d) == d {
			break
		}
	}
	return filepath.Join(cwd, ".crush")
}

// crushGitTop is the top of dir's git work tree (the closest folder with a
// .git), else dir: where Crush's search for a .crush folder stops.
func crushGitTop(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

// crushCwd is the folder whose sessions the database in dataDir holds:
// projects.json's, else the folder the .crush folder is in.
func crushCwd(dataDir string) string {
	for _, p := range crushProjects() {
		if p.DataDir == dataDir {
			return p.Path
		}
	}
	if filepath.Base(dataDir) == ".crush" {
		return filepath.Dir(dataDir)
	}
	return ""
}

// crushSession is one of a database's top-level sessions.
type crushSession struct {
	id               string
	updated, created int64
}

// crushDB is what's known of one database, as of key.
type crushDB struct {
	key      crushKey
	sessions []crushSession // newest first
	listed   bool
	stats    map[string]crushFileInfo
}

// crushKey is a database's files' sizes and times: any write changes them
// (its -wal file's while Crush has it open, the database's after).
type crushKey struct{ dbSize, dbMod, walSize, walMod int64 }

var crushDBs = struct {
	sync.Mutex
	m map[string]*crushDB
}{m: map[string]*crushDB{}}

func crushKeyOf(db string) (crushKey, bool) {
	st, err := os.Stat(db)
	if err != nil || !st.Mode().IsRegular() {
		return crushKey{}, false
	}
	k := crushKey{dbSize: st.Size(), dbMod: st.ModTime().UnixNano()}
	if w, err := os.Stat(db + "-wal"); err == nil {
		k.walSize, k.walMod = w.Size(), w.ModTime().UnixNano()
	}
	return k, true
}

// crushState is db's cache entry, emptied if db changed since. Callers
// hold crushDBs.
func crushState(db string) (*crushDB, bool) {
	k, ok := crushKeyOf(db)
	if !ok {
		delete(crushDBs.m, db)
		return nil, false
	}
	c := crushDBs.m[db]
	if c == nil || c.key != k {
		c = &crushDB{key: k, stats: map[string]crushFileInfo{}}
		crushDBs.m[db] = c
	}
	return c, true
}

// crushOpenDB opens a database to read: read-only, and while nothing has
// it open (no -wal file), as immutable, so reading it creates no files
// beside it. Immutable is only safe while nothing writes: should crush
// start writing during one of these short reads, what's read may be off,
// but its -wal file appearing changes the cache key (crushKeyOf), so it's
// read again.
func crushOpenDB(db string) (*sql.DB, error) {
	q := url.Values{}
	if _, err := os.Stat(db + "-wal"); err == nil {
		q.Set("mode", "ro")
		q.Add("_pragma", "busy_timeout(2000)")
	} else {
		q.Set("immutable", "1")
	}
	u := url.URL{Scheme: "file", Path: db, RawQuery: q.Encode()}
	h, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	h.SetMaxOpenConns(1)
	return h, nil
}

// crushList is db's top-level sessions, newest first.
func crushList(db string) []crushSession {
	crushDBs.Lock()
	defer crushDBs.Unlock()
	c, ok := crushState(db)
	if !ok {
		return nil
	}
	if !c.listed {
		c.listed = true
		h, err := crushOpenDB(db)
		if err != nil {
			return nil
		}
		defer h.Close()
		rows, err := h.Query(`SELECT id, created_at, updated_at FROM sessions WHERE parent_session_id IS NULL ORDER BY updated_at DESC`)
		if err != nil {
			return nil
		}
		defer rows.Close()
		for rows.Next() {
			var s crushSession
			if rows.Scan(&s.id, &s.created, &s.updated) == nil && safeSegment(s.id) {
				c.sessions = append(c.sessions, s)
			}
		}
	}
	return c.sessions
}

func crushLocate(id string) (string, bool) {
	if !safeSegment(id) {
		return "", false
	}
	for _, d := range crushDataDirs() {
		for _, s := range crushList(filepath.Join(d, "crush.db")) {
			if s.id == id {
				return crushPath(d, id), true
			}
		}
	}
	return "", false
}

func crushNewest(cwd string) (string, bool) {
	d := CrushDataDir(cwd)
	if l := crushList(filepath.Join(d, "crush.db")); len(l) > 0 {
		return crushPath(d, l[0].id), true
	}
	return "", false
}

func crushSessionPaths(roots []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range roots {
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		for _, s := range crushList(filepath.Join(d, "crush.db")) {
			out = append(out, crushPath(d, s.id))
		}
	}
	return out
}

// crushFileInfo is a session as a file: its size the length of its
// messages (which grows as one streams), its time when it last changed.
type crushFileInfo struct {
	name string
	size int64
	mod  time.Time
}

func (f crushFileInfo) Name() string       { return f.name }
func (f crushFileInfo) Size() int64        { return f.size }
func (f crushFileInfo) Mode() fs.FileMode  { return 0o444 }
func (f crushFileInfo) ModTime() time.Time { return f.mod }
func (f crushFileInfo) IsDir() bool        { return false }
func (f crushFileInfo) Sys() any           { return nil }

// crushStat stats one session, querying its database only when that has
// changed since it last did.
func crushStat(path string) (os.FileInfo, error) {
	db, id, ok := crushSplit(path)
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrInvalid}
	}
	crushDBs.Lock()
	defer crushDBs.Unlock()
	c, ok := crushState(db)
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	if fi, ok := c.stats[id]; ok {
		return fi, nil
	}
	h, err := crushOpenDB(db)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	var updated, n, length, msgUpdated int64
	var title string
	err = h.QueryRow(`SELECT s.title, s.updated_at,
			(SELECT count(*) FROM messages WHERE session_id = s.id),
			(SELECT coalesce(sum(length(parts)), 0) FROM messages WHERE session_id = s.id),
			(SELECT coalesce(max(updated_at), 0) FROM messages WHERE session_id = s.id)
		FROM sessions s WHERE s.id = ?`, id).Scan(&title, &updated, &n, &length, &msgUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	if err != nil {
		return nil, err
	}
	fi := crushFileInfo{name: id, size: n + length + int64(len(title)), mod: time.Unix(max(updated, msgUpdated), 0)}
	c.stats[id] = fi
	return fi, nil
}

// CrushMeta is the first line crushOpen gives: the session itself.
type CrushMeta struct {
	Type             string `json:"type"` // "hammock_session"
	ID               string `json:"id"`
	Cwd              string `json:"cwd,omitempty"`
	Title            string `json:"title,omitempty"`
	Created          int64  `json:"created_at"`
	Updated          int64  `json:"updated_at"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

// CrushMessage is each line after it: one of its messages, as its row.
type CrushMessage struct {
	Type     string          `json:"type"` // "message"
	ID       string          `json:"id"`
	Role     string          `json:"role"`
	Parts    json.RawMessage `json:"parts"`
	Model    string          `json:"model,omitempty"`
	Provider string          `json:"provider,omitempty"`
	Created  int64           `json:"created_at"`
	Updated  int64           `json:"updated_at"`
	Finished int64           `json:"finished_at,omitempty"`
	Summary  bool            `json:"summary,omitempty"`
}

// crushOpen reads one session from its database as lines: the session,
// then its messages in the order they were made.
func crushOpen(path string) (io.ReadCloser, error) {
	db, id, ok := crushSplit(path)
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrInvalid}
	}
	if _, err := os.Stat(db); err != nil {
		return nil, err
	}
	h, err := crushOpenDB(db)
	if err != nil {
		return nil, err
	}
	defer h.Close()
	m := CrushMeta{Type: "hammock_session", ID: id, Cwd: crushCwd(filepath.Dir(db))}
	err = h.QueryRow(`SELECT title, created_at, updated_at, prompt_tokens, completion_tokens FROM sessions WHERE id = ?`, id).
		Scan(&m.Title, &m.Created, &m.Updated, &m.PromptTokens, &m.CompletionTokens)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(m)

	// provider and is_summary_message came in 2025: a database no newer
	// Crush has opened since lacks them.
	rows, err := h.Query(`SELECT id, role, parts, coalesce(model, ''), coalesce(provider, ''), created_at, updated_at,
			coalesce(finished_at, 0), is_summary_message
		FROM messages WHERE session_id = ? ORDER BY created_at, rowid`, id)
	if err != nil {
		rows, err = h.Query(`SELECT id, role, parts, coalesce(model, ''), '', created_at, updated_at, coalesce(finished_at, 0), 0
			FROM messages WHERE session_id = ? ORDER BY created_at, rowid`, id)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		l := CrushMessage{Type: "message"}
		var parts string
		if rows.Scan(&l.ID, &l.Role, &parts, &l.Model, &l.Provider, &l.Created, &l.Updated, &l.Finished, &l.Summary) != nil {
			continue
		}
		if json.Valid([]byte(parts)) {
			l.Parts = json.RawMessage(parts)
		} else {
			l.Parts = json.RawMessage("[]")
		}
		_ = enc.Encode(l)
	}
	return io.NopCloser(&buf), rows.Err()
}

// crushRecord reads one of crushOpen's lines into the finder's records.
func crushRecord(line []byte, t *thread.Thread, emit func(thread.Record)) {
	var l struct {
		CrushMessage
		ID      string `json:"id"`
		Cwd     string `json:"cwd"`
		Title   string `json:"title"`
		Created int64  `json:"created_at"`
	}
	if json.Unmarshal(line, &l) != nil {
		return
	}
	if l.Type == "hammock_session" {
		t.NativeID = cmp.Or(t.NativeID, l.ID)
		t.Cwd = cmp.Or(t.Cwd, l.Cwd)
		t.Title = cmp.Or(t.Title, crushTitle(l.Title))
		if t.Created.IsZero() && l.Created > 0 {
			t.Created = time.Unix(l.Created, 0)
		}
		return
	}
	at := time.Unix(l.Created, 0)
	for _, p := range agentview.CrushParts(l.Parts) {
		switch {
		case p.Type == "text" && !p.Hidden && l.Role == "user":
			if s := strings.TrimSpace(p.Text); s != "" {
				emit(thread.Record{Kind: thread.KindUser, Time: at, Text: s})
			}
		case p.Type == "text" && l.Role == "assistant" && !l.Summary:
			if s := strings.TrimSpace(p.Text); s != "" {
				emit(thread.Record{Kind: thread.KindAssistant, Time: at, Text: s})
			}
		case p.Type == "tool_call":
			in := json.RawMessage(p.Input)
			if !json.Valid(in) {
				in = json.RawMessage("{}")
			}
			emit(thread.Record{Kind: thread.KindToolCall, Time: at, NativeTool: p.Name, Input: in})
		}
	}
}

// crushTitle is a session's title, unless it's Crush's stand-in for one
// not yet made.
func crushTitle(s string) string {
	if s == "New Session" || s == "Untitled Session" {
		return ""
	}
	return s
}
