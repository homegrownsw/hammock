package agents

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/homegrownsw/hammock/internal/agentview"
)

// OpenCode is anomalyco's (formerly sst's) open-source OpenCode: a TUI
// (`opencode`; 2.x talks to a background server it shares between
// terminals), its state from a plugin Hammock writes (cmd/hmk/hooks_opencode.go),
// its sessions in one SQLite database (opencodedb.go). Facts from its
// source, anomalyco/opencode v2.2.5 (packages/cli, core, tui, schema).
var OpenCode = &Agent{
	ID: "opencode", Order: 30, Name: "OpenCode", Short: "OpenCode", Command: "opencode",
	// The npm wrapper (node …/bin/opencode.cjs) runs the native binary,
	// or a cached copy of it named .opencode.
	Procs:     []string{"opencode.cjs", ".opencode"},
	Install:   "curl -fsSL https://opencode.ai/v2/install | bash",
	LiveUsage: openCodeLiveUsage,
	Hooks:     true,
	// The prompt is sent once it's started (`--prompt`); a bare argument
	// would be the folder to start in.
	Start:       openCodeStart("opencode"),
	Resume:      func(id string) string { return "opencode --session " + Quote(id) },
	ResumeID:    openCodeResumeID,
	Schedulable: true,
	Screen:      openCodeScreen,

	// $XDG_DATA_HOME/opencode, with opencode.db (or opencode-<channel>.db
	// for a channel build) in it; OPENCODE_DB names another.
	Roots:    openCodeRoots,
	RootEnv:  map[string]string{"XDG_DATA_HOME": "opencode"},
	Locate:   func(id string) (string, bool) { return openCodeStore.locate(openCodeRoots(), id) },
	Newest:   func(cwd string) (string, bool) { return openCodeStore.newest(openCodeRoots(), cwd) },
	Sessions: openCodeStore.sessions,
	Index:    openCodeIndex,
	OwnsFile: openCodeStore.owns,
	Stat:     openCodeStat,

	NewParser: func(cwd string, _ bool) agentview.Parser { return agentview.NewOpenCode(cwd) },
	Whole:     true,
	Open:      openCodeOpenSession,
	// Its usage limits come only from its providers' servers, with your
	// key: nothing on disk to read.
}

// openCodeStore is OpenCode's databases: opencode.db, a channel build's
// opencode-<channel>.db and OPENCODE_DB's, not the preview opencode-next.db
// 2.x imported its sessions from.
var openCodeStore = openCodeDBStore{dbName: func(name string) bool {
	if db := envOrEmpty("OPENCODE_DB"); filepath.IsAbs(db) && name == filepath.Base(db) {
		return true
	}
	return name == "opencode.db" || strings.HasPrefix(name, "opencode-") && strings.HasSuffix(name, ".db") && name != "opencode-next.db"
}}

// openCodeStart starts command (OpenCode's, Kilo's: they take the same
// flags) with a first message, which it sends once it's ready.
func openCodeStart(command string) func(string) string {
	return func(prompt string) string {
		if p := strings.TrimSpace(prompt); p != "" {
			return command + " --prompt " + Quote(p)
		}
		return command
	}
}

// openCodeResumeID is the session `--session ID` (`-s ID`) continues.
func openCodeResumeID(argv []string) string {
	if id := argAfter(argv, "--session", "-s"); openCodeSessionID.MatchString(id) {
		return id
	}
	return ""
}

// openCodeScreen: its prompt's footer says "esc interrupt" ("esc again to
// interrupt" once pressed) while it works; a permission it asks for says
// "Permission required" over Allow once / Allow always / Reject.
var openCodeScreen = Screen{
	Working:    regexp.MustCompile(`(?i)esc (again to )?interrupt`),
	NeedsInput: regexp.MustCompile(`Permission required|Allow once|Allow always`),
}

func openCodeRoots() []string {
	dirs := []string{filepath.Join(xdg("XDG_DATA_HOME", ".local", "share"), "opencode")}
	if db := envOrEmpty("OPENCODE_DB"); filepath.IsAbs(db) {
		dirs = append(dirs, filepath.Dir(db))
	}
	return dirs
}

func init() { Register(OpenCode) }
