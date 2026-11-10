package agentview

// xAI's Grok CLI's sessions (~/.grok/sessions/<cwd>/<id>/updates.jsonl) as
// the agent view's blocks. Each line is one ACP (Agent Client Protocol)
// session update, as Grok sent it to its own TUI:
//
//	{"timestamp": 1786372566, "method": "session/update",
//	 "params": {"sessionId": …, "update": {"sessionUpdate": "agent_message_chunk", …},
//	            "_meta": {"agentTimestampMs": …, "totalTokens": …}}}
//
// Grok's own updates ("_x.ai/session/update": turn_completed,
// model_changed…) are lines of the same shape. Your messages, its replies
// and its thinking are streamed as chunks, a few words each, joined here
// into one block; a tool call is a tool_call and the tool_call_updates
// that follow it (its title, its output as it runs, a diff for an edit).
// Shapes from Grok 1.0.41's ACP stream (t3code's recordings), ACP's schema
// and the documentation built into Grok 1.0.77.

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Grok)(nil)

// Grok parses a Grok CLI updates.jsonl, keeping its blocks in a Codex
// parser's state (they share how paths and blocks are kept).
type Grok struct {
	cx *Codex

	// The block a stream of chunks is going into, and its kind.
	stream     string
	streamKind string
	prompt     int // the promptIndex of your message being streamed

	calls map[string]*grokCall
	plans map[string]string // turn -> its plan's block

	model          string
	tokens, window int64
}

// grokCall is what a tool call's block is made from, as its updates come.
type grokCall struct {
	name  string
	input map[string]any
}

func NewGrok(cwd string) *Grok {
	return &Grok{cx: NewCodex(cwd), prompt: -1, calls: map[string]*grokCall{}, plans: map[string]string{}}
}

func (g *Grok) Blocks() []*pb.AgentBlock        { return g.cx.blocks }
func (g *Grok) Pending() *pb.AgentBlock         { return g.cx.Pending() }
func (g *Grok) Image(string) (Image, bool)      { return Image{}, false }
func (g *Grok) Referenced(path string) bool     { return g.cx.Referenced(path) }
func (g *Grok) Rewound() bool                   { return false }
func (g *Grok) Context() (string, int64, int64) { return g.model, g.tokens, g.window }

type grokLine struct {
	Timestamp float64    `json:"timestamp"`
	Params    grokParams `json:"params"`
	// A bare update, should a line be one.
	Update json.RawMessage `json:"update"`
}

type grokParams struct {
	Update json.RawMessage `json:"update"`
	Meta   struct {
		AgentTimestampMs int64 `json:"agentTimestampMs"`
		TotalTokens      int64 `json:"totalTokens"`
	} `json:"_meta"`
}

type grokUpdate struct {
	Kind    string          `json:"sessionUpdate"`
	Content json.RawMessage `json:"content"` // a block on chunks; a list on tool calls
	Meta    struct {
		PromptIndex *int                          `json:"promptIndex"`
		Tool        *struct{ Name, Label string } `json:"x.ai/tool"`
	} `json:"_meta"`
	// tool_call, tool_call_update
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title"`
	Status     string          `json:"status"`
	RawInput   json.RawMessage `json:"rawInput"`
	RawOutput  json.RawMessage `json:"rawOutput"`
	Locations  []struct {
		Path string `json:"path"`
	} `json:"locations"`
	// plan
	Entries []any `json:"entries"`
	// usage_update
	Used int64 `json:"used"`
	Size int64 `json:"size"`
	// config_option_update
	ConfigOptions []struct {
		ID           string `json:"id"`
		CurrentValue any    `json:"currentValue"`
	} `json:"configOptions"`
	// Grok's own: model_changed, turn_completed
	ModelID    string `json:"model_id"`
	StopReason string `json:"stop_reason"`
}

// grokContent is an ACP content block (text, image, resource…) or tool
// call content ({"type": "content", "content": block}, {"type": "diff",
// "path", "oldText", "newText"}, {"type": "terminal"}).
type grokContent struct {
	Type     string       `json:"type"`
	Text     string       `json:"text"`
	URI      string       `json:"uri"`
	Name     string       `json:"name"`
	Content  *grokContent `json:"content"`
	Path     string       `json:"path"`
	OldText  *string      `json:"oldText"`
	NewText  string       `json:"newText"`
	Resource *struct {
		URI string `json:"uri"`
	} `json:"resource"`
}

func (g *Grok) Feed(line []byte) []*pb.AgentBlock {
	g.cx.n++
	var l grokLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	raw := l.Params.Update
	if len(raw) == 0 {
		raw = l.Update
	}
	var u grokUpdate
	if json.Unmarshal(raw, &u) != nil || u.Kind == "" {
		return nil
	}
	ms := l.Params.Meta.AgentTimestampMs
	switch {
	case ms > 0:
	case l.Timestamp > 1e12:
		ms = int64(l.Timestamp)
	default:
		ms = int64(l.Timestamp * 1000)
	}
	if t := l.Params.Meta.TotalTokens; t > 0 {
		g.tokens = t
	}
	switch u.Kind {
	case "user_message_chunk":
		text := grokChunkText(u.Content)
		if u.Meta.PromptIndex != nil && *u.Meta.PromptIndex != g.prompt {
			g.stream = "" // the next message, though no reply came between
			g.prompt = *u.Meta.PromptIndex
		}
		return g.chunk(pb.AgentBlock_KIND_USER, "user", text, ms)
	case "agent_message_chunk":
		return g.chunk(pb.AgentBlock_KIND_TEXT, "text", grokChunkText(u.Content), ms)
	case "agent_thought_chunk":
		return g.chunk(pb.AgentBlock_KIND_THINKING, "thinking", grokChunkText(u.Content), ms)
	case "tool_call":
		g.stream = ""
		return g.toolCall(u, ms)
	case "tool_call_update":
		g.stream = ""
		return g.toolUpdate(u)
	case "plan":
		g.stream = ""
		return g.plan(u.Entries, ms)
	case "usage_update":
		if u.Used > 0 {
			g.tokens = u.Used
		}
		if u.Size > 0 {
			g.window = u.Size
		}
	case "config_option_update":
		for _, o := range u.ConfigOptions {
			if s, ok := o.CurrentValue.(string); ok && o.ID == "model" && s != "" {
				g.model = s
			}
		}
	case "model_changed":
		if u.ModelID != "" {
			g.model = u.ModelID
		}
	case "turn_completed":
		g.stream = ""
		return g.turnEnded(u.StopReason, ms)
	}
	return nil
}

// grokChunkText is a streamed chunk's text (an image or a file attached
// to your message as a mention of it).
func grokChunkText(raw json.RawMessage) string {
	var c grokContent
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	switch c.Type {
	case "text":
		return c.Text
	case "resource_link":
		return "@" + cmpOr(c.Name, c.URI)
	case "resource":
		if c.Resource != nil {
			return "@" + c.Resource.URI
		}
	case "image":
		return "[image]"
	}
	return ""
}

// chunk adds text to the block being streamed, or begins one.
func (g *Grok) chunk(kind pb.AgentBlock_Kind, name, text string, ms int64) []*pb.AgentBlock {
	if text == "" {
		return nil
	}
	if g.stream != "" && g.streamKind == name {
		i := g.cx.byID[g.stream]
		b := cloneBlock(g.cx.blocks[i])
		b.Text += text
		if kind == pb.AgentBlock_KIND_USER {
			g.cx.mentions(b.Text)
		}
		return g.cx.add(b)
	}
	id := fmt.Sprintf("g%d", g.cx.n)
	g.stream, g.streamKind = id, name
	b := &pb.AgentBlock{Id: id, Kind: kind, TimeUnixMs: ms, Text: text}
	if kind == pb.AgentBlock_KIND_USER {
		g.cx.turn = id
		b.TurnId = id
		g.cx.mentions(text)
	}
	return g.cx.add(b)
}

// toolCall begins a tool call's block, pending until it's done.
func (g *Grok) toolCall(u grokUpdate, ms int64) []*pb.AgentBlock {
	if u.ToolCallID == "" {
		return nil
	}
	if i, seen := g.cx.byID[u.ToolCallID]; seen && !g.cx.blocks[i].Pending {
		return nil // already done (a restored session replays)
	}
	name := u.Title
	if u.Meta.Tool != nil && u.Meta.Tool.Name != "" {
		name = u.Meta.Tool.Name
	}
	var in map[string]any
	_ = json.Unmarshal(u.RawInput, &in)
	c := &grokCall{name: name, input: in}
	g.calls[u.ToolCallID] = c
	b := g.tool(c)
	if b == nil {
		return nil
	}
	if b.Tool == "todo_write" {
		g.plans[g.cx.turn] = u.ToolCallID
	}
	b.Id, b.TimeUnixMs, b.Pending = u.ToolCallID, ms, true
	if u.Meta.Tool != nil && strings.HasPrefix(b.Title, "Used ") && u.Meta.Tool.Label != "" && !strings.Contains(name, "__") {
		b.Title = u.Meta.Tool.Label
	}
	if done := grokFinished(u.Status); done {
		b.Pending = false
	}
	out := g.cx.add(b)
	if len(u.Content) > 0 || len(u.RawOutput) > 0 || u.Status != "" {
		out = append(out, g.toolUpdate(u)...)
	}
	return out
}

func grokFinished(status string) bool { return status == "completed" || status == "failed" }

// tool is a block for one of Grok's tools, from its name and the model's
// arguments, without its id or time (nil: not worth showing).
func (g *Grok) tool(c *grokCall) *pb.AgentBlock {
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := c.input[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	path := func() string {
		return g.cx.path(str("file_path", "path", "target_file", "target_notebook", "filePath"))
	}
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: c.name}
	switch c.name {
	case "run_terminal_command", "Bash", "Shell":
		s := g.cx.shell(c.name, str("command"))
		if bg, _ := c.input["background"].(bool); bg {
			s.Title += " in the background"
		}
		return s
	case "read_file", "Read":
		p := path()
		b.Title, b.Input = "Read "+p, p
	case "search_replace", "StrReplace", "Edit", "hashline_edit", "EditNotebook":
		p := path()
		b.Title, b.Input = "Edited "+p, p
		if old, nw := str("old_string", "old_str"), str("new_string", "new_str"); old != "" || nw != "" {
			b.Diff, b.Added, b.Removed = clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		}
	case "write", "Write":
		p := path()
		content := str("content", "contents")
		b.Title, b.Input = "Wrote "+p, p
		if content != "" {
			b.Diff, b.Added = clip(addedDiff(content), maxDiff), int32(lines(content))
		}
	case "apply_patch":
		p := g.cx.patch(str("patch", "input"))
		p.Tool = c.name
		return p
	case "delete_file", "Delete":
		p := path()
		b.Title, b.Input = "Deleted "+p, p
	case "grep", "Grep":
		b.Input = str("pattern", "query")
		b.Title = "Searched for “" + firstLine(b.Input) + "”"
		if p := str("path"); p != "" {
			b.Title += " in " + g.cx.path(p)
		}
	case "list_dir", "List", "Glob", "glob":
		if pat := str("pattern", "glob_pattern"); pat != "" {
			b.Title, b.Input = "Found files matching “"+firstLine(pat)+"”", pat
		} else {
			p := path()
			b.Title, b.Input = "Listed "+cmpOr(p, "the folder"), p
		}
	case "web_search", "WebSearch":
		b.Input = str("query", "search_term")
		b.Title = "Searched the web for “" + firstLine(b.Input) + "”"
	case "web_fetch", "Fetch", "WebFetch":
		b.Input = str("url")
		b.Title = "Fetched " + b.Input
	case "spawn_subagent", "Task", "Agent":
		b.Kind = pb.AgentBlock_KIND_SUBAGENT
		b.Input = str("prompt")
		b.Title = cmpOr(str("description"), "Started an agent")
	case "todo_write", "TodoWrite":
		b.Tool, b.Title, b.Input = "todo_write", "Updated the plan", strings.TrimRight(todos(c.input["todos"]), "\n")
	case "get_command_or_subagent_output", "AwaitShell":
		b.Title = "Checked on background work"
	case "monitor":
		b.Input = str("command")
		b.Title = "Watched `" + firstLine(b.Input) + "`"
	case "ask_user_question", "AskQuestion", "AskUserQuestion":
		b.Title = "Asked you"
		if raw, err := json.Marshal(c.input); err == nil {
			b.Questions = Questions(raw)
		}
		if len(b.Questions) > 0 {
			b.Kind = pb.AgentBlock_KIND_QUESTION
		}
		b.Input = str("question")
	default:
		if server, tool, ok := strings.Cut(c.name, "__"); ok && server != "" && tool != "" {
			b.Title = "Used " + tool + " (" + server + ")"
		} else {
			b.Title = "Used " + c.name
		}
		if len(c.input) > 0 {
			if raw, err := json.Marshal(c.input); err == nil {
				b.Input = clip(string(raw), maxOutput)
			}
		}
	}
	return b
}

// toolUpdate brings a call's block up to date: its title, an edit's diff,
// what it printed so far, and whether it's done.
func (g *Grok) toolUpdate(u grokUpdate) []*pb.AgentBlock {
	i, ok := g.cx.byID[u.ToolCallID]
	if !ok {
		return nil
	}
	blk := cloneBlock(g.cx.blocks[i])
	c := g.calls[u.ToolCallID]
	if c != nil && len(c.input) == 0 && len(u.RawInput) > 0 {
		// The first update can carry the arguments the call didn't.
		_ = json.Unmarshal(u.RawInput, &c.input)
		if b := g.tool(c); b != nil {
			blk.Title, blk.Input, blk.Diff, blk.Added, blk.Removed = b.Title, b.Input, b.Diff, b.Added, b.Removed
		}
	}
	if u.Title != "" && strings.HasPrefix(blk.Title, "Used ") && (c == nil || !strings.Contains(c.name, "__")) {
		blk.Title = u.Title // Grok's own words for a tool Hammock doesn't know
	}
	if blk.Input == "" && len(u.Locations) > 0 && u.Locations[0].Path != "" {
		blk.Input = g.cx.path(u.Locations[0].Path)
	}

	var items []grokContent
	_ = json.Unmarshal(u.Content, &items)
	var text []string
	var diffs []grokContent
	for _, it := range items {
		switch it.Type {
		case "diff":
			diffs = append(diffs, it)
		case "content":
			if it.Content != nil && it.Content.Type == "text" {
				text = append(text, it.Content.Text)
			}
		}
	}
	if len(diffs) > 0 {
		g.diffs(blk, diffs)
	}

	var ro struct {
		Type      string `json:"type"`
		ExitCode  *int   `json:"exit_code"`
		OutputFor string `json:"output_for_prompt"`
		Text      string `json:"text"`
		TimedOut  bool   `json:"timed_out"`
		Summary   string `json:"summary"`
		Result    *struct {
			Output   string `json:"output"`
			ExitCode *int   `json:"exit_code"`
		} `json:"Result"`
	}
	_ = json.Unmarshal(u.RawOutput, &ro)
	out := strings.Join(text, "\n")
	switch {
	case ro.Result != nil:
		out = cmpOr(ro.Result.Output, out)
	case out == "" && ro.Type == "Text":
		out = ro.Text
	case out == "" && ro.OutputFor != "":
		out = strings.TrimPrefix(ro.OutputFor, grokExitLine(ro.OutputFor))
	case out == "" && ro.Summary != "":
		out = ro.Summary
	}
	if u.Status == "failed" || grokFinished(u.Status) && (ro.ExitCode != nil && *ro.ExitCode != 0 || ro.TimedOut) {
		blk.Error = true
	}
	if grokFinished(u.Status) {
		blk.Pending = false
	}
	// An edit shows as its diff; its output only when it failed. A question
	// shows its answer.
	if out != "" && (blk.Diff == "" || blk.Error) {
		blk.Output = clip(strings.TrimRight(stripANSI(out), "\n"), maxOutput)
	}
	return g.cx.add(blk)
}

// grokExitLine is the "exit: N" line Grok puts above a command's output for
// the model, if it's there.
func grokExitLine(s string) string {
	if strings.HasPrefix(s, "exit: ") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			return s[:i+1]
		}
	}
	return ""
}

// diffs puts an edit's diffs on its block: one file's as it is, several
// under an "@@ path" header each.
func (g *Grok) diffs(blk *pb.AgentBlock, diffs []grokContent) {
	var b strings.Builder
	var added, removed int
	var paths []string
	for _, d := range diffs {
		p := g.cx.path(d.Path)
		paths = append(paths, p)
		old := ""
		if d.OldText != nil {
			old = *d.OldText
		}
		if len(diffs) > 1 {
			b.WriteString("@@ " + p + "\n")
		}
		if d.OldText == nil || old == "" {
			b.WriteString(addedDiff(d.NewText))
		} else {
			b.WriteString(simpleDiff(old, d.NewText))
			removed += lines(old)
		}
		added += lines(d.NewText)
	}
	blk.Diff, blk.Added, blk.Removed = clip(b.String(), maxDiff), int32(added), int32(removed)
	if blk.Input == "" && len(paths) == 1 {
		blk.Input = paths[0]
	}
	if strings.HasPrefix(blk.Title, "Used ") || blk.Title == "" {
		if len(paths) == 1 {
			blk.Title = "Edited " + paths[0]
		} else {
			blk.Title = fmt.Sprintf("Edited %s and %s", paths[0], plural(len(paths)-1, "other file"))
		}
	}
}

// plan shows its task list: one block a turn, updated as it goes (the one
// its todo_write call made, when it did).
func (g *Grok) plan(entries []any, ms int64) []*pb.AgentBlock {
	text := strings.TrimRight(todos(entries), "\n")
	if text == "" {
		return nil
	}
	if id, ok := g.plans[g.cx.turn]; ok {
		if i, ok := g.cx.byID[id]; ok {
			b := cloneBlock(g.cx.blocks[i])
			b.Input = text
			return g.cx.add(b)
		}
	}
	id := "plan:" + g.cx.turn
	g.plans[g.cx.turn] = id
	return g.cx.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TOOL, Tool: "plan", TimeUnixMs: ms, Title: "Updated the plan", Input: text})
}

// turnEnded notes a turn that didn't finish: its calls still running never
// will.
func (g *Grok) turnEnded(reason string, ms int64) []*pb.AgentBlock {
	note := ""
	switch reason {
	case "cancelled":
		note = "Interrupted"
	case "max_tokens":
		note = "Ran out of output tokens"
	case "max_turn_requests":
		note = "Reached the turn limit"
	case "refusal":
		note = "Refused"
	default:
		return nil
	}
	var out []*pb.AgentBlock
	for _, b := range g.cx.blocks {
		if b.Pending && b.TurnId == g.cx.turn {
			blk := cloneBlock(b)
			blk.Pending, blk.Error, blk.Output = false, true, cmpOr(blk.Output, "Cancelled")
			out = append(out, g.cx.add(blk)...)
		}
	}
	id := fmt.Sprintf("g%d", g.cx.n)
	return append(out, g.cx.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: note})...)
}
