package agents

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Kiro is AWS's Kiro CLI (kiro.dev/cli), the successor of Amazon Q Developer
// CLI. Since 3.0 (2026-11-03) it is closed source and runs its "V3" engine
// by default: a Rust host (kiro-cli) that starts a bundled JavaScript agent
// (@kiro/agent's acp-server.js) and TUI (tui.js, process title "kiro"). The
// open-source Q CLI (github.com/aws/amazon-q-developer-cli, 1.19.7) is
// V2's ancestor and no longer describes it. What Hammock relies on was read from
// the 3.1.0 release (kirocli-universal-darwin.tar.gz, build 5acd3b9,
// 2026-11-05), its embedded acp-server.js (@kiro/agent 0.66.91) and tui.js:
//
//   - `kiro-cli chat` starts it; a prompt as its argument is sent as the
//     first message and it stays interactive (tui.js: `!noInteractive &&
//     input`). `kiro-cli chat --resume-id ID` resumes a session, `--resume`
//     the latest in the folder; `--v3` picks the V3 engine for someone who
//     kept V2 when 3.0 asked.
//   - Hooks: every *.json in ~/.kiro/hooks ($KIRO_HOME/hooks) is a global
//     hook file, {"version":"v1","hooks":[{name, trigger, matcher, action:
//     {type:"command", command}, timeout}]}, run for every agent (Hammock needn't
//     touch the person's agent configs), the event's JSON on stdin:
//     session_id, hook_event_name, cwd, and per event prompt, tool_name,
//     tool_input, tool_response (acp-server.js buildHookPayload). Its
//     triggers are named as Claude Code's (SessionStart, UserPromptSubmit,
//     PreToolUse, PostToolUse, Stop, SessionEnd; also PreCompact,
//     PostCompact, file and spec-task ones). No hook says it's waiting for
//     approval, so its screen does (ScreenAsks). Hooks only run in a folder
//     you've trusted (its "Trust this workspace?"); elsewhere Hammock reads its
//     screen.
//   - Sessions: <KIRO_HOME or ~/.kiro>/sessions/<workspace>/sess_<uuid>/,
//     session.json (id, title, workspacePaths, createdAt, lastModifiedAt,
//     createdReason…) beside messages.jsonl, one {id, timestamp, payload}
//     per line, a row written again under its id as it changes
//     (agentview.Kiro). <workspace> is the first 16 hex digits of the SHA-256
//     of its folders, normalized, sorted and joined by NUL ("_global" for
//     none).
//   - Its limits (credits) come from its server (/usage); nothing on disk
//     says them, so no Usage.
//
// V2 (`--v2`, the pre-3.0 engine) keeps sessions elsewhere
// (~/.kiro/sessions/cli/<id>.jsonl) and hooks only in agent configs: Hammock
// reads neither, and finds V2 by its process and screen alone.
var Kiro = &Agent{
	ID: "kiro", Order: 160, Name: "Kiro CLI", Short: "Kiro", Command: "kiro-cli",
	// The TUI sets its process title to "kiro"; kiro-cli-chat ran chats
	// before 3.0's single binary.
	Procs:      []string{"kiro", "kiro-cli-chat"},
	Install:    "curl -fsSL https://cli.kiro.dev/install | bash",
	LiveUsage:  kiroLiveUsage,
	Hooks:      true,
	HookEvents: kiroHookEvents,
	Start: func(prompt string) string {
		if p := strings.TrimSpace(prompt); p != "" {
			return "kiro-cli chat " + Quote(p)
		}
		return "kiro-cli chat"
	},
	// Hammock's sessions are V3's: --v3 so someone who kept V2 resumes them in
	// the engine that wrote them.
	Resume:      func(id string) string { return "kiro-cli chat --v3 --resume-id " + Quote(id) },
	ResumeID:    kiroResumeID,
	Schedulable: true,
	ScreenAsks:  true,
	Screen: Screen{
		// Its spinner: "Thinking... (esc to cancel)", or "(esc to cancel ·
		// ctrl+o to view)", the key being whatever
		// chat.keybindings.cancelStream says (tui.js). Its approval card's
		// title says "esc to cancel" too, unbracketed, so only the bracket
		// counts.
		Working: regexp.MustCompile(`(?i)\([a-z0-9+]{1,12} to cancel(?:\)| ·)`),
		// The approval card: "<tool> requires approval", "[y] allow once ·
		// [n] deny", its options "Yes, single permission" / "Trust, always
		// allow in this session"; V2's "Allow this action? [y/n/t]".
		NeedsInput: regexp.MustCompile(`(?i)requires approval|\[y\] allow once|yes, single permission|trust, always allow in this session|allow this action\?`),
		// What Esc leaves in the transcript.
		Interrupted: regexp.MustCompile(`user interrupted`),
	},

	Roots:   kiroRoots,
	RootEnv: map[string]string{"KIRO_HOME": "sessions"},
	Locate: func(id string) (string, bool) {
		if !kiroIDRE.MatchString(id) {
			return "", false
		}
		var found []string
		for _, r := range kiroRoots() {
			m, _ := filepath.Glob(filepath.Join(r, "*", id, "messages.jsonl"))
			found = append(found, m...)
		}
		return kiroNewestOf(found)
	},
	Newest: func(cwd string) (string, bool) {
		if cwd == "" {
			return "", false
		}
		var found []string
		for _, r := range kiroRoots() {
			for _, h := range kiroWorkspaceHashes(cwd) {
				m, _ := filepath.Glob(filepath.Join(r, h, "sess_*", "messages.jsonl"))
				for _, p := range m {
					if !kiroSubagent(p) {
						found = append(found, p)
					}
				}
			}
		}
		return kiroNewestOf(found)
	},
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			m, _ := filepath.Glob(filepath.Join(r, "*", "sess_*", "messages.jsonl"))
			for _, p := range m {
				if !kiroSubagent(p) {
					out = append(out, p)
				}
			}
		}
		return out
	},
	Index:    kiroIndex,
	Open:     kiroOpen,
	OwnsFile: kiroOwnsFile,

	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewKiro(cwd) },
	SlashCommands: func(string) []string { return kiroSlashCommands },
}

func init() { Register(Kiro) }

// kiroHookEvents: its triggers are Claude Code's names; those Hammock has no
// use for are ignored (2.x's camelCase names, for an agent config's hooks).
var kiroHookEvents = map[string]string{
	"agentSpawn": "SessionStart", "userPromptSubmit": "UserPromptSubmit", "preToolUse": "PreToolUse",
	"postToolUse": "PostToolUse", "stop": "Stop",
	"PreCompact": "", "PostCompact": "", "PostFileCreate": "", "PostFileSave": "", "PostFileDelete": "",
	"PreTaskExec": "", "PostTaskExec": "", "Manual": "",
}

// kiroSlashCommands are the V3 TUI's own (tui.js, 3.1.0).
var kiroSlashCommands = []string{"/agent", "/chat", "/clear", "/compact", "/context", "/effort", "/exit", "/feedback",
	"/goal", "/help", "/hooks", "/knowledge", "/mcp", "/memories", "/model", "/permissions", "/plan", "/prompts",
	"/quit", "/rewind", "/sessions", "/settings", "/spec", "/stats", "/tangent", "/tools", "/usage", "/workflows"}

// kiroIDRE is a session id as it names a folder (its isValidSessionId).
var kiroIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// kiroRoots are the folders Kiro CLI keeps its sessions in.
func kiroRoots() []string {
	dirs := []string{home(".kiro", "sessions")}
	if d := envOrEmpty("KIRO_HOME"); filepath.IsAbs(d) {
		dirs = append(dirs, filepath.Join(d, "sessions"))
	}
	return dirs
}

// kiroResumeID is the session `kiro-cli chat --resume-id ID` resumes.
func kiroResumeID(argv []string) string {
	if id := argAfter(argv, "--resume-id"); kiroIDRE.MatchString(id) {
		return id
	}
	return ""
}

// kiroWorkspaceHashes are the names Kiro may have given cwd's folder of
// sessions: the hash of the folder as given, and as its real path (Node's
// process.cwd() resolves symlinks, /tmp to /private/tmp).
func kiroWorkspaceHashes(cwd string) []string {
	hash := func(p string) string {
		p = filepath.Clean(p)
		sum := sha256.Sum256([]byte(p))
		return hex.EncodeToString(sum[:])[:16]
	}
	out := []string{hash(cwd)}
	if real, err := filepath.EvalSymlinks(cwd); err == nil && hash(real) != out[0] {
		out = append(out, hash(real))
	}
	return out
}

// kiroOwnsFile: path is a session's messages.jsonl.
func kiroOwnsFile(path string) bool {
	return filepath.Base(path) == "messages.jsonl" && strings.HasPrefix(filepath.Base(filepath.Dir(path)), "sess_")
}

// kiroMeta is what Hammock reads of a session's session.json.
type kiroMeta struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	WorkspacePaths []string `json:"workspacePaths"`
	CreatedAt      string   `json:"createdAt"`
	CreatedReason  string   `json:"createdReason"`
}

// kiroReadMeta reads the session.json beside a session's messages.jsonl.
func kiroReadMeta(path string) (kiroMeta, bool) {
	var m kiroMeta
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "session.json"))
	if err != nil || len(b) > 4<<20 || json.Unmarshal(b, &m) != nil {
		return m, false
	}
	return m, true
}

// kiroSubagent: path is a subagent's session, which its own session list
// hides too (it belongs to the one that started it).
func kiroSubagent(path string) bool {
	m, _ := kiroReadMeta(path)
	return m.CreatedReason == "subagent"
}

// kiroOpen opens a session for the session finder (and a look before
// resuming): its messages, after a line with what session.json says, as
// the messages don't say the session's folder, title or id.
func kiroOpen(path string) (io.ReadCloser, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, _ := kiroReadMeta(path)
	head := map[string]string{"type": "hammock_session", "id": m.ID, "title": m.Title, "created": m.CreatedAt}
	if head["id"] == "" {
		head["id"] = filepath.Base(filepath.Dir(path))
	}
	if len(m.WorkspacePaths) > 0 {
		head["cwd"] = m.WorkspacePaths[0]
	}
	line, _ := json.Marshal(head)
	return io.NopCloser(bytes.NewReader(append(append(line, '\n'), b...))), nil
}

// kiroIndex reads a session (as kiroOpen gives it) for the session finder:
// your messages and its replies, a row written again replacing itself.
func kiroIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
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
			Type      string                `json:"type"`
			ID        string                `json:"id"`
			Title     string                `json:"title"`
			Cwd       string                `json:"cwd"`
			Created   string                `json:"created"`
			Timestamp time.Time             `json:"timestamp"`
			Payload   agentview.KiroPayload `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Type == "hammock_session" {
			t.NativeID, t.Cwd = l.ID, l.Cwd
			if !t.Named && l.Title != "" {
				t.Title = l.Title
			}
			if at, err := time.Parse(time.RFC3339Nano, l.Created); err == nil {
				t.Created = at
			}
			continue
		}
		p := l.Payload
		var m *msg
		switch {
		case p.Type == "user" && p.Shown():
			m = &msg{at: l.Timestamp, user: true, text: strings.TrimSpace(p.Content)}
		case p.Type == "assistant" && (p.OperationType == "" || p.OperationType == "Say") && p.SubExecutionID == "":
			m = &msg{at: l.Timestamp, text: strings.TrimSpace(p.Content)}
		default:
			continue
		}
		if l.ID == "" {
			l.ID = fmt.Sprint("line", len(order))
		}
		if _, ok := msgs[l.ID]; !ok {
			order = append(order, l.ID)
		}
		msgs[l.ID] = m
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

// kiroNewestOf is the most recently changed of paths.
func kiroNewestOf(paths []string) (string, bool) {
	best, bestT := "", time.Time{}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.ModTime().After(bestT) {
			best, bestT = p, st.ModTime()
		}
	}
	return best, best != ""
}
