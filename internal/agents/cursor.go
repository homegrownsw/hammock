package agents

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Cursor is Cursor's CLI agent (cursor-agent, `agent` in newer installs).
// Closed source; what Hammock relies on was read from its bundle
// (2026.11.05-86047df, downloads.cursor.com/lab/…/agent-cli-package.tar.gz)
// and its docs (cursor.com/docs/cli, cursor.com/docs/agent/hooks):
//
//   - It's a node app: ~/.local/bin/cursor-agent links to
//     ~/.local/share/cursor-agent/versions/<v>/cursor-agent, a bash script
//     that execs that folder's own node with index.js (the heuristics spot
//     it by the versions folder).
//   - Hooks are in ~/.cursor/hooks.json ({"version": 1, "hooks": {"event":
//     [{"command": …}]}}), each run with the event's JSON on stdin; every
//     payload has conversation_id, generation_id, hook_event_name,
//     cursor_version and, once it exists, transcript_path.
//   - Each chat writes a transcript as it goes: JSONL under
//     $CURSOR_DATA_DIR (~/.cursor)/projects/<cwd as a slug>/agent-transcripts/
//     <id>/<id>.jsonl (older: agent-transcripts/<id>.jsonl), your messages,
//     its replies and its tool calls, no tool output or times. Its own state
//     is a per-chat SQLite store, $CURSOR_CONFIG_DIR (or
//     $XDG_CONFIG_HOME/cursor, else ~/.cursor)/chats/<md5 of cwd>/<id>/store.db,
//     whose blobs are protobuf and (now) encrypted, so Hammock reads the
//     transcripts instead.
//   - `cursor-agent "prompt"` starts it interactively with a first message;
//     `--resume <chatId>` resumes a chat (of the folder it's run in).
//   - Its usage limits are only on Cursor's servers: none on disk.
var Cursor = &Agent{
	ID: "cursor", Order: 40, Name: "Cursor Agent", Short: "Cursor", Command: "cursor-agent",
	Install:   "curl https://cursor.com/install -fsS | bash",
	LiveUsage: cursorLiveUsage,
	Hooks:     true,
	ParseHook: cursorHook,
	Resume:    func(id string) string { return "cursor-agent --resume " + Quote(id) },
	ResumeID: func(argv []string) string {
		if id := argAfter(argv, "--resume"); uuidLike.MatchString(id) {
			return id
		}
		return ""
	},
	Schedulable: true,
	// Cursor has no hook for its approval prompts.
	ScreenAsks: true,
	Screen: Screen{
		// Its prompt says "ctrl+c to stop" while it works.
		Working: regexp.MustCompile(`(?i)ctrl\+c to stop|esc to interrupt`),
		// Its approval prompts.
		NeedsInput: regexp.MustCompile(`Run this (command|MCP tool)( outside the sandbox)?\?|Allow this web (fetch|search)\?|Approve mode switch|Waiting for approval|Waiting for decision \(y/n/p\)`),
	},

	Roots:   cursorRoots,
	RootEnv: map[string]string{"CURSOR_DATA_DIR": "projects"},
	Locate: func(id string) (string, bool) {
		if !safeSegment(id) {
			return "", false
		}
		for _, r := range cursorRoots() {
			for _, pat := range []string{filepath.Join(r, "*", "agent-transcripts", id, id+".jsonl"), filepath.Join(r, "*", "agent-transcripts", id+".jsonl")} {
				if m, _ := filepath.Glob(pat); len(m) > 0 {
					return m[0], true
				}
			}
		}
		return "", false
	},
	Newest: func(cwd string) (string, bool) {
		var files []string
		for _, r := range cursorRoots() {
			files = append(files, cursorTranscripts(filepath.Join(r, CursorSlug(cwd)))...)
		}
		return newestFile(files)
	},
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			projects, _ := filepath.Glob(filepath.Join(r, "*"))
			for _, p := range projects {
				out = append(out, cursorTranscripts(p)...)
			}
		}
		return out
	},
	// Its transcripts mostly grow, but Open puts a line of Hammock's in front,
	// so the finder reads a changed one whole.
	Open: openCursor,
	Index: func(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
		br := bufio.NewReaderSize(r, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				cursorRecord(line, t, emit)
			}
			if err != nil {
				return 0, nil
			}
		}
	},
	OwnsFile: func(path string) bool {
		return strings.HasSuffix(path, ".jsonl") && strings.Contains(filepath.ToSlash(path), "/agent-transcripts/")
	},

	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewCursor(cwd) },
	SlashCommands: func(string) []string { return cursorSlashCommands },
}

func init() { Register(Cursor) }

// cursorRoots are its projects folders: ~/.cursor/projects, and
// $CURSOR_DATA_DIR/projects (where it writes them when that's set; its
// hooks still name ~/.cursor's).
func cursorRoots() []string {
	dirs := []string{home(".cursor", "projects")}
	if d := envOrEmpty("CURSOR_DATA_DIR"); d != "" {
		dirs = append(dirs, filepath.Join(d, "projects"))
	}
	return dirs
}

// cursorSlashCommands are its built-in commands (2026.11.05's).
var cursorSlashCommands = []string{"/add-dir", "/ask", "/btw", "/changes", "/clear", "/commit", "/config", "/context", "/copy",
	"/copy-conversation-id", "/exit", "/fast", "/feedback", "/fork", "/full-conversation", "/goal", "/help", "/jobs", "/logout",
	"/mcp", "/model", "/plan", "/plugin", "/quit", "/rename", "/resume", "/rewind", "/rule", "/skills", "/summarize", "/usage"}

// cursorHook reads one of its hook events. Its payloads are Claude-like
// (hook_event_name, tool_name, tool_input, prompt, transcript_path), with
// its own event names and a few events of its own.
func cursorHook(event string, payload []byte) Hook {
	h := ParseHook(event, payload, nil)
	var p struct {
		ConversationID string          `json:"conversation_id"`
		Command        string          `json:"command"`
		FilePath       string          `json:"file_path"`
		Status         string          `json:"status"` // stop: completed, aborted, error
		ToolInput      json.RawMessage `json:"tool_input"`
	}
	_ = json.Unmarshal(payload, &p)
	h.SessionID = cmp.Or(p.ConversationID, h.SessionID)
	input := func(k, v string) json.RawMessage { b, _ := json.Marshal(map[string]string{k: v}); return b }
	switch h.Event {
	case "sessionStart":
		h.Event = "SessionStart"
	case "sessionEnd":
		h.Event = "SessionEnd"
	case "beforeSubmitPrompt":
		h.Event = "UserPromptSubmit"
	case "beforeShellExecution", "afterShellExecution":
		h.Event, h.ToolName, h.ToolInput = cursorPrePost(h.Event), "Shell", input("command", p.Command)
	case "beforeMCPExecution", "afterMCPExecution":
		h.Event = cursorPrePost(h.Event)
		// Its MCP tool_input is the arguments as a JSON string.
		var s string
		if json.Unmarshal(p.ToolInput, &s) == nil && json.Valid([]byte(s)) {
			h.ToolInput = json.RawMessage(s)
		}
	case "beforeReadFile":
		h.Event, h.ToolName, h.ToolInput = "PreToolUse", "Read", input("file_path", p.FilePath)
	case "afterFileEdit":
		h.Event, h.ToolName, h.ToolInput = "PostToolUse", "Edit", input("file_path", p.FilePath)
	case "preToolUse":
		h.Event = "PreToolUse"
	case "postToolUse", "postToolUseFailure":
		h.Event = "PostToolUse"
	case "stop":
		h.Event = "Stop"
		if p.Status == "error" {
			h.Event, h.Error = "StopFailure", "error"
		}
	default:
		// afterAgentResponse, afterAgentThought, subagentStart, preCompact…
		// say nothing Hammock shows.
		h.Event = ""
	}
	return h
}

// cursorPrePost: a before… event is PreToolUse, an after… one PostToolUse.
func cursorPrePost(ev string) string {
	if strings.HasPrefix(ev, "before") {
		return "PreToolUse"
	}
	return "PostToolUse"
}

// CursorSlug is the folder Cursor keeps a working folder's transcripts in,
// under projects: the path with each run of characters other than ASCII
// letters and digits made one "-", trimmed ("/Users/a/my.app" →
// "Users-a-my-app").
func CursorSlug(path string) string {
	var b strings.Builder
	dash := false
	for _, r := range path {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	return b.String()
}

// cursorTranscripts are the chats' transcripts in one project folder:
// agent-transcripts/<id>/<id>.jsonl and older agent-transcripts/<id>.jsonl
// (not subagents', in <id>/subagents).
func cursorTranscripts(project string) []string {
	dir := filepath.Join(project, "agent-transcripts")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		switch name := e.Name(); {
		case e.IsDir():
			p := filepath.Join(dir, name, name+".jsonl")
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				out = append(out, p)
			}
		case strings.HasSuffix(name, ".jsonl"):
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// newestFile is the most recently changed of files.
func newestFile(files []string) (string, bool) {
	best, at := "", time.Time{}
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.ModTime().After(at) {
			best, at = f, st.ModTime()
		}
	}
	return best, best != ""
}

// safeSegment: id can name a file (no separators, not . or ..).
func safeSegment(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`) && !strings.Contains(id, "\x00")
}

// cursorMeta is the line openCursor puts in front of a transcript, which
// says nothing of its chat but what it's a transcript of.
type cursorMeta struct {
	Type string `json:"type"` // "hammock_session"
	ID   string `json:"id"`
	Cwd  string `json:"cwd,omitempty"`
}

// openCursor opens a transcript for the session finder, after a line
// saying its chat's id and working folder: the transcript only has the
// folder as a slug, which is found again on disk (CursorCwd).
func openCursor(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	project := filepath.Dir(filepath.Dir(path))
	if filepath.Base(filepath.Dir(path)) == id {
		project = filepath.Dir(project)
	}
	meta, _ := json.Marshal(cursorMeta{Type: "hammock_session", ID: id, Cwd: CursorCwd(filepath.Base(project), id)})
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(append(meta, '\n')), f), f}, nil
}

var cursorCwds sync.Map // slug -> []string, the folders it may be

// CursorCwd finds the folder slug (CursorSlug) was made from, by walking
// down from / through the folders whose own slugs spell it. Several may:
// one with chat id in Cursor's store (chats/<md5 of the folder>/<id>)
// wins, else one with any chats there, else the first found. "" if none
// exists.
func CursorCwd(slug, id string) string {
	if slug == "" {
		return ""
	}
	var found []string
	if v, ok := cursorCwds.Load(slug); ok {
		found = v.([]string)
	} else {
		found = cursorFolders("/", slug)
		cursorCwds.Store(slug, found)
	}
	best, top := "", -1
	for _, p := range found {
		sum := md5.Sum([]byte(p))
		h := hex.EncodeToString(sum[:])
		s := 0
		for _, d := range cursorConfigDirs() {
			if _, err := os.Stat(filepath.Join(d, "chats", h, id)); id != "" && err == nil {
				s = 2
				break
			}
			if _, err := os.Stat(filepath.Join(d, "chats", h)); err == nil {
				s = 1
			}
		}
		if s > top {
			best, top = p, s
		}
	}
	return best
}

// cursorFolders are the folders under root whose path, from root, has
// the slug rest.
func cursorFolders(root, rest string) []string {
	var found []string
	var walk func(dir, rest string, depth int)
	walk = func(dir, rest string, depth int) {
		if depth > 40 || len(found) >= 16 {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
				continue
			}
			s := CursorSlug(e.Name())
			if s == "" || !strings.HasPrefix(rest, s) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			switch {
			case rest == s:
				if st, err := os.Stat(p); err == nil && st.IsDir() {
					found = append(found, p)
				}
			case rest[len(s)] == '-':
				walk(p, rest[len(s)+1:], depth+1)
			}
		}
	}
	walk(root, rest, 0)
	return found
}

// cursorConfigDirs are where its chats may be: $CURSOR_CONFIG_DIR, else
// $XDG_CONFIG_HOME/cursor, else ~/.cursor (Hammock's own environment may not
// be the agent's, so each is tried).
func cursorConfigDirs() []string {
	var out []string
	if d := envOrEmpty("CURSOR_CONFIG_DIR"); d != "" {
		out = append(out, d)
	}
	if d := envOrEmpty("XDG_CONFIG_HOME"); d != "" {
		out = append(out, filepath.Join(d, "cursor"))
	}
	return append(out, home(".config", "cursor"), home(".cursor"))
}

// cursorRecord reads one transcript line into the finder's records.
func cursorRecord(line []byte, t *thread.Thread, emit func(thread.Record)) {
	var l struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Cwd     string `json:"cwd"`
		Role    string `json:"role"`
		Message struct {
			Content []struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil {
		return
	}
	if l.Type == "hammock_session" {
		t.NativeID = cmp.Or(t.NativeID, l.ID)
		t.Cwd = cmp.Or(t.Cwd, l.Cwd)
		return
	}
	for _, c := range l.Message.Content {
		switch {
		case c.Type == "text" && l.Role == "user":
			text, at := agentview.CursorUserText(c.Text)
			if text == "" {
				continue
			}
			if t.Created.IsZero() && !at.IsZero() {
				t.Created = at
			}
			emit(thread.Record{Kind: thread.KindUser, Time: at, Text: text})
		case c.Type == "text" && l.Role == "assistant":
			if s := strings.TrimSpace(c.Text); s != "" {
				emit(thread.Record{Kind: thread.KindAssistant, Text: s})
			}
		case c.Type == "tool_use":
			emit(thread.Record{Kind: thread.KindToolCall, NativeTool: c.Name, Input: c.Input})
		}
	}
}
