package agents

// OpenCode (and Kilo CLI, a fork of it) keep every session in one SQLite
// database (~/.local/share/opencode/opencode.db, ~/.local/share/kilo/kilo.db),
// not a file each, while the agent view and the session finder take a
// session to be a file. So a session's "file" here is a path under the
// database, as if it were a folder: <database>/<session id>. Stat (the
// database's size and time for that session), Open (its rows as
// agentview.OpenCodeLines) and Sessions (one path per session) read it.
//
// Two schemas, read only, never written:
//   - V1 (OpenCode 1.x, Kilo 7.x): session, message, part, each row's data
//     JSON (packages/opencode/src/session in sst/opencode 1.x;
//     packages/core/src/session/sql.ts in Kilo-Org/kilocode).
//   - V2 (OpenCode 2.x): session_v2, session_message (with type and seq
//     columns) and, since 2.2, session_part for a reply's content
//     (packages/core/src/session/sql.ts in anomalyco/opencode 2.2.5).
//     V2 keeps a V1 database's tables beside its own while it imports
//     them; with session_v2 there, only it is read.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
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

// openCodeSessionID is what an OpenCode session id looks like ("ses_" and an
// identifier), so a path's last element can be trusted as one.
var openCodeSessionID = regexp.MustCompile(`^ses_[A-Za-z0-9]+$`)

// openCodePlaceholderTitle is the title a session has until one is made for it
// ("New session - 2026-11-07T09:12:00.000Z"; a subagent's "… (@general
// subagent)").
var openCodePlaceholderTitle = regexp.MustCompile(`^(New session|Child session) - \d{4}-`)

// openCodeDBStore is one agent's databases: its data folders and the names its
// databases have there.
type openCodeDBStore struct {
	// dbName: name is one of its session databases.
	dbName func(name string) bool
}

// openCodeSplit splits a session path into its database and session id.
func openCodeSplit(path string) (db, id string, ok bool) {
	db, id = filepath.Dir(path), filepath.Base(path)
	return db, id, openCodeSessionID.MatchString(id)
}

// owns: path is a session in one of the store's databases.
func (s openCodeDBStore) owns(path string) bool {
	db, _, ok := openCodeSplit(path)
	return ok && s.dbName(filepath.Base(db))
}

// databases are the store's databases in roots.
func (s openCodeDBStore) databases(roots []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		entries, err := os.ReadDir(r)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(r, e.Name())
			if e.Type().IsRegular() && s.dbName(e.Name()) && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// sessions lists every top-level session (not subagents') in roots.
func (s openCodeDBStore) sessions(roots []string) []string {
	var out []string
	for _, db := range s.databases(roots) {
		c, err := openCodeOpen(db)
		if err != nil {
			continue
		}
		rows, err := c.db.Query(`SELECT id FROM ` + c.sessionTable() + ` WHERE parent_id IS NULL`)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil && openCodeSessionID.MatchString(id) {
				out = append(out, filepath.Join(db, id))
			}
		}
		rows.Close()
	}
	return out
}

// locate finds session id in roots.
func (s openCodeDBStore) locate(roots []string, id string) (string, bool) {
	if !openCodeSessionID.MatchString(id) {
		return "", false
	}
	for _, db := range s.databases(roots) {
		c, err := openCodeOpen(db)
		if err != nil {
			continue
		}
		var n int
		if c.db.QueryRow(`SELECT COUNT(*) FROM `+c.sessionTable()+` WHERE id = ?`, id).Scan(&n) == nil && n > 0 {
			return filepath.Join(db, id), true
		}
	}
	return "", false
}

// newest is the latest top-level session begun in cwd.
func (s openCodeDBStore) newest(roots []string, cwd string) (string, bool) {
	best, bestAt := "", int64(-1)
	for _, db := range s.databases(roots) {
		c, err := openCodeOpen(db)
		if err != nil {
			continue
		}
		var id string
		var at int64
		err = c.db.QueryRow(`SELECT id, time_updated FROM `+c.sessionTable()+` WHERE parent_id IS NULL AND directory = ? ORDER BY time_updated DESC LIMIT 1`, cwd).Scan(&id, &at)
		if err == nil && at > bestAt && openCodeSessionID.MatchString(id) {
			best, bestAt = filepath.Join(db, id), at
		}
	}
	return best, best != ""
}

// openCodeConn is an open database and what's in it.
type openCodeConn struct {
	db *sql.DB
	v2 bool // session_v2 (OpenCode 2.x), else V1's tables
	// parts: V2's session_part table (2.2 on) is there.
	parts   bool
	checked time.Time
}

func (c *openCodeConn) sessionTable() string {
	if c.v2 {
		return "session_v2"
	}
	return "session"
}

var openCodeConns = struct {
	sync.Mutex
	m map[string]*openCodeConn
}{m: map[string]*openCodeConn{}}

// openCodeOpen opens db read only, once (the agent view asks about a session
// several times a second), seeing again now and then which schema it has:
// OpenCode 2 adds its tables to a 1.x database it's upgraded over.
func openCodeOpen(db string) (*openCodeConn, error) {
	openCodeConns.Lock()
	defer openCodeConns.Unlock()
	c, ok := openCodeConns.m[db]
	if ok && time.Since(c.checked) < time.Minute {
		return c, nil
	}
	if !ok {
		if st, err := os.Stat(db); err != nil || !st.Mode().IsRegular() {
			return nil, fs.ErrNotExist
		}
		d, err := sql.Open("sqlite", "file:"+db+"?mode=ro&_pragma=busy_timeout(2000)&_pragma=query_only(1)")
		if err != nil {
			return nil, err
		}
		d.SetMaxOpenConns(2)
		c = &openCodeConn{db: d}
	}
	tables := map[string]bool{}
	rows, err := c.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				tables[n] = true
			}
		}
		rows.Close()
	}
	if !tables["session_v2"] && !(tables["session"] && tables["message"] && tables["part"]) {
		c.db.Close()
		delete(openCodeConns.m, db)
		if err == nil {
			err = errors.New(db + ": not an OpenCode database")
		}
		return nil, err
	}
	// A new value, not c changed: others may be reading c.
	c = &openCodeConn{db: c.db, v2: tables["session_v2"], parts: tables["session_part"], checked: time.Now()}
	openCodeConns.m[db] = c
	return c, nil
}

// openCodeStat is a session's fingerprint as a file's: a size (its rows and their
// data's length) and a time (its latest change), which change whenever the
// session does.
func openCodeStat(path string) (fs.FileInfo, error) {
	db, id, ok := openCodeSplit(path)
	if !ok {
		return nil, fs.ErrNotExist
	}
	c, err := openCodeOpen(db)
	if err != nil {
		return nil, err
	}
	var exists int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM `+c.sessionTable()+` WHERE id = ?`, id).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, fs.ErrNotExist
	}
	tables := []string{"message", "part"}
	if c.v2 {
		tables = []string{"session_message"}
		if c.parts {
			tables = append(tables, "session_part")
		}
	}
	var size, mod int64
	for _, t := range tables {
		var n, length, updated int64
		err := c.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(LENGTH(data)), 0), COALESCE(MAX(time_updated), 0) FROM `+t+` WHERE session_id = ?`, id).Scan(&n, &length, &updated)
		if err != nil {
			return nil, err
		}
		size += n + length
		mod = max(mod, updated)
	}
	var updated int64
	_ = c.db.QueryRow(`SELECT time_updated FROM `+c.sessionTable()+` WHERE id = ?`, id).Scan(&updated)
	return openCodeInfo{name: id, size: size, mod: time.UnixMilli(max(mod, updated))}, nil
}

type openCodeInfo struct {
	name string
	size int64
	mod  time.Time
}

func (i openCodeInfo) Name() string       { return i.name }
func (i openCodeInfo) Size() int64        { return i.size }
func (i openCodeInfo) Mode() fs.FileMode  { return 0o400 }
func (i openCodeInfo) ModTime() time.Time { return i.mod }
func (i openCodeInfo) IsDir() bool        { return false }
func (i openCodeInfo) Sys() any           { return nil }

// openCodeOpenSession reads a session from its database as OpenCodeLines.
func openCodeOpenSession(path string) (io.ReadCloser, error) {
	db, id, ok := openCodeSplit(path)
	if !ok {
		return nil, fs.ErrNotExist
	}
	c, err := openCodeOpen(db)
	if err != nil {
		return nil, err
	}
	var lines []agentview.OpenCodeLine
	if c.v2 {
		lines, err = c.readV2(id)
	} else {
		lines, err = c.readV1(id)
	}
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, l := range lines {
		_ = enc.Encode(l)
	}
	return io.NopCloser(&buf), nil
}

func (c *openCodeConn) header(id string) (*agentview.OpenCodeSession, error) {
	var s agentview.OpenCodeSession
	var title, parent sql.NullString
	err := c.db.QueryRow(`SELECT id, directory, title, time_created, parent_id FROM `+c.sessionTable()+` WHERE id = ?`, id).
		Scan(&s.ID, &s.Cwd, &title, &s.Created, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	s.Title, s.Parent = title.String, parent.String
	return &s, nil
}

// readV2 reads OpenCode 2.x's session_message rows (a reply's content in
// its session_part rows, else inline as before 2.2).
func (c *openCodeConn) readV2(id string) ([]agentview.OpenCodeLine, error) {
	h, err := c.header(id)
	if err != nil {
		return nil, err
	}
	out := []agentview.OpenCodeLine{{Session: h}}
	parts := map[string][]json.RawMessage{}
	if c.parts {
		rows, err := c.db.Query(`SELECT message_id, type, data FROM session_part WHERE session_id = ? ORDER BY message_id, ordinal`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var mid, typ string
			var data []byte
			if rows.Scan(&mid, &typ, &data) == nil {
				parts[mid] = append(parts[mid], openCodeWithType(data, typ))
			}
		}
		rows.Close()
	}
	rows, err := c.db.Query(`SELECT id, type, time_created, data FROM session_message WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid, typ string
		var at int64
		var data []byte
		if rows.Scan(&mid, &typ, &at, &data) != nil {
			continue
		}
		if l, ok := openCodeV2Line(mid, typ, at, data, parts[mid]); ok {
			out = append(out, l)
		}
	}
	return out, rows.Err()
}

// openCodeWithType puts a part row's type column back into its data.
func openCodeWithType(data []byte, typ string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return data
	}
	if _, ok := m["type"]; !ok {
		t, _ := json.Marshal(typ)
		m["type"] = t
	}
	b, _ := json.Marshal(m)
	return b
}

type openCodeTokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

func (t *openCodeTokens) total() int64 {
	if t == nil {
		return 0
	}
	return t.Input + t.Output + t.Reasoning + t.Cache.Read + t.Cache.Write
}

type openCodeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// openCodeV2Line is one session_message row (data without its id and type, which
// are columns) as a line.
func openCodeV2Line(id, typ string, at int64, data []byte, parts []json.RawMessage) (agentview.OpenCodeLine, bool) {
	var m struct {
		Text        string            `json:"text"`
		Description string            `json:"description"`
		Name        string            `json:"name"`
		Command     string            `json:"command"`
		Status      string            `json:"status"`
		Outcome     string            `json:"outcome"`
		Error       *openCodeError    `json:"error"`
		Tokens      *openCodeTokens   `json:"tokens"`
		Content     []json.RawMessage `json:"content"`
		Output      *struct {
			Output string `json:"output"`
		} `json:"output"`
		Model *struct {
			ID string `json:"id"`
		} `json:"model"`
	}
	if json.Unmarshal(data, &m) != nil {
		return agentview.OpenCodeLine{}, false
	}
	l := agentview.OpenCodeLine{ID: id, Time: at}
	switch typ {
	case "user":
		l.Role, l.Text = "user", m.Text
	case "assistant":
		l.Role, l.Tokens = "assistant", m.Tokens.total()
		if m.Model != nil {
			l.Model = m.Model.ID
		}
		if m.Error != nil && m.Error.Message != "" {
			l.Error = m.Error.Message
		}
		for _, raw := range append(parts, m.Content...) {
			if p, ok := openCodeV2Part(raw); ok {
				l.Parts = append(l.Parts, p)
			}
		}
	case "shell":
		l.Role, l.Text, l.Status = "shell", m.Command, m.Status
		if m.Output != nil {
			l.Output = m.Output.Output
		}
	case "model-switched":
		if m.Model == nil {
			return l, false
		}
		l.Model = m.Model.ID
	case "compaction":
		if m.Status != "completed" {
			return l, false
		}
		l.Role, l.Text = "note", "Conversation compacted"
	case "skill":
		l.Role, l.Text = "note", "Loaded the "+m.Name+" skill"
	case "idle":
		switch m.Outcome {
		case "interrupted":
			l.Role, l.Text = "note", "Interrupted"
		case "failed":
			l.Role, l.Text = "note", "Failed"
			if m.Error != nil && m.Error.Message != "" {
				l.Text = "Failed: " + m.Error.Message
			}
		default:
			return l, false
		}
	default:
		// agent-switched, location-switched, synthetic and system text
		// meant for the model.
		return l, false
	}
	return l, true
}

// openCodeV2Part is an assistant content part: text, reasoning, or a tool with its
// state (streaming, running, completed, error).
func openCodeV2Part(raw json.RawMessage) (agentview.OpenCodePart, bool) {
	var p struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Text  string `json:"text"`
		Name  string `json:"name"`
		State struct {
			Status  string          `json:"status"`
			Input   json.RawMessage `json:"input"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Error *openCodeError `json:"error"`
		} `json:"state"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return agentview.OpenCodePart{}, false
	}
	switch p.Type {
	case "text", "reasoning":
		return agentview.OpenCodePart{Type: p.Type, Text: p.Text}, true
	case "tool":
		out := agentview.OpenCodePart{ID: p.ID, Type: "tool", Tool: p.Name, Status: p.State.Status}
		if in := bytes.TrimSpace(p.State.Input); len(in) > 0 && in[0] == '{' {
			out.Input = in // a streaming call's input is still a string
		}
		var text []string
		for _, c := range p.State.Content {
			if c.Type == "text" {
				text = append(text, c.Text)
			}
		}
		out.Output = strings.Join(text, "\n")
		if p.State.Error != nil {
			out.Error = p.State.Error.Message
		}
		return out, true
	}
	return agentview.OpenCodePart{}, false
}

// readV1 reads OpenCode 1.x's and Kilo's message and part rows: a message's
// text is in its parts, a user's too.
func (c *openCodeConn) readV1(id string) ([]agentview.OpenCodeLine, error) {
	h, err := c.header(id)
	if err != nil {
		return nil, err
	}
	out := []agentview.OpenCodeLine{{Session: h}}
	parts := map[string][]openCodeV1Part{}
	rows, err := c.db.Query(`SELECT id, message_id, data FROM part WHERE session_id = ? ORDER BY message_id, id`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var pid, mid string
		var data []byte
		if rows.Scan(&pid, &mid, &data) != nil {
			continue
		}
		var p openCodeV1Part
		if json.Unmarshal(data, &p) == nil {
			p.id = pid
			parts[mid] = append(parts[mid], p)
		}
	}
	rows.Close()
	rows, err = c.db.Query(`SELECT id, time_created, data FROM message WHERE session_id = ? ORDER BY time_created, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid string
		var at int64
		var data []byte
		if rows.Scan(&mid, &at, &data) != nil {
			continue
		}
		out = append(out, openCodeV1Lines(mid, at, data, parts[mid])...)
	}
	return out, rows.Err()
}

type openCodeV1Part struct {
	id        string
	Type      string `json:"type"`
	Text      string `json:"text"`
	Synthetic bool   `json:"synthetic"`
	Ignored   bool   `json:"ignored"`
	CallID    string `json:"callID"`
	Tool      string `json:"tool"`
	State     struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Error  string          `json:"error"`
	} `json:"state"`
}

// openCodeV1Lines is one message row and its parts as lines: a user's message (and
// a compaction it asked for), or a reply.
func openCodeV1Lines(id string, at int64, data []byte, parts []openCodeV1Part) []agentview.OpenCodeLine {
	var m struct {
		Role    string          `json:"role"`
		ModelID string          `json:"modelID"`
		Tokens  *openCodeTokens `json:"tokens"`
		Error   *struct {
			Name string `json:"name"`
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	switch m.Role {
	case "user":
		var text []string
		var out []agentview.OpenCodeLine
		for _, p := range parts {
			switch {
			case p.Type == "text" && !p.Synthetic && !p.Ignored:
				text = append(text, p.Text)
			case p.Type == "compaction":
				out = append(out, agentview.OpenCodeLine{ID: id + "/" + p.id, Role: "note", Time: at, Text: "Conversation compacted"})
			}
		}
		if t := strings.Join(text, "\n"); strings.TrimSpace(t) != "" {
			out = append([]agentview.OpenCodeLine{{ID: id, Role: "user", Time: at, Text: t}}, out...)
		}
		return out
	case "assistant":
		l := agentview.OpenCodeLine{ID: id, Role: "assistant", Time: at, Model: m.ModelID, Tokens: m.Tokens.total()}
		if e := m.Error; e != nil {
			switch {
			case e.Name == "MessageAbortedError":
				l.Error = "Interrupted"
			case e.Data.Message != "":
				l.Error = e.Data.Message
			default:
				l.Error = e.Name
			}
		}
		for _, p := range parts {
			switch p.Type {
			case "text", "reasoning":
				if !p.Synthetic && !p.Ignored {
					l.Parts = append(l.Parts, agentview.OpenCodePart{ID: p.id, Type: p.Type, Text: p.Text})
				}
			case "tool":
				l.Parts = append(l.Parts, agentview.OpenCodePart{ID: p.id, Type: "tool", Tool: p.Tool, Status: p.State.Status,
					Input: p.State.Input, Output: p.State.Output, Error: p.State.Error})
			}
		}
		return []agentview.OpenCodeLine{l}
	}
	return nil
}

// openCodeIndex reads a session's lines for the session finder.
func openCodeIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	dec := json.NewDecoder(r)
	for {
		var l agentview.OpenCodeLine
		if err := dec.Decode(&l); err == io.EOF {
			return 0, nil
		} else if err != nil {
			return 0, err
		}
		at := time.UnixMilli(l.Time)
		switch {
		case l.Session != nil:
			t.NativeID, t.Cwd = l.Session.ID, l.Session.Cwd
			if !openCodePlaceholderTitle.MatchString(l.Session.Title) {
				t.Title = l.Session.Title
			}
			if l.Session.Created > 0 {
				t.Created = time.UnixMilli(l.Session.Created)
			}
		case l.Role == "user":
			emit(thread.Record{Kind: thread.KindUser, Time: at, Text: l.Text})
		case l.Role == "assistant":
			var text []string
			for _, p := range l.Parts {
				if p.Type == "text" {
					text = append(text, p.Text)
				}
			}
			if len(text) > 0 {
				emit(thread.Record{Kind: thread.KindAssistant, Time: at, Text: strings.Join(text, "\n")})
			}
		}
	}
}
