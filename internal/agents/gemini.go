package agents

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Gemini is Google's Gemini CLI (github.com/google-gemini/gemini-cli,
// formats as of 0.69): hooks in ~/.gemini/settings.json, sessions as JSONL
// in ~/.gemini/tmp/<project>/chats/session-<time>-<id[:8]>.jsonl, where
// <project> is a short name ~/.gemini/projects.json gives the folder (its
// .project_root file says which), or, before 0.2x, the folder's SHA-256.
// GEMINI_CLI_HOME moves ~/.gemini. Its quota is only on Google's servers,
// so no usage.
var Gemini = &Agent{
	ID: "gemini", Order: 20, Name: "Gemini CLI", Short: "Gemini", Command: "gemini",
	Install:   "brew install gemini-cli",
	LiveUsage: geminiLiveUsage,
	Hooks:     true,
	ParseHook: geminiHook,
	// A prompt as an argument would start it interactively too, but with a
	// notice that it will: -i says so.
	Start:       func(prompt string) string { return geminiStart("gemini", "-i", prompt) },
	Resume:      func(id string) string { return "gemini --resume " + Quote(id) },
	ResumeID:    geminiResumeID,
	Schedulable: true,
	Screen:      geminiScreen,

	Roots:   geminiRoots,
	RootEnv: map[string]string{"GEMINI_CLI_HOME": filepath.Join(".gemini", "tmp")},
	Locate: func(id string) (string, bool) {
		if len(id) < 8 || strings.ContainsAny(id, `/\*?[`) {
			return "", false
		}
		var found []string
		for _, r := range geminiRoots() {
			m, _ := filepath.Glob(filepath.Join(r, "*", "chats", "session-*-"+id[:8]+".jsonl"))
			for _, p := range m {
				if geminiFileHas(p, `"sessionId":"`+id+`"`) {
					found = append(found, p)
				}
			}
		}
		return geminiNewestOf(found)
	},
	Newest: func(cwd string) (string, bool) {
		var found []string
		for _, r := range geminiRoots() {
			for _, dir := range geminiProjectDirs(r, cwd) {
				m, _ := filepath.Glob(filepath.Join(dir, "chats", "session-*.jsonl"))
				found = append(found, m...)
			}
		}
		return geminiNewestOf(found)
	},
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			m, _ := filepath.Glob(filepath.Join(r, "*", "chats", "session-*.jsonl"))
			out = append(out, m...)
		}
		return out
	},
	Index:    geminiIndex,
	Open:     geminiOpen,
	OwnsFile: geminiSession,

	NewParser: func(cwd string, _ bool) agentview.Parser { return agentview.NewGemini(cwd) },
}

// geminiScreen is how Gemini CLI and Qwen Code (its fork) show working,
// asking (ToolConfirmationMessage.tsx) and having been stopped.
var geminiScreen = Screen{
	NeedsInput:  regexp.MustCompile(`(?i)apply this change\?|do you want to proceed\?|allow execution of|allow once|allow for this session`),
	Interrupted: regexp.MustCompile(`Request cancelled\.`),
}

// geminiStart is command, then flag and the prompt quoted when there is one.
func geminiStart(command, flag, prompt string) string {
	if p := strings.TrimSpace(prompt); p != "" {
		return command + " " + flag + " " + Quote(p)
	}
	return command
}

// geminiSession: a main session's file (subagents' are in a folder beside
// them, named by their parent's id).
func geminiSession(path string) bool {
	name := filepath.Base(path)
	return strings.HasPrefix(name, "session-") && strings.HasSuffix(name, ".jsonl") && filepath.Base(filepath.Dir(path)) == "chats"
}

// geminiResumeID is the session `gemini --resume <id>` resumes (not an
// index or "latest", which name no session), or the one --session-id starts.
func geminiResumeID(argv []string) string {
	if id := argAfter(argv, "--resume", "-r"); uuidLike.MatchString(id) {
		return id
	}
	if id := argAfter(argv, "--session-id"); id != "" && !strings.HasPrefix(id, "-") {
		return id
	}
	return ""
}

// geminiEvents maps Gemini CLI's hook events (hooks/types.ts) to Hammock's.
var geminiEvents = map[string]string{
	"SessionStart": "SessionStart",
	"SessionEnd":   "SessionEnd",
	"BeforeAgent":  "UserPromptSubmit",
	"AfterAgent":   "Stop",
	"BeforeTool":   "PreToolUse",
	"AfterTool":    "PostToolUse",
	"Notification": "Notification",
	// Fired for every request and streamed chunk: not state.
	"BeforeModel": "", "AfterModel": "", "BeforeToolSelection": "", "PreCompress": "",
}

// geminiHook reads a Gemini CLI hook: Claude-shaped (session_id,
// transcript_path, hook_event_name, tool_name, tool_input, prompt), with
// AfterAgent's reply in prompt_response and its one notification, a tool
// waiting for permission, as notification_type "ToolPermission".
func geminiHook(event string, payload []byte) Hook {
	h := ParseHook(event, payload, geminiEvents)
	var p struct {
		PromptResponse   string `json:"prompt_response"`
		NotificationType string `json:"notification_type"`
		Details          struct {
			Title string `json:"title"`
		} `json:"details"`
	}
	_ = json.Unmarshal(payload, &p)
	switch h.Event {
	case "Stop":
		if h.LastMessage == "" {
			h.LastMessage = p.PromptResponse
		}
	case "Notification":
		if p.NotificationType == "ToolPermission" {
			h.Event = "PermissionRequest"
			if h.Message == "" {
				h.Message = p.Details.Title
			}
		}
	}
	return h
}

// geminiProjectDirs are the folders under root (…/.gemini/tmp) that may
// hold cwd's sessions: the short name projects.json gives it, any folder
// whose .project_root names it, and the hash older versions used.
func geminiProjectDirs(root, cwd string) []string {
	if cwd == "" {
		return nil
	}
	cwd = filepath.Clean(cwd)
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if !seen[d] && geminiDirExists(d) {
			seen[d] = true
			out = append(out, d)
		}
	}
	var reg struct {
		Projects map[string]string `json:"projects"`
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(root), "projects.json")); err == nil && json.Unmarshal(b, &reg) == nil {
		if slug := reg.Projects[cwd]; slug != "" && !strings.ContainsAny(slug, `/\`) {
			add(filepath.Join(root, slug))
		}
	}
	marks, _ := filepath.Glob(filepath.Join(root, "*", ".project_root"))
	for _, m := range marks {
		if b, err := os.ReadFile(m); err == nil && filepath.Clean(strings.TrimSpace(string(b))) == cwd {
			add(filepath.Dir(m))
		}
	}
	sum := sha256.Sum256([]byte(cwd))
	add(filepath.Join(root, hex.EncodeToString(sum[:])))
	return out
}

// geminiProjectRoot is the folder a session file's project is, from its
// project folder's .project_root, else projects.json.
func geminiProjectRoot(path string) string {
	proj := filepath.Dir(filepath.Dir(path))
	if b, err := os.ReadFile(filepath.Join(proj, ".project_root")); err == nil {
		if s := strings.TrimSpace(string(b)); filepath.IsAbs(s) {
			return s
		}
	}
	var reg struct {
		Projects map[string]string `json:"projects"`
	}
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(proj)), "projects.json")); err == nil && json.Unmarshal(b, &reg) == nil {
		for p, slug := range reg.Projects {
			if slug == filepath.Base(proj) {
				return p
			}
		}
	}
	return ""
}

// geminiOpen opens a session for the session finder: the file, after a
// line saying its project's folder, which the file itself doesn't.
func geminiOpen(path string) (io.ReadCloser, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	head, _ := json.Marshal(map[string]string{"$hammockProjectRoot": geminiProjectRoot(path)})
	return io.NopCloser(bytes.NewReader(append(append(head, '\n'), b...))), nil
}

// geminiIndex reads a Gemini CLI session (as geminiOpen gives it) for the
// session finder: your messages and its replies, in the order a resume
// would give them (a message written again replaces itself; $rewindTo
// drops what it rewound).
func geminiIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	type msg struct {
		at   time.Time
		user bool
		text string
	}
	var order []string
	msgs := map[string]*msg{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l struct {
			Root           string          `json:"$hammockProjectRoot"`
			SessionID      string          `json:"sessionId"`
			StartTime      time.Time       `json:"startTime"`
			Set            json.RawMessage `json:"$set"`
			RewindTo       string          `json:"$rewindTo"`
			ID             string          `json:"id"`
			Timestamp      time.Time       `json:"timestamp"`
			Type           string          `json:"type"`
			Content        json.RawMessage `json:"content"`
			DisplayContent json.RawMessage `json:"displayContent"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		switch {
		case l.Root != "":
			t.Cwd = l.Root
		case len(l.Set) > 0:
			var s struct {
				SessionID string `json:"sessionId"`
				Summary   string `json:"summary"`
			}
			_ = json.Unmarshal(l.Set, &s)
			if s.SessionID != "" {
				t.NativeID = s.SessionID
			}
			if s.Summary != "" && !t.Named {
				t.Title = s.Summary
			}
		case l.RewindTo != "":
			for i, id := range order {
				if id == l.RewindTo {
					for _, gone := range order[i:] {
						delete(msgs, gone)
					}
					order = order[:i]
					break
				}
			}
		case l.ID == "" && l.SessionID != "":
			if t.NativeID == "" {
				t.NativeID = l.SessionID
			}
			if t.Created.IsZero() {
				t.Created = l.StartTime
			}
		case l.Type == "user" || l.Type == "gemini":
			raw := l.Content
			if l.Type == "user" && len(l.DisplayContent) > 0 && string(l.DisplayContent) != "null" {
				raw = l.DisplayContent
			}
			text := strings.TrimSpace(geminiText(raw))
			if l.Type == "user" && (strings.HasPrefix(text, "<state_snapshot>") || strings.HasPrefix(text, "<session_context>") || strings.HasPrefix(text, "<hook_context>")) {
				text = ""
			}
			if _, ok := msgs[l.ID]; !ok {
				order = append(order, l.ID)
			}
			msgs[l.ID] = &msg{at: l.Timestamp, user: l.Type == "user", text: text}
		}
	}
	for _, id := range order {
		m := msgs[id]
		switch {
		case m.text == "":
		case m.user:
			emit(thread.Record{Kind: thread.KindUser, Time: m.at, Text: m.text})
		default:
			emit(thread.Record{Kind: thread.KindAssistant, Time: m.at, Text: m.text})
		}
	}
	return 0, sc.Err()
}

// geminiText is a message's text (a string, or parts, without thoughts).
func geminiText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text    string `json:"text"`
		Thought bool   `json:"thought"`
	}
	_ = json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// geminiFileHas: the start of the file at path contains s.
func geminiFileHas(path, s string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, _ := io.ReadFull(f, buf)
	return bytes.Contains(buf[:n], []byte(s))
}

// geminiNewestOf is the most recently changed of paths.
func geminiNewestOf(paths []string) (string, bool) {
	best, bestT := "", time.Time{}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.ModTime().After(bestT) {
			best, bestT = p, st.ModTime()
		}
	}
	return best, best != ""
}

func geminiDirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func init() { Register(Gemini) }

// geminiRoots are the folders Gemini CLI keeps its projects' sessions in.
func geminiRoots() []string {
	dirs := []string{home(".gemini", "tmp")}
	if d := envOrEmpty("GEMINI_CLI_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, ".gemini", "tmp"))
	}
	// Under macOS's sandbox-exec it keeps its runtime state here instead.
	if d := home(".cache", ".gemini", "tmp"); geminiDirExists(d) {
		dirs = append(dirs, d)
	}
	return dirs
}
