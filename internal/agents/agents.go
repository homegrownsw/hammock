// Package agents is Hammock's registry of the coding agents it knows (ADR-0015):
// one Agent per harness, each defined in its own file, saying everything the
// rest of Hammock asks about it (how to spot, start, resume and schedule it,
// where its sessions are and how to read them, its usage limits). The rest
// of Hammock asks the registry rather than switching on harness names; a
// harness it doesn't know gets nil, and the generic behaviour.
package agents

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agentview"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/thread"
)

// Agent is one coding agent, as Hammock knows it. Only ID, Name, Short and
// Command are required; a nil func means Hammock can't do that with it.
type Agent struct {
	ID      string // the harness: "claude", in hooks, the proto, config keys and schedules
	Order   int    // where it's offered, lowest first (Claude Code 0, Codex 10)
	Name    string // as its makers name it: "Claude Code"
	Short   string // in menus: "Continue in Claude"
	Command string // the executable on PATH, typed to start it
	// Procs are the process or executable names (lower case) that run it,
	// besides Command's own.
	Procs []string
	// Namesake: the process (its executable's path, and its arguments) is
	// another program by the same name (the goose database migration tool),
	// so not the agent (nil: never).
	Namesake func(path string, args []string) bool
	// Install is a command that installs it, for onboarding.
	Install string
	// Hooks: Hammock can install hooks for it (`hmk hooks install --harness ID`),
	// so its state comes from the agent rather than its screen.
	Hooks bool
	// HookEvents maps its hook events' names to Hammock's (the Claude Code
	// names classify reads: SessionStart, UserPromptSubmit, PreToolUse,
	// PostToolUse, PermissionRequest, Notification, Stop, StopFailure,
	// SessionEnd). Nil: it uses those names. An event it maps to "" is ignored.
	HookEvents map[string]string
	// ParseHook reads one of its hook events (event: the name `hmk hook`
	// was given, if any; payload: the JSON it sent). Nil: ParseHook's
	// generic reading, with HookEvents.
	ParseHook func(event string, payload []byte) Hook

	// Start is the command line that starts it interactively in a shell,
	// with a first message when prompt isn't empty. Nil: Command, then the
	// prompt quoted.
	Start func(prompt string) string
	// TypePrompt: it takes no first message on its command line, so it's
	// started bare (Start("")) and Hammock types the message in once it's at its
	// prompt. Scheduled runs work that way too.
	TypePrompt bool
	// Resume is the command line that resumes session id. Nil: it can't.
	Resume func(id string) string
	// ResumeID is the session a command line that runs it resumes, if any
	// (`codex resume ID`): known before its hooks say.
	ResumeID func(argv []string) string
	// Schedule is a scheduled run's command line, with the agent's own
	// options from s. Nil: Start(s.Prompt). Schedulable must be set too.
	Schedule    func(s *pb.Schedule) string
	Schedulable bool

	// Screen is what its terminal shows when it's working, waiting or
	// interrupted, for when its hooks don't say. Zero: the generic patterns.
	Screen Screen
	// ScreenAsks: its hooks never say it's waiting for permission, so its
	// screen is read for Screen.NeedsInput even while they're flowing.
	ScreenAsks bool

	// Roots are the folders its session files are in (for the agent view,
	// the session finder and checking a path a hook names); RootEnv the
	// environment variables that move them, each to the subfolder of it
	// that's a root ("CLAUDE_CONFIG_DIR": "projects").
	Roots   func() []string
	RootEnv map[string]string
	// Locate finds session id's file; Newest the latest session in a folder.
	Locate func(id string) (string, bool)
	Newest func(cwd string) (string, bool)
	// Sessions lists every session file under roots, for the session finder.
	Sessions func(roots []string) []string
	// Appends: its session files only grow, so the finder reads a changed
	// one on from where it left off.
	Appends bool
	// Index reads a session (from where r is) for the session finder,
	// filling in t and calling emit for each record; it returns the bytes
	// it read whole lines of (Appends agents' next starting point).
	Index func(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error)
	// Names are session names it keeps apart from the sessions (Codex's
	// session_index.jsonl), by session id.
	Names func(roots []string) map[string]string
	// OwnsFile: path (under one of its roots) is one of its sessions'
	// files, not something else kept there.
	OwnsFile func(path string) bool

	// NewParser reads a session file into the agent view; sub is a
	// subagent's own transcript. Nil: no agent view.
	NewParser func(cwd string, sub bool) agentview.Parser
	// Whole: its session files are rewritten whole (one JSON document, not
	// lines appended), so a changed one is read again from the start.
	Whole bool
	// Open opens a session file as the lines its parser and Index read.
	// Nil: the file itself (decompressed if it's .zst).
	Open func(path string) (io.ReadCloser, error)
	// Stat is os.Stat for its session paths when they aren't files of
	// their own (OpenCode's sessions, all in one database: a size and a
	// time that change when the session does). Nil: os.Stat.
	Stat func(path string) (os.FileInfo, error)
	// Subagents: its sessions keep subagents' transcripts beside them,
	// as Claude Code's do.
	Subagents bool
	// ShellMode: a message starting with "!" runs as a shell command in it,
	// as at Claude Code's prompt: the agent view's composer says so.
	ShellMode bool
	// SlashCommands lists what can follow "/" in it, for the agent view's
	// composer.
	SlashCommands func(cwd string) []string
	// SessionURL is the web page session id can be opened at (Claude Code's
	// Remote Control link), "" when it has none now. Nil: never.
	SessionURL func(id string) string

	// Usage returns a reader of its subscription limits from what it keeps
	// on disk; each reader remembers what it read last. Nil: none.
	Usage func() func() *pb.AgentUsage
	// LiveUsage asks the agent's own service for its limits, with the
	// sign-in its CLI keeps here (live.go), only while usage-live is on.
	// wait is how long a throttled answer asked to wait. Nil: it has none
	// Hammock can ask.
	LiveUsage func(ctx context.Context) (u *pb.AgentUsage, wait time.Duration, err error)
}

// Screen holds the patterns the heuristics read an agent's screen with.
type Screen struct {
	Working     *regexp.Regexp // it's working (its "esc to interrupt")
	NeedsInput  *regexp.Regexp // it's asking (a permission prompt)
	Interrupted *regexp.Regexp // Esc stopped it: no hook says so
	// Idle: it's at its prompt, waiting for your next message. For an agent
	// that shows nothing while it works (Aider streams its reply with no
	// spinner or "esc to interrupt"): set, anything else on its screen is
	// working, and Working isn't read.
	Idle *regexp.Regexp
}

var (
	genericNeedsInput = regexp.MustCompile(`(?i)do you want to (proceed|make this edit|create|run)|would you like to (run|make|apply)|allow (this|command)|❯ 1\. yes|press enter to (approve|confirm)|\(y/n\)`)
	genericWorking    = regexp.MustCompile(`(?i)esc to interrupt|esc to cancel|ctrl\+c to interrupt`)
)

// WorkingRE and NeedsInputRE are a's patterns, or the generic ones.
func (a *Agent) WorkingRE() *regexp.Regexp {
	if a != nil && a.Screen.Working != nil {
		return a.Screen.Working
	}
	return genericWorking
}

func (a *Agent) NeedsInputRE() *regexp.Regexp {
	if a != nil && a.Screen.NeedsInput != nil {
		return a.Screen.NeedsInput
	}
	return genericNeedsInput
}

// StartCommand is the command line typed to start a, with a first message.
func (a *Agent) StartCommand(prompt string) string {
	if a.TypePrompt {
		prompt = "" // typed in once it's up (the daemon's typeFirstPrompt)
	}
	prompt = CleanPrompt(prompt)
	if a.Start != nil {
		return a.Start(prompt)
	}
	cmd := a.Command
	if p := strings.TrimSpace(prompt); p != "" {
		cmd += " " + Quote(p)
	}
	return cmd
}

// ScheduleCommand is the command line a scheduled run types.
func (a *Agent) ScheduleCommand(s *pb.Schedule) string {
	if a.Schedule != nil {
		c := proto.CloneOf(s)
		c.Prompt = CleanPrompt(c.Prompt)
		return a.Schedule(c)
	}
	return a.StartCommand(s.Prompt)
}

// Codec is a's session codec for switching to or from it, if it has one.
func (a *Agent) Codec() thread.Codec {
	c, _ := thread.CodecFor(a.ID)
	return c
}

// CanSwitch: a conversation can be continued in a (and from it).
func (a *Agent) CanSwitch() bool { return a.Codec() != nil && a.Resume != nil }

// Kind describes a for clients (StateUpdate.agent_kinds).
func (a *Agent) Kind(installed bool) *pb.AgentKind {
	return &pb.AgentKind{
		Id: a.ID, Name: a.Name, ShortName: a.Short, Installed: installed,
		CanResume: a.Resume != nil, AgentView: a.NewParser != nil, CanSwitch: a.CanSwitch(),
		Schedulable: a.Schedulable, Usage: a.Usage != nil || a.LiveUsage != nil, LiveUsage: a.LiveUsage != nil, Hooks: a.Hooks, Sessions: a.Sessions != nil,
		ShellMode: a.ShellMode,
		Install:   a.Install,
	}
}

// All is every agent Hammock knows, in the order they're offered. Each
// agent's file adds it with Register.
var All []*Agent

// Register adds a to All, in its Order. For package init functions.
func Register(a *Agent) {
	i := len(All)
	for i > 0 && All[i-1].Order > a.Order {
		i--
	}
	All = append(All[:i], append([]*Agent{a}, All[i:]...)...)
}

// GetWhole is a.Whole, false for nil.
func (a *Agent) GetWhole() bool { return a != nil && a.Whole }

// OpenSession opens one of a's session files as lines.
func (a *Agent) OpenSession(path string) (io.ReadCloser, error) {
	if a != nil && a.Open != nil {
		return a.Open(path)
	}
	return thread.OpenTranscript(path)
}

// StatSession stats one of a's session paths.
func (a *Agent) StatSession(path string) (os.FileInfo, error) {
	if a != nil && a.Stat != nil {
		return a.Stat(path)
	}
	return os.Stat(path)
}

// Get is the agent for harness id, or nil.
func Get(id string) *Agent {
	for _, a := range All {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// ByProcess is the agent a process or executable named name runs, or nil.
func ByProcess(name string) *Agent {
	name = strings.ToLower(name)
	for _, a := range All {
		if a.Command == name {
			return a
		}
		for _, p := range a.Procs {
			if p == name {
				return a
			}
		}
	}
	return nil
}

// IsName: s is one of the agents' names or commands (a tab titled "claude").
func IsName(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, a := range All {
		if s == a.Command || s == strings.ToLower(a.Name) || s == strings.ToLower(a.Short) {
			return true
		}
	}
	return false
}

// Quote quotes s as one word for a POSIX shell, and for fish: every byte
// is inside single quotes but a quote or a backslash, each escaped outside
// them (fish reads \' and \\ inside single quotes; a POSIX shell nothing).
// Newlines and other controls stay literal inside the quotes. A NUL, which
// no argument can hold, is dropped.
func Quote(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.NewReplacer("'", `'\''`, `\`, `'\\'`).Replace(s)
	return "'" + s + "'"
}

// CleanPrompt is a message for an agent made safe to type or pass on: tabs
// and newlines stay (a carriage return becomes a newline), and every other
// control character goes: C0 (ESC among them, which could end a bracketed
// paste early or start a terminal sequence), DEL, C1, and bytes that
// aren't UTF-8 (a lone 0x9b is CSI to some terminals).
func CleanPrompt(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch {
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r == '\r':
			b.WriteByte('\n')
		case r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f:
		case r == utf8.RuneError && !strings.HasPrefix(s[i:], "\uFFFD"):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// home is the home folder joined with parts.
func home(parts ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, parts...)...)
}

// envOr is $key, else def.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// xdg is an XDG base folder ($XDG_DATA_HOME, say), else its default under home.
func xdg(key string, def ...string) string {
	if v := os.Getenv(key); v != "" && filepath.IsAbs(v) {
		return v
	}
	return home(def...)
}

// argAfter is the argument after flag in argv (any of names), or "".
func argAfter(argv []string, names ...string) string {
	for i, a := range argv {
		for _, n := range names {
			if a == n && i+1 < len(argv) {
				return argv[i+1]
			}
			if v, ok := strings.CutPrefix(a, n+"="); ok {
				return v
			}
		}
	}
	return ""
}

// lookPath is exec.LookPath, a seam for tests.
var lookPath = exec.LookPath

// Other is the agent a session of id's is continued in by default (the
// session finder's ⌥↩): Claude Code's in Codex, any other in Claude Code.
// "" when id's conversations can't be converted.
func Other(id string) string {
	a := Get(id)
	switch {
	case a == nil || !a.CanSwitch():
		return ""
	case id == Claude.ID:
		return Codex.ID
	}
	return Claude.ID
}

// Parse is the agent a person typed ("claude", "Claude Code", "codex"), or nil.
func Parse(s string) *Agent {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, a := range All {
		if s == a.ID || s == a.Command || s == strings.ToLower(a.Name) || s == strings.ToLower(a.Short) {
			return a
		}
	}
	return nil
}

// IDs are the agents' ids, in order.
func IDs() []string {
	out := make([]string, len(All))
	for i, a := range All {
		out[i] = a.ID
	}
	return out
}
