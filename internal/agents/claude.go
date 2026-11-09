package agents

import (
	"bufio"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alexjsp/hammock/internal/agentview"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/thread"
)

// Claude is Anthropic's Claude Code: hooks in ~/.claude/settings.json,
// sessions as JSONL in ~/.claude/projects/<folder>/<id>.jsonl (subagents'
// beside them), usage cached in ~/.claude.json.
var Claude = &Agent{
	ID: "claude", Order: 0, Name: "Claude Code", Short: "Claude", Command: "claude",
	ShellMode: true,
	Install:   "curl -fsSL https://claude.ai/install.sh | bash",
	Hooks:     true,
	Resume:    func(id string) string { return "claude --resume " + Quote(id) },
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
	Usage:         claudeUsage,
	LiveUsage:     claudeLiveUsage,
}

// envOrEmpty is $key when it's an absolute path, else "".
func envOrEmpty(key string) string {
	if v := envOr(key, ""); filepath.IsAbs(v) {
		return v
	}
	return ""
}

func init() { Register(Claude) }
