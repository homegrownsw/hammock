package agentview

// Cursor's CLI agent's transcripts (~/.cursor/projects/<cwd slug>/
// agent-transcripts/<id>/<id>.jsonl) as the agent view's blocks.
//
// The format, from its bundle (2026.11.05-86047df, the agent-transcript
// package's TranscriptStore) and what people have posted of real ones
// (forum.cursor.com/t/166592): one line per message,
//
//	{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>…</timestamp>\n<user_query>\n…\n</user_query>"}]}}
//	{"role":"assistant","message":{"content":[{"type":"text","text":"…"},{"type":"tool_use","name":"Shell","input":{"command":"ls"}}]}}
//
// and now and then {"type":"turn_ended","status":"success|error|aborted",
// "error":…} or {"type":"metadata",…}. There are no ids, times (but the
// one in your message), tool results or token counts; its thinking, when
// kept, is in the reply's text. So tool calls show what they did, never
// what they printed, and none is ever pending.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Cursor)(nil)

type Cursor struct {
	Cwd string

	blocks []*pb.AgentBlock
	files  map[string]bool
	turn   string
	n      int // lines read: blocks' ids, as lines have none
}

func NewCursor(cwd string) *Cursor { return &Cursor{Cwd: cwd, files: map[string]bool{}} }

func (c *Cursor) Blocks() []*pb.AgentBlock        { return c.blocks }
func (c *Cursor) Pending() *pb.AgentBlock         { return nil }
func (c *Cursor) Image(string) (Image, bool)      { return Image{}, false }
func (c *Cursor) Referenced(path string) bool     { return c.files[filepath.Clean(path)] }
func (c *Cursor) Rewound() bool                   { return false }
func (c *Cursor) Context() (string, int64, int64) { return "", 0, 0 }

type cuLine struct {
	Type    string `json:"type"`
	Cwd     string `json:"cwd"` // Hammock's hammock_session line (agents.openCursor)
	Role    string `json:"role"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Message struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

func (c *Cursor) Feed(line []byte) []*pb.AgentBlock {
	c.n++
	var l cuLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	id := func(i int) string { return fmt.Sprintf("cu%d.%d", c.n, i) }
	switch l.Type {
	case "hammock_session":
		if c.Cwd == "" {
			c.Cwd = l.Cwd
		}
		return nil
	case "turn_ended":
		switch l.Status {
		case "aborted":
			return c.add(&pb.AgentBlock{Id: id(0), Kind: pb.AgentBlock_KIND_NOTE, Text: "Interrupted"})
		case "error":
			return c.add(&pb.AgentBlock{Id: id(0), Kind: pb.AgentBlock_KIND_NOTE, Text: strings.TrimSpace("Stopped with an error: " + firstLine(l.Error))})
		}
		return nil
	}
	var out []*pb.AgentBlock
	for i, item := range l.Message.Content {
		switch {
		case item.Type == "text" && l.Role == "user":
			text, at := CursorUserText(item.Text)
			if text == "" {
				continue
			}
			c.turn = id(i)
			c.mentions(text)
			out = append(out, c.add(&pb.AgentBlock{Id: id(i), Kind: pb.AgentBlock_KIND_USER, Text: text, TimeUnixMs: unixMs(at), TurnId: id(i)})...)
		case item.Type == "text" && l.Role == "assistant":
			if s := strings.TrimSpace(item.Text); s != "" {
				c.mentions(s)
				out = append(out, c.add(&pb.AgentBlock{Id: id(i), Kind: pb.AgentBlock_KIND_TEXT, Text: s})...)
			}
		case item.Type == "tool_use":
			b := c.tool(item.Name, item.Input)
			b.Id = id(i)
			out = append(out, c.add(b)...)
		}
	}
	return out
}

func unixMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// tool is one of its tool calls as a block. Its tools' names and inputs
// are its servers' (Shell, Read, Write, StrReplace, Delete, Grep, Glob,
// TodoWrite, Task, AskQuestion, MCP tools…), so older and other spellings
// are read too.
func (c *Cursor) tool(name string, raw json.RawMessage) *pb.AgentBlock {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	path := func() string { return c.path(str("path", "file_path", "target_file", "filePath", "target_notebook")) }
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	switch strings.ToLower(strings.ReplaceAll(name, "_", "")) {
	case "shell", "runterminalcmd", "bash", "terminal":
		cmd := str("command", "cmd")
		c.mentions(cmd)
		b.Input, b.Title = cmd, "Ran `"+firstLine(cmd)+"`"
	case "read", "readfile":
		p := path()
		b.Input, b.Title = p, "Read "+p
	case "write", "createfile":
		p, content := path(), str("contents", "content", "file_text")
		b.Input, b.Title, b.Diff, b.Added = p, "Wrote "+p, clip(addedDiff(content), maxDiff), int32(lines(content))
	case "strreplace", "searchreplace", "edit", "editfile", "multiedit", "editnotebook":
		p := path()
		b.Input, b.Title = p, "Edited "+p
		if old, nw := str("old_string", "old_str", "oldString"), str("new_string", "new_str", "newString"); old != "" || nw != "" {
			b.Diff, b.Added, b.Removed = clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		} else if code := str("code_edit", "contents", "new_source"); code != "" {
			b.Diff, b.Added = clip(addedDiff(code), maxDiff), int32(lines(code))
		}
	case "applypatch":
		p := str("patch", "input")
		b.Input, b.Title, b.Diff = p, "Edited files", clip(p, maxDiff)
	case "delete", "deletefile":
		p := path()
		b.Input, b.Title = p, "Deleted "+p
	case "grep", "rg", "ripgrep", "grepsearch":
		q := str("pattern", "query", "regex")
		b.Input, b.Title = q, "Searched for "+strconv.Quote(q)
		if p := c.path(str("path", "target_directory")); p != "" {
			b.Title += " in " + p
		}
	case "glob", "globfilesearch":
		q := str("glob_pattern", "pattern", "glob")
		b.Input, b.Title = q, "Found files matching "+q
	case "ls", "listdir", "list":
		p := c.path(str("target_directory", "path", "directory"))
		b.Input, b.Title = p, "Listed "+cmpOr(p, "the folder")
	case "semanticsearch", "codebasesearch":
		q := str("query")
		b.Input, b.Title = q, "Searched the code for "+strconv.Quote(q)
	case "websearch":
		q := str("search_term", "query")
		b.Input, b.Title = q, "Searched the web for "+strconv.Quote(q)
	case "webfetch", "fetch":
		u := str("url")
		b.Input, b.Title = u, "Fetched "+u
	case "todowrite", "updatetodos":
		b.Input, b.Title = todos(in["todos"]), "Updated the plan"
	case "task":
		b.Input, b.Title = str("prompt", "description"), "Started an agent"
		if d := str("description"); d != "" {
			b.Title += ": " + d
		}
	case "askquestion":
		var qs []string
		for _, q := range asAny(in["questions"]) {
			m, _ := q.(map[string]any)
			if s, _ := m["prompt"].(string); s != "" {
				qs = append(qs, s)
			} else if s, _ := m["question"].(string); s != "" {
				qs = append(qs, s)
			}
		}
		b.Input, b.Title = strings.Join(qs, "\n"), "Asked you"
		if len(qs) > 0 {
			b.Title = "Asked you: " + firstLine(qs[0])
		}
	case "readlints":
		b.Title = "Checked for problems"
	default:
		b.Title = "Used " + name
		switch {
		case strings.HasPrefix(name, "mcp__"):
			if server, tool, ok := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__"); ok {
				b.Title = "Used " + tool + " (" + server + ")"
			}
		case strings.HasPrefix(name, "MCP:"):
			b.Title = "Used " + strings.TrimPrefix(name, "MCP:")
		}
		if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
			b.Input = clip(string(raw), maxOutput)
		}
	}
	return b
}

var (
	cuTimestamp = regexp.MustCompile(`(?s)<timestamp>(.*?)</timestamp>`)
	cuQuery     = regexp.MustCompile(`(?s)<user_query>\s*(.*?)\s*</user_query>`)
	cuOffset    = regexp.MustCompile(`\(UTC([+-]\d{1,2})(?::?(\d{2}))?\)`)
)

// CursorUserText is what you wrote in one of its user messages (the
// <user_query> it wraps it in), and when (its <timestamp>, local time with
// its UTC offset: "Thursday, Jul 23, 2026, 11:20 PM (UTC+2)").
func CursorUserText(s string) (string, time.Time) {
	var at time.Time
	if m := cuTimestamp.FindStringSubmatch(s); m != nil {
		at = cursorTime(m[1])
		s = strings.Replace(s, m[0], "", 1)
	}
	if m := cuQuery.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	return strings.TrimSpace(s), at
}

func cursorTime(s string) time.Time {
	s = strings.TrimSpace(s)
	loc := time.Local
	if m := cuOffset.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		off := h*3600 + sign(h)*mins*60
		loc = time.FixedZone("", off)
		s = strings.TrimSpace(strings.Replace(s, m[0], "", 1))
	}
	for _, layout := range []string{"Monday, Jan 2, 2006, 3:04 PM", "Monday, January 2, 2006, 3:04 PM", "Monday, Jan 2, 2006 at 3:04 PM"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	return 1
}

func (c *Cursor) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = c.turn
	}
	c.blocks = append(c.blocks, b)
	return []*pb.AgentBlock{b}
}

func (c *Cursor) path(p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && c.Cwd != "" {
		p = filepath.Join(c.Cwd, p)
	}
	c.files[filepath.Clean(p)] = true
	if c.Cwd != "" {
		if r, err := filepath.Rel(c.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

func (c *Cursor) mentions(s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		c.files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}
