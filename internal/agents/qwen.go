package agents

import (
	"bufio"
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/thread"
)

// Qwen is Alibaba's Qwen Code (github.com/QwenLM/qwen-code, formats as of
// 0.26), a fork of Gemini CLI that has since gone its own way: Claude Code's
// hook events and payloads, in ~/.qwen/settings.json; sessions as
// append-only JSONL chained like Claude Code's, in
// ~/.qwen/projects/<cwd with every non-alphanumeric made "-">/chats/<id>.jsonl.
// QWEN_HOME moves ~/.qwen, QWEN_RUNTIME_DIR just its sessions. Like Gemini
// CLI it starts interactively with -i (a bare prompt runs once and exits)
// and its limits aren't kept on disk.
var Qwen = &Agent{
	ID: "qwen", Order: 70, Name: "Qwen Code", Short: "Qwen", Command: "qwen",
	Install: "brew install qwen-code",
	Hooks:   true,
	// Its own names are Claude Code's, plus a few Hammock has no use for.
	HookEvents: map[string]string{
		"PostToolUseFailure": "PostToolUse",
		"PostToolBatch":      "", "MessageDisplay": "", "UserPromptExpansion": "",
		"SubagentStart": "", "SubagentStop": "", "PreCompact": "", "PostCompact": "",
		"PermissionDenied": "", "SessionDelete": "", "TodoCreated": "",
	},
	Start:       func(prompt string) string { return geminiStart("qwen", "-i", prompt) },
	Resume:      func(id string) string { return "qwen --resume " + Quote(id) },
	ResumeID:    qwenResumeID,
	Schedulable: true,
	Screen:      geminiScreen,

	Roots:   qwenRoots,
	RootEnv: map[string]string{"QWEN_HOME": "projects", "QWEN_RUNTIME_DIR": "projects"},
	Locate: func(id string) (string, bool) {
		if id == "" || strings.ContainsAny(id, `/\*?[`) {
			return "", false
		}
		var found []string
		for _, r := range qwenRoots() {
			m, _ := filepath.Glob(filepath.Join(r, "*", "chats", id+".jsonl"))
			found = append(found, m...)
		}
		return geminiNewestOf(found)
	},
	Newest: func(cwd string) (string, bool) {
		if cwd == "" {
			return "", false
		}
		var found []string
		for _, r := range qwenRoots() {
			m, _ := filepath.Glob(filepath.Join(r, qwenProject(cwd), "chats", "*.jsonl"))
			found = append(found, m...)
		}
		return geminiNewestOf(found)
	},
	Sessions: func(roots []string) []string {
		var out []string
		for _, r := range roots {
			m, _ := filepath.Glob(filepath.Join(r, "*", "chats", "*.jsonl"))
			out = append(out, m...)
		}
		return out
	},
	Appends: true,
	Index:   qwenIndex,
	OwnsFile: func(path string) bool {
		return strings.HasSuffix(path, ".jsonl") && filepath.Base(filepath.Dir(path)) == "chats"
	},

	NewParser: func(cwd string, _ bool) agentview.Parser { return agentview.NewQwen(cwd) },
}

// qwenProject is the folder Qwen Code keeps cwd's sessions in (its
// sanitizeCwd).
func qwenProject(cwd string) string { return qwenNonAlnum.ReplaceAllString(cwd, "-") }

var qwenNonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// qwenResumeID is the session `qwen --resume <id>` resumes, or the one
// --session-id starts.
func qwenResumeID(argv []string) string {
	for _, flag := range [][]string{{"--resume", "-r"}, {"--session-id"}} {
		if id := argAfter(argv, flag...); id != "" && !strings.HasPrefix(id, "-") {
			return id
		}
	}
	return ""
}

// qwenIndex reads Qwen Code session lines for the session finder: what you
// typed, its replies' text and the session's title.
func qwenIndex(r io.Reader, t *thread.Thread, emit func(thread.Record)) (int64, error) {
	var n int64
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return n, nil // a last line still being written is read next time
		}
		n += int64(len(line))
		qwenLine(line, t, emit)
	}
}

func qwenLine(line []byte, t *thread.Thread, emit func(thread.Record)) {
	var l struct {
		SessionID   string    `json:"sessionId"`
		Cwd         string    `json:"cwd"`
		Timestamp   time.Time `json:"timestamp"`
		Type        string    `json:"type"`
		Subtype     string    `json:"subtype"`
		IsSidechain bool      `json:"isSidechain"`
		Message     *struct {
			Parts json.RawMessage `json:"parts"`
		} `json:"message"`
		SystemPayload *struct {
			DisplayText *string `json:"displayText"`
			CustomTitle string  `json:"customTitle"`
			TitleSource string  `json:"titleSource"`
		} `json:"systemPayload"`
	}
	if json.Unmarshal(line, &l) != nil || l.IsSidechain {
		return
	}
	if t.NativeID == "" {
		t.NativeID = l.SessionID
	}
	if t.Cwd == "" {
		t.Cwd = l.Cwd
	}
	if t.Created.IsZero() && !l.Timestamp.IsZero() {
		t.Created = l.Timestamp
	}
	text := ""
	if l.Message != nil {
		text = geminiText(l.Message.Parts)
	}
	switch {
	case l.Type == "system" && l.Subtype == "custom_title" && l.SystemPayload != nil && l.SystemPayload.CustomTitle != "":
		// An automatic title doesn't replace one you gave it.
		if auto := l.SystemPayload.TitleSource == "auto"; !auto || !t.Named {
			t.Title, t.Named = l.SystemPayload.CustomTitle, !auto
		}
	case l.Type == "user" && (l.Subtype == "" || l.Subtype == "mid_turn_user_message"):
		if l.SystemPayload != nil && l.SystemPayload.DisplayText != nil {
			text = *l.SystemPayload.DisplayText
		}
		if text = strings.TrimSpace(text); text != "" {
			emit(thread.Record{Kind: thread.KindUser, Time: l.Timestamp, Text: text})
		}
	case l.Type == "assistant":
		if text = strings.TrimSpace(text); text != "" {
			emit(thread.Record{Kind: thread.KindAssistant, Time: l.Timestamp, Text: text})
		}
	}
}

func init() { Register(Qwen) }

// qwenRoots are the folders Qwen Code keeps its projects' sessions in.
func qwenRoots() []string {
	dirs := []string{home(".qwen", "projects")}
	for _, env := range []string{"QWEN_HOME", "QWEN_RUNTIME_DIR"} {
		if d := envOrEmpty(env); d != "" {
			dirs = append(dirs, filepath.Join(d, "projects"))
		}
	}
	return dirs
}
