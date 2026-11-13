package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/homegrownsw/hammock/internal/agentview"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/thread"
)

// Claude is Anthropic's Claude Code: hooks in ~/.claude/settings.json,
// sessions as JSONL in ~/.claude/projects/<folder>/<id>.jsonl (subagents'
// beside them), usage cached in ~/.claude.json.
var Claude = &Agent{
	ID: "claude", Order: 0, Name: "Claude Code", Short: "Claude", Command: "claude",
	ShellMode:   true,
	Install:     "curl -fsSL https://claude.ai/install.sh | bash",
	Hooks:       true,
	Resume:      func(id string) string { return "claude --resume " + Quote(id) },
	ResumeFlags: claudeResumeFlags,
	Schedule: func(s *pb.Schedule) string {
		typed := "claude " + Quote(s.Prompt)
		if len(s.AllowedTools) > 0 {
			// After the prompt: the option takes every argument after it.
			typed += " --allowedTools " + Quote(strings.Join(s.AllowedTools, ","))
		}
		return typed
	},
	Schedulable: true,
	Screen:      Screen{Interrupted: regexp.MustCompile(`Interrupted`)},

	Roots: func() []string {
		dirs := []string{home(".claude", "projects")}
		if d := envOrEmpty("CLAUDE_CONFIG_DIR"); d != "" {
			dirs = append(dirs, filepath.Join(d, "projects"))
		}
		return dirs
	},
	RootEnv: map[string]string{"CLAUDE_CONFIG_DIR": "projects"},
	Locate:  thread.Claude{}.Locate,
	Newest:  thread.Claude{}.Newest,
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			m, _ := filepath.Glob(filepath.Join(r, "*", "*.jsonl"))
			out = append(out, m...)
		}
		return out
	},
	Appends: true,
	Index: func(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
		var n int64
		br := bufio.NewReaderSize(r, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if err != nil {
				return n, nil // a last line still being written is read next time
			}
			n += int64(len(line))
			thread.ClaudeLine(line, t, emit)
		}
	},
	OwnsFile: func(path string) bool { return strings.HasSuffix(path, ".jsonl") },

	NewParser: func(cwd string, sub bool) agentview.Parser {
		cc := agentview.NewClaude(cwd)
		cc.Sidechain = sub
		return cc
	},
	Subagents:     true,
	SlashCommands: slashCommands,
	StartCommands: claudeBuiltins,
	SessionURL:    claudeSessionURL,
	Usage:         claudeUsage,
	LiveUsage:     claudeLiveUsage,
}

// bridgeID is a Remote Control session's id, as claude.ai's links have it.
var bridgeID = regexp.MustCompile(`^session_[A-Za-z0-9]+$`)

// claudeSessionURL is session id's Remote Control link, from the file each
// running Claude Code keeps in ~/.claude/sessions (<pid>.json: its session,
// and bridgeSessionId while Remote Control is on). The newest file wins: a
// session resumed elsewhere leaves the old process's behind if it crashed.
func claudeSessionURL(id string) string {
	if id == "" {
		return ""
	}
	dirs := []string{home(".claude", "sessions")}
	if d := envOrEmpty("CLAUDE_CONFIG_DIR"); d != "" {
		dirs = []string{filepath.Join(d, "sessions")}
	}
	var best string
	var bestAt int64
	for _, dir := range dirs {
		m, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		for _, f := range m {
			b, err := os.ReadFile(f)
			if err != nil || len(b) > 64<<10 {
				continue
			}
			var s struct {
				SessionID string `json:"sessionId"`
				Bridge    string `json:"bridgeSessionId"`
				UpdatedAt int64  `json:"updatedAt"`
			}
			if json.Unmarshal(b, &s) != nil || s.SessionID != id || !bridgeID.MatchString(s.Bridge) {
				continue
			}
			if best == "" || s.UpdatedAt > bestAt {
				best, bestAt = "https://claude.ai/code/"+s.Bridge, s.UpdatedAt
			}
		}
	}
	return best
}

// envOrEmpty is $key when it's an absolute path, else "".
func envOrEmpty(key string) string {
	if v := envOr(key, ""); filepath.IsAbs(v) {
		return v
	}
	return ""
}

func init() { Register(Claude) }

// claudeOptions are Claude Code's options that take a value: "1" one,
// "?" an optional one (the next argument, unless it's an option), "*"
// every argument up to the next option (as its command-line parser
// takes them). Any other option takes none.
var claudeOptions = map[string]string{
	"--add-dir": "*", "--agent": "1", "--agents": "1", "--allowedTools": "*", "--allowed-tools": "*",
	"--append-system-prompt": "1", "--append-system-prompt-file": "1", "--autocompact": "1", "--betas": "*",
	"--cloud": "?", "-d": "?", "--debug": "?", "--debug-file": "1", "--disallowedTools": "*",
	"--disallowed-tools": "*", "--effort": "1", "--environment": "1", "--fallback-model": "1", "--file": "*",
	"--from-pr": "?", "--input-format": "1", "--json-schema": "1", "--max-budget-usd": "1", "--mcp-config": "*",
	"--model": "1", "-n": "1", "--name": "1", "--output-format": "1", "--permission-mode": "1",
	"--permission-prompts": "1", "--permission-prompt-tool": "1", "--plugin-dir": "1", "--plugin-url": "1",
	"--prompt-suggestions": "?", "--remote-control": "?", "--remote-control-session-name-prefix": "1",
	"-r": "?", "--resume": "?", "--session-id": "1", "--setting-sources": "1", "--settings": "1",
	"--system-prompt": "1", "--system-prompt-file": "1", "--system-prompt-snapshot": "1", "--teleport": "?",
	"--tools": "*", "-w": "?", "--worktree": "?",
}

// claudeDropped are options not carried into a resumed session: what
// session to start or resume (that's the one resumed now), printing
// instead of talking, its own name (resuming keeps it), and starting
// somewhere else (a worktree, tmux, the cloud, the desktop app).
var claudeDropped = map[string]bool{
	"-p": true, "--print": true, "-c": true, "--continue": true, "-r": true, "--resume": true,
	"--session-id": true, "--fork-session": true, "--from-pr": true, "--cloud": true, "--teleport": true,
	"--desktop": true, "--bg": true, "--background": true, "-w": true, "--worktree": true, "--tmux": true,
	"-n": true, "--name": true, "-h": true, "--help": true, "-v": true, "--version": true,
	"--input-format": true, "--output-format": true, "--json-schema": true, "--replay-user-messages": true,
	"--include-partial-messages": true, "--include-hook-events": true, "--no-session-persistence": true,
}

// claudeResumeFlags keeps the options of Claude Code's arguments args
// (without the program) that a resumed session should start with again:
// not its first message (an argument that isn't an option's), nor
// claudeDropped's. Options are read as its parser reads them
// (claudeOptions); one it doesn't list is taken to take no value.
func claudeResumeFlags(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break // the rest are its first message
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			continue // its first message
		}
		name, _, inline := strings.Cut(a, "=")
		group := []string{a}
		if !inline {
			switch claudeOptions[name] {
			case "1":
				if i+1 < len(args) {
					i++
					group = append(group, args[i])
				}
			case "?":
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					group = append(group, args[i])
				}
			case "*":
				for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					group = append(group, args[i])
				}
			}
		}
		if !claudeDropped[name] {
			out = append(out, group...)
		}
	}
	return out
}
