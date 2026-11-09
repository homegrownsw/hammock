// Package sessions indexes every agent session on this Mac (each agent's,
// as internal/agents reads them),
// for the session finder (Phase 5's thread history browser): their titles,
// folders, first and last prompts, and conversation text, searchable with
// SQLite's full-text search.
//
// The index is a cache (sessions.db beside state.db), rebuilt if its schema
// changes. Refresh brings it up to date: a session that grew is read from
// where it was last read to when its agent only appends (Claude's: the one
// you're in can be hundreds of megabytes); others (Codex's, small and
// compressed) are read again whole.
package sessions

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"

	"github.com/alexjsp/hammock/internal/thread"
)

// Session is one session as the finder shows it.
type Session struct {
	Harness     string // its agent: "claude", "codex"…
	ID          string // its own session ID (what --resume takes)
	Path        string
	Cwd         string
	Title       string // its name (/rename, Codex's thread name) or generated title; else its first prompt
	FirstPrompt string
	LastPrompt  string
	Created     time.Time
	Updated     time.Time
	Messages    int    // your prompts and the agent's replies
	Snippet     string // for a search: where it matched, with the match in «»
}

// Source is one agent's sessions.
type Source struct {
	Harness string
	// Files lists its session files.
	Files func() []string
	// Appends: its files only grow, so a changed one is read on from where
	// it was left.
	Appends bool
	// Read reads a session from r (whole, or from where it was left), filling
	// in t and calling emit for each record; it returns how far into r it
	// read whole lines (where an Appends source carries on next time).
	Read func(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error)
	// Names are names it keeps apart from its sessions, by session ID.
	Names func() map[string]string
	// Open opens a session file as Read reads it (nil: the file, or its
	// decompressed content).
	Open func(path string) (io.ReadCloser, error)
	// Stat stats a session file (nil: os.Stat), for sessions that aren't
	// files of their own.
	Stat func(path string) (os.FileInfo, error)
}

// Sources says where sessions live.
type Sources []Source

type Index struct {
	db  *sql.DB
	src Sources
	mu  sync.Mutex // one Refresh at a time
}

const schemaVersion = 3 // bumped when what is read from sessions changes

// Open opens (or creates) the index at path.
func Open(path string, src Sources) (*Index, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var v int
	_ = db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != schemaVersion {
		// A cache: start again rather than migrate.
		for _, t := range []string{"files", "text"} {
			if _, err := db.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS files(
			id INTEGER PRIMARY KEY, path TEXT UNIQUE, size INTEGER, mtime INTEGER, offset INTEGER,
			harness TEXT, sid TEXT, cwd TEXT, title TEXT, named INTEGER, first TEXT, last TEXT,
			created INTEGER, updated INTEGER, messages INTEGER)`,
		`CREATE INDEX IF NOT EXISTS files_updated ON files(updated)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS text USING fts5(title, cwd, body, tokenize='porter unicode61')`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion),
	} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, fmt.Errorf("sessions index: %w", err)
		}
	}
	return &Index{db: db, src: src}, nil
}

func (x *Index) Close() error { return x.db.Close() }

// maxBody bounds a session's indexed text: its beginning and its most
// recent part are kept.
const (
	maxBody  = 256 << 10
	keepHead = 64 << 10
)

type file struct {
	path        string
	size, mtime int64
	harness     string
}

// Refresh brings the index up to date with the session files on disk. It
// returns how many sessions it (re)read.
func (x *Index) Refresh() (int, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	files := x.list()
	known := map[string]struct {
		id, size, mtime, offset int64
	}{}
	rows, err := x.db.Query(`SELECT id, path, size, mtime, offset FROM files`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var p string
		var k struct{ id, size, mtime, offset int64 }
		if rows.Scan(&k.id, &p, &k.size, &k.mtime, &k.offset) == nil {
			known[p] = k
		}
	}
	rows.Close()

	n := 0
	seen := map[string]bool{}
	for _, f := range files {
		// A file under two of an agent's folders (CLAUDE_CONFIG_DIR naming
		// the default one) is read once: a second read would append its
		// new lines again.
		if seen[f.path] {
			continue
		}
		seen[f.path] = true
		k, ok := known[f.path]
		if ok && k.size == f.size && k.mtime == f.mtime {
			continue
		}
		// A Claude session that only grew is read from where it was left.
		from := int64(0)
		if ok && x.source(f.harness).Appends && f.size > k.size && k.offset > 0 {
			from = k.offset
		}
		if err := x.read(f, from); err == nil {
			n++
		}
	}
	for p, k := range known {
		if !seen[p] {
			_, _ = x.db.Exec(`DELETE FROM files WHERE id = ?`, k.id)
			_, _ = x.db.Exec(`DELETE FROM text WHERE rowid = ?`, k.id)
		}
	}
	if err := x.names(); err != nil {
		return n, err
	}
	return n, nil
}

// list finds the session files.
func (x *Index) list() []file {
	var out []file
	for _, src := range x.src {
		stat := src.Stat
		if stat == nil {
			stat = os.Stat
		}
		for _, p := range src.Files() {
			if st, err := stat(p); err == nil && st.Mode().IsRegular() {
				out = append(out, file{path: p, size: st.Size(), mtime: st.ModTime().UnixNano(), harness: src.Harness})
			}
		}
	}
	return out
}

// read (re)indexes one session file, from offset from (Claude) or whole.
func (x *Index) read(f file, from int64) error {
	var s Session
	var body strings.Builder
	var id int64
	var named bool
	if from > 0 {
		// Carry on from what's stored.
		var created, updated int64
		err := x.db.QueryRow(`SELECT id, sid, cwd, title, named, first, last, created, updated, messages FROM files WHERE path = ?`, f.path).
			Scan(&id, &s.ID, &s.Cwd, &s.Title, &named, &s.FirstPrompt, &s.LastPrompt, &created, &updated, &s.Messages)
		if err != nil {
			from = 0
		} else {
			s.Created, s.Updated = time.UnixMilli(created), time.UnixMilli(updated)
			var old string
			_ = x.db.QueryRow(`SELECT body FROM text WHERE rowid = ?`, id).Scan(&old)
			body.WriteString(old)
		}
	}
	if from == 0 {
		s, id = Session{}, 0
		body.Reset()
	}
	s.Harness, s.Path = f.harness, f.path

	open := x.source(f.harness).Open
	if open == nil {
		open = thread.OpenTranscript
	}
	r, err := open(f.path)
	if err != nil {
		return err
	}
	defer r.Close()
	if from > 0 {
		if sk, ok := r.(io.Seeker); ok {
			if _, err := sk.Seek(from, io.SeekStart); err != nil {
				return err
			}
		}
	}
	t := thread.Thread{NativeID: s.ID, Cwd: s.Cwd, Title: s.Title, Named: named, Created: s.Created}
	if s.Title == s.FirstPrompt && !named {
		t.Title = "" // the stand-in title isn't Claude's
	}
	emit := func(rec thread.Record) {
		if rec.Kind != thread.KindUser && rec.Kind != thread.KindAssistant {
			return
		}
		text := strings.TrimSpace(rec.Text)
		if rec.Kind == thread.KindUser {
			text = strings.TrimSpace(thread.UnwrapPasted(text)) // sent from the agent view

			if noise(text) {
				return
			}
			if s.FirstPrompt == "" {
				s.FirstPrompt = oneLine(text, 300)
			}
			s.LastPrompt = oneLine(text, 300)
		}
		s.Messages++
		if !rec.Time.IsZero() && rec.Time.After(s.Updated) {
			s.Updated = rec.Time
		}
		body.WriteString(text)
		body.WriteByte('\n')
	}
	offset := from
	src := x.source(f.harness)
	if src.Read == nil {
		return fmt.Errorf("no reader for %s sessions", f.harness)
	}
	n, err := src.Read(r, &t, emit)
	if err != nil {
		return err
	}
	if src.Appends {
		offset += n
	} else {
		offset = 0
	}
	if s.Messages == 0 || s.FirstPrompt == "" {
		// Nothing you said (a session opened and left, or only commands):
		// not worth finding.
		if id != 0 {
			_, _ = x.db.Exec(`DELETE FROM files WHERE id = ?`, id)
			_, _ = x.db.Exec(`DELETE FROM text WHERE rowid = ?`, id)
		}
		_, err := x.db.Exec(`INSERT INTO files(path, size, mtime, offset, harness, sid, cwd, title, named, first, last, created, updated, messages)
			VALUES(?, ?, ?, ?, ?, '', '', '', 0, '', '', 0, 0, 0)
			ON CONFLICT(path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, offset = excluded.offset, messages = 0`,
			f.path, f.size, f.mtime, offset, f.harness)
		return err
	}
	s.ID, s.Cwd = t.NativeID, t.Cwd
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(f.path), ".jsonl")
	}
	s.Created = t.Created
	if s.Updated.IsZero() {
		s.Updated = time.Unix(0, f.mtime)
	}
	if s.Created.IsZero() {
		s.Created = s.Updated
	}
	s.Title = cmpOr(oneLine(t.Title, 200), s.FirstPrompt)

	b := body.String()
	if len(b) > maxBody {
		b = b[:keepHead] + "\n…\n" + b[len(b)-(maxBody-keepHead):]
	}
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRow(`INSERT INTO files(path, size, mtime, offset, harness, sid, cwd, title, named, first, last, created, updated, messages)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, offset = excluded.offset,
			harness = excluded.harness, sid = excluded.sid, cwd = excluded.cwd, title = excluded.title, named = excluded.named,
			first = excluded.first, last = excluded.last, created = excluded.created, updated = excluded.updated,
			messages = excluded.messages
		RETURNING id`,
		f.path, f.size, f.mtime, offset, s.Harness, s.ID, s.Cwd, s.Title, t.Named, s.FirstPrompt, s.LastPrompt,
		s.Created.UnixMilli(), s.Updated.UnixMilli(), s.Messages).Scan(&id)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM text WHERE rowid = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO text(rowid, title, cwd, body) VALUES(?, ?, ?, ?)`, id, s.Title, s.Cwd, b); err != nil {
		return err
	}
	return tx.Commit()
}

// source is the source of harness's sessions.
func (x *Index) source(harness string) Source {
	for _, src := range x.src {
		if src.Harness == harness {
			return src
		}
	}
	return Source{}
}

// names applies the names agents keep apart from their sessions (Codex's
// session_index.jsonl, its own list, with renames appended). Every
// refresh: a session read since needs its name too, and they're short lists.
func (x *Index) names() error {
	for _, src := range x.src {
		if src.Names == nil {
			continue
		}
		for id, name := range src.Names() {
			var rowID int64
			var title string
			if x.db.QueryRow(`SELECT id, title FROM files WHERE harness = ? AND sid = ? AND messages > 0`, src.Harness, id).Scan(&rowID, &title) != nil {
				continue
			}
			if name == "" || name == title {
				continue
			}
			_, _ = x.db.Exec(`UPDATE files SET title = ?, named = 1 WHERE id = ?`, name, rowID)
			_, _ = x.db.Exec(`UPDATE text SET title = ? WHERE rowid = ?`, name, rowID)
		}
	}
	return nil
}

// Search finds sessions matching query (every word, as a prefix, in the
// title, folder or conversation), best first; an empty query lists the most
// recent. limit ≤ 0 means 50.
func (x *Index) Search(query string, limit int) ([]Session, error) {
	return x.SearchIn(query, "", nil, limit)
}

// SearchIn is Search among the sessions started in folder or inside it
// ("": all of them) and, unless among is nil, in or inside one of among's
// folders too (none, if it's empty).
func (x *Index) SearchIn(query, folder string, among []string, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 50
	}
	if among != nil && len(among) == 0 {
		return nil, nil
	}
	const cols = `f.harness, f.sid, f.path, f.cwd, f.title, f.first, f.last, f.created, f.updated, f.messages`
	// The folder itself, or a path under it (not a sibling sharing its prefix).
	const under = `(f.cwd = ? OR substr(f.cwd, 1, length(?) + 1) = ? || '/')`
	folder = strings.TrimRight(folder, "/")
	inFolder := `(? = '' OR ` + under + `)`
	in := []any{folder, folder, folder, folder}
	if among != nil {
		ors := make([]string, len(among))
		for i, f := range among {
			f = strings.TrimRight(f, "/")
			ors[i] = under
			in = append(in, f, f, f)
		}
		inFolder += ` AND (` + strings.Join(ors, ` OR `) + `)`
	}
	var rows *sql.Rows
	var err error
	if q := ftsQuery(query); q == "" {
		rows, err = x.db.Query(`SELECT `+cols+`, '' FROM files f WHERE f.messages > 0 AND `+inFolder+` ORDER BY f.updated DESC LIMIT ?`,
			append(in, limit)...)
	} else {
		// Title matches count most, then the folder, then the conversation.
		rows, err = x.db.Query(`SELECT `+cols+`, snippet(text, 2, '«', '»', '…', 12)
			FROM text JOIN files f ON f.id = text.rowid
			WHERE text MATCH ? AND f.messages > 0 AND `+inFolder+`
			ORDER BY bm25(text, 10.0, 3.0, 1.0) LIMIT ?`, append(append([]any{q}, in...), limit)...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var created, updated int64
		if err := rows.Scan(&s.Harness, &s.ID, &s.Path, &s.Cwd, &s.Title, &s.FirstPrompt, &s.LastPrompt, &created, &updated, &s.Messages, &s.Snippet); err != nil {
			return nil, err
		}
		s.Created, s.Updated = time.UnixMilli(created), time.UnixMilli(updated)
		s.Snippet = oneLine(s.Snippet, 240)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Get finds one session by harness and ID.
func (x *Index) Get(harness, id string) (Session, error) {
	var s Session
	var created, updated int64
	err := x.db.QueryRow(`SELECT harness, sid, path, cwd, title, first, last, created, updated, messages
		FROM files WHERE harness = ? AND sid = ? AND messages > 0 ORDER BY updated DESC LIMIT 1`, harness, id).
		Scan(&s.Harness, &s.ID, &s.Path, &s.Cwd, &s.Title, &s.FirstPrompt, &s.LastPrompt, &created, &updated, &s.Messages)
	if errors.Is(err, sql.ErrNoRows) {
		return s, fmt.Errorf("no %s session %q", harness, id)
	}
	s.Created, s.Updated = time.UnixMilli(created), time.UnixMilli(updated)
	return s, err
}

// ftsQuery turns what someone typed into an FTS5 query: each word as a
// prefix, all of them required. Punctuation separates words.
func ftsQuery(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	var parts []string
	for _, w := range words {
		parts = append(parts, `"`+strings.ToLower(w)+`"*`)
	}
	return strings.Join(parts, " ")
}

// noise: a user message that isn't something you typed (a slash command's
// echo, its output, an interruption marker, a reminder).
var noiseRE = regexp.MustCompile(`^(<(command-[a-z]+|local-command-[a-z]+|system-reminder|bash-[a-z]+|user-prompt-submit-hook|task-notification)[ >]|\[Request interrupted|Caveat: The messages below)`)

func noise(text string) bool { return text == "" || noiseRE.MatchString(text) }

// oneLine flattens whitespace and cuts to n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n-1]) + "…"
	}
	return s
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
