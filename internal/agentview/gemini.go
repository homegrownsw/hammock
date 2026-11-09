package agentview

// Gemini CLI's sessions (~/.gemini/tmp/<project>/chats/session-*.jsonl) as
// the agent view's blocks.
//
// The format, from gemini-cli's packages/core/src/services/
// chatRecordingService.ts and chatRecordingTypes.ts (0.69, November 2026):
// a JSONL file, appended to. Its first line is the session's metadata
// ({sessionId, projectHash, startTime, …}); then message records
// ({id, timestamp, type: user|gemini|info|error|warning, content,
// displayContent, toolCalls, thoughts, tokens, model}), a message written
// again (same id) whenever it changes, as when its tool calls finish;
// {"$set": {…}} metadata updates; {"$rewindTo": id}, dropping that message
// and everything after it; and {"$patch": {…}}, keeping the file in step
// with the model's history (compression, masking), which the view doesn't
// follow: it shows what happened.
//
// Qwen Code (qwen.go) began as a fork of Gemini CLI and still speaks its
// API's shapes (parts, functionCall, functionResponse) and much of its tool
// set, so both read those with the helpers here.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Gemini)(nil)

// Gemini parses a Gemini CLI session file.
type Gemini struct {
	Cwd string

	blocks []*pb.AgentBlock
	byID   map[string]int
	files  map[string]bool
	turn   string
	// msgAt is where each message's blocks began (for $rewindTo), set even
	// for messages that showed nothing.
	msgAt   map[string]int
	rewound bool
	// compacted is when a compression's summary was recorded: Gemini then
	// records the turns it kept again, as new messages, all at once; the
	// view has shown them already.
	compacted time.Time

	model          string
	tokens, window int64
}

func NewGemini(cwd string) *Gemini {
	return &Gemini{Cwd: cwd, byID: map[string]int{}, files: map[string]bool{}, msgAt: map[string]int{}}
}

func (g *Gemini) Blocks() []*pb.AgentBlock        { return g.blocks }
func (g *Gemini) Image(string) (Image, bool)      { return Image{}, false }
func (g *Gemini) Referenced(path string) bool     { return g.files[filepath.Clean(path)] }
func (g *Gemini) Context() (string, int64, int64) { return g.model, g.tokens, g.window }

func (g *Gemini) Rewound() bool {
	r := g.rewound
	g.rewound = false
	return r
}

func (g *Gemini) Pending() *pb.AgentBlock { return pendingAfterUser(g.blocks) }

// pendingAfterUser is the newest pending block since your last message.
func pendingAfterUser(blocks []*pb.AgentBlock) *pb.AgentBlock {
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		if b.Kind == pb.AgentBlock_KIND_USER {
			return nil
		}
		if b.Pending {
			return b
		}
	}
	return nil
}

type gmLine struct {
	ID             string          `json:"id"`
	Timestamp      time.Time       `json:"timestamp"`
	Type           string          `json:"type"`
	Content        json.RawMessage `json:"content"`
	DisplayContent json.RawMessage `json:"displayContent"`
	ToolCalls      []gmToolCall    `json:"toolCalls"`
	Thoughts       []struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
	} `json:"thoughts"`
	Tokens *struct {
		Input int64 `json:"input"`
	} `json:"tokens"`
	Model    string          `json:"model"`
	RewindTo string          `json:"$rewindTo"`
	Patch    json.RawMessage `json:"$patch"`
}

type gmToolCall struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Args          json.RawMessage `json:"args"`
	Result        json.RawMessage `json:"result"`
	Status        string          `json:"status"`
	ResultDisplay json.RawMessage `json:"resultDisplay"`
}

// compactionReplay bounds how long after a compression's summary the turns
// it kept are recorded again (synchronously, so in practice milliseconds).
const compactionReplay = 5 * time.Second

func (g *Gemini) Feed(line []byte) []*pb.AgentBlock {
	var l gmLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	switch {
	case l.RewindTo != "":
		if i, ok := g.msgAt[l.RewindTo]; ok {
			g.rewind(i)
		}
		return nil
	case len(l.Patch) > 0:
		// The patch that follows a compression's re-recorded turns ends them.
		g.compacted = time.Time{}
		return nil
	case l.ID == "" || l.Type == "":
		return nil // metadata
	}
	if !g.compacted.IsZero() {
		if l.Timestamp.Sub(g.compacted) <= compactionReplay {
			return nil
		}
		g.compacted = time.Time{}
	}
	if _, ok := g.msgAt[l.ID]; !ok {
		g.msgAt[l.ID] = len(g.blocks)
	}
	ms := l.Timestamp.UnixMilli()
	switch l.Type {
	case "user":
		return g.user(l, ms)
	case "gemini":
		return g.reply(l, ms)
	case "info":
		if strings.TrimSpace(gemText(l.Content)) == "Request cancelled." {
			return g.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Interrupted"})
		}
	case "error":
		if t := strings.TrimSpace(gemText(l.Content)); t != "" {
			return g.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: t})
		}
	}
	return nil
}

func (g *Gemini) user(l gmLine, ms int64) []*pb.AgentBlock {
	raw := l.Content
	if len(l.DisplayContent) > 0 && string(l.DisplayContent) != "null" {
		raw = l.DisplayContent // what you typed, before @file expansion
	}
	t := strings.TrimSpace(gemText(raw))
	switch {
	case t == "":
		return nil // tool responses, which the calls show
	case strings.HasPrefix(t, "<state_snapshot>"):
		g.compacted = time.UnixMilli(ms)
		return g.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted", Output: t})
	case strings.HasPrefix(t, "<session_context>"), strings.HasPrefix(t, "<hook_context>"):
		return nil
	}
	g.turn = l.ID
	mentionPaths(g.files, t)
	return g.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, Text: t, TurnId: l.ID})
}

// reply is one of Gemini's messages: its thoughts, its text and the tools
// it called, each a block that's replaced when the message is written again.
func (g *Gemini) reply(l gmLine, ms int64) []*pb.AgentBlock {
	if l.Model != "" {
		g.model = l.Model
		g.window = geminiWindow(l.Model)
	}
	if l.Tokens != nil && l.Tokens.Input > 0 {
		g.tokens = l.Tokens.Input
	}
	var out []*pb.AgentBlock
	for i, th := range l.Thoughts {
		text := strings.TrimSpace(th.Description)
		if th.Subject != "" {
			text = strings.TrimSpace("**" + th.Subject + "**\n" + text)
		}
		if text != "" {
			out = append(out, g.add(&pb.AgentBlock{Id: fmt.Sprintf("%s/t%d", l.ID, i), Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: text})...)
		}
	}
	if t := strings.TrimSpace(gemText(l.Content)); t != "" {
		mentionPaths(g.files, t)
		out = append(out, g.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: ms, Text: t})...)
	}
	for _, tc := range l.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal(tc.Args, &args)
		b := gemToolBlock(tc.Name, args, g.Cwd, g.files)
		b.Id, b.TimeUnixMs = "call:"+tc.ID, ms
		switch tc.Status {
		case "success", "error", "cancelled":
			gemResult(b, tc.Status != "success", tc.ResultDisplay, tc.Result)
		default:
			b.Pending = true
		}
		out = append(out, g.add(b)...)
	}
	return out
}

func (g *Gemini) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = g.turn
	}
	if i, ok := g.byID[b.Id]; ok {
		g.blocks[i] = b
	} else {
		g.byID[b.Id] = len(g.blocks)
		g.blocks = append(g.blocks, b)
	}
	return []*pb.AgentBlock{b}
}

// rewind drops the blocks from index i on.
func (g *Gemini) rewind(i int) {
	if i > len(g.blocks) {
		return
	}
	for _, b := range g.blocks[i:] {
		delete(g.byID, b.Id)
	}
	g.blocks = g.blocks[:i]
	for id, j := range g.msgAt {
		if j >= i {
			delete(g.msgAt, id)
		}
	}
	g.turn = ""
	for j := len(g.blocks) - 1; j >= 0; j-- {
		if g.blocks[j].Kind == pb.AgentBlock_KIND_USER {
			g.turn = g.blocks[j].Id
			break
		}
	}
	g.rewound = true
}

// geminiWindow is the context window Gemini CLI gives model (its
// tokenLimits.ts): a million tokens, but 256k for Gemma 4.
func geminiWindow(model string) int64 {
	if strings.HasPrefix(model, "gemma-4") {
		return 256_000
	}
	return 1_048_576
}

// gemPart is one part of a Gemini API message (Gemini CLI's and Qwen
// Code's content).
type gemPart struct {
	Text         string `json:"text"`
	Thought      bool   `json:"thought"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
}

// gemParts reads a PartListUnion: a string, a part, or a list of either.
func gemParts(raw json.RawMessage) []gemPart {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []gemPart{{Text: s}}
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	var out []gemPart
	for _, item := range list {
		var p gemPart
		if json.Unmarshal(item, &s) == nil {
			p.Text = s
		} else if json.Unmarshal(item, &p) != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// gemText is a message's text, without its thoughts.
func gemText(raw json.RawMessage) string { return partsText(gemParts(raw)) }

func partsText(parts []gemPart) string {
	var b strings.Builder
	for _, p := range parts {
		if !p.Thought {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// gemToolBlock is a call to one of the Gemini family's tools (Gemini CLI's
// tool-names.ts and base-declarations.ts, Qwen Code's tool-names.ts).
func gemToolBlock(name string, args map[string]any, cwd string, files map[string]bool) *pb.AgentBlock {
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, _ := args[k].(string); s != "" {
				return s
			}
		}
		return ""
	}
	path := func(keys ...string) string {
		p := str(keys...)
		if p == "" {
			return ""
		}
		if !filepath.IsAbs(p) && cwd != "" {
			p = filepath.Join(cwd, p)
		}
		files[filepath.Clean(p)] = true
		if cwd != "" {
			if r, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(r, "..") {
				return r
			}
		}
		return p
	}
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	switch name {
	case "run_shell_command", "exec":
		b.Input = str("command")
		mentionPaths(files, b.Input)
		b.Title = "Ran `" + firstLine(b.Input) + "`"
		if d := str("description"); d != "" {
			b.Title = d
		}
	case "read_file":
		b.Input = path("file_path", "absolute_path", "path")
		b.Title = "Read " + b.Input
	case "read_many_files":
		var ps []string
		for _, p := range append(asStrings(args["paths"]), asStrings(args["include"])...) {
			ps = append(ps, p)
		}
		b.Input = strings.Join(ps, "\n")
		b.Title = "Read " + plural(len(ps), "file")
		if len(ps) == 1 {
			b.Title = "Read " + ps[0]
		}
	case "write_file":
		b.Input = path("file_path", "absolute_path")
		content := str("content")
		b.Title = "Wrote " + b.Input
		b.Diff, b.Added = clip(addedDiff(content), maxDiff), int32(lines(content))
	case "replace", "edit":
		b.Input = path("file_path", "absolute_path")
		b.Title = "Edited " + b.Input
		if old, nw := str("old_string"), str("new_string"); old != "" || nw != "" {
			b.Diff = clip(simpleDiff(old, nw), maxDiff)
			b.Added, b.Removed = int32(lines(nw)), int32(lines(old))
		}
	case "glob":
		b.Input = str("pattern")
		b.Title = "Found files matching `" + b.Input + "`"
	case "grep_search", "grep", "search_file_content":
		b.Input = str("pattern")
		b.Title = "Searched for `" + b.Input + "`"
	case "list_directory", "ls":
		b.Input = path("dir_path", "path")
		if b.Input == "" {
			b.Input = "."
		}
		b.Title = "Listed " + b.Input
	case "google_web_search", "web_search":
		b.Input = str("query")
		b.Title = "Searched the web for “" + b.Input + "”"
	case "web_fetch":
		b.Input = str("url", "prompt")
		b.Title = "Fetched " + firstLine(b.Input)
	case "write_todos", "todo_write":
		b.Title = "Updated the to-do list"
		b.Input = gemTodos(args["todos"])
	case "save_memory":
		b.Title = "Saved a memory"
		b.Input = str("fact", "content")
	case "activate_skill", "skill":
		b.Title = "Used the " + str("name", "skill") + " skill"
	case "exit_plan_mode":
		b.Kind = pb.AgentBlock_KIND_PLAN
		b.Title = "Plan"
		b.Text = str("plan")
	case "invoke_agent", "agent", "task":
		b.Kind = pb.AgentBlock_KIND_SUBAGENT
		b.Title = str("description", "agent_name", "subagent_type")
		b.Input = str("prompt", "query", "task")
		b.Text = str("subagent_type", "agent_name")
	default:
		shown := name
		if strings.HasPrefix(name, "mcp__") {
			if parts := strings.SplitN(name, "__", 3); len(parts) == 3 {
				shown = parts[2] + " (" + parts[1] + ")"
			}
		}
		b.Title = "Used " + shown
		raw, _ := json.Marshal(args)
		if len(args) > 0 {
			b.Input = clip(string(raw), 2000)
		}
	}
	return b
}

// gemTodos is a to-do list as a checklist: Gemini's [{description,
// status}], Qwen's [{content, status}].
func gemTodos(v any) string {
	var b strings.Builder
	for _, it := range asAny(v) {
		m, _ := it.(map[string]any)
		text, _ := m["description"].(string)
		if text == "" {
			text, _ = m["content"].(string)
		}
		status, _ := m["status"].(string)
		box := "- [ ] "
		switch status {
		case "completed":
			box = "- [x] "
		case "in_progress":
			box = "- [~] "
		case "cancelled":
			box = "- [-] "
		}
		b.WriteString(box + text + "\n")
	}
	return b.String()
}

// gemResult fills in a finished call: its output (what the CLI displayed,
// else what the tool told the model), or for an edit the diff it made.
func gemResult(b *pb.AgentBlock, failed bool, display, response json.RawMessage) {
	b.Pending, b.Error = false, failed
	var fd struct {
		FileDiff string `json:"fileDiff"`
	}
	if json.Unmarshal(display, &fd) == nil && fd.FileDiff != "" {
		if d, added, removed := unifiedDiff(fd.FileDiff); d != "" {
			b.Diff, b.Added, b.Removed = clip(d, maxDiff), added, removed
		}
		if !failed {
			return
		}
	}
	out := displayText(display)
	if out == "" {
		out = responseText(response)
	}
	b.Output = clip(strings.TrimRight(stripANSI(out), "\n"), maxOutput)
}

// displayText is a tool's display, when it's text (a string, or terminal
// lines of styled runs).
func displayText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if sr, ok := shellResult(raw); ok {
		return sr.Output
	}
	var ansiLines [][]struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &ansiLines) == nil && len(ansiLines) > 0 {
		var b strings.Builder
		for _, l := range ansiLines {
			for _, run := range l {
				b.WriteString(run.Text)
			}
			b.WriteByte('\n')
		}
		return strings.TrimRight(b.String(), "\n ")
	}
	return ""
}

// shellDisplay is Qwen Code's display of a command's result (its
// utils/shell-result.ts).
type shellDisplay struct {
	Type     string `json:"type"`
	Output   string `json:"output"`
	ExitCode *int   `json:"exitCode"`
	Outcome  string `json:"outcome"`
}

func shellResult(raw json.RawMessage) (shellDisplay, bool) {
	var sr shellDisplay
	if json.Unmarshal(raw, &sr) != nil || sr.Type != "shell_result" {
		return sr, false
	}
	return sr, true
}

// responseText is what a call's functionResponse parts told the model:
// their output or error.
func responseText(raw json.RawMessage) string {
	var parts []string
	for _, p := range gemParts(raw) {
		if p.FunctionResponse == nil {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
			continue
		}
		var r map[string]any
		if json.Unmarshal(p.FunctionResponse.Response, &r) != nil {
			continue
		}
		for _, k := range []string{"output", "error", "llmContent", "content"} {
			if s, ok := r[k].(string); ok && s != "" {
				parts = append(parts, s)
				break
			}
		}
	}
	return strings.Join(parts, "\n")
}

// unifiedDiff turns a unified diff (the fileDiff both CLIs display) into
// the view's +/- lines, under an "@@ file" header when it names one.
func unifiedDiff(d string) (string, int32, int32) {
	var b strings.Builder
	var added, removed int32
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "Index: "),
			strings.HasPrefix(l, "==="), strings.HasPrefix(l, "@@"), strings.HasPrefix(l, `\`):
		case strings.HasPrefix(l, "+"):
			b.WriteString(l + "\n")
			added++
		case strings.HasPrefix(l, "-"):
			b.WriteString(l + "\n")
			removed++
		case strings.HasPrefix(l, " "):
			b.WriteString(l + "\n")
		}
	}
	return b.String(), added, removed
}

// mentionPaths records absolute paths in s as referenced.
func mentionPaths(files map[string]bool, s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}
