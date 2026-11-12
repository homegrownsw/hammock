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
