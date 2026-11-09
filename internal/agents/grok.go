package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Grok is xAI's Grok CLI (`grok`: npm @xai-official/grok, or x.ai/cli's
// install script; not the unofficial superagent-ai "grok-cli"). Its hooks
// are Claude-shaped files in ~/.grok/hooks/*.json; its sessions are folders
// ~/.grok/sessions/<cwd, URL-encoded>/<session id>/, whose updates.jsonl
// is the session as ACP session updates, one per line, and summary.json
// its title. $GROK_HOME moves ~/.grok.
//
// Facts from @xai-official/grok 1.0.77: the documentation built into its
// binary (Hooks, Session Management, Permissions), its CLI's strings, and
// recordings of it speaking ACP (t3code's grok fixtures, 1.0.41). Its plan's
// usage limit is only on xAI's servers (`grok usage` asks its background
// service), so there's no Usage.
var Grok = &Agent{
	ID: "grok", Order: 110, Name: "Grok CLI", Short: "Grok", Command: "grok",
	// npm links its bin to grok-native; ~/.grok/bin/grok links to a
	// versioned binary. Run through a link, a process is named as the link.
	Procs:     []string{"grok-native"},
	Install:   "npm install -g @xai-official/grok",
	LiveUsage: grokLiveUsage,
	Hooks:     true,
	ParseHook: grokHook,

	// A prompt argument starts the TUI with it as the first turn.
	Start: func(prompt string) string {
		if p := strings.TrimSpace(prompt); p != "" {
			return "grok " + Quote(p)
		}
		return "grok"
	},
	Resume:      func(id string) string { return "grok --resume " + Quote(id) },
	ResumeID:    grokResumeID,
	Schedulable: true,
	Screen: Screen{
		// Its permission prompt's rows: "Allow once", "Reject once",
		// "Always allow …", "Allow all edits this session".
		NeedsInput: regexp.MustCompile(`(?i)\ballow once\b|\breject once\b|allow all edits (this|during this) session`),
		// Esc doesn't stop a turn ("Press Ctrl+c to cancel the turn").
		Working: regexp.MustCompile(`(?i)ctrl\+c to cancel|send a message to interrupt|esc to interrupt`),
	},

	Roots:    grokRoots,
	RootEnv:  map[string]string{"GROK_HOME": "sessions"},
	Locate:   func(id string) (string, bool) { return grokLocate(grokRoots(), id) },
	Newest:   func(cwd string) (string, bool) { return grokNewest(grokRoots(), cwd) },
	Sessions: grokSessions,
	Appends:  true,
	Index:    grokIndex,
	Names:    grokNames,
	OwnsFile: grokOwnsFile,

	NewParser: func(cwd string, _ bool) agentview.Parser { return agentview.NewGrok(cwd) },
}

func init() { Register(Grok) }

// grokRoots are its sessions folders: ~/.grok's, and $GROK_HOME's.
func grokRoots() []string {
	dirs := []string{home(".grok", "sessions")}
	if d := envOrEmpty("GROK_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "sessions"))
	}
	return dirs
}

// grokHookEvents map the events Hammock's hooks are set up for (by their
// Claude Code names, which Grok uses) to Hammock's.
var grokHookEvents = map[string]string{
	"PostToolUseFailure": "PostToolUse",
	// A turn that ended without completing: interrupted, a permission
	// declined, the turn limit. It's back at its prompt.
	"StopCancelled": "Stop",
	"SubagentStart": "",
	"SubagentStop":  "",
	"PreCompact":    "",
	"PostCompact":   "",
}

// grokHook reads one of Grok's hook events. Its payloads are Claude Code's
// in camelCase, with Claude's snake_case names alongside (session_id,
// transcript_path, tool_name, tool_input, last_assistant_message,
// notification_type…); ParseHook reads either.
func grokHook(event string, payload []byte) Hook {
	h := ParseHook(event, payload, grokHookEvents)
	var p struct {
		Reason       string `json:"reason"`
		ErrorDetails string `json:"errorDetails"`
		SubagentType string `json:"subagentType"`
		Name         string `json:"hook_event_name"`
	}
	_ = json.Unmarshal(payload, &p)
	if event == "" {
		event = p.Name
	}
	if h.SessionID == "" {
		// Grok puts it in every hook's environment too.
		h.SessionID = os.Getenv("GROK_SESSION_ID")
	}
	if h.ErrorDetails == "" {
		h.ErrorDetails = p.ErrorDetails
	}
	if p.SubagentType != "" {
		// A subagent's (its own session's end, its turn's failure): not
		// the session in the pane.
		h.Event = ""
		return h
	}
	switch h.Event {
	case "Stop":
		switch {
		case event == "StopCancelled":
			h.LastMessage = "" // what it had said when you stopped it
		case grokSessionEndReasons[p.Reason]:
			// Fired as the session ends, beside SessionEnd: not a turn's end.
			h.Event = ""
		}
	case "Notification":
		switch h.NotificationType {
		case "permission_prompt", "idle_prompt":
		case "elicitation_dialog":
			// Its question (ask_user_question) waits for you as a
			// permission does.
			h.NotificationType = "permission_prompt"
		default:
			h.Event = "" // task_complete and the like: Stop says it
		}
	}
	return h
}

// grokSessionEndReasons are a Stop's reasons when it fires as the session
// ends, not a turn.
var grokSessionEndReasons = map[string]bool{"channel_closed": true, "shutdown": true}

// grokResumeID is the session a grok command line resumes (--resume ID,
// -r ID, by id: a title isn't one Locate can find) or starts with
// --session-id; not a fork's, which gets a new id.
func grokResumeID(argv []string) string {
	if slices.Contains(argv, "--fork-session") {
		return ""
	}
	for _, names := range [][]string{{"--resume", "-r"}, {"--session-id", "-s"}} {
		if id := strings.ToLower(argAfter(argv, names...)); uuidLike.MatchString(id) {
			return id
		}
	}
	return ""
}

// grokLocate finds session id's updates.jsonl, in whichever folder's group
// it is.
func grokLocate(roots []string, id string) (string, bool) {
	if !safeSegment(id) {
		return "", false
	}
	for _, r := range roots {
		m, _ := filepath.Glob(filepath.Join(r, "*", id, "updates.jsonl"))
		if len(m) > 0 {
			return m[0], true
		}
	}
	return "", false
}

// grokSessions are every session's updates.jsonl under roots, but not a
// subagent's (its child sessions are kept beside the others).
func grokSessions(roots []string) []string {
	var out []string
	for _, r := range roots {
		groups, _ := os.ReadDir(r)
		for _, g := range groups {
			if g.IsDir() {
				out = append(out, grokGroupSessions(filepath.Join(r, g.Name()))...)
			}
		}
	}
	return out
}

// grokGroupSessions are the sessions in one folder's group.
func grokGroupSessions(group string) []string {
	m, _ := filepath.Glob(filepath.Join(group, "*", "updates.jsonl"))
	children := map[string]bool{}
	for _, p := range m {
		for _, c := range grokChildren(filepath.Dir(p)) {
			children[c] = true
		}
	}
	var out []string
	for _, p := range m {
		dir := filepath.Dir(p)
		if !children[filepath.Base(dir)] && !grokReadSummary(dir).subagent() {
			out = append(out, p)
		}
	}
	return out
}

// grokOwnsFile: path is a session's updates.jsonl.
func grokOwnsFile(path string) bool { return filepath.Base(path) == "updates.jsonl" }

// grokChildren are the subagent sessions a session started, from its
// subagents/<id>/meta.json files.
func grokChildren(session string) []string {
	metas, _ := filepath.Glob(filepath.Join(session, "subagents", "*", "meta.json"))
	var out []string
	for _, p := range metas {
		doc, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m struct {
			Child      string `json:"childSessionId"`
			ChildSnake string `json:"child_session_id"`
		}
		if json.Unmarshal(doc, &m) == nil {
			if id := grokOr(m.Child, m.ChildSnake); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// grokSummary is the part of a session's summary.json Hammock reads.
type grokSummary struct {
	Info struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"info"`
	Summary      string    `json:"session_summary"`
	Title        string    `json:"generated_title"`
	Manual       bool      `json:"title_is_manual"`
	Created      time.Time `json:"created_at"`
	Kind         string    `json:"session_kind"`
	Hidden       bool      `json:"hidden"`
	SubagentType string    `json:"subagent_type"`
}

func (s grokSummary) subagent() bool {
	return s.Hidden || s.SubagentType != "" || strings.Contains(strings.ToLower(s.Kind), "subagent")
}

func grokReadSummary(session string) grokSummary {
	var s grokSummary
	if doc, err := os.ReadFile(filepath.Join(session, "summary.json")); err == nil {
		_ = json.Unmarshal(doc, &s)
	}
	return s
}

// grokGroupCwd is the folder a group of sessions is for: its name
// URL-decoded, or, for a name too long for that, its .cwd file.
func grokGroupCwd(group string) string {
	if b, err := os.ReadFile(filepath.Join(group, ".cwd")); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	if s, err := url.PathUnescape(filepath.Base(group)); err == nil && filepath.IsAbs(s) {
		return s
	}
	return ""
}

// grokNewest is the latest session begun in cwd.
func grokNewest(roots []string, cwd string) (string, bool) {
	cwd = filepath.Clean(cwd)
	var found []string
	for _, r := range roots {
		groups, _ := os.ReadDir(r)
		for _, g := range groups {
			dir := filepath.Join(r, g.Name())
			if g.IsDir() && grokGroupCwd(dir) != "" && filepath.Clean(grokGroupCwd(dir)) == cwd {
				found = append(found, grokGroupSessions(dir)...)
			}
		}
	}
	var best string
	var mod time.Time
	for _, p := range found {
		if st, err := os.Stat(p); err == nil && st.ModTime().After(mod) {
			best, mod = p, st.ModTime()
		}
	}
	return best, best != ""
}

// grokLine is the part of an updates.jsonl line the session finder reads:
// a session/update notification as Grok records it, {"timestamp",
// "method", "params": {"sessionId", "update": {"sessionUpdate", …},
// "_meta"}}.
type grokLine struct {
	Timestamp float64 `json:"timestamp"`
	Params    struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Kind    string `json:"sessionUpdate"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
				URI  string `json:"uri"`
			} `json:"content"`
			Title string `json:"title"`
			Meta  struct {
				PromptIndex *int `json:"promptIndex"`
			} `json:"_meta"`
		} `json:"update"`
		Meta struct {
			AgentTimestampMs int64 `json:"agentTimestampMs"`
		} `json:"_meta"`
	} `json:"params"`
}

func (l grokLine) time() time.Time {
	switch {
	case l.Params.Meta.AgentTimestampMs > 0:
		return time.UnixMilli(l.Params.Meta.AgentTimestampMs)
	case l.Timestamp > 1e12:
		return time.UnixMilli(int64(l.Timestamp))
	case l.Timestamp > 0:
		return time.UnixMilli(int64(l.Timestamp * 1000))
	}
	return time.Time{}
}

// grokIndex reads a session's updates for the session finder: your
// messages and its replies, each streamed as chunks that it joins. A
// message still being written at the end is left to be read next time.
func grokIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	if path, ok := r.(interface{ Name() string }); ok && t.Cwd == "" {
		t.Cwd = grokGroupCwd(filepath.Dir(filepath.Dir(path.Name())))
	}
	var n, start int64 // bytes read; where the message being joined began
	var kind thread.Kind
	var text strings.Builder
	var at time.Time
	prompt := -1
	flush := func() {
		if kind != "" && strings.TrimSpace(text.String()) != "" {
			emit(thread.Record{Kind: kind, Time: at, Text: text.String()})
		}
		kind = ""
		text.Reset()
	}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if kind != "" {
				return start, nil // its message may go on
			}
			return n, nil
		}
		var l grokLine
		if json.Unmarshal(line, &l) != nil {
			n += int64(len(line))
			continue
		}
		u := l.Params.Update
		if t.NativeID == "" {
			t.NativeID = l.Params.SessionID
		}
		if t.Created.IsZero() {
			t.Created = l.time()
		}
		var k thread.Kind
		switch u.Kind {
		case "user_message_chunk":
			k = thread.KindUser
		case "agent_message_chunk":
			k = thread.KindAssistant
		case "agent_thought_chunk", "usage_update", "available_commands_update", "":
			// Neither ends a message: thinking streams between its chunks.
			n += int64(len(line))
			continue
		case "session_info_update":
			if u.Title != "" {
				t.Title = u.Title
			}
		}
		newPrompt := k == thread.KindUser && u.Meta.PromptIndex != nil && *u.Meta.PromptIndex != prompt
		if k != kind || newPrompt {
			flush()
			if k != "" {
				kind, at, start = k, l.time(), n
			}
		}
		if u.Meta.PromptIndex != nil {
			prompt = *u.Meta.PromptIndex
		}
		if k != "" {
			switch u.Content.Type {
			case "text":
				text.WriteString(u.Content.Text)
			case "resource_link":
				text.WriteString("@" + grokOr(u.Content.Name, u.Content.URI))
			}
		}
		n += int64(len(line))
	}
}

// grokNames are sessions' titles (set with /rename, else generated), from
// their summary.json.
func grokNames(roots []string) map[string]string {
	names := map[string]string{}
	for _, p := range grokSessions(roots) {
		dir := filepath.Dir(p)
		s := grokReadSummary(dir)
		if title := strings.TrimSpace(grokOr(s.Title, s.Summary)); title != "" {
			names[grokOr(s.Info.ID, filepath.Base(dir))] = title
		}
	}
	return names
}

func grokOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
