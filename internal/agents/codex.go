package agents

import (
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alexjsp/hammock/internal/agentview"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/thread"
)

// Codex is OpenAI's Codex CLI: hooks in ~/.codex/hooks.json, sessions as
// rollouts in ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl (older ones
// compressed), with its usage limits in them.
var Codex = &Agent{
	ID: "codex", Order: 10, Name: "Codex", Short: "Codex", Command: "codex",
	ShellMode: true,
	Install:   "brew install codex",
	LiveUsage: codexLiveUsage,
	Hooks:     true,
	Resume:    func(id string) string { return "codex resume " + Quote(id) },
	ResumeID: func(argv []string) string {
		for i, a := range argv {
			if a == "resume" && i+1 < len(argv) && uuidLike.MatchString(argv[i+1]) {
				return argv[i+1]
			}
		}
		return ""
	},
	Schedule: func(s *pb.Schedule) string {
		typed := "codex"
		if s.CodexSandbox != "" {
			typed += " --sandbox " + Quote(s.CodexSandbox)
		}
		if s.CodexApproval != "" {
			typed += " --ask-for-approval " + Quote(s.CodexApproval)
		}
		return typed + " " + Quote(s.Prompt)
	},
	Schedulable: true,

	Roots: func() []string {
		dirs := []string{home(".codex", "sessions")}
		if d := envOrEmpty("CODEX_HOME"); d != "" {
			dirs = append(dirs, filepath.Join(d, "sessions"))
		}
		return dirs
	},
	RootEnv: map[string]string{"CODEX_HOME": "sessions"},
	Locate:  thread.Codex{}.Locate,
	Newest:  thread.Codex{}.Newest,
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			_ = filepath.WalkDir(r, func(p string, e fs.DirEntry, err error) error {
				if err == nil && !e.IsDir() && codexRollout(e.Name()) {
					out = append(out, p)
				}
				return nil
			})
		}
		return out
	},
	Index: func(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
		th, recs, err := thread.Codex{}.Decode(r)
		if err != nil {
			return 0, err
		}
		*t = th
		for _, rec := range recs {
			emit(rec)
		}
		return 0, nil
	},
	Names: func(roots []string) map[string]string {
		names := map[string]string{}
		for _, r := range roots {
			for id, n := range thread.CodexNames(filepath.Dir(r)) {
				names[id] = n
			}
		}
		return names
	},
	OwnsFile: func(path string) bool { return codexRollout(filepath.Base(path)) },

	NewParser:     func(cwd string, _ bool) agentview.Parser { return agentview.NewCodex(cwd) },
	SlashCommands: func(string) []string { return codexSlashCommands() },
	Usage:         codexUsage,
}

func codexRollout(name string) bool {
	return strings.HasPrefix(name, "rollout-") && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst"))
}

var uuidLike = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func init() { Register(Codex) }
