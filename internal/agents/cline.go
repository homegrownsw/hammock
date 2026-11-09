package agents

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Cline is Cline's CLI (github.com/cline/cline apps/cli, npm "cline"), the
// 3.x one built on Cline's SDK, with its own TUI. What Hammock relies on was
// read from its source at CLI 3.0.74 (94eecbb, 2026-11-06):
//
//   - `cline` starts it interactively; `cline -i "prompt"` too, with a first
//     message (a bare prompt runs one turn and exits). `cline --id ID`
//     resumes session ID (apps/cli/src/commands/program.ts, main.ts).
//   - npm's `cline` is a Node script (bin/cline) that runs the compiled
//     binary, hard-linked beside it as bin/.cline; Hammock finds it by the
//     script's name (.cline is left out of Procs: hooks run under both, and
//     two agents above a hook make it look like an agent run by an agent).
//   - Hooks are script files named after their event (TaskStart,
//     TaskResume, UserPromptSubmit, PreToolUse, PostToolUse, TaskComplete,
//     TaskError, TaskCancel, SessionShutdown, with any of a few extensions)
//     in ~/Documents/Cline/Hooks, ~/.cline/hooks (<CLINE_DIR>/hooks) and a
//     project's .cline/hooks; every one found runs, with the event's JSON on
//     stdin (sdk/packages/core/src/hooks). PreToolUse and PostToolUse are
//     awaited and their stdout read as JSON ({} changes nothing); the rest
//     run detached. No event says it's waiting for permission.
//   - Its sessions are run by a shared background "hub" daemon whenever one
//     is up, which is usual: the hub runs the hooks then, with its own
//     environment (the one of whichever cline started it), so Hammock's hook
//     scripts only report sessions cline runs in its own process
//     (cmd/hmk/hooks_cline.go). The screen says the rest.
//   - Sessions: ~/.cline/data/sessions/<id>/<id>.json (the manifest) and
//     <id>.messages.json, rewritten whole after each reply
//     (sdk/packages/core/src/session; CLINE_DIR, CLINE_DATA_DIR and
//     CLINE_SESSION_DATA_DIR move them). Ids are <ms since 1970>_<5 of
//     [0-9a-z]>; a subagent's messages are <agent id>.messages.json beside
//     its parent's.
//   - Its limits are its providers' (Cline's credits, an API key, a ChatGPT
//     subscription), on their servers: nothing on disk says them.
var Cline = &Agent{
	ID: "cline", Order: 170, Name: "Cline", Short: "Cline", Command: "cline",
	Install:   "npm install -g cline",
	LiveUsage: clineLiveUsage,
	Hooks:     true,
	ParseHook: clineHook,
	Start: func(prompt string) string {
		if p := strings.TrimSpace(prompt); p != "" {
			return "cline -i " + Quote(p)
		}
		return "cline"
	},
	Resume:      func(id string) string { return "cline --id " + Quote(id) },
	ResumeID:    clineResumeID,
	Schedulable: true,
	// Its approval prompt comes before PreToolUse, and no hook says it.
	ScreenAsks: true,
	Screen: Screen{
		// Its spinner while it waits on the model, and the queued
		// prompts' hint while a turn runs (apps/cli/src/tui/components
		// chat-message-list.tsx, queued-prompts.tsx). Not the generic "esc
		// to cancel": its pickers and dialogs say that too.
		Working: regexp.MustCompile(`Thinking\.\.\. \(esc to cancel\)|Esc cancels turn`),
		// The TUI's approval and question panels (inline-tool-response.tsx),
		// and the plain prompt outside it (README "Tool approval").
		NeedsInput: regexp.MustCompile(`Cline needs permission|Approve tool call\?|Cline is asking a question|Approve tool "[^"\n]*" with input`),
	},

	Roots: clineRoots,
	RootEnv: map[string]string{
		"CLINE_DIR":              filepath.Join("data", "sessions"),
		"CLINE_DATA_DIR":         "sessions",
		"CLINE_SESSION_DATA_DIR": "",
	},
	Locate: func(id string) (string, bool) {
		if !clineIDRE.MatchString(id) {
			return "", false
		}
		for _, r := range clineRoots() {
			if p := clineMessagesPath(r, id); clineExists(p) {
				return p, true
			}
		}
		return "", false
	},
	Newest:   clineNewest,
	Sessions: clineSessions,
	Index:    clineIndex,
	OwnsFile: clineOwnsFile,

	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewCline(cwd) },
	Whole:         true,
	Open:          clineOpen,
	SlashCommands: func(string) []string { return clineSlashCommands },
}

func init() { Register(Cline) }

// clineSlashCommands are its TUI's (apps/cli/src/tui/commands
// slash-command-registry.ts, 3.0.74).
var clineSlashCommands = []string{"/account", "/clear", "/compact", "/config", "/fork", "/help", "/history", "/mcp",
	"/model", "/plugins", "/quit", "/settings", "/skills", "/team", "/theme", "/undo"}

// clineIDRE is a session id Hammock takes as one of Cline's folders (it only
// refuses ids with a slash, "." and ".."; it makes them <ms>_<xxxxx>).
var clineIDRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

// clineRoots are the folders Cline keeps its sessions in.
func clineRoots() []string {
	var out []string
	add := func(d string) {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	if d := os.Getenv("CLINE_SESSION_DATA_DIR"); filepath.IsAbs(d) {
		add(d)
	}
	if d := os.Getenv("CLINE_DATA_DIR"); filepath.IsAbs(d) {
		add(filepath.Join(d, "sessions"))
	}
	if d := os.Getenv("CLINE_DIR"); filepath.IsAbs(d) {
		add(filepath.Join(d, "data", "sessions"))
	}
	add(home(".cline", "data", "sessions"))
	return out
}

// clineMessagesPath is session id's messages file in root.
func clineMessagesPath(root, id string) string {
	return filepath.Join(root, id, id+".messages.json")
}

// clineOwnsFile: path is a session's messages file (<root>/<id>/<id>.messages.json),
// not a subagent's beside it.
func clineOwnsFile(path string) bool {
	id, ok := strings.CutSuffix(filepath.Base(path), ".messages.json")
	return ok && id != "." && id != ".." && clineIDRE.MatchString(id) && filepath.Base(filepath.Dir(path)) == id
}

// clineSessions lists the sessions' messages files under roots.
func clineSessions(roots []string) []string {
	var out []string
	for _, r := range roots {
		m, _ := filepath.Glob(filepath.Join(r, "*", "*.messages.json"))
		for _, p := range m {
			if clineOwnsFile(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// clineManifest is what Hammock reads of a session's manifest (SessionManifest,
// sdk/packages/core/src/session/models/session-manifest.ts).
type clineManifest struct {
	SessionID     string `json:"session_id"`
	Cwd           string `json:"cwd"`
	WorkspaceRoot string `json:"workspace_root"`
	StartedAt     string `json:"started_at"`
	Model         string `json:"model"`
	Prompt        string `json:"prompt"`
	Metadata      struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

// clineReadManifest reads the manifest beside a session's messages file.
func clineReadManifest(messages string) (clineManifest, bool) {
	var m clineManifest
	id := strings.TrimSuffix(filepath.Base(messages), ".messages.json")
	b, err := os.ReadFile(filepath.Join(filepath.Dir(messages), id+".json"))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return m, false
	}
	return m, true
}

// clineNewest is the latest session (by its messages' last write) that
// ran in cwd.
func clineNewest(cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	cwd = filepath.Clean(cwd)
	type cand struct {
		path string
		at   time.Time
	}
	var all []cand
	for _, p := range clineSessions(clineRoots()) {
		if st, err := os.Stat(p); err == nil {
			all = append(all, cand{p, st.ModTime()})
		}
	}
	slices.SortFunc(all, func(a, b cand) int { return b.at.Compare(a.at) })
	for _, c := range all {
		if m, ok := clineReadManifest(c.path); ok && (clineSameDir(m.Cwd, cwd) || clineSameDir(m.WorkspaceRoot, cwd)) {
			return c.path, true
		}
	}
	return "", false
}

func clineSameDir(a, b string) bool { return a != "" && filepath.Clean(a) == b }

// clineOpen opens a session as the lines agentview.Cline and clineIndex
// read: one saying what its manifest does, then its messages, one a line.
func clineOpen(path string) (io.ReadCloser, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		SessionID string            `json:"sessionId"`
		Messages  []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	s := agentview.ClineSession{ID: doc.SessionID}
	if m, ok := clineReadManifest(path); ok {
		s.ID = cmp.Or(m.SessionID, s.ID)
		s.Cwd = cmp.Or(m.Cwd, m.WorkspaceRoot)
		s.Title = m.Metadata.Title
		s.Started = m.StartedAt
		s.Model = m.Model
	}
	if s.ID == "" {
		s.ID = strings.TrimSuffix(filepath.Base(path), ".messages.json")
	}
	var out bytes.Buffer
	head, _ := json.Marshal(map[string]any{"$hammockSession": s})
	out.Write(head)
	out.WriteByte('\n')
	for _, m := range doc.Messages {
		var line bytes.Buffer
		if json.Compact(&line, m) == nil {
			out.Write(line.Bytes())
			out.WriteByte('\n')
		}
	}
	return io.NopCloser(&out), nil
}

// clineIndex reads a session (as clineOpen gives it) for the session
// finder: your messages and its replies.
func clineIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		m, blocks, ok := agentview.ParseClineLine(sc.Bytes())
		if !ok {
			continue
		}
		var at time.Time
		if m.Ts > 0 {
			at = time.UnixMilli(m.Ts)
		}
		switch {
		case m.Session != nil:
			s := m.Session
			t.NativeID, t.Cwd = cmp.Or(t.NativeID, s.ID), cmp.Or(t.Cwd, s.Cwd)
			if s.Title != "" && !t.Named {
				t.Title = s.Title
			}
			if st, err := time.Parse(time.RFC3339Nano, s.Started); err == nil && t.Created.IsZero() {
				t.Created = st
			}
		case !m.Shown() || m.Metadata.DisplayOnly:
		case m.Role == "user":
			if text := agentview.ClineUserText(blocks); text != "" {
				if t.Created.IsZero() {
					t.Created = at
				}
				emit(thread.Record{Kind: thread.KindUser, Time: at, Text: text})
			}
		case m.Role == "assistant":
			for _, b := range blocks {
				if s := strings.TrimSpace(b.Text); b.Type == "text" && s != "" {
					emit(thread.Record{Kind: thread.KindAssistant, Time: at, Text: s})
				}
			}
		}
	}
	return 0, sc.Err()
}

// clineResumeID is the session `cline --id ID` resumes.
func clineResumeID(argv []string) string {
	if id := argAfter(argv, "--id"); clineIDRE.MatchString(id) {
		return id
	}
	return ""
}

// clineHook reads one of Cline's hook events (HookEventPayload,
// sdk/packages/core/src/hooks/subprocess.ts): the file it ran is named for
// its event, which is `hmk hook cline <Event>`'s, and its payload's
// hookName says it too. The session is sessionContext.rootSessionId (taskId
// is the conversation's id, which isn't the session's).
func clineHook(event string, payload []byte) Hook {
	var p struct {
		HookName       string `json:"hookName"`
		SessionContext struct {
			RootSessionID string `json:"rootSessionId"`
		} `json:"sessionContext"`
		ToolCall *struct {
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"tool_call"`
		ToolResult *struct {
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"tool_result"`
		UserPromptSubmit struct {
			Prompt string `json:"prompt"`
		} `json:"userPromptSubmit"`
		Turn struct {
			OutputText string `json:"outputText"`
		} `json:"turn"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &p) != nil || p.HookName == "" {
		return Hook{} // not Cline's
	}
	h := Hook{SessionID: p.SessionContext.RootSessionID}
	switch cmp.Or(event, p.HookName) {
	case "UserPromptSubmit", "prompt_submit":
		// Also sent for the messages that carry tool results, with no text.
		if h.Prompt = agentview.ClineDisplayText(p.UserPromptSubmit.Prompt); h.Prompt != "" {
			h.Event = "UserPromptSubmit"
		}
	case "PreToolUse", "tool_call":
		h.Event = "PreToolUse"
		if p.ToolCall != nil {
			h.ToolName, h.ToolInput = p.ToolCall.Name, clineToolInput(p.ToolCall.Name, p.ToolCall.Input)
		}
	case "PostToolUse", "tool_result":
		h.Event = "PostToolUse"
		if p.ToolResult != nil {
			h.ToolName, h.ToolInput = p.ToolResult.Name, clineToolInput(p.ToolResult.Name, p.ToolResult.Input)
		}
	case "TaskComplete", "agent_end":
		h.Event, h.LastMessage = "Stop", p.Turn.OutputText
	case "TaskError", "agent_error":
		h.Event, h.ErrorDetails = "StopFailure", p.Error.Message
	case "TaskCancel", "agent_abort":
		// Esc stopped the turn: it's back at its prompt.
		h.Event = "Stop"
	}
	// TaskStart and TaskResume come at the start of each turn, before its
	// prompt; SessionShutdown only when one is cancelled: neither is state.
	return h
}

// clineToolInput is a tool's input as Hammock's summaries read it (Claude
// Code's command and file_path), else as it came.
func clineToolInput(name string, raw json.RawMessage) json.RawMessage {
	var in struct {
		Commands []json.RawMessage `json:"commands"`
		Path     string            `json:"path"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	_ = json.Unmarshal(raw, &in)
	var out map[string]string
	switch name {
	case "run_commands":
		if len(in.Commands) > 0 {
			var cmd string
			if json.Unmarshal(in.Commands[0], &cmd) != nil {
				var c struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				}
				_ = json.Unmarshal(in.Commands[0], &c)
				cmd = strings.TrimSpace(c.Command + " " + strings.Join(c.Args, " "))
			}
			if cmd != "" {
				out = map[string]string{"command": cmd}
			}
		}
	case "editor":
		if in.Path != "" {
			out = map[string]string{"file_path": in.Path}
		}
	case "read_files":
		if len(in.Files) > 0 && in.Files[0].Path != "" {
			out = map[string]string{"file_path": in.Files[0].Path}
		}
	}
	if out == nil {
		return rawOr(raw)
	}
	b, _ := json.Marshal(out)
	return b
}

func clineExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
