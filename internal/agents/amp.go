package agents

import (
	"encoding/json"
	"path"
	"regexp"
	"strings"
)

// Amp is Amp's CLI (ampcode.com; read from @ampcode/cli
// 0.0.1794038434-gd5a1bf, the strings of its compiled Bun binary, and
// ampcode.com's manual: /docs/threads, /docs/cli, /docs/customize/plugins
// and /docs/plugin-api):
//
//   - no shell hooks, but plugins: TypeScript files Amp's CLI loads (under
//     Bun, in a process of its own with the CLI's environment) from
//     ~/.config/amp/plugins, which see its events (session.start,
//     agent.start, tool.call, tool.result, agent.end) and each thread's
//     state ('idle', 'running', 'awaiting-approval', 'error'). Hammock's plugin
//     (cmd/hmk/hooks_amp.go) forwards them as `hmk hook amp <event>` with
//     the event's JSON on stdin; ampParseHook reads them.
//   - threads are kept on Amp's servers (ampcode.com/threads/T-…), not on
//     disk: there's no local transcript, so no agent view or session finder.
//     `amp threads export` fetches one, from the server.
//   - `amp threads continue T-…` resumes a thread; interactive mode takes
//     its first message from stdin (`echo "…" | amp`), as it has no prompt
//     argument (`amp "…"` isn't one: -x runs a prompt and exits).
//   - one compiled binary, "amp" (~/.amp/bin/amp from its installer;
//     npm's is bin/amp.exe, whatever the platform).
//   - its credits are Amp's, on its servers: nothing on disk.
var Amp = &Agent{
	ID: "amp", Order: 100, Name: "Amp", Short: "Amp", Command: "amp",
	Procs:     []string{"amp.exe"},
	Namesake:  ampEditor,
	Install:   "curl -fsSL https://ampcode.com/install.sh | bash",
	LiveUsage: ampLiveUsage,
	Hooks:     true,
	ParseHook: ampParseHook,
	// Its interactive mode takes no first message as an argument (-x runs
	// one and exits; a piped one leaves stdin a pipe): typed in once it's up.
	TypePrompt:  true,
	Resume:      func(id string) string { return "amp threads continue " + Quote(id) },
	ResumeID:    ampResumeID,
	Schedulable: true,
	Screen: Screen{
		// Its status line while a turn runs ("Running Tools",
		// "Auto-Compacting", "Preparing Thread") and the footer's "Esc
		// cancel"; its approval prompt's options ("Allow Once", "Always
		// Allow for Me", "Always Allow for Workspace") and "Waiting for
		// approval".
		Working:    regexp.MustCompile(`Esc cancel|Running Tools|Auto-Compacting|Preparing Thread|(?i)esc to (cancel|interrupt)|ctrl\+c to interrupt`),
		NeedsInput: regexp.MustCompile(`Allow Once|Always Allow for (Me|Workspace)|(?i)waiting for approval|\[y/n\]|\(y/n\)`),
	},
}

// ampThreadID is a thread's id: T- and a UUID.
var ampThreadID = regexp.MustCompile(`^T-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ampResumeID is the thread `amp threads continue T-…` resumes (by id or
// URL; the first, when there are several, is the one shown).
func ampResumeID(argv []string) string {
	for i := 0; i+2 < len(argv); i++ {
		if argv[i] != "threads" || argv[i+1] != "continue" {
			continue
		}
		for _, a := range argv[i+2:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			if id := path.Base(strings.TrimRight(strings.SplitN(a, "?", 2)[0], "/")); ampThreadID.MatchString(id) {
				return id
			}
			return ""
		}
	}
	return ""
}

// ampEvent is what Hammock's Amp plugin sends: the event's own payload (Amp's
// plugin API types), and for agent.end the reply's text it found in the
// turn's messages instead of the messages themselves.
type ampEvent struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Message string          `json:"message"` // agent.start, agent.end: your prompt
	Tool    string          `json:"tool"`
	Input   json.RawMessage `json:"input"`
	Status  string          `json:"status"` // tool.result, agent.end: done, error, cancelled
	Error   string          `json:"error"`
	Reply   string          `json:"reply"`
	// state: the thread's new state, and the one before.
	State    string `json:"state"`
	Previous string `json:"previous"`
}

// ampParseHook reads an event from Hammock's Amp plugin (event: Amp's name for
// it, or "state" for a change of the thread's state) as Hammock's.
func ampParseHook(event string, payload []byte) Hook {
	var e ampEvent
	_ = json.Unmarshal(payload, &e)
	h := Hook{SessionID: e.Thread.ID}
	switch event {
	case "session.start":
		h.Event = "SessionStart"
	case "agent.start":
		h.Event, h.Prompt = "UserPromptSubmit", e.Message
	case "tool.call":
		h.Event, h.ToolName, h.ToolInput = "PreToolUse", e.Tool, ampToolInput(e.Input)
	case "tool.result":
		h.Event, h.ToolName, h.ToolInput = "PostToolUse", e.Tool, ampToolInput(e.Input)
	case "agent.end":
		switch e.Status {
		case "error":
			h.Event, h.LastMessage, h.ErrorDetails = "StopFailure", e.Reply, "Amp stopped with an error"
		default: // done, or cancelled (Esc)
			h.Event, h.LastMessage = "Stop", e.Reply
		}
	case "state":
		switch {
		case e.State == "awaiting-approval":
			h.Event, h.Message = "PermissionRequest", "Amp wants your approval"
		case e.State == "running" && e.Previous == "awaiting-approval":
			// Approved (a refusal goes on too, or ends the turn: agent.end).
			h.Event = "PostToolUse"
		case e.State == "running" && e.Previous == "":
			// The thread it shows now (switched to, or continued) is working.
			h.Event = "PreToolUse"
		case e.State == "idle" && e.Previous == "":
			h.Event = "SessionStart"
		}
	}
	return h
}

// ampToolInput is a tool's input with the keys Hammock summarises tools by:
// Amp's Bash takes "cmd", its file tools "path".
func ampToolInput(in json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(in, &m) != nil || m == nil {
		return rawOr(in)
	}
	if c, ok := m["cmd"].(string); ok && m["command"] == nil {
		m["command"] = c
	}
	if p, ok := m["path"].(string); ok && m["file_path"] == nil {
		m["file_path"] = p
	}
	out, err := json.Marshal(m)
	if err != nil {
		return in
	}
	return out
}

func init() { Register(Amp) }

// ampEditor: the process is the Amp text editor (jmacdonald/amp), also
// "amp", installed with cargo or Homebrew, where Amp the agent isn't.
func ampEditor(p string, _ []string) bool {
	return strings.Contains(p, "/.cargo/bin/") || strings.Contains(p, "/Cellar/amp/")
}
