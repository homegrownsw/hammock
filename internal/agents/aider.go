package agents

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/homegrownsw/hammock/internal/agentview"
)

// Aider is Paul Gauthier's open source pair programmer
// (github.com/Aider-AI/aider; read from its source at 0.86.3.dev, commit
// 5dc9490, and checked against aider-chat 0.86.2 run in a terminal):
//
//   - no hooks. Its --notifications-command runs a command when a reply is
//     done, but only once per reply, at whichever comes first of its next
//     prompt and its next (Y)es/(N)o question, with nothing saying which;
//     and any hook pauses the screen heuristics for two minutes, so the
//     turn after it would go unseen. Its screen says more (Screen.Idle).
//   - a Python script (`aider`, or `python -m aider`), so its process is
//     Python's, found by the script's name.
//   - no session files under a home folder: every session in a project is
//     appended to <git top>/.aider.chat.history.md (agentview/aider.go), so
//     there are no ids to Locate, no Roots, and no session finder; Newest
//     finds the file for a pane's folder, which the agent view reads.
//   - no first message on its command line (--message runs one and exits),
//     so Hammock types it in once it's at its prompt.
//   - `aider --restore-chat-history` resumes, in the same folder: it loads
//     that whole file back into the conversation.
//   - its costs are on its screen; no subscription limits on disk.
var Aider = &Agent{
	ID: "aider", Order: 150, Name: "Aider", Short: "Aider", Command: "aider",
	// aider.chat/docs/install.html's one-liner: uv's installer, putting
	// aider in an environment of its own (with Python 3.12 if needed).
	Install:    "curl -LsSf https://aider.chat/install.sh | sh",
	TypePrompt: true,
	// Sessions have no ids: the id is ignored, and it resumes the
	// conversation of the folder it's started in.
	Resume:      func(string) string { return "aider --restore-chat-history" },
	Schedulable: true,
	Screen: Screen{
		// At its prompt the last line is "> ", or the chat mode's (and
		// multi-line mode's) name before it: "ask> ", "architect multi> "
		// (io.py get_input), with what you've typed after it and its
		// completions' menu below it. While it works there's no marker
		// (a "Waiting for <model>" spinner until the reply streams in), so
		// anything else is working.
		Idle: regexp.MustCompile(`(?:\A|\n)[a-z-]*(?: multi)?>(?: [^\n]*)?(?:\n {2,}[^\n░█]*)*\z`),
		// Its questions end "(Y)es/(N)o … [Yes]: " (confirm_ask): only
		// while one is the last line with nothing typed after it, so one
		// answered earlier, still on the screen, isn't read as asking.
		NeedsInput: regexp.MustCompile(`\(Y\)es/\(N\)o[^\]\n]*\[[^\]\n]*\]:\z|\[(?:Yes|No)\]:\z`),
	},
	Newest:        aiderNewest,
	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewAider(cwd) },
	SlashCommands: func(string) []string { return aiderCommands },
}

func init() { Register(Aider) }

// aiderNewest is the history file Aider writes for a pane in cwd: at the
// top of its git repository, else in cwd (the newer, when both are
// there). Only a regular file by that name: not a link a repository could
// point anywhere (the agent view shows it, to guests of a shared pane too).
func aiderNewest(cwd string) (string, bool) {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", false
	}
	best, bestT := "", int64(0)
	for _, dir := range []string{agentview.AiderRoot(cwd), filepath.Clean(cwd)} {
		p := filepath.Join(dir, agentview.AiderHistoryFile)
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() && st.ModTime().UnixNano() > bestT {
			best, bestT = p, st.ModTime().UnixNano()
		}
	}
	return best, best != ""
}

// aiderCommands are its commands (aider/commands.py's cmd_ methods, 0.86).
var aiderCommands = []string{
	"/add", "/architect", "/ask", "/chat-mode", "/clear", "/code", "/commit", "/context", "/copy", "/copy-context",
	"/diff", "/drop", "/edit", "/editor", "/editor-model", "/exit", "/git", "/help", "/lint", "/load", "/ls", "/map",
	"/map-refresh", "/model", "/models", "/multiline-mode", "/ok", "/paste", "/quit", "/read-only", "/reasoning-effort",
	"/report", "/reset", "/run", "/save", "/settings", "/test", "/think-tokens", "/tokens", "/undo", "/voice",
	"/weak-model", "/web",
}
