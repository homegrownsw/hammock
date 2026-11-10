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
	"strings"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/thread"
)

// Antigravity is Google's Antigravity CLI (agy; github.com/google-
// antigravity/antigravity-cli, closed source). It isn't Gemini CLI, though
// it keeps everything under ~/.gemini too. What Hammock relies on was read as
// text from its binary (1.5.3, the darwin-arm64 build its install.sh
// fetches): its bundled hooks.json docs, changelog, help and screen
// strings, and from what people have posted of real use (agy 1.5.0:
// github.com/ZingerLittleBee/Heeler/issues/644):
//
//   - One native binary, installed as ~/.local/bin/agy, that updates itself.
//   - Hooks are in ~/.gemini/config/hooks.json, by hook name: {"<name>":
//     {"SessionStart": [{"type": "command", "command": …}], "PreToolUse":
//     [{"matcher": "*", "hooks": [{…}]}], …}}. Its events: SessionStart,
//     PreToolUse, PostToolUse, PreInvocation (before every model call),
//     PostInvocation, Stop. Each gets a camelCase JSON payload on stdin
//     that doesn't name the event (conversationId, transcriptPath,
//     lastUserInput, parentConversationId for a subagent's…) and must
//     print a JSON object. None says it's asking you for permission.
//   - Its data is in ~/.gemini/antigravity-cli: each conversation's steps
//     as JSONL in brain/<id>/.system_generated/logs/transcript.jsonl (long
//     fields clipped) and transcript_full.jsonl (whole), its own state in
//     conversations/<id>.db (SQLite of protobuf), and history.jsonl, a
//     line per message you send ({"display", "timestamp", "workspace",
//     "conversationId"}): the only place that says which folder a
//     conversation was in, and which conversations are yours rather than
//     its subagents' (they get conversations of their own).
//   - `agy -i "prompt"` starts it with a first message (a bare argument
//     isn't one); `agy --conversation=<id>` resumes one; -c is --continue.
//   - Its quota is only on Google's servers (/usage asks them): no usage.
var Antigravity = &Agent{
	ID: "antigravity", Order: 25, Name: "Antigravity CLI", Short: "Antigravity", Command: "agy",
	Install:   "curl -fsSL https://antigravity.google/cli/install.sh | bash",
	LiveUsage: antigravityLiveUsage,
	Hooks:     true,
	ParseHook: antigravityHook,
	Start: func(prompt string) string {
		if p := strings.TrimSpace(prompt); p != "" {
			return "agy -i " + Quote(p)
		}
		return "agy"
	},
	Resume: func(id string) string { return "agy --conversation=" + Quote(id) },
	ResumeID: func(argv []string) string {
		if id := argAfter(argv, "--conversation"); uuidLike.MatchString(id) {
			return id
		}
		return ""
	},
	Schedulable: true,
	// No hook says it's waiting for you: its approval prompts are read off
	// its screen.
	ScreenAsks: true,
	Screen: Screen{
		NeedsInput: regexp.MustCompile(`Run this command\?|Allow (access to this (URL|file)|calling this tool|creation of this file|administrator elevation|sandbox bypass for command execution)\?|Do you want to proceed\?|Requesting permission for:|Approval Required`),
	},

	Roots: func() []string { return []string{antigravityRoot()} },
	Locate: func(id string) (string, bool) {
		if !uuidLike.MatchString(id) {
			return "", false
		}
		return antigravityTranscript(filepath.Join(antigravityRoot(), id))
	},
	Newest: func(cwd string) (string, bool) {
		if cwd == "" {
			return "", false
		}
		root := antigravityRoot()
		var files []string
		for id, ws := range antigravityHistory(filepath.Dir(root)) {
			if filepath.Clean(ws) == filepath.Clean(cwd) {
				if p, ok := antigravityTranscript(filepath.Join(root, id)); ok {
					files = append(files, p)
				}
			}
		}
		return newestFile(files)
	},
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			yours := antigravityHistory(filepath.Dir(r))
			dirs, _ := os.ReadDir(r)
			for _, d := range dirs {
				id := d.Name()
				if !d.IsDir() || !uuidLike.MatchString(id) {
					continue
				}
				// Subagents' conversations aren't in your history.
				if _, ok := yours[id]; yours != nil && !ok {
					continue
				}
				if p, ok := antigravityTranscript(filepath.Join(r, id)); ok {
					out = append(out, p)
				}
			}
		}
		return out
	},
	// Open puts a line of Hammock's in front, so the finder reads a changed
	// transcript whole.
	Open:     antigravityOpen,
	Index:    antigravityIndex,
	OwnsFile: antigravityOwns,

	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewAntigravity(cwd) },
	SlashCommands: func(string) []string { return antigravitySlashCommands },
}

func init() { Register(Antigravity) }

// antigravityRoot is the folder its conversations' transcripts are in.
func antigravityRoot() string { return home(".gemini", "antigravity-cli", "brain") }

// antigravitySlashCommands are its built-in commands (1.5.3's, from its
// changelog and help).
var antigravitySlashCommands = []string{"/add-dir", "/agents", "/artifact", "/btw", "/changelog", "/compact", "/config", "/context",
	"/copy", "/credits", "/diff", "/effort", "/fork", "/goal", "/help", "/hooks", "/keybindings", "/logout", "/mcp", "/model",
	"/open", "/permissions", "/plan", "/plugin", "/projects", "/quota", "/remote-control", "/rename", "/resume", "/rewind",
	"/settings", "/skills", "/switch", "/tasks", "/title", "/usage", "/voice"}

// antigravityTranscript is a conversation folder's transcript: the whole
// one, else the clipped one.
func antigravityTranscript(dir string) (string, bool) {
	logs := filepath.Join(dir, ".system_generated", "logs")
	for _, name := range []string{"transcript_full.jsonl", "transcript.jsonl"} {
		p := filepath.Join(logs, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// antigravityOwns: path is a conversation's transcript,
// brain/<id>/.system_generated/logs/transcript(_full).jsonl.
func antigravityOwns(path string) bool {
	name := filepath.Base(path)
	logs := filepath.Dir(path)
	sys := filepath.Dir(logs)
	conv := filepath.Dir(sys)
	return (name == "transcript_full.jsonl" || name == "transcript.jsonl") &&
		filepath.Base(logs) == "logs" && filepath.Base(sys) == ".system_generated" &&
		uuidLike.MatchString(filepath.Base(conv)) && filepath.Base(filepath.Dir(conv)) == "brain"
}

// antigravityConv is the conversation a transcript is of.
func antigravityConv(path string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
}

var antigravityHist struct {
	sync.Mutex
	path string
	mod  time.Time
	size int64
	ws   map[string]string
}

// antigravityHistory is the folder each conversation in appData's
// history.jsonl was in (its latest message's), by id; nil when there's no
// history to go by.
func antigravityHistory(appData string) map[string]string {
	path := filepath.Join(appData, "history.jsonl")
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	h := &antigravityHist
	h.Lock()
	defer h.Unlock()
	if h.path == path && h.mod.Equal(st.ModTime()) && h.size == st.Size() {
		return h.ws
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	ws := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var l struct {
			Workspace      string `json:"workspace"`
			ConversationID string `json:"conversationId"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.ConversationID != "" {
			ws[l.ConversationID] = cmp.Or(l.Workspace, ws[l.ConversationID])
		}
	}
	h.path, h.mod, h.size, h.ws = path, st.ModTime(), st.Size(), ws
	return ws
}

// antigravityOpen opens a transcript for the session finder, after a line
// saying its conversation and the folder it was in (from history.jsonl),
// which the transcript doesn't.
func antigravityOpen(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	id := antigravityConv(path)
	appData := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path)))))
	meta, _ := json.Marshal(map[string]string{"hammock_id": id, "hammock_cwd": antigravityHistory(appData)[id]})
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(append(meta, '\n')), f), f}, nil
}

// antigravityIndex reads a transcript (as antigravityOpen gives it) for
// the session finder: your messages, its replies and its tool calls.
func antigravityIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	var lines [][]byte
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if s := bytes.TrimSpace(line); len(s) > 0 {
			var meta struct {
				ID  string `json:"hammock_id"`
				Cwd string `json:"hammock_cwd"`
			}
			if bytes.HasPrefix(s, []byte(`{"hammock_`)) && json.Unmarshal(s, &meta) == nil {
				t.NativeID = cmp.Or(t.NativeID, meta.ID)
				t.Cwd = cmp.Or(t.Cwd, meta.Cwd)
			} else {
				lines = append(lines, s)
			}
		}
		if err != nil {
			break
		}
	}
	for _, s := range agentview.AntigravitySteps(lines) {
		switch {
		case s.Type == "USER_INPUT":
			text := agentview.AntigravityUserText(s.Content)
			if text == "" {
				continue
			}
			if t.Created.IsZero() {
				t.Created = s.CreatedAt
			}
			emit(thread.Record{Kind: thread.KindUser, Time: s.CreatedAt, Text: text})
		case s.Type == "PLANNER_RESPONSE":
			if text := strings.TrimSpace(s.Content); text != "" {
				emit(thread.Record{Kind: thread.KindAssistant, Time: s.CreatedAt, Text: text})
			}
			for _, tc := range s.ToolCalls {
				in, _ := json.Marshal(agentview.AntigravityArgs(tc.Args))
				emit(thread.Record{Kind: thread.KindToolCall, Time: s.CreatedAt, NativeTool: tc.Name, Input: in})
			}
		}
	}
	return 0, nil
}

// antigravityEvents: the Hammock event each of its hook events is (those
// antigravityHook doesn't decide itself).
var antigravityEvents = map[string]string{
	"SessionStart":   "SessionStart",
	"PreToolUse":     "PreToolUse",
	"PostToolUse":    "PostToolUse",
	"PostInvocation": "", // after every model call: says nothing new
}

// antigravityHook reads one of its hook events (the event named by Hammock's
// hook command: the payload doesn't say).
func antigravityHook(event string, payload []byte) Hook {
	var p struct {
		ConversationID       string `json:"conversationId"`
		ParentConversationID string `json:"parentConversationId"`
		TranscriptPath       string `json:"transcriptPath"`
		LastUserInput        string `json:"lastUserInput"`
		ToolCall             struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"toolCall"`
		InvocationNum     int    `json:"invocationNum"`
		TerminationReason string `json:"terminationReason"`
		Error             string `json:"error"`
		FinalModelOutput  string `json:"finalModelOutput"`
	}
	_ = json.Unmarshal(payload, &p)
	h := Hook{Event: event, SessionID: p.ConversationID, TranscriptPath: antigravityFull(p.TranscriptPath)}
	if mapped, ok := antigravityEvents[event]; ok {
		h.Event = mapped
	}
	if p.ToolCall.Name != "" {
		h.ToolName = p.ToolCall.Name
		h.ToolInput = antigravityToolInput(p.ToolCall.Args)
	}
	switch event {
	case "PreInvocation":
		// Before every call to the model: the first of a turn is your
		// message being taken up; the rest, it working on.
		h.Event = "PostToolUse"
		if p.InvocationNum <= 1 {
			h.Event, h.Prompt = "UserPromptSubmit", p.LastUserInput
		}
	case "Stop":
		h.LastMessage = p.FinalModelOutput
		switch p.TerminationReason {
		case "ERROR", "MAX_TOKEN_BUDGET_EXCEEDED":
			h.Event = "StopFailure"
			h.Error = strings.ToLower(p.TerminationReason)
			h.ErrorDetails = cmp.Or(p.Error, "Stopped with an error")
		}
	}
	if p.ParentConversationID != "" {
		// A subagent's: its conversation isn't the pane's, and its turn
		// ending isn't the agent's.
		h.SessionID, h.TranscriptPath = "", ""
		switch h.Event {
		case "SessionStart", "Stop", "StopFailure":
			h.Event = ""
		case "UserPromptSubmit":
			h.Event, h.Prompt = "PostToolUse", ""
		}
	}
	return h
}

// antigravityFull is the whole transcript beside the clipped one a hook
// names, when it's there.
func antigravityFull(path string) string {
	if filepath.Base(path) != "transcript.jsonl" {
		return path
	}
	full := filepath.Join(filepath.Dir(path), "transcript_full.jsonl")
	if _, err := os.Stat(full); err == nil {
		return full
	}
	return path
}

// antigravityToolInput is a tool call's arguments as Hammock's summaries read
// them: the command, or the file, under Claude's names.
func antigravityToolInput(raw json.RawMessage) json.RawMessage {
	in := agentview.AntigravityArgs(raw)
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	out := map[string]string{}
	if c := str("CommandLine"); c != "" {
		out["command"] = c
	}
	if f := str("TargetFile", "AbsolutePath", "Path", "File", "DirectoryPath"); f != "" {
		out["file_path"] = f
	}
	b, _ := json.Marshal(out)
	return b
}
