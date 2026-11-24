package agents

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/agentview"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/thread"
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
	StartCommands: codexBuiltins,
	Usage:         codexUsage,
	TurnFailed:    codexTurnTail,
	UsageLimit:    codexLimit,
}

// TurnTail follows an agent's session file as it grows, for how its
// turns end (Agent.TurnFailed).
type TurnTail interface {
	// Next reads what's been added to the file since it last did: a
	// StopFailure when that leaves its last turn ended on an error, at
	// since or later.
	Next(since time.Time) (Hook, bool)
}

// codexTail follows a Codex session file: Codex runs no Stop hook when an
// error ends a turn (its usage limit, a dropped connection), but its
// task_complete keeps the error, and the token_count before it the limits
// it hit (each window's used_percent and resets_at). Each Next reads only
// what was appended; a new file starts its last 512 KB in.
type codexTail struct {
	path    string
	off     int64
	partial []byte // a line not yet ended
	skip    bool   // the first line read starts mid-line
	ended   *codexLine
	limits  json.RawMessage // the last rate_limits in the turn
}

type codexLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type       string          `json:"type"`
		LastAgent  string          `json:"last_agent_message"`
		Error      *codexError     `json:"error"`
		RateLimits json.RawMessage `json:"rate_limits"`
	} `json:"payload"`
}

// codexTailStart is how far from a session file's end a tail starts.
const codexTailStart = 512 << 10

func codexTurnTail(path string) TurnTail {
	t := &codexTail{path: path}
	if st, err := os.Stat(path); err == nil && st.Size() > codexTailStart {
		t.off, t.skip = st.Size()-codexTailStart, true
	}
	return t
}

func (t *codexTail) Next(since time.Time) (Hook, bool) {
	if strings.HasSuffix(t.path, ".zst") {
		return Hook{}, false // an old one, compressed: nothing more comes
	}
	f, err := os.Open(t.path)
	if err != nil {
		return Hook{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Hook{}, false
	}
	if st.Size() < t.off { // rewritten: from the start
		*t = codexTail{path: t.path}
	}
	if st.Size() == t.off {
		return Hook{}, false
	}
	if _, err := f.Seek(t.off, io.SeekStart); err != nil {
		return Hook{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, st.Size()-t.off))
	if err != nil {
		return Hook{}, false
	}
	t.off += int64(len(data))
	data = append(t.partial, data...)
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		t.partial = data
		return Hook{}, false
	}
	t.partial = slices.Clone(data[end+1:])
	for _, b := range bytes.Split(data[:end], []byte{'\n'}) {
		if t.skip {
			t.skip = false
			continue
		}
		if !bytes.Contains(b, []byte(`"event_msg"`)) {
			continue
		}
		var l codexLine
		if json.Unmarshal(b, &l) != nil || l.Type != "event_msg" {
			continue
		}
		switch l.Payload.Type {
		case "task_started":
			t.ended, t.limits = nil, nil
		case "task_complete":
			t.ended = &l
		case "token_count":
			if len(rawOr(l.Payload.RateLimits)) > 0 && t.ended == nil {
				t.limits = l.Payload.RateLimits
			}
		}
	}
	e := t.ended
	if e == nil || e.Payload.Error == nil {
		return Hook{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil || at.Before(since) {
		return Hook{}, false
	}
	h := Hook{Event: "StopFailure", Error: e.Payload.Error.kind(), ErrorDetails: e.Payload.Error.Message, LastMessage: e.Payload.LastAgent}
	if t.limits != nil {
		if u := parseCodexUsage([]byte(`{"payload":{"rate_limits":` + string(t.limits) + `}}`)); u != nil {
			// Resets after the turn ended: one before is a window that
			// had already reset.
			if r := ExhaustedReset(u.Windows, at); !r.IsZero() {
				h.ResetsUnix = r.Unix()
			}
		}
	}
	return h, true
}

// codexError is a turn's error in Codex's session file: its message, and
// its kind (codex_error_info: "usage_limit_exceeded", or an object named
// for its kind, {"http_connection_failed": {…}}).
type codexError struct {
	Message string          `json:"message"`
	Info    json.RawMessage `json:"codex_error_info"`
}

func (e *codexError) kind() string {
	var s string
	if json.Unmarshal(e.Info, &s) == nil {
		return s
	}
	var o map[string]json.RawMessage
	if json.Unmarshal(e.Info, &o) == nil {
		for k := range o {
			return k
		}
	}
	return ""
}

func codexRollout(name string) bool {
	return strings.HasPrefix(name, "rollout-") && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst"))
}

var uuidLike = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func init() { Register(Codex) }
