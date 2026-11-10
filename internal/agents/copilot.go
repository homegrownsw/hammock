package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/thread"
)

// Copilot is GitHub's Copilot CLI (npm @github/copilot, or Homebrew's
// copilot-cli): hooks in ~/.copilot/hooks/*.json, sessions as
// ~/.copilot/session-state/<id>/events.jsonl, with each session's name and
// folder in workspace.yaml beside it. $COPILOT_HOME moves ~/.copilot.
//
// Facts from @github/copilot 1.0.107 (its bundled schemas, SDK types and
// `copilot --help`) and docs.github.com's hooks reference. It doesn't keep
// its premium-request quota on disk (the session log's quota events are
// "ephemeral", never written), so there's no Usage.
var Copilot = &Agent{
	ID: "copilot", Order: 50, Name: "GitHub Copilot CLI", Short: "Copilot", Command: "copilot",
	Install:   "npm install -g @github/copilot",
	LiveUsage: copilotLiveUsage,
	Hooks:     true,
	// Hooks are configured with Claude Code's event names (PascalCase), so
	// Copilot sends Claude-shaped, snake_case payloads; see ParseHook.
	ParseHook: copilotHook,

	// -i starts it interactively and sends the prompt; a bare prompt
	// argument isn't one.
	Start: func(prompt string) string {
		if p := strings.TrimSpace(prompt); p != "" {
			return "copilot -i " + Quote(p)
		}
		return "copilot"
	},
	// --resume takes an optional value, so it's given with "=".
	Resume:      func(id string) string { return "copilot --resume=" + Quote(id) },
	ResumeID:    copilotResumeID,
	Schedulable: true,
	Screen: Screen{
		// Its permission prompts: "Do you want to run this command?", "…
		// use this tool?", "… allow this tool call?", with "❯ 1. Yes".
		// Its spinner's "(Esc to cancel)" is the generic working pattern.
		NeedsInput: regexp.MustCompile(`(?i)do you want to (run|use|allow|approve|read|proceed|make|create|ignore)\b|❯ 1\. yes`),
	},

	Roots:    copilotRoots,
	RootEnv:  map[string]string{"COPILOT_HOME": "session-state"},
	Locate:   func(id string) (string, bool) { return copilotLocate(copilotRoots(), id) },
	Newest:   func(cwd string) (string, bool) { return copilotNewest(copilotRoots(), cwd) },
	Sessions: copilotSessions,
	Appends:  true,
	Index:    copilotIndex,
	Names:    copilotNames,
	OwnsFile: copilotOwnsFile,

	NewParser: func(cwd string, _ bool) agentview.Parser { return agentview.NewCopilot(cwd) },
}

func init() { Register(Copilot) }

// copilotRoots are its session folders: ~/.copilot's, and $COPILOT_HOME's.
func copilotRoots() []string {
	dirs := []string{home(".copilot", "session-state")}
	if d := envOrEmpty("COPILOT_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "session-state"))
	}
	return dirs
}

// copilotHookEvents are the events Hammock's hooks are configured for (by
// their Claude Code names, which Copilot accepts), mapped to Hammock's.
var copilotHookEvents = map[string]string{
	"PostToolUseFailure": "PostToolUse",
	"ErrorOccurred":      "StopFailure", // only an unrecoverable one: see copilotHook
	"PreCompact":         "",
	"SubagentStop":       "",
}

// copilotHook reads one of Copilot's hook events: Claude Code's shape,
// except that ErrorOccurred's error is an object ({message, name}) and
// Notification's types are its own.
func copilotHook(event string, payload []byte) Hook {
	h := ParseHook(event, payload, copilotHookEvents)
	var p struct {
		Error struct {
			Message string `json:"message"`
			Name    string `json:"name"`
		} `json:"error"`
		Context     string `json:"error_context"`
		ContextCaml string `json:"errorContext"`
		Recoverable *bool  `json:"recoverable"`
	}
	_ = json.Unmarshal(payload, &p)
	switch h.Event {
	case "StopFailure":
		// A recoverable error (a retried request) doesn't end the turn, nor
		// does one in a tool, which the model is told about.
		ctx := p.Context
		if ctx == "" {
			ctx = p.ContextCaml
		}
		if p.Recoverable == nil || *p.Recoverable || (ctx != "" && ctx != "model_call" && ctx != "system") {
			h.Event = ""
			return h
		}
		h.Error, h.ErrorDetails = p.Error.Name, p.Error.Message
	case "Notification":
		switch h.NotificationType {
		case "permission_prompt":
		case "elicitation_dialog":
			// Its question form (ask_user) waits for you, as a permission does.
			h.NotificationType = "permission_prompt"
		default:
			// shell_completed, agent_completed, agent_idle: background
			// shells' and agents', not the session's own state.
			h.Event = ""
		}
	}
	return h
}

// copilotResumeID is the session a copilot command line resumes:
// --resume=ID or --session-id ID, when ID is a session id (it also takes a
// name or a prefix, which Locate can't find).
func copilotResumeID(argv []string) string {
	for i, a := range argv {
		if a == "--continue" {
			return ""
		}
		for _, flag := range []string{"--resume", "--session-id", "--new-session-id"} {
			v, ok := strings.CutPrefix(a, flag+"=")
			if !ok && a == flag && i+1 < len(argv) {
				v, ok = argv[i+1], true
			}
			if ok && uuidLike.MatchString(strings.ToLower(v)) {
				return strings.ToLower(v)
			}
		}
	}
	return ""
}

// copilotLocate finds session id's events.jsonl (or, from older versions,
// session-state/<id>.jsonl).
func copilotLocate(roots []string, id string) (string, bool) {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", false
	}
	for _, r := range roots {
		for _, p := range []string{filepath.Join(r, id, "events.jsonl"), filepath.Join(r, id+".jsonl")} {
			if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
				return p, true
			}
		}
	}
	return "", false
}

// copilotSessions are every session's events file under roots.
func copilotSessions(roots []string) []string {
	var out []string
	for _, r := range roots {
		m, _ := filepath.Glob(filepath.Join(r, "*", "events.jsonl"))
		out = append(out, m...)
		flat, _ := filepath.Glob(filepath.Join(r, "*.jsonl"))
		for _, p := range flat {
			if copilotOwnsFile(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// copilotOwnsFile: path is a session's events file (<id>/events.jsonl, or
// an older <id>.jsonl).
func copilotOwnsFile(path string) bool {
	base := filepath.Base(path)
	if base == "events.jsonl" {
		return true
	}
	id, ok := strings.CutSuffix(base, ".jsonl")
	return ok && uuidLike.MatchString(id)
}

// copilotNewest is the latest session begun in cwd: of the most recently
// written, the first whose session.start names it.
func copilotNewest(roots []string, cwd string) (string, bool) {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, p := range copilotSessions(roots) {
		if st, err := os.Stat(p); err == nil {
			files = append(files, file{p, st.ModTime()})
		}
	}
	slices.SortFunc(files, func(a, b file) int { return b.mod.Compare(a.mod) })
	for i, f := range files {
		if i == 20 {
			break
		}
		if start, ok := copilotStart(f.path); ok && filepath.Clean(start.cwd) == filepath.Clean(cwd) {
			return f.path, true
		}
	}
	return "", false
}

type copilotStartInfo struct {
	id, cwd string
	created time.Time
}

// copilotStart reads a session's first event, its session.start.
func copilotStart(path string) (copilotStartInfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return copilotStartInfo{}, false
	}
	defer f.Close()
	line, _ := bufio.NewReaderSize(f, 64<<10).ReadBytes('\n')
	var e copilotEvent
	if json.Unmarshal(line, &e) != nil || e.Type != "session.start" {
		return copilotStartInfo{}, false
	}
	return e.start(), true
}

// copilotEvent is the part of a session event the session finder reads.
type copilotEvent struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	AgentID   string    `json:"agentId"`
	Data      struct {
		SessionID string    `json:"sessionId"`
		StartTime time.Time `json:"startTime"`
		Context   *struct {
			Cwd string `json:"cwd"`
		} `json:"context"`
		Content string `json:"content"`
		Source  string `json:"source"`
	} `json:"data"`
}

func (e copilotEvent) start() copilotStartInfo {
	s := copilotStartInfo{id: e.Data.SessionID, created: e.Data.StartTime}
	if e.Data.Context != nil {
		s.cwd = e.Data.Context.Cwd
	}
	if s.created.IsZero() {
		s.created = e.Timestamp
	}
	return s
}

// copilotIndex reads a session's events for the session finder: its id,
// folder and start from session.start, your messages and its replies.
func copilotIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	var n int64
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return n, nil // a last line still being written is read next time
		}
		n += int64(len(line))
		var e copilotEvent
		if json.Unmarshal(line, &e) != nil || e.AgentID != "" {
			continue
		}
		switch e.Type {
		case "session.start":
			s := e.start()
			if t.NativeID == "" {
				t.NativeID = s.id
			}
			if t.Cwd == "" {
				t.Cwd = s.cwd
			}
			if t.Created.IsZero() {
				t.Created = s.created
			}
		case "session.resume":
			if t.Cwd == "" && e.Data.Context != nil {
				t.Cwd = e.Data.Context.Cwd
			}
		case "user.message":
			if strings.HasPrefix(e.Data.Source, "skill-") || strings.HasPrefix(e.Data.Source, "agent-") {
				continue
			}
			if strings.TrimSpace(e.Data.Content) != "" {
				emit(thread.Record{Kind: thread.KindUser, Time: e.Timestamp, Text: e.Data.Content})
			}
		case "assistant.message":
			if strings.TrimSpace(e.Data.Content) != "" {
				emit(thread.Record{Kind: thread.KindAssistant, Time: e.Timestamp, Text: e.Data.Content})
			}
		}
	}
}

// copilotNames are sessions' names (given with /rename or --name, or the
// one Copilot generates), from each session's workspace.yaml.
func copilotNames(roots []string) map[string]string {
	names := map[string]string{}
	for _, r := range roots {
		m, _ := filepath.Glob(filepath.Join(r, "*", "workspace.yaml"))
		for _, p := range m {
			doc, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			ws := yamlScalars(doc)
			id := ws["id"]
			if id == "" {
				id = filepath.Base(filepath.Dir(p))
			}
			if name := strings.TrimSpace(ws["name"]); name != "" {
				names[id] = name
			}
		}
	}
	return names
}

// yamlScalars reads a YAML document's top-level "key: value" lines (plain,
// single- or double-quoted scalars), as workspace.yaml is written.
func yamlScalars(doc []byte) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(string(doc), "\n") {
		if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '#' || l[0] == '-' {
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch {
		case strings.HasPrefix(v, `"`):
			if s, err := strconv.Unquote(v); err == nil {
				v = s
			}
		case strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") && len(v) >= 2:
			v = strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		case strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">"):
			v = "" // a block scalar: not one this reads
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}
