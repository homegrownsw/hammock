package agentview

// Antigravity CLI's transcripts (~/.gemini/antigravity-cli/brain/<id>/
// .system_generated/logs/transcript_full.jsonl, and transcript.jsonl, the
// same with long fields clipped) as the agent view's blocks.
//
// The format is the one the CLI's own bundled docs give its agent (agy
// 1.5.3, read as text from the binary), with what people have found in
// real ones (github.com/ZingerLittleBee/Heeler/issues/644, agy 1.5.0; the
// DEV tutorial "Antigravity CLI Tutorial Series: Part 2"): one line per
// step of the conversation, appended as it goes,
//
//	{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"…","content":"<USER_REQUEST>\n…\n</USER_REQUEST>\n<ADDITIONAL_METADATA>…"}
//	{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","thinking":"…","content":"…","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"ls\""}}],"input_tokens":…}
//	{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","content":"<what the tool returned>"}
//
// A model step's tool calls are answered, in order, by the model-sourced
// steps after it (GENERIC, or a tool's own type such as SEARCH_WEB): there
// are no call ids. A step written while it runs (a command moved to the
// background, "status":"RUNNING") is written again, with the same
// step_index, when it's done. Each tool argument is itself JSON in a
// string. SYSTEM steps are compaction checkpoints (CHECKPOINT), errors
// (ERROR_MESSAGE) and messages hooks and the harness add to the model's
// context (SYSTEM_MESSAGE, EPHEMERAL_MESSAGE), which aren't shown. /rewind
// writes the conversation on from an earlier step: a step_index seen
// before, with another type, drops what was after it.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Antigravity)(nil)

type Antigravity struct {
	Cwd string

	blocks  []*pb.AgentBlock
	byID    map[string]int
	steps   map[int]agStep // step_index -> what it was and the blocks it made
	queue   []string       // tool calls not answered yet, oldest first
	files   map[string]bool
	turn    string
	tokens  int64
	rewound bool
}

type agStep struct {
	typ string
	ids []string
}

func NewAntigravity(cwd string) *Antigravity {
	return &Antigravity{Cwd: cwd, byID: map[string]int{}, steps: map[int]agStep{}, files: map[string]bool{}}
}

func (a *Antigravity) Blocks() []*pb.AgentBlock    { return a.blocks }
func (a *Antigravity) Image(string) (Image, bool)  { return Image{}, false }
func (a *Antigravity) Referenced(path string) bool { return a.files[filepath.Clean(path)] }

// Context: its steps say how many tokens each request took, not which model
// or how big its window is.
func (a *Antigravity) Context() (string, int64, int64) { return "", a.tokens, 0 }

func (a *Antigravity) Rewound() bool {
	r := a.rewound
	a.rewound = false
	return r
}

func (a *Antigravity) Pending() *pb.AgentBlock {
	for i := len(a.blocks) - 1; i >= 0; i-- {
		b := a.blocks[i]
		if b.Kind == pb.AgentBlock_KIND_USER {
			return nil
		}
		if b.Pending {
			return b
		}
	}
	return nil
}

// AntigravityStep is one line of its transcripts.
type AntigravityStep struct {
	StepIndex *int      `json:"step_index"`
	Source    string    `json:"source"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	Content   string    `json:"content"`
	Thinking  string    `json:"thinking"`
	Error     string    `json:"error"`
	ToolCalls []struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"tool_calls"`
	InputTokens int64 `json:"input_tokens"`
	Media       []struct {
		MimeType string `json:"mime_type"`
		URI      string `json:"uri"`
	} `json:"media"`

	// The line agents.Antigravity's Open puts first, for the finder: the
	// conversation and the folder it was in, which the file doesn't say.
	HammockID  string `json:"hammock_id"`
	HammockCwd string `json:"hammock_cwd"`
}

func (a *Antigravity) Feed(line []byte) []*pb.AgentBlock {
	var l AntigravityStep
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	if l.StepIndex == nil {
		if a.Cwd == "" && l.HammockCwd != "" {
			a.Cwd = l.HammockCwd
		}
		return nil
	}
	idx := *l.StepIndex
	var out []*pb.AgentBlock
	if old, ok := a.steps[idx]; ok {
		switch {
		case old.typ != l.Type:
			a.rewind(idx)
		case l.Source == "MODEL" && l.Type != "PLANNER_RESPONSE":
			// A tool's step written again (it ran on in the background).
			for _, id := range old.ids {
				out = append(out, a.update(id, func(b *pb.AgentBlock) { a.result(b, &l) })...)
			}
			return out
		case l.Type != "USER_INPUT" && l.Type != "PLANNER_RESPONSE":
			return nil // said already
		}
	}
	ms := unixMs(l.CreatedAt)
	id := fmt.Sprintf("ag%d", idx)
	switch {
	case l.Type == "USER_INPUT":
		text := AntigravityUserText(l.Content)
		if n := len(l.Media); n > 0 {
			text = strings.TrimSpace(text + "\n\n" + plural(n, "attachment"))
		}
		if text == "" {
			return nil
		}
		out = append(out, a.settle(true)...)
		if old, ok := a.steps[idx]; ok && len(old.ids) > 0 {
			// The same message written again.
			return append(out, a.update(old.ids[0], func(b *pb.AgentBlock) { b.Text = text })...)
		}
		a.turn = id
		a.mentions(text)
		a.steps[idx] = agStep{typ: l.Type, ids: []string{id}}
		out = append(out, a.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, Text: text, TimeUnixMs: ms, TurnId: id})...)
	case l.Type == "PLANNER_RESPONSE":
		if l.InputTokens > 0 {
			a.tokens = l.InputTokens
		}
		if old, ok := a.steps[idx]; ok {
			// Written again: say it as it is now.
			for _, oid := range old.ids {
				if j, ok := a.byID[oid]; ok {
					a.blocks[j] = nil
				}
			}
			a.compact()
		}
		out = append(out, a.settle(false)...)
		var ids []string
		if s := strings.TrimSpace(l.Thinking); s != "" {
			ids = append(ids, id+".t")
			out = append(out, a.add(&pb.AgentBlock{Id: id + ".t", Kind: pb.AgentBlock_KIND_THINKING, Text: s, TimeUnixMs: ms})...)
		}
		if s := strings.TrimSpace(l.Content); s != "" {
			a.mentions(s)
			ids = append(ids, id)
			out = append(out, a.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TEXT, Text: s, TimeUnixMs: ms})...)
		}
		for i, tc := range l.ToolCalls {
			b := a.tool(tc.Name, AntigravityArgs(tc.Args))
			b.Id, b.TimeUnixMs, b.Pending = fmt.Sprintf("%s.c%d", id, i), ms, true
			ids = append(ids, b.Id)
			a.queue = append(a.queue, b.Id)
			out = append(out, a.add(b)...)
		}
		a.steps[idx] = agStep{typ: l.Type, ids: ids}
	case l.Source == "MODEL":
		// A tool's result: the oldest call not answered yet.
		a.steps[idx] = agStep{typ: l.Type}
		if len(a.queue) == 0 {
			return nil
		}
		call := a.queue[0]
		a.queue = a.queue[1:]
		a.steps[idx] = agStep{typ: l.Type, ids: []string{call}}
		out = append(out, a.update(call, func(b *pb.AgentBlock) { a.result(b, &l) })...)
	case l.Type == "ERROR_MESSAGE":
		out = append(out, a.settle(false)...)
		msg := firstLine(cmpOr(l.Error, l.Content))
		a.steps[idx] = agStep{typ: l.Type, ids: []string{id}}
		out = append(out, a.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, Text: strings.TrimSpace("Error: " + msg), TimeUnixMs: ms})...)
	case l.Type == "CHECKPOINT":
		a.steps[idx] = agStep{typ: l.Type, ids: []string{id}}
		out = append(out, a.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, Text: "Compacted the conversation", TimeUnixMs: ms})...)
	default:
		// SYSTEM_MESSAGE, EPHEMERAL_MESSAGE: for the model, not you.
		a.steps[idx] = agStep{typ: l.Type}
	}
	return out
}

// result fills in a tool call's block from the step that answered it.
func (a *Antigravity) result(b *pb.AgentBlock, l *AntigravityStep) {
	out := agToolOutput(l.Content)
	b.Pending = l.Status == "RUNNING"
	b.Error = l.Status == "ERROR"
	if b.Error && l.Error != "" && !strings.Contains(out, l.Error) {
		out = strings.TrimSpace(l.Error + "\n" + out)
	}
	b.Output = clip(strings.TrimRight(stripANSI(out), "\n"), maxOutput)
}

// settle ends calls still waiting: a new message from you (or a reply,
// or an error) means they won't be answered; your message also ends any
// still running in the background, as far as the view goes.
func (a *Antigravity) settle(user bool) []*pb.AgentBlock {
	var out []*pb.AgentBlock
	for _, id := range a.queue {
		out = append(out, a.update(id, func(b *pb.AgentBlock) {
			b.Pending = false
			if b.Output == "" {
				b.Output = "Interrupted"
			}
		})...)
	}
	a.queue = nil
	if user {
		for i, b := range a.blocks {
			if b.Pending {
				c := cloneBlock(b)
				c.Pending = false
				a.blocks[i] = c
				out = append(out, c)
			}
		}
	}
	return out
}

// rewind drops the steps from idx on (/rewind wrote the conversation on
// from there).
func (a *Antigravity) rewind(idx int) {
	var gone []int
	for i, s := range a.steps {
		if i >= idx {
			gone = append(gone, i)
			for _, id := range s.ids {
				if j, ok := a.byID[id]; ok {
					a.blocks[j] = nil
				}
			}
		}
	}
	for _, i := range gone {
		delete(a.steps, i)
	}
	a.queue = nil
	a.compact()
	a.turn = ""
	for i := len(a.blocks) - 1; i >= 0; i-- {
		if a.blocks[i].Kind == pb.AgentBlock_KIND_USER {
			a.turn = a.blocks[i].Id
			break
		}
	}
	a.rewound = true
}

// compact takes out the blocks set to nil and indexes the rest again.
func (a *Antigravity) compact() {
	kept := a.blocks[:0]
	for _, b := range a.blocks {
		if b != nil {
			kept = append(kept, b)
		}
	}
	a.blocks = kept
	a.byID = map[string]int{}
	for i, b := range a.blocks {
		a.byID[b.Id] = i
	}
}

func (a *Antigravity) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = a.turn
	}
	a.byID[b.Id] = len(a.blocks)
	a.blocks = append(a.blocks, b)
	return []*pb.AgentBlock{b}
}

// update changes block id (a copy: one already sent isn't changed).
func (a *Antigravity) update(id string, f func(*pb.AgentBlock)) []*pb.AgentBlock {
	j, ok := a.byID[id]
	if !ok {
		return nil
	}
	b := cloneBlock(a.blocks[j])
	f(b)
	a.blocks[j] = b
	return []*pb.AgentBlock{b}
}

// tool is one of its tool calls as a block. Its tools are named as the
// model calls them (run_command, view_file, write_to_file,
// replace_file_content, multi_replace_file_content, list_dir, grep_search,
// find_by_name, codebase_search, search_web, read_url_content,
// command_status, ask_question, invoke_subagent…), their arguments in
// PascalCase (CommandLine, AbsolutePath, TargetFile, CodeContent…).
func (a *Antigravity) tool(name string, in map[string]any) *pb.AgentBlock {
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	switch strings.ToLower(name) {
	case "run_command":
		cmd := str("CommandLine", "Command", "command")
		a.mentions(cmd)
		b.Input, b.Title = cmd, "Ran `"+firstLine(cmd)+"`"
	case "view_file", "read_file", "view_code_item", "view_file_outline":
		p := a.path(str("AbsolutePath", "Path", "File", "FilePath", "TargetFile"))
		b.Input, b.Title = p, "Read "+p
		if strings.EqualFold(name, "view_file_outline") {
			b.Title = "Read the outline of " + p
		}
	case "write_to_file", "create_file", "write_file":
		p, content := a.path(str("TargetFile", "AbsolutePath", "Path")), str("CodeContent", "Content")
		b.Input, b.Title, b.Diff, b.Added = p, "Wrote "+p, clip(addedDiff(content), maxDiff), int32(lines(content))
	case "replace_file_content", "multi_replace_file_content", "edit_file":
		p := a.path(str("TargetFile", "AbsolutePath", "Path"))
		b.Input, b.Title = p, "Edited "+p
		var diff strings.Builder
		var added, removed int
		chunk := func(m map[string]any) {
			old, _ := agStr(m["TargetContent"]).(string)
			nw, _ := agStr(m["ReplacementContent"]).(string)
			if old == "" && nw == "" {
				return
			}
			diff.WriteString(simpleDiff(old, nw))
			added, removed = added+lines(nw), removed+lines(old)
		}
		chunk(in)
		for _, c := range asAny(agStr(in["ReplacementChunks"])) {
			if m, ok := c.(map[string]any); ok {
				chunk(m)
			}
		}
		if diff.Len() == 0 {
			if code := str("CodeEdit"); code != "" {
				diff.WriteString(addedDiff(code))
				added = lines(code)
			}
		}
		b.Diff, b.Added, b.Removed = clip(diff.String(), maxDiff), int32(added), int32(removed)
	case "list_dir":
		p := a.path(str("DirectoryPath", "Path"))
		b.Input, b.Title = p, "Listed "+cmpOr(p, "the folder")
	case "grep_search":
		q := str("Query", "Pattern")
		b.Input, b.Title = q, "Searched for "+strconv.Quote(q)
		if p := a.path(str("SearchPath", "SearchDirectory")); p != "" {
			b.Title += " in " + p
		}
	case "find_by_name":
		q := str("Pattern", "Query")
		b.Input, b.Title = q, "Found files matching "+q
	case "codebase_search":
		q := str("Query")
		b.Input, b.Title = q, "Searched the code for "+strconv.Quote(q)
	case "search_web":
		q := str("query", "Query")
		b.Input, b.Title = q, "Searched the web for "+strconv.Quote(q)
	case "read_url_content":
		u := str("Url", "URL", "url")
		b.Input, b.Title = u, "Fetched "+u
	case "command_status":
		b.Input, b.Title = str("CommandId"), "Checked on a command"
	case "send_command_input":
		b.Input, b.Title = str("Input"), "Sent input to a command"
	case "ask_question":
		var qs []string
		for _, q := range asAny(agStr(in["questions"])) {
			m, _ := q.(map[string]any)
			if s, _ := m["question"].(string); s != "" {
				qs = append(qs, s)
			}
		}
		b.Input, b.Title = strings.Join(qs, "\n"), "Asked you"
		if len(qs) > 0 {
			b.Title = "Asked you: " + firstLine(qs[0])
		}
	case "invoke_subagent":
		var prompts []string
		for _, s := range asAny(agStr(in["Subagents"])) {
			m, _ := s.(map[string]any)
			if p, _ := m["Prompt"].(string); p != "" {
				prompts = append(prompts, p)
			}
		}
		if p := str("Prompt"); p != "" {
			prompts = append(prompts, p)
		}
		b.Input, b.Title = strings.Join(prompts, "\n\n"), "Started an agent"
		if n := len(prompts); n > 1 {
			b.Title = fmt.Sprintf("Started %d agents", n)
		}
	default:
		b.Title = "Used " + name
		if s := str("toolSummary"); s != "" {
			b.Title = s
		}
		if raw, err := json.Marshal(in); err == nil && len(in) > 0 {
			b.Input = clip(string(raw), maxOutput)
		}
	}
	return b
}

// agStr decodes v if it is JSON in a string (as a nested argument may be).
func agStr(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") {
		var d any
		if json.Unmarshal([]byte(t), &d) == nil {
			return d
		}
	}
	return s
}

// AntigravityArgs is a tool call's arguments, each decoded from the JSON
// its transcript keeps it as ("\"ls\"" is "ls"); one that isn't JSON (as
// its hooks send them) stays as it is.
func AntigravityArgs(raw json.RawMessage) map[string]any {
	var in map[string]json.RawMessage
	_ = json.Unmarshal(raw, &in)
	out := make(map[string]any, len(in))
	for k, v := range in {
		var s string
		if json.Unmarshal(v, &s) == nil {
			var d any
			if json.Unmarshal([]byte(s), &d) == nil {
				out[k] = d
			} else {
				out[k] = s
			}
			continue
		}
		var d any
		_ = json.Unmarshal(v, &d)
		out[k] = d
	}
	return out
}

var (
	agRequest  = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
	agMetadata = regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`)
	agStamps   = regexp.MustCompile(`\A(?:(?:Created|Completed) At: [^\n]*\n)+`)
)

// AntigravityUserText is what you wrote in one of its user steps: the
// <USER_REQUEST> it wraps your message in, without the
// <ADDITIONAL_METADATA> (the time, open files) it adds.
func AntigravityUserText(s string) string {
	if m := agRequest.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(agMetadata.ReplaceAllString(s, ""))
}

// agToolOutput is a tool step's content without the times it starts with.
func agToolOutput(s string) string {
	return agStamps.ReplaceAllString(s, "")
}

func (a *Antigravity) path(p string) string {
	if p == "" {
		return ""
	}
	p = strings.TrimPrefix(p, "file://")
	if !filepath.IsAbs(p) && a.Cwd != "" {
		p = filepath.Join(a.Cwd, p)
	}
	a.files[filepath.Clean(p)] = true
	if a.Cwd != "" {
		if r, err := filepath.Rel(a.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

func (a *Antigravity) mentions(s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		a.files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}

// AntigravitySteps are a transcript's steps as a resume would give them:
// each step_index's latest line, in order, a step_index written again with
// another type (after /rewind) dropping those after it.
func AntigravitySteps(lines [][]byte) []AntigravityStep {
	steps := map[int]AntigravityStep{}
	for _, line := range lines {
		var l AntigravityStep
		if json.Unmarshal(line, &l) != nil || l.StepIndex == nil {
			continue
		}
		idx := *l.StepIndex
		if old, ok := steps[idx]; ok && old.Type != l.Type {
			for i := range steps {
				if i >= idx {
					delete(steps, i)
				}
			}
		}
		steps[idx] = l
	}
	keys := make([]int, 0, len(steps))
	for k := range steps {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]AntigravityStep, len(keys))
	for i, k := range keys {
		out[i] = steps[k]
	}
	return out
}
