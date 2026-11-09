package agents

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Droid is Factory's Droid CLI (read from droid 1.6.0, the npm package
// @factory/cli-darwin-arm64's bundled source, and docs.factory.com's CLI
// and hooks references):
//
//   - hooks in Claude Code's shape, in ~/.factory/hooks.json (keyed by
//     event), or the "hooks" key of ~/.factory/settings.json when there's no
//     hooks.json; payloads carry session_id, transcript_path and
//     hook_event_name; permission prompts send Notification with
//     notification_type permission_prompt (there's no PermissionRequest).
//   - sessions as JSONL in ~/.factory/sessions/<project>/<id>.jsonl, the
//     project being the folder's real path with "/" as "-" ("-Users-me-app"),
//     with <id>.settings.json beside each; a subagent's session is a file of
//     its own there, its session_start naming the calling session.
//     $FACTORY_HOME_OVERRIDE replaces the home folder.
//   - `droid "prompt"` starts it with a first message, `droid --resume ID`
//     (-r) resumes. A Bun binary, so its process is "droid".
//   - its usage limits are Factory's, from its servers: nothing on disk.
var Droid = &Agent{
	ID: "droid", Order: 60, Name: "Factory Droid", Short: "Droid", Command: "droid",
	Install:   "curl -fsSL https://app.factory.ai/cli | sh",
	LiveUsage: droidLiveUsage,
	Hooks:     true,
	// AskUser waits for you as soon as it starts, and sends a Notification
	// of type elicitation_dialog ("Factory CLI is asking the user 1
	// question(s)"): waiting for you, though not a permission prompt
	// (which is a Notification of type permission_prompt).
	ParseHook: func(event string, payload []byte) Hook {
		h := ParseHook(event, payload, nil)
		switch {
		case h.Event == "PreToolUse" && h.ToolName == "AskUser":
			h = Hook{Event: "PermissionRequest", SessionID: h.SessionID, TranscriptPath: h.TranscriptPath, Message: "Droid is asking you a question"}
		case h.Event == "Notification" && h.NotificationType == "elicitation_dialog":
			h.Event = "PermissionRequest"
		}
		return h
	},
	Resume: func(id string) string { return "droid --resume " + Quote(id) },
	ResumeID: func(argv []string) string {
		// -r is --resume in the interactive CLI (in `droid exec`, it's the
		// reasoning effort, never a session id).
		if id := argAfter(argv, "--resume", "-r", "--session-id", "-s"); uuidLike.MatchString(id) {
			return id
		}
		return ""
	},
	Schedulable: true,
	Screen: Screen{
		// Its spinner line ("ESC to stop") and the permission prompt's
		// options ("Yes, allow", "Yes, and always allow …", "No, cancel").
		Working:    regexp.MustCompile(`(?i)esc to (stop|interrupt)|ctrl\+c to interrupt`),
		NeedsInput: regexp.MustCompile(`Yes, allow|Yes, and always allow|No, cancel|(?i)do you want to proceed|\(y/n\)`),
	},

	Roots:   droidRoots,
	RootEnv: map[string]string{"FACTORY_HOME_OVERRIDE": filepath.Join(".factory", "sessions")},
	Locate: func(id string) (string, bool) {
		if id == "" || filepath.Base(id) != id || strings.HasPrefix(id, ".") {
			return "", false
		}
		for _, r := range droidRoots() {
			if m, _ := filepath.Glob(filepath.Join(r, "*", id+".jsonl")); len(m) > 0 {
				return m[0], true
			}
			if p := filepath.Join(r, id+".jsonl"); droidFileExists(p) {
				return p, true
			}
		}
		return "", false
	},
	Newest: func(cwd string) (string, bool) {
		for _, r := range droidRoots() {
			if p := newestDroid(filepath.Join(r, droidProject(cwd))); p != "" {
				return p, true
			}
		}
		return "", false
	},
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			// Project folders start with "-"; btw holds /btw side questions.
			m, _ := filepath.Glob(filepath.Join(r, "-*", "*.jsonl"))
			out = append(out, m...)
			m, _ = filepath.Glob(filepath.Join(r, "*.jsonl"))
			out = append(out, m...)
		}
		return out
	},
	// Not Appends: renaming a session rewrites its first line.
	Index:    droidIndex,
	OwnsFile: func(path string) bool { return strings.HasSuffix(path, ".jsonl") },

	NewParser:     func(cwd string, sub bool) agentview.Parser { return agentview.NewDroid(cwd, sub) },
	SlashCommands: droidSlashCommands,
}

func droidRoots() []string {
	dirs := []string{home(".factory", "sessions")}
	if d := envOrEmpty("FACTORY_HOME_OVERRIDE"); d != "" {
		dirs = append(dirs, filepath.Join(d, ".factory", "sessions"))
	}
	return dirs
}

func droidFileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// droidProject is the folder Droid keeps cwd's sessions in: its real path,
// without leading or trailing slashes, "/" as "-", after a "-".
func droidProject(cwd string) string {
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	cwd = strings.Trim(filepath.Clean(cwd), "/")
	return "-" + strings.ReplaceAll(cwd, "/", "-")
}

// newestDroid is the latest session file in dir that's yours (not a
// subagent's).
func newestDroid(dir string) string {
	m, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	type f struct {
		path string
		mod  int64
	}
	var fs []f
	for _, p := range m {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			fs = append(fs, f{p, st.ModTime().UnixNano()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].mod > fs[j].mod })
	for _, f := range fs {
		if !droidSubagent(f.path) {
			return f.path
		}
	}
	return ""
}

// droidSubagent: the session at path is a subagent's (its session_start
// names the session that called it).
func droidSubagent(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	line, _ := bufio.NewReaderSize(file, 64<<10).ReadBytes('\n')
	e, ok := agentview.ParseDroid(line)
	return ok && e.Type == "session_start" && e.CallingSessionID != ""
}

// droidIndex reads a Droid session for the session finder: your messages
// and its replies, not its notices, hooks' records or reminders. A
// subagent's session yields nothing, so it isn't listed.
func droidIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		e, ok := agentview.ParseDroid(sc.Bytes())
		if !ok {
			continue
		}
		if e.Type == "session_start" {
			if e.CallingSessionID != "" {
				return 0, nil
			}
			t.NativeID = e.ID
			if e.Cwd != "" {
				t.Cwd = e.Cwd
			}
			t.Title, t.Named = e.Title, e.ManualTitle
			// Droid's stand-ins until it has titled the session.
			if !e.ManualTitle && (strings.EqualFold(e.Title, "New Session") || strings.EqualFold(e.Title, "Generating title…")) {
				t.Title = ""
			}
			continue
		}
		if !e.Shown() || e.Notice() {
			continue
		}
		if t.Created.IsZero() && !e.Timestamp.IsZero() {
			t.Created = e.Timestamp
		}
		text := e.Text()
		if text == "" {
			continue
		}
		switch e.Message.Role {
		case "user":
			emit(thread.Record{Kind: thread.KindUser, Time: e.Timestamp, Text: text})
		case "assistant":
			emit(thread.Record{Kind: thread.KindAssistant, Time: e.Timestamp, Text: text})
		}
	}
	return 0, sc.Err()
}

// droidSlashCommands lists what can follow "/" in Droid: its built-ins
// (docs.factory.com's CLI reference), and your commands and skills and the
// project's (~/.factory/commands, <project>/.factory/commands: each .md
// file, by name; skills likewise).
func droidSlashCommands(cwd string) []string {
	set := map[string]bool{}
	for _, c := range []string{"account", "archive", "billing", "btw", "bug", "cd", "clear", "commands", "compress", "context", "copy",
		"cost", "create-skill", "cwd", "diagnostics", "droids", "fast", "favorite", "fork", "help", "hooks", "ide", "install-code-review",
		"install-slack-app", "language", "limits", "login", "logout", "mcp", "missions", "model", "new", "plugins", "quit",
		"readiness-fix", "readiness-report", "rename", "review", "rewind-conversation", "sessions", "settings", "setup-incident-response",
		"share", "skills", "stats", "status", "statusline", "terminal-setup", "themes", "tree", "vim"} {
		set[c] = true
	}
	factory := home(".factory")
	if d := envOrEmpty("FACTORY_HOME_OVERRIDE"); d != "" {
		factory = filepath.Join(d, ".factory")
	}
	for _, root := range []string{factory, filepath.Join(cwd, ".factory")} {
		for _, c := range commandsIn(filepath.Join(root, "commands")) {
			// Droid finds commands in subfolders too, by their own name.
			c = c[strings.LastIndex(c, ":")+1:]
			set[strings.ToLower(strings.ReplaceAll(c, " ", "-"))] = true
		}
		for _, s := range skillsIn(filepath.Join(root, "skills")) {
			set[s] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, "/"+c)
	}
	sort.Strings(out)
	return out
}

func init() { Register(Droid) }
