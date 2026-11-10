package agentview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Moonshot's Kimi Code CLI (github.com/MoonshotAI/kimi-code, read at 2.5.2:
// packages/agent-core-v2's wire records, docs/wire-manifest.d.ts, protocol
// 1.5) keeps each session in ~/.kimi-code/sessions/<workspace>/<session id>/,
// the main agent's journal in agents/main/wire.jsonl (subagents' beside it,
// agents/agent-0/…). The journal opens with a metadata line, then one
// record per line, {"type": …, …payload, "time": <unix ms>}:
//
//	{"type":"metadata","protocol_version":"1.5",…}
//	{"type":"config.update","modelAlias":…,"environmentDisclosure":{"cwd":…},…}
//	{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":…}],"toolCalls":[],"origin":{"kind":"user"}},…}
//	{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":…},…}
//	{"type":"context.append_loop_event","event":{"type":"content.part","stepUuid":…,"part":{"type":"text"|"think",…}},…}
//	{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":…,"name":"Bash","args":{…}},…}
//	{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":…,"result":{"output":…,"isError":…}},…}
//	{"type":"turn.ended","reason":"completed"|"cancelled"|"failed",…}
//
// Your messages are context.append_message records (with an origin saying
// who wrote them: you, a skill, a background task, Kimi itself); its
// replies are loop events, a part or a tool call at a time (older and
// migrated journals keep whole assistant and tool messages instead). Its
// tools are named as Claude Code's, mostly (path, not file_path), so the
// Kimi parser renames their inputs and lets the Claude parser read them.

// KimiRecord is one line of a Kimi Code wire.jsonl, as far as Hammock reads it.
type KimiRecord struct {
	Type    string       `json:"type"`
	Time    float64      `json:"time"` // unix ms
	Message *KimiMessage `json:"message"`
	Event   *struct {
		Type       string          `json:"type"`
		UUID       string          `json:"uuid"`
		StepUUID   string          `json:"stepUuid"`
		Part       *kimiPart       `json:"part"`
		ToolCallID string          `json:"toolCallId"`
		Name       string          `json:"name"`
		Args       json.RawMessage `json:"args"`
		Result     *struct {
			Output  json.RawMessage `json:"output"`
			IsError bool            `json:"isError"`
		} `json:"result"`
		Usage *kimiUsage `json:"usage"`
	} `json:"event"`
	// config.update's, usage.record's.
	ModelAlias  string `json:"modelAlias"`
	Model       string `json:"model"`
	Cwd         string `json:"cwd"` // protocol 1.1's config.update
	Environment *struct {
		Cwd string `json:"cwd"`
	} `json:"environmentDisclosure"`
	// context.undo's: your last Count messages, and what followed them.
	Count int `json:"count"`
	// context.apply_compaction's: a string, or a message.
	Summary json.RawMessage `json:"summary"`
	// turn.ended's.
	Reason string `json:"reason"`
	Error  *struct {
		Message string `json:"message"`
		Name    string `json:"name"`
	} `json:"error"`
}

// KimiMessage is a context message: an API message with Kimi's own fields.
type KimiMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []kimiToolCall  `json:"toolCalls"`
	ToolCallID string          `json:"toolCallId"`
	IsError    bool            `json:"isError"`
	Origin     *KimiOrigin     `json:"origin"`
}

// KimiOrigin says who wrote a message (nil: you).
type KimiOrigin struct {
	Kind        string `json:"kind"`
	Trigger     string `json:"trigger"`
	Name        string `json:"name"`
	SkillName   string `json:"skillName"`
	SkillArgs   string `json:"skillArgs"`
	CommandName string `json:"commandName"`
	CommandArgs string `json:"commandArgs"`
	Phase       string `json:"phase"`
	IsError     bool   `json:"isError"`
	Status      string `json:"status"`
}

type kimiPart struct {
	Type   string         `json:"type"`
	Text   string         `json:"text"`
	Think  string         `json:"think"`
	Hidden bool           `json:"hidden"`
	Meta   map[string]any `json:"meta"`
}

type kimiToolCall struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Arguments *string `json:"arguments"`
}

type kimiUsage struct {
	InputOther    int64 `json:"inputOther"`
	InputCacheRd  int64 `json:"inputCacheRead"`
	InputCacheCrt int64 `json:"inputCacheCreation"`
}

// kimiWanted are the records the agent view and the finder read; the rest
// (requests, tool lists, bookkeeping, most of the file) are skipped unread.
var kimiWanted = [][]byte{[]byte(`"type":"context.`), []byte(`"type":"turn.ended"`), []byte(`"type":"usage.record"`), []byte(`"type":"config.update"`)}

// ParseKimi reads one line of a Kimi Code wire.jsonl.
func ParseKimi(line []byte) (KimiRecord, bool) {
	var r KimiRecord
	hit := false
	for _, w := range kimiWanted {
		if bytes.Contains(line, w) {
			hit = true
			break
		}
	}
	if !hit || json.Unmarshal(line, &r) != nil {
		return r, false
	}
	return r, true
}

// At is when the record was written.
func (r KimiRecord) At() time.Time {
	if r.Time <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(r.Time))
}

// CwdOf is the folder a config.update says the session works in.
func (r KimiRecord) CwdOf() string {
	if r.Environment != nil && r.Environment.Cwd != "" {
		return r.Environment.Cwd
	}
	return r.Cwd
}

// Typed: the message is one you sent (not Kimi's own reminders, retries or
// a background task's report). A skill or plugin command counts when you
// ran it with a slash.
func (m *KimiMessage) Typed() bool {
	if m == nil || m.Role != "user" {
		return false
	}
	o := m.Origin
	if o == nil {
		return true
	}
	switch o.Kind {
	case "", "user":
		return true
	case "skill_activation", "plugin_command":
		return o.Trigger == "user-slash"
	}
	return false
}

// Text is what you see of the message: a slash command as you typed it,
// else its text parts, without hooks' additions or attached files' tags.
func (m *KimiMessage) Text() string {
	if m == nil {
		return ""
	}
	if o := m.Origin; o != nil {
		switch o.Kind {
		case "skill_activation":
			return strings.TrimSpace("/skill:" + o.SkillName + " " + o.SkillArgs)
		case "plugin_command":
			return strings.TrimSpace("/" + o.CommandName + " " + o.CommandArgs)
		}
	}
	return kimiText(m.Content)
}

var kimiMediaTag = regexp.MustCompile(`^<(image|video|audio|file)\b[^>]*?\bpath="[^"]*"[^>]*>(?:</(image|video|audio|file)>)?$`)

// kimiText joins a message's text parts (content is parts, or a string).
func kimiText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var parts []kimiPart
	_ = json.Unmarshal(raw, &parts)
	var out []string
	for _, p := range parts {
		if p.Type != "text" || p.Meta["source"] != nil {
			continue // a hook's addition
		}
		t := strings.TrimSpace(p.Text)
		if t == "" || kimiMediaTag.MatchString(t) {
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n\n")
}

// KimiReplyText is the text a record adds to Kimi's replies (a text part,
// or a whole assistant message's), for the session finder.
func (r KimiRecord) KimiReplyText() string {
	switch {
	case r.Type == "context.append_loop_event" && r.Event != nil && r.Event.Type == "content.part" && r.Event.Part != nil:
		if r.Event.Part.Type == "text" {
			return strings.TrimSpace(r.Event.Part.Text)
		}
	case r.Type == "context.append_message" && r.Message != nil && r.Message.Role == "assistant":
		if o := r.Message.Origin; o == nil || o.Kind != "hook_result" {
			return kimiText(r.Message.Content)
		}
	}
	return ""
}

// Kimi parses a Kimi Code wire.jsonl for the agent view.
type Kimi struct {
	*Claude
	model  string
	tokens int64
	n      int    // lines read, for blocks with no id of their own
	shell  string // the block of a "!" command waiting for its output
}

var _ Parser = (*Kimi)(nil)

// NewKimi reads a session of Kimi's in cwd; sub is a subagent's journal.
func NewKimi(cwd string, sub bool) *Kimi {
	c := NewClaude(cwd)
	c.Sidechain = sub
	return &Kimi{Claude: c}
}

// Context is the latest reply's model and the context it took (input,
// cached or not). The journal doesn't record the window.
func (k *Kimi) Context() (string, int64, int64) { return k.model, k.tokens, 0 }

// Feed parses one line and returns the blocks it added or changed.
func (k *Kimi) Feed(line []byte) []*pb.AgentBlock {
	k.n++
	r, ok := ParseKimi(line)
	if !ok {
		return nil
	}
	ms := int64(r.Time)
	switch r.Type {
	case "config.update":
		if k.Cwd == "" {
			k.Cwd = r.CwdOf()
		}
		if r.ModelAlias != "" {
			k.model = r.ModelAlias
		}
	case "usage.record":
		if r.Model != "" {
			k.model = r.Model
		}
	case "context.append_message":
		if r.Message != nil {
			return k.message(r.Message, ms)
		}
	case "context.append_loop_event":
		if r.Event != nil {
			return k.loop(r, ms)
		}
	case "context.apply_compaction":
		summary := ""
		if json.Unmarshal(r.Summary, &summary) != nil {
			var m KimiMessage
			if json.Unmarshal(r.Summary, &m) == nil {
				summary = kimiText(m.Content)
			}
		}
		return k.add(&pb.AgentBlock{Id: k.id(""), Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted", Output: summary})
	case "context.clear":
		return k.add(&pb.AgentBlock{Id: k.id(""), Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Context cleared"})
	case "context.undo":
		k.undo(r.Count)
	case "turn.ended":
		out := k.settle()
		switch r.Reason {
		case "cancelled":
			out = append(out, k.add(&pb.AgentBlock{Id: k.id(""), Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Interrupted"})...)
		case "failed":
			text := "The turn failed"
			if r.Error != nil && r.Error.Message != "" {
				text = firstLine(r.Error.Message)
			}
			out = append(out, k.add(&pb.AgentBlock{Id: k.id(""), Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: text, Error: true})...)
		}
		return out
	}
	return nil
}

// settle ends the turn's calls left waiting (it was interrupted, or
// failed, before their results): none is pending any more.
func (k *Kimi) settle() []*pb.AgentBlock {
	var out []*pb.AgentBlock
	for i := len(k.blocks) - 1; i >= 0; i-- {
		b := k.blocks[i]
		if b.Kind == pb.AgentBlock_KIND_USER {
			break
		}
		if b.Pending {
			nb := cloneBlock(b)
			nb.Pending, nb.Error = false, true
			if nb.Output == "" && nb.Kind != pb.AgentBlock_KIND_QUESTION {
				nb.Output = "Interrupted"
			}
			out = append(out, k.add(nb)...)
		}
	}
	return out
}

// id is id, else one made from the line's number.
func (k *Kimi) id(id string) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("kimi%d", k.n)
}

// undo takes your last n messages back, with what followed each (/undo).
func (k *Kimi) undo(n int) {
	for i := len(k.blocks) - 1; i >= 0 && n > 0; i-- {
		if k.blocks[i].Kind == pb.AgentBlock_KIND_USER {
			if n--; n == 0 {
				k.rewind(i)
			}
		}
	}
}

func (k *Kimi) message(m *KimiMessage, ms int64) []*pb.AgentBlock {
	id := k.id(m.ID)
	switch m.Role {
	case "user":
		return k.userMessage(m, id, ms)
	case "assistant":
		if m.Origin != nil && m.Origin.Kind == "hook_result" {
			if t := kimiText(m.Content); t != "" {
				return k.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: t})
			}
			return nil
		}
		var parts []kimiPart
		_ = json.Unmarshal(m.Content, &parts)
		var out []*pb.AgentBlock
		for i, p := range parts {
			out = append(out, k.part(fmt.Sprintf("%s:%d", id, i), p, ms)...)
		}
		for _, tc := range m.ToolCalls {
			args := json.RawMessage("{}")
			if tc.Arguments != nil && json.Valid([]byte(*tc.Arguments)) {
				args = json.RawMessage(*tc.Arguments)
			}
			out = append(out, k.call(tc.ID, tc.ID, tc.Name, args, ms)...)
		}
		return out
	case "tool":
		return k.result(m.ToolCallID, m.Content, m.IsError)
	}
	return nil
}

func (k *Kimi) userMessage(m *KimiMessage, id string, ms int64) []*pb.AgentBlock {
	if m.Typed() {
		t := m.Text()
		if m.Origin != nil && m.Origin.Kind != "user" && m.Origin.Kind != "" {
			// A slash command, shown as typed (userText would read it as
			// one of Claude Code's).
			k.turn = id
			return k.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, Text: t, TurnId: id})
		}
		return k.userText(id, t, nil, ms)
	}
	if m.Origin == nil {
		return nil
	}
	text := kimiText(m.Content)
	switch m.Origin.Kind {
	case "shell_command": // "! command" typed at Kimi's prompt
		if m.Origin.Phase == "input" {
			cmd := html.UnescapeString(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "<bash-input>"), "</bash-input>")))
			k.shell = id
			k.turn = id
			return k.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TOOL, Tool: "Bash", TimeUnixMs: ms, Input: cmd,
				Title: "Ran `" + firstLine(cmd) + "`", Pending: true, TurnId: id})
		}
		if i, ok := k.byID[k.shell]; ok && k.shell != "" {
			blk := cloneBlock(k.blocks[i])
			blk.Pending, blk.Error = false, m.Origin.IsError
			blk.Output = clip(stripANSI(kimiShellOutput(text)), maxOutput)
			k.shell = ""
			return k.add(blk)
		}
	case "task":
		return k.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms,
			Text: "Background task " + cmpOr(strings.ReplaceAll(m.Origin.Status, "_", " "), "finished"), Output: clip(text, maxOutput)})
	case "cron_job":
		return k.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Scheduled prompt", Output: clip(text, maxOutput)})
	}
	return nil // Kimi's reminders, retries, compaction summaries
}

// kimiShellOutput is a "!" command's stdout and stderr (escaped, as
// Claude Code's aren't).
func kimiShellOutput(text string) string {
	var out []string
	stdout, stderr := bashStreams(text)
	for _, s := range []string{stdout, stderr} {
		if s = strings.TrimRight(html.UnescapeString(s), "\n"); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

func (k *Kimi) loop(r KimiRecord, ms int64) []*pb.AgentBlock {
	e := r.Event
	switch e.Type {
	case "content.part":
		if e.Part != nil {
			return k.part(k.id(e.UUID), *e.Part, ms)
		}
	case "tool.call":
		return k.call(k.id(e.UUID), e.ToolCallID, e.Name, e.Args, ms)
	case "tool.result":
		if e.Result != nil {
			return k.result(e.ToolCallID, e.Result.Output, e.Result.IsError)
		}
	case "step.end":
		if u := e.Usage; u != nil {
			if n := u.InputOther + u.InputCacheRd + u.InputCacheCrt; n > 0 {
				k.tokens = n
			}
		}
	}
	return nil
}

// part adds a reply's text or thinking.
func (k *Kimi) part(id string, p kimiPart, ms int64) []*pb.AgentBlock {
	switch p.Type {
	case "text":
		if strings.TrimSpace(p.Text) == "" {
			return nil
		}
		k.mentions(p.Text)
		return k.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: ms, Text: p.Text})
	case "think":
		if p.Hidden || strings.TrimSpace(p.Think) == "" {
			return nil
		}
		return k.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: p.Think})
	}
	return nil
}

// call adds a tool call, read as Claude Code's of the same name.
func (k *Kimi) call(id, callID, name string, args json.RawMessage, ms int64) []*pb.AgentBlock {
	in := map[string]any{}
	_ = json.Unmarshal(args, &in)
	if in == nil {
		in = map[string]any{}
	}
	name = kimiToClaude(name, in)
	input, _ := json.Marshal(in)
	blk := k.toolCall(ccBlock{Type: "tool_use", ID: callID, Name: name, Input: input})
	blk.Id, blk.TimeUnixMs = id, ms
	k.byTool[callID] = id
	return k.add(blk)
}

// kimiToClaude renames a Kimi tool to Claude Code's that does the same,
// moving its input's fields to Claude's names.
func kimiToClaude(name string, in map[string]any) string {
	move := func(from, to string) {
		if v, ok := in[from]; ok {
			if _, taken := in[to]; !taken {
				in[to] = v
			}
			delete(in, from)
		}
	}
	switch name {
	case "Read", "Write", "Edit":
		move("path", "file_path")
	case "FetchURL":
		return "WebFetch"
	case "TodoList":
		// {title, status: pending|in_progress|done}
		for _, t := range asAny(in["todos"]) {
			if m, ok := t.(map[string]any); ok {
				if title, ok := m["title"]; ok {
					m["content"] = title
					delete(m, "title")
				}
				if m["status"] == "done" {
					m["status"] = "completed"
				}
			}
		}
		return "TodoWrite"
	case "AskUserQuestion":
		for _, q := range asAny(in["questions"]) {
			if m, ok := q.(map[string]any); ok {
				if v, ok := m["multi_select"]; ok {
					m["multiSelect"] = v
				}
			}
		}
	}
	return name // Bash, Grep, Glob, WebSearch, Agent, Skill, ExitPlanMode, MCP tools
}

// result fills in a call's output (a string, or parts).
func (k *Kimi) result(callID string, output json.RawMessage, isError bool) []*pb.AgentBlock {
	text := kimiText(output)
	content, _ := json.Marshal(text)
	return k.Claude.result(ccBlock{Type: "tool_result", ToolUseID: callID, Content: content, IsError: isError}, nil)
}

// KimiToolInput is a Kimi tool's input (a hook's tool_input, say) with
// Claude Code's field names, as Hammock reads Claude's.
func KimiToolInput(name string, input json.RawMessage) json.RawMessage {
	in := map[string]any{}
	if json.Unmarshal(input, &in) != nil || in == nil {
		return input
	}
	kimiToClaude(name, in)
	out, err := json.Marshal(in)
	if err != nil {
		return input
	}
	return out
}
