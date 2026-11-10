package agents

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Kimi is Moonshot AI's Kimi Code CLI (github.com/MoonshotAI/kimi-code,
// read at 2.5.2, its source and docs/en): a TypeScript CLI whose command is
// `kimi`, installed as a native binary in ~/.kimi-code/bin (or by npm, as
// @moonshot-ai/kimi-code), whose process calls itself kimi-code. It
// replaced the Python kimi-cli (archived at 1.52, data in ~/.kimi), whose
// command was kimi too.
//
//   - hooks: [[hooks]] tables in ~/.kimi-code/config.toml ({event, matcher,
//     command, timeout}), with Claude Code's event names and snake_case,
//     Claude-shaped payloads (hook_event_name, session_id, tool_name,
//     tool_input; UserPromptSubmit's prompt is content parts). No
//     transcript_path: the session's file is found by its id. A hook fails
//     open (only exit 2 blocks).
//   - sessions: ~/.kimi-code/sessions/<workspace>/<session id>/, the
//     workspace "wd_<folder name>_<sha256(folder)[:12]>" (or a managed
//     project's "wm_<uuid>"), the id "session_<uuid>"; state.json (title,
//     cwd, createdAt) and agents/main/wire.jsonl, the main agent's journal,
//     which only grows (agentview/kimi.go). session_index.jsonl lists each
//     session's folder. KIMI_CODE_HOME moves ~/.kimi-code.
//   - `kimi --session ID` (-S, or the hidden -r/--resume) resumes; -c
//     continues the folder's latest. The interactive CLI takes no first
//     message (an argument is an unknown command, and -p runs one prompt
//     without the TUI or any approvals), so Start can't pass one.
//   - its plan's quotas come from Kimi's servers (/usage): nothing on disk.
var Kimi = &Agent{
	ID: "kimi", Order: 120, Name: "Kimi Code", Short: "Kimi", Command: "kimi",
	ShellMode: true,
	Procs:     []string{"kimi-code"},
	Install:   "curl -fsSL https://code.kimi.com/kimi-code/install.sh | bash",
	LiveUsage: kimiLiveUsage,
	Hooks:     true,
	ParseHook: kimiHook,
	// Kimi starts with no first message: see above.
	// No first message on its command line: typed in once it is up.
	TypePrompt:  true,
	Schedulable: true,
	Resume:      func(id string) string { return "kimi --session " + Quote(id) },
	ResumeID:    kimiResumeID,
	Screen: Screen{
		// Its activity line: a braille spinner and "Working…" or
		// "Compacting…", or a moon while it waits for the model
		// (tui/kimi-tui.ts, constant/rendering.ts).
		Working: regexp.MustCompile(`[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏] (Working|Compacting)…|[🌑🌒🌓🌔🌕🌖🌗🌘]`),
		// The approval panel's question (dialogs/approval-panel.ts).
		NeedsInput: regexp.MustCompile(`Run this command\?|Write this file\?|Apply these edits\?|Stop this task\?|Ready to build with this plan\?|Approve [A-Za-z_][\w.-]*\?`),
	},

	Roots:   kimiRoots,
	RootEnv: map[string]string{"KIMI_CODE_HOME": "sessions"},
	Locate: func(id string) (string, bool) {
		if !kimiIDOK(id) {
			return "", false
		}
		for _, r := range kimiRoots() {
			if m, _ := filepath.Glob(filepath.Join(r, "*", id, "agents", "main", "wire.jsonl")); len(m) > 0 {
				return m[0], true
			}
		}
		return "", false
	},
	Newest: kimiNewest,
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			m, _ := filepath.Glob(filepath.Join(r, "*", "*", "agents", "main", "wire.jsonl"))
			out = append(out, m...)
		}
		return out
	},
	// Not Appends: the finder reads the title from state.json beside the
	// journal (kimiOpen), which a rename changes.
	Index:    kimiIndex,
	Open:     kimiOpen,
	Names:    kimiNames,
	OwnsFile: kimiOwns,

	NewParser:     func(cwd string, sub bool) agentview.Parser { return agentview.NewKimi(cwd, sub) },
	SlashCommands: kimiSlashCommands,
}

// kimiHome is ~/.kimi-code, or $KIMI_CODE_HOME.
func kimiHome() string {
	if d := envOrEmpty("KIMI_CODE_HOME"); d != "" {
		return d
	}
	return home(".kimi-code")
}

func kimiRoots() []string {
	dirs := []string{home(".kimi-code", "sessions")}
	if d := envOrEmpty("KIMI_CODE_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "sessions"))
	}
	return dirs
}

// kimiIDOK: id can be a session folder's name.
func kimiIDOK(id string) bool {
	return id != "" && filepath.Base(id) == id && !strings.HasPrefix(id, ".") && !strings.ContainsAny(id, `*?[\`)
}

// kimiOwns: path is a session's journal (an agent's wire.jsonl in it).
func kimiOwns(path string) bool {
	return filepath.Base(path) == "wire.jsonl" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "agents"
}

// kimiResumeID is the session `kimi --session ID` (-S, -r, --resume) resumes.
func kimiResumeID(argv []string) string {
	if id := argAfter(argv, "--session", "-S", "--resume", "-r"); kimiIDOK(id) && !strings.HasPrefix(id, "-") {
		return id
	}
	return ""
}

// kimiWorkspace is the folder Kimi keeps cwd's sessions in
// (_base/utils/workdir-slug.ts's encodeWorkDirKey).
func kimiWorkspace(cwd string) string {
	norm := strings.TrimRight(strings.ReplaceAll(cwd, `\`, "/"), "/")
	base := norm[strings.LastIndex(norm, "/")+1:]
	slug := kimiSlugJunk.ReplaceAllString(strings.ToLower(base), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	if slug == "" || slug == "." || slug == ".." {
		slug = "workspace"
	}
	sum := sha256.Sum256([]byte(norm))
	return "wd_" + slug + "_" + hex.EncodeToString(sum[:])[:12]
}

var kimiSlugJunk = regexp.MustCompile(`[^a-z0-9._-]+`)

// kimiNewest is the latest session started in cwd: in its workspace
// folder, else any session_index.jsonl says was (a managed project's).
func kimiNewest(cwd string) (string, bool) {
	cwds := []string{filepath.Clean(cwd)}
	if real, err := filepath.EvalSymlinks(cwd); err == nil && real != cwds[0] {
		cwds = append(cwds, real)
	}
	var found []string
	for _, r := range kimiRoots() {
		for _, c := range cwds {
			m, _ := filepath.Glob(filepath.Join(r, kimiWorkspace(c), "*", "agents", "main", "wire.jsonl"))
			found = append(found, m...)
		}
	}
	if len(found) == 0 {
		for _, r := range kimiRoots() {
			for _, dir := range kimiIndexDirs(filepath.Join(filepath.Dir(r), "session_index.jsonl"), cwds) {
				if p := filepath.Join(dir, "agents", "main", "wire.jsonl"); kimiFileExists(p) && within(p, r) {
					found = append(found, p)
				}
			}
		}
	}
	return kimiNewestOf(found)
}

// kimiIndexDirs are the session folders session_index.jsonl gives for any
// of cwds, less those since deleted.
func kimiIndexDirs(path string, cwds []string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	want := map[string]bool{}
	for _, c := range cwds {
		want[c] = true
	}
	dirs := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var l struct {
			SessionID  string `json:"sessionId"`
			SessionDir string `json:"sessionDir"`
			WorkDir    string `json:"workDir"`
			Deleted    bool   `json:"deleted"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.SessionID == "" {
			continue
		}
		switch {
		case l.Deleted:
			delete(dirs, l.SessionID)
		case want[filepath.Clean(l.WorkDir)] && l.SessionDir != "":
			dirs[l.SessionID] = l.SessionDir
		}
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, d)
	}
	return out
}

func kimiFileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// kimiNewestOf is the most recently written of paths.
func kimiNewestOf(paths []string) (string, bool) {
	best, bestMod := "", time.Time{}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.ModTime().After(bestMod) {
			best, bestMod = p, st.ModTime()
		}
	}
	return best, best != ""
}

// kimiState is a session's state.json, as far as Hammock reads it
// (session/sessionMetadata/sessionMetadata.ts).
type kimiState struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	TitleKind     string          `json:"titleKind"` // replaceable, generated, custom
	IsCustomTitle bool            `json:"isCustomTitle"`
	Cwd           string          `json:"cwd"`
	CreatedAt     json.RawMessage `json:"createdAt"` // unix ms; an ISO time in older ones
}

func (s kimiState) named() bool { return s.TitleKind == "custom" || s.IsCustomTitle }

func (s kimiState) created() time.Time {
	var ms float64
	if json.Unmarshal(s.CreatedAt, &ms) == nil && ms > 0 {
		return time.UnixMilli(int64(ms))
	}
	var t time.Time
	_ = json.Unmarshal(s.CreatedAt, &t)
	return t
}

// kimiSessionDir is the session folder a journal is in
// (<session>/agents/<agent>/wire.jsonl).
func kimiSessionDir(journal string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(journal)))
}

// kimiStateLine is the first line kimiOpen gives the finder: the session's
// id (its folder's name) and state.json.
type kimiStateLine struct {
	Type  string          `json:"type"` // "hammock.kimi.state"
	ID    string          `json:"id"`
	State json.RawMessage `json:"state,omitempty"`
}

const kimiStateType = "hammock.kimi.state"

// kimiOpen opens a journal for the finder, after a line of its own saying
// what state.json beside it does.
func kimiOpen(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	dir := kimiSessionDir(path)
	line := kimiStateLine{Type: kimiStateType, ID: filepath.Base(dir)}
	if st, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil && len(st) < 1<<20 && json.Valid(st) {
		var buf bytes.Buffer
		if json.Compact(&buf, st) == nil {
			line.State = buf.Bytes()
		}
	}
	head, _ := json.Marshal(line)
	return struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(append(head, '\n')), f), f}, nil
}

// kimiIndex reads a session for the finder (through kimiOpen): its id,
// folder and title, your messages and Kimi's replies.
func kimiIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			kimiIndexLine(line, t, emit)
		}
		if errors.Is(err, io.EOF) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func kimiIndexLine(line []byte, t *thread.Thread, emit func(thread.Record)) {
	if bytes.Contains(line, []byte(`"type":"`+kimiStateType+`"`)) {
		var l kimiStateLine
		if json.Unmarshal(line, &l) != nil || l.Type != kimiStateType {
			return
		}
		t.NativeID = l.ID
		var s kimiState
		if json.Unmarshal(l.State, &s) == nil {
			if s.ID != "" {
				t.NativeID = s.ID
			}
			if s.Cwd != "" {
				t.Cwd = s.Cwd
			}
			t.Title, t.Named = s.Title, s.named()
			t.Created = s.created()
		}
		return
	}
	rec, ok := agentview.ParseKimi(line)
	if !ok {
		return
	}
	at := rec.At()
	switch {
	case rec.Type == "config.update":
		if t.Cwd == "" {
			t.Cwd = rec.CwdOf()
		}
	case rec.Message.Typed():
		if text := rec.Message.Text(); text != "" {
			if t.Created.IsZero() {
				t.Created = at
			}
			emit(thread.Record{Kind: thread.KindUser, Time: at, Text: text})
		}
	default:
		if text := rec.KimiReplyText(); text != "" {
			emit(thread.Record{Kind: thread.KindAssistant, Time: at, Text: text})
		}
	}
}

// kimiNames are the titles you gave sessions (/title), by id: a rename
// changes state.json, not the journal the finder watches.
func kimiNames(roots []string) map[string]string {
	out := map[string]string{}
	for _, r := range roots {
		m, _ := filepath.Glob(filepath.Join(r, "*", "*", "state.json"))
		for _, p := range m {
			data, err := os.ReadFile(p)
			if err != nil || len(data) > 1<<20 {
				continue
			}
			var s kimiState
			if json.Unmarshal(data, &s) != nil || !s.named() || strings.TrimSpace(s.Title) == "" {
				continue
			}
			out[cmp.Or(s.ID, filepath.Base(filepath.Dir(p)))] = s.Title
		}
	}
	return out
}

// kimiHook reads one of Kimi Code's hook events. Its payloads are Claude
// Code's shape, but UserPromptSubmit's prompt is content parts, and the
// events Hammock reads under other names: a failed tool's, an approval's
// result (back to work), an interruption (Esc: in place of Stop) and a
// failed turn's error. Its tools' inputs are given Claude Code's field
// names.
func kimiHook(event string, payload []byte) Hook {
	h := ParseHook(event, payload, kimiHookEvents)
	var p struct {
		Prompt       json.RawMessage `json:"prompt"`
		ErrorType    string          `json:"error_type"`
		ErrorMessage string          `json:"error_message"`
	}
	if json.Unmarshal(payload, &p) == nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if h.Prompt == "" && json.Unmarshal(p.Prompt, &parts) == nil {
			var text []string
			for _, part := range parts {
				if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
					text = append(text, strings.TrimSpace(part.Text))
				}
			}
			h.Prompt = strings.Join(text, "\n")
		}
		if h.Event == "StopFailure" {
			h.Error, h.ErrorDetails = cmp.Or(p.ErrorType, h.Error), cmp.Or(p.ErrorMessage, h.ErrorDetails)
		}
	}
	if h.ToolInput != nil {
		// path as file_path, multi_select as multiSelect: as Claude's read.
		h.ToolInput = agentview.KimiToolInput(h.ToolName, h.ToolInput)
	}
	return h
}

// kimiHookEvents maps Kimi's events onto Hammock's.
var kimiHookEvents = map[string]string{
	"PostToolUseFailure": "PostToolUse",
	"PermissionResult":   "PostToolUse",
	"Interrupt":          "Stop",
	// Fired often, or about subagents and background tasks: not what the
	// pane is doing.
	"UserPromptQueued": "", "TurnStarted": "", "SessionHeartbeat": "", "SubagentStart": "", "SubagentStop": "",
	"TaskStarted": "", "PreCompact": "", "PostCompact": "", "Notification": "",
}

// kimiSlashCommands lists what can follow "/" in Kimi Code: its built-ins
// (tui/commands/registry.ts, with aliases), and skills, run as
// /skill:<name>, from ~/.kimi-code/skills, ~/.agents/skills and the
// project's .kimi-code/skills and .agents/skills.
func kimiSlashCommands(cwd string) []string {
	set := map[string]bool{}
	for _, c := range []string{"add-dir", "auto", "btw", "clear", "compact", "config", "copy", "desktop", "editor", "effort", "exit",
		"experimental", "experiments", "export-debug-zip", "export-md", "feedback", "fork", "goal", "help", "import-sessions", "init",
		"login", "logout", "mcp", "model", "new", "permission", "plan", "plugins", "provider", "providers", "reload", "reload-tui",
		"remote-control", "rename", "resume", "secondary-model", "sessions", "settings", "status", "subagent-model", "swarm", "task",
		"tasks", "theme", "thinking", "title", "tower", "undo", "usage", "version", "web", "yes", "yolo"} {
		set["/"+c] = true
	}
	for _, dir := range []string{filepath.Join(kimiHome(), "skills"), home(".agents", "skills"),
		filepath.Join(cwd, ".kimi-code", "skills"), filepath.Join(cwd, ".agents", "skills")} {
		for _, s := range skillsIn(dir) {
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

func init() { Register(Kimi) }
