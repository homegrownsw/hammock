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
	"sort"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Pi is Mario Zechner's pi coding agent (github.com/badlogic/pi-mono,
// packages/coding-agent; npm @earendil-works/pi-coding-agent, formerly
// @mariozechner/pi-coding-agent). It has no shell hooks: Hammock's extension
// in ~/.pi/agent/extensions forwards its events (cmd/hmk/hooks_pi.go).
// Sessions are JSONL trees in ~/.pi/agent/sessions/--<cwd>--/<time>_<id>.jsonl
// ($PI_CODING_AGENT_DIR moves ~/.pi/agent; $PI_CODING_AGENT_SESSION_DIR or
// settings.json's sessionDir put every session flat in one folder).
// It keeps no subscription limits on disk.
var Pi = &Agent{
	ID: "pi", Order: 80, Name: "Pi", Short: "Pi", Command: "pi",
	Install:   "curl -fsSL https://pi.dev/install.sh | sh",
	Hooks:     true,
	ParseHook: parsePiHook,
	Start:     piStart,
	Resume:    func(id string) string { return "pi --session " + Quote(id) },
	ResumeID:  piResumeID,
	// No permission prompts to configure: Pi runs its tools without asking.
	Schedulable: true,
	Screen: Screen{
		// While it works, its editor's top border reads "── ⠋ Working ───"
		// (a braille spinner, then the message: "Working", a retry's, a
		// compaction's); status-indicator.ts and custom-editor.ts, checked
		// against pi 1.5.0's screen. An older one said "(escape to interrupt)".
		Working: regexp.MustCompile(`── [\x{2800}-\x{28FF}] |\([a-z+/]+ to interrupt\)`),
	},

	Roots:   piRoots,
	RootEnv: map[string]string{"PI_CODING_AGENT_DIR": "sessions", "PI_CODING_AGENT_SESSION_DIR": ""},
	Locate:  piLocate,
	Newest:  piNewest,
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			for _, pat := range []string{filepath.Join(r, "*", "*.jsonl"), filepath.Join(r, "*.jsonl")} {
				m, _ := filepath.Glob(pat)
				for _, f := range m {
					if piSessionName(filepath.Base(f)) {
						out = append(out, f)
					}
				}
			}
		}
		return out
	},
	Appends:  true,
	Index:    piIndex,
	OwnsFile: func(path string) bool { return piSessionName(filepath.Base(path)) },

	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewPi(cwd) },
	SlashCommands: piSlashCommands,
}

func init() { Register(Pi) }

// piAgentDir is Pi's config folder: $PI_CODING_AGENT_DIR (~ expanded), else ~/.pi/agent.
func piAgentDir() string {
	if v := os.Getenv("PI_CODING_AGENT_DIR"); v != "" {
		if v = expandTilde(v); filepath.IsAbs(v) {
			return v
		}
	}
	return home(".pi", "agent")
}

func expandTilde(p string) string {
	if p == "~" {
		return home()
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return home(rest)
	}
	return p
}

// piRoots: the default sessions folder (one folder per cwd inside it), and
// a custom one ($PI_CODING_AGENT_SESSION_DIR, settings.json's sessionDir:
// sessions flat inside it).
func piRoots() []string {
	dirs := []string{home(".pi", "agent", "sessions")}
	if d := piAgentDir(); d != home(".pi", "agent") {
		dirs = append(dirs, filepath.Join(d, "sessions"))
	}
	return append(dirs, piCustomDirs()...)
}

// piCustomDirs are the folders sessions go flat into instead.
func piCustomDirs() []string {
	var out []string
	if v := expandTilde(os.Getenv("PI_CODING_AGENT_SESSION_DIR")); filepath.IsAbs(v) {
		out = append(out, filepath.Clean(v))
	}
	if b, err := os.ReadFile(filepath.Join(piAgentDir(), "settings.json")); err == nil {
		var s struct {
			SessionDir string `json:"sessionDir"`
		}
		if json.Unmarshal(b, &s) == nil {
			if v := expandTilde(s.SessionDir); filepath.IsAbs(v) {
				out = append(out, filepath.Clean(v))
			}
		}
	}
	return out
}

// piSessionFile matches Pi's session file names: the ISO time it started
// with ":" and "." made "-", then its id.
var piSessionFile = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d-\d\d-\d\d-\d{3}Z_([A-Za-z0-9][A-Za-z0-9._-]*)\.jsonl$`)

func piSessionName(name string) bool { return piSessionFile.MatchString(name) }

// piID is a session id Pi accepts: letters, digits, ".", "_" and "-",
// starting and ending with a letter or digit.
var piID = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// piCwdDir is the folder Pi keeps a working directory's sessions in, under
// its sessions folder: "--" + the path without its leading "/", with "/",
// "\" and ":" made "-", + "--" (session-manager.ts getDefaultSessionDirPath).
func piCwdDir(cwd string) string {
	p := filepath.Clean(cwd)
	p = strings.TrimPrefix(strings.TrimPrefix(p, "/"), `\`)
	return "--" + strings.NewReplacer("/", "-", `\`, "-", ":", "-").Replace(p) + "--"
}

func piLocate(id string) (string, bool) {
	if !piID.MatchString(id) {
		return "", false
	}
	var found []string
	for _, r := range piRoots() {
		for _, pat := range []string{filepath.Join(r, "*", "*_"+id+".jsonl"), filepath.Join(r, "*_"+id+".jsonl")} {
			m, _ := filepath.Glob(pat)
			found = append(found, m...)
		}
	}
	return piNewestFile(found)
}

// piNewest is the latest session started in cwd: in its folder in the
// sessions folders, or a flat custom folder's whose header names cwd.
func piNewest(cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	var found []string
	custom := map[string]bool{}
	for _, d := range piCustomDirs() {
		custom[d] = true
	}
	for _, r := range piRoots() {
		if custom[r] {
			m, _ := filepath.Glob(filepath.Join(r, "*.jsonl"))
			for _, f := range m {
				if piSessionName(filepath.Base(f)) && piHeaderCwd(f) == filepath.Clean(cwd) {
					found = append(found, f)
				}
			}
			continue
		}
		m, _ := filepath.Glob(filepath.Join(r, piCwdDir(cwd), "*.jsonl"))
		for _, f := range m {
			if piSessionName(filepath.Base(f)) {
				found = append(found, f)
			}
		}
	}
	return piNewestFile(found)
}

// piHeaderCwd is the cwd in a session file's header (its first line).
func piHeaderCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	line, _ := bufio.NewReaderSize(f, 64<<10).ReadBytes('\n')
	var h struct {
		Type string `json:"type"`
		Cwd  string `json:"cwd"`
	}
	if json.Unmarshal(line, &h) != nil || h.Type != "session" || h.Cwd == "" {
		return ""
	}
	return filepath.Clean(h.Cwd)
}

// piNewestFile is the most recently changed of files.
func piNewestFile(files []string) (string, bool) {
	best, bestT := "", int64(0)
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() && st.ModTime().UnixNano() > bestT {
			best, bestT = f, st.ModTime().UnixNano()
		}
	}
	return best, best != ""
}

// piStart starts Pi interactively with a first message. Pi reads an
// argument starting with "@" as a file to attach and one starting with "-"
// as an option, so such a prompt gets a leading space.
func piStart(prompt string) string {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return "pi"
	}
	if strings.HasPrefix(p, "@") || strings.HasPrefix(p, "-") {
		p = " " + p
	}
	return "pi " + Quote(p)
}

// piResumeID is the session `pi --session <path|id>` or `--session-id <id>`
// opens: the id in a session file's name, or the (possibly partial) id.
func piResumeID(argv []string) string {
	v := argAfter(argv, "--session", "--session-id")
	if v == "" {
		return ""
	}
	if m := piSessionFile.FindStringSubmatch(filepath.Base(v)); m != nil {
		return m[1]
	}
	if piID.MatchString(v) {
		return v
	}
	return ""
}

// parsePiHook reads an event from Hammock's Pi extension: Pi's own event
// name as `hmk hook pi <event>` was given, and the small JSON payload the
// extension builds (session_id, transcript_path, and per event prompt,
// tool_name and tool_input, message, idle, aborted, error,
// last_assistant_message, reason).
func parsePiHook(event string, payload []byte) Hook {
	h := ParseHook("", payload, nil)
	var p struct {
		Idle    bool   `json:"idle"`
		Aborted bool   `json:"aborted"`
		Reason  string `json:"reason"`
		Title   string `json:"title"`
	}
	_ = json.Unmarshal(payload, &p)
	h.ToolInput = piToolInput(h.ToolInput)
	switch event {
	case "session_start":
		h.Event = "SessionStart"
	case "before_agent_start":
		h.Event = "UserPromptSubmit"
	case "tool_execution_start":
		h.Event = "PreToolUse"
	case "tool_execution_end":
		h.Event = "PostToolUse"
	case "ui_prompt_start":
		// An extension's dialog (a confirm, a select) waits for you.
		h.Event, h.Message = "PermissionRequest", cmp.Or(h.Message, p.Title, "Waiting for you")
	case "ui_prompt_end":
		// Answered: back to work, or to its prompt if it was idle (an
		// extension's command asked).
		if p.Idle {
			h.Event, h.NotificationType = "Notification", "idle_prompt"
		} else {
			h.Event, h.ToolName = "PostToolUse", ""
		}
	case "agent_settled":
		// It won't carry on by itself: done, stopped (Esc) or failed.
		h.Event = "Stop"
		if h.Error != "" && !p.Aborted {
			h.Event = "StopFailure"
			h.ErrorDetails = cmp.Or(h.ErrorDetails, h.Error)
			h.LastMessage = cmp.Or(h.LastMessage, h.Error)
		}
	case "session_shutdown":
		// Quitting; anything else (/new, /resume, /fork, /reload) starts
		// another session straight after.
		h.Event = ""
		if p.Reason == "quit" {
			h.Event = "SessionEnd"
		}
	default:
		h.Event = ""
	}
	return h
}

// piToolInput gives Pi's tool input the file_path key Hammock's summaries
// read ("edit: src/x.go"), from its own "path".
func piToolInput(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return in
	}
	var m map[string]any
	if json.Unmarshal(in, &m) != nil {
		return in
	}
	if p, ok := m["path"].(string); ok && m["file_path"] == nil {
		m["file_path"] = p
		if out, err := json.Marshal(m); err == nil {
			return out
		}
	}
	return in
}

// piIndex reads a Pi session for the session finder: what you said and
// what it replied, on every branch, its name (/name), cwd and start.
func piIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	var n int64
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return n, nil // a last line still being written is read next time
		}
		n += int64(len(line))
		piIndexLine(line, t, emit)
	}
}

func piIndexLine(line []byte, t *thread.Thread, emit func(thread.Record)) {
	var e struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Timestamp string          `json:"timestamp"`
		Cwd       string          `json:"cwd"`
		Name      *string         `json:"name"`
		Message   json.RawMessage `json:"message"`
	}
	if json.Unmarshal(bytes.TrimSpace(line), &e) != nil {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
	switch e.Type {
	case "session":
		if t.NativeID == "" {
			t.NativeID = e.ID
		}
		if t.Cwd == "" {
			t.Cwd = e.Cwd
		}
		if t.Created.IsZero() {
			t.Created = at
		}
		return
	case "session_info":
		if e.Name != nil {
			t.Title = strings.TrimSpace(*e.Name)
			t.Named = t.Title != ""
		}
		return
	case "message":
	default:
		return
	}
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(e.Message, &m) != nil {
		return
	}
	text := agentview.PiText(m.Content)
	if strings.TrimSpace(text) == "" {
		return
	}
	switch m.Role {
	case "user":
		emit(thread.Record{Kind: thread.KindUser, Time: at, Text: text})
	case "assistant":
		emit(thread.Record{Kind: thread.KindAssistant, Time: at, Text: text})
	}
}

// piSlashCommands lists what can follow "/" in Pi: its built-ins, your
// prompt templates (/name) and skills (/skill:name), and the project's.
func piSlashCommands(cwd string) []string {
	set := map[string]bool{}
	// pi-mono packages/coding-agent/src/core/slash-commands.ts, 1.5.0.
	for _, c := range []string{"settings", "model", "tree", "thinking", "scoped-models", "export", "import", "share", "bug",
		"copy", "name", "session", "changelog", "hotkeys", "fork", "clone", "trust", "login", "logout", "new", "compact",
		"resume", "reload", "quit", "mcp"} {
		set["/"+c] = true
	}
	dir := piAgentDir()
	prompts := []string{filepath.Join(dir, "prompts")}
	skills := []string{filepath.Join(dir, "skills"), home(".agents", "skills")}
	if cwd != "" {
		prompts = append(prompts, filepath.Join(cwd, ".pi", "prompts"))
		skills = append(skills, filepath.Join(cwd, ".pi", "skills"), filepath.Join(cwd, ".agents", "skills"))
	}
	for _, d := range prompts {
		if ents, err := os.ReadDir(d); err == nil {
			for _, e := range ents {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					set["/"+strings.TrimSuffix(e.Name(), ".md")] = true
				}
			}
		}
	}
	for _, d := range skills {
		for _, s := range skillsIn(d) {
			set["/skill:"+s] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
