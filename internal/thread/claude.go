package thread

// Claude Code session codec. The on-disk format and the rules for writing a
// session `claude --resume` accepts follow Atuin's implementation
// (github.com/atuinsh/atuin, crates/atuin-common/src/harnesstools/ccode,
// MIT licensed): ~/.claude/projects/<project>/<session-id>.jsonl, one JSON
// object per line, a parentUuid chain, no thinking blocks, strictly
// alternating user/assistant turns, and every tool_use answered by a
// tool_result in the following user line.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Claude reads and writes Claude Code sessions.
type Claude struct {
	// Home overrides the config dir (default $CLAUDE_CONFIG_DIR or ~/.claude).
	Home string
}

func (Claude) Harness() string { return "claude" }

func (c Claude) configDir() string {
	if c.Home != "" {
		return c.Home
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

type ccLine struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	SessionID   string          `json:"sessionId"`
	Cwd         string          `json:"cwd"`
	Timestamp   string          `json:"timestamp"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	IsCompact   bool            `json:"isCompactSummary"`
	Message     json.RawMessage `json:"message"`
	AITitle     string          `json:"aiTitle"`
	CustomTitle string          `json:"customTitle"`
	Summary     string          `json:"summary"`
}

type ccMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type ccBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Decode reads a Claude Code session's main chain into neutral records.
func (c Claude) Decode(r io.Reader) (Thread, []Record, error) {
	t := Thread{Harness: "claude"}
	var recs []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		ClaudeLine(sc.Bytes(), &t, func(r Record) { recs = append(recs, r) })
	}
	return t, recs, sc.Err()
}

// claudeEcho: user lines Claude Code writes for a slash command (its name
// and arguments, its output) and other injected text, not your words.
var claudeEcho = regexp.MustCompile(`^\s*(<(command-name|command-message|command-args|local-command-stdout|local-command-stderr|local-command-caveat|bash-input|bash-stdout|bash-stderr|system-reminder|task-notification|user-prompt-submit-hook)>|\[Request interrupted|Caveat: The messages below)`)

// ClaudeLine reads one line of a Claude Code session: what it says about the
// session goes into t, and its records to emit. Decode is this over every
// line; the session index (internal/sessions) uses it to read only what's
// been added since it last looked.
func ClaudeLine(line []byte, t *Thread, emit func(Record)) {
	var l ccLine
	if json.Unmarshal(line, &l) != nil {
		return
	}
	if t.NativeID == "" && l.SessionID != "" {
		t.NativeID = l.SessionID
	}
	if t.Cwd == "" && l.Cwd != "" {
		t.Cwd = l.Cwd
	}
	switch l.Type {
	case "ai-title":
		// Claude keeps generating titles after a /rename; the name you gave
		// it wins, as in its own resume list.
		if !t.Named {
			t.Title = l.AITitle
		}
		return
	case "custom-title":
		t.Title, t.Named = l.CustomTitle, l.CustomTitle != ""
		return
	case "summary":
		if t.Title == "" {
			t.Title = l.Summary
		}
		return
	case "user", "assistant":
	default:
		return
	}
	if l.IsSidechain || l.IsMeta || l.IsCompact || len(l.Message) == 0 {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
	if t.Created.IsZero() {
		t.Created = at
	}
	var m ccMessage
	if json.Unmarshal(l.Message, &m) != nil {
		return
	}
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		if l.Type == "user" && claudeEcho.MatchString(text) {
			return // a slash command's echo or output, not something you said
		}
		if l.Type == "user" && strings.TrimSpace(text) != "" {
			emit(Record{Kind: KindUser, Time: at, Text: text})
		} else if l.Type == "assistant" && text != "" {
			emit(Record{Kind: KindAssistant, Time: at, Text: text})
		}
		return
	}
	var blocks []ccBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			kind := KindAssistant
			if l.Type == "user" {
				kind = KindUser
			}
			emit(Record{Kind: kind, Time: at, Text: b.Text})
		case "thinking", "redacted_thinking":
			emit(Record{Kind: KindReasoning, Time: at})
		case "tool_use":
			emit(claudeToolCall(b, at))
		case "tool_result":
			emit(Record{Kind: KindToolResult, Time: at, CallID: b.ToolUseID, Text: Clip(blockText(b.Content)), IsError: b.IsError})
		}
	}
}

// blockText flattens a tool_result's content (a string or text blocks).
func blockText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []ccBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

func claudeToolCall(b ccBlock, at time.Time) Record {
	r := Record{Kind: KindToolCall, Time: at, CallID: b.ID, NativeTool: b.Name, Input: clipJSON(b.Input)}
	var in struct {
		Command   string `json:"command"`
		FilePath  string `json:"file_path"`
		Content   string `json:"content"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
		Pattern   string `json:"pattern"`
		Path      string `json:"path"`
		URL       string `json:"url"`
	}
	_ = json.Unmarshal(b.Input, &in)
	switch b.Name {
	case "Bash":
		r.Tool, r.Input = ToolShell, mustJSON(map[string]string{"command": in.Command})
	case "Read":
		r.Tool, r.Input = ToolRead, mustJSON(map[string]string{"path": in.FilePath})
	case "Write":
		r.Tool, r.Edit = ToolWrite, &Edit{Path: in.FilePath, New: Clip(in.Content)}
		r.Input = mustJSON(map[string]string{"path": in.FilePath})
	case "Edit":
		r.Tool, r.Edit = ToolEdit, &Edit{Path: in.FilePath, Old: Clip(in.OldString), New: Clip(in.NewString)}
		r.Input = mustJSON(map[string]string{"path": in.FilePath})
	case "Grep", "Glob":
		r.Tool, r.Input = ToolSearch, mustJSON(map[string]string{"pattern": in.Pattern, "path": in.Path})
	case "WebFetch":
		r.Tool, r.Input = ToolWebFetch, mustJSON(map[string]string{"url": in.URL})
	}
	return r
}

// claudeNative are Claude Code's own tool names; foreign tools with these
// names are prefixed so they can't be mistaken for them.
var claudeNative = map[string]bool{
	"Bash": true, "Read": true, "Write": true, "Edit": true, "MultiEdit": true, "Grep": true, "Glob": true,
	"WebFetch": true, "WebSearch": true, "Task": true, "TodoWrite": true, "NotebookEdit": true,
}

// Encode writes records as a new Claude Code session.
func (c Claude) Encode(t Thread, recs []Record) (string, string, []byte, error) {
	if t.Cwd == "" {
		return "", "", nil, fmt.Errorf("claude: session needs a working directory")
	}
	sid := uuid4()
	w := &ccWriter{sid: sid, cwd: t.Cwd, at: time.Now().UTC().Add(-time.Duration(len(recs)+2) * time.Millisecond), callIDs: map[string]string{}}
	w.metaUser(continuationMarker(t, recs))

	var asst []any
	var results []any
	pending := map[string]bool{} // tool_use ids awaiting results, in the current assistant line
	order := []string{}
	used := map[string]int{}
	flush := func() {
		if len(asst) > 0 {
			stop := "end_turn"
			if len(pending) > 0 {
				stop = "tool_use"
			}
			w.assistant(asst, stop)
			asst = nil
		}
		for _, id := range order {
			if pending[id] {
				results = append(results, map[string]any{"type": "tool_result", "tool_use_id": id, "content": "[no result was recorded]", "is_error": false})
			}
		}
		if len(results) > 0 {
			w.userBlocks(results)
			results = nil
		}
		pending, order = map[string]bool{}, nil
	}
	sawUser := false
	for _, r := range recs {
		switch r.Kind {
		case KindUser:
			flush()
			w.userText(r.Text)
			sawUser = true
		case KindAssistant, KindNote:
			if !sawUser {
				w.userText("[continuing an earlier session]")
				sawUser = true
			}
			if len(results) > 0 {
				flush()
			}
			text := r.Text
			if r.Kind == KindNote {
				text = "[" + text + "]"
			}
			asst = append(asst, map[string]any{"type": "text", "text": text})
		case KindToolCall:
			if !sawUser {
				w.userText("[continuing an earlier session]")
				sawUser = true
			}
			if len(results) > 0 {
				flush()
			}
			name, input := claudeTool(r, t.Source)
			id := uniqueID(sanitize(r.CallID, "toolu"), used)
			asst = append(asst, map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
			pending[id] = true
			order = append(order, id)
			w.callIDs[r.CallID] = id
		case KindToolResult:
			id, ok := w.callIDs[r.CallID]
			if !ok || !pending[id] {
				continue // result for a call we couldn't carry over
			}
			delete(pending, id)
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": id, "content": r.Text, "is_error": r.IsError})
		}
	}
	flush()
	// Its name carries over as a name (/rename's custom-title); otherwise a
	// title from its first prompt, which Claude replaces with its own.
	switch {
	case t.Named && t.Title != "":
		w.raw(map[string]any{"type": "custom-title", "customTitle": t.Title, "sessionId": sid})
	case t.Title != "":
		w.raw(map[string]any{"type": "ai-title", "aiTitle": t.Title, "sessionId": sid})
	default:
		for _, r := range recs {
			if r.Kind == KindUser && strings.TrimSpace(r.Text) != "" {
				w.raw(map[string]any{"type": "ai-title", "aiTitle": clipText(firstLine(r.Text), 60), "sessionId": sid})
				break
			}
		}
	}
	path := filepath.Join(c.configDir(), "projects", claudeProjectDir(t.Cwd), sid+".jsonl")
	return sid, path, w.buf.Bytes(), nil
}

func (Claude) ResumeCommand(id string) []string { return []string{"claude", "--resume", id} }

// claudeTool maps a record to a Claude Code tool name and input object.
func claudeTool(r Record, from string) (string, any) {
	var nin struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		URL     string `json:"url"`
	}
	_ = json.Unmarshal(r.Input, &nin)
	switch r.Tool {
	case ToolShell:
		return "Bash", map[string]string{"command": nin.Command}
	case ToolRead:
		return "Read", map[string]string{"file_path": nin.Path}
	case ToolWrite:
		if r.Edit != nil {
			return "Write", map[string]string{"file_path": r.Edit.Path, "content": r.Edit.New}
		}
	case ToolEdit:
		if r.Edit != nil {
			return "Edit", map[string]string{"file_path": r.Edit.Path, "old_string": r.Edit.Old, "new_string": r.Edit.New}
		}
	case ToolSearch:
		return "Grep", map[string]string{"pattern": nin.Pattern, "path": nin.Path}
	case ToolWebFetch:
		return "WebFetch", map[string]string{"url": nin.URL}
	}
	name := sanitize(r.NativeTool, "tool")
	if claudeNative[name] && from != "claude" {
		name = sanitize(from+"_"+name, "tool")
	}
	var obj map[string]any
	if json.Unmarshal(r.Input, &obj) == nil && obj != nil {
		return name, obj
	}
	var s string
	if json.Unmarshal(r.Input, &s) == nil {
		return name, map[string]string{"input": s}
	}
	return name, map[string]string{"input": string(r.Input)}
}

type ccWriter struct {
	buf     bytes.Buffer
	sid     string
	cwd     string
	parent  string
	at      time.Time
	callIDs map[string]string
}

func (w *ccWriter) base(typ string) map[string]any {
	id := uuid4()
	w.at = w.at.Add(time.Millisecond)
	m := map[string]any{
		"parentUuid": nil, "isSidechain": false, "userType": "external", "cwd": w.cwd,
		"sessionId": w.sid, "type": typ, "uuid": id, "timestamp": w.at.Format("2006-01-02T15:04:05.000Z"),
	}
	if w.parent != "" {
		m["parentUuid"] = w.parent
	}
	w.parent = id
	return m
}

func (w *ccWriter) raw(m map[string]any) {
	b, _ := json.Marshal(m)
	w.buf.Write(b)
	w.buf.WriteByte('\n')
}

func (w *ccWriter) metaUser(text string) {
	m := w.base("user")
	m["isMeta"] = true
	m["message"] = map[string]any{"role": "user", "content": text}
	w.raw(m)
}

func (w *ccWriter) userText(text string) {
	m := w.base("user")
	m["message"] = map[string]any{"role": "user", "content": text}
	w.raw(m)
}

func (w *ccWriter) userBlocks(blocks []any) {
	m := w.base("user")
	m["message"] = map[string]any{"role": "user", "content": blocks}
	w.raw(m)
}

func (w *ccWriter) assistant(blocks []any, stop string) {
	m := w.base("assistant")
	m["message"] = map[string]any{
		"id": "msg_hammock_" + strings.ReplaceAll(uuid4(), "-", ""), "type": "message", "role": "assistant",
		"content": blocks, "stop_reason": stop, "stop_sequence": nil,
	}
	w.raw(m)
}

// claudeProjectDir encodes a cwd the way Claude Code names project dirs:
// every UTF-16 unit that isn't an ASCII letter or digit becomes '-'; names
// over 200 units are cut and suffixed with a base-36 Java String.hashCode.
func claudeProjectDir(cwd string) string {
	units := utf16.Encode([]rune(cwd))
	out := make([]byte, len(units))
	for i, u := range units {
		if (u >= 'a' && u <= 'z') || (u >= 'A' && u <= 'Z') || (u >= '0' && u <= '9') {
			out[i] = byte(u)
		} else {
			out[i] = '-'
		}
	}
	if len(out) <= 200 {
		return string(out)
	}
	var h int32
	for _, u := range units {
		h = 31*h + int32(u)
	}
	abs := int64(h)
	if abs < 0 {
		abs = -abs
	}
	return string(out[:200]) + "-" + strconv.FormatInt(abs, 36)
}

func continuationMarker(t Thread, recs []Record) string {
	return fmt.Sprintf("Continued from a %s session (Hammock thread %s). The conversation so far was carried over from that agent: "+
		"tool calls are shown with their results, but the previous agent's private reasoning is not included and some long tool "+
		"output was shortened. Check the working tree before relying on earlier results.", orDefault(t.Source, "another agent"), t.Parent)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

func sanitize(s, fallback string) string {
	s = unsafeName.ReplaceAllString(s, "_")
	if s == "" {
		s = fallback
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func uniqueID(id string, used map[string]int) string {
	if n := used[id]; n > 0 {
		used[id] = n + 1
		return sanitize(fmt.Sprintf("%s_%d", id, n+1), id)
	}
	used[id] = 1
	return id
}

func uuid4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// clipJSON bounds a raw JSON value; oversized values become a clipped string.
func clipJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) <= MaxIO {
		return raw
	}
	return mustJSON(Clip(string(raw)))
}

// Locate finds a session's transcript in any project dir.
// Newest is the most recently written session file for cwd, for agents
// whose session id isn't known (no hooks).
func (c Claude) Newest(cwd string) (string, bool) {
	m, _ := filepath.Glob(filepath.Join(c.configDir(), "projects", claudeProjectDir(cwd), "*.jsonl"))
	best, bestT := "", int64(0)
	for _, f := range m {
		if st, err := os.Stat(f); err == nil && st.ModTime().UnixNano() > bestT {
			best, bestT = f, st.ModTime().UnixNano()
		}
	}
	return best, best != ""
}

func (c Claude) Locate(id string) (string, bool) {
	m, _ := filepath.Glob(filepath.Join(c.configDir(), "projects", "*", id+".jsonl"))
	if len(m) == 1 {
		return m[0], true
	}
	return "", false
}
