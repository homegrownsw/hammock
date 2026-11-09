package agentview

// GitHub Copilot CLI's sessions (~/.copilot/session-state/<id>/events.jsonl)
// as the agent view's blocks. Each line is one session event:
//
//	{"type": "user.message", "data": {…}, "id": …, "timestamp": …, "parentId": …, "agentId"?: …}
//
// The event types and their data are Copilot's own schema, shipped in its
// package as schemas/session-events.schema.json (and the SDK's
// generated/session-events.d.ts); this reads the ones that are written to
// disk (not "ephemeral") as of @github/copilot 1.0.107: user.message,
// assistant.message (its text, reasoning and tool requests),
// tool.execution_start/complete, abort, session.start/resume/model_change,
// session.compaction_complete, session.error and session.shutdown. Events a
// subagent sends (with an agentId) are its own; the main view shows the
// task's call and result.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Copilot)(nil)

// Copilot parses a Copilot CLI events.jsonl, keeping its blocks in a Codex
// parser's state (they share how paths, patches and blocks are kept).
type Copilot struct {
	cx *Codex

	model          string
	tokens, window int64
}

func NewCopilot(cwd string) *Copilot { return &Copilot{cx: NewCodex(cwd)} }

func (c *Copilot) Blocks() []*pb.AgentBlock              { return c.cx.blocks }
func (c *Copilot) Pending() *pb.AgentBlock               { return c.cx.Pending() }
func (c *Copilot) Image(string) (Image, bool)            { return Image{}, false }
func (c *Copilot) Referenced(path string) bool           { return c.cx.Referenced(path) }
func (c *Copilot) Rewound() bool                         { return false }
func (c *Copilot) Context() (string, int64, int64)       { return c.model, c.tokens, c.window }
func (c *Copilot) add(b *pb.AgentBlock) []*pb.AgentBlock { return c.cx.add(b) }

type cpEvent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp time.Time       `json:"timestamp"`
	AgentID   string          `json:"agentId"`
	Data      json.RawMessage `json:"data"`
}

type cpData struct {
	Content       string          `json:"content"`
	Source        string          `json:"source"`
	MessageID     string          `json:"messageId"`
	ReasoningText string          `json:"reasoningText"`
	Model         string          `json:"model"`
	ToolRequests  []cpToolRequest `json:"toolRequests"`
	// tool.execution_start
	ToolCallID    string          `json:"toolCallId"`
	ToolName      string          `json:"toolName"`
	Arguments     json.RawMessage `json:"arguments"`
	MCPServerName string          `json:"mcpServerName"`
	MCPToolName   string          `json:"mcpToolName"`
	// tool.execution_complete
	Success bool `json:"success"`
	Result  *struct {
		Content         string `json:"content"`
		DetailedContent string `json:"detailedContent"`
	} `json:"result"`
	Error          json.RawMessage `json:"error"` // {message} on a tool, a string elsewhere
	ShellExecution *struct {
		ExitCode int `json:"exitCode"`
	} `json:"shellExecution"`
	// session.*
	SelectedModel string `json:"selectedModel"`
	NewModel      string `json:"newModel"`
	CurrentModel  string `json:"currentModel"`
	CurrentTokens int64  `json:"currentTokens"`
	Context       *struct {
		Cwd string `json:"cwd"`
	} `json:"context"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	// session.compaction_complete (with Success)
	PostCompactionToks int64 `json:"postCompactionTokens"`
	TokenLimit         int64 `json:"tokenLimit"`
}

type cpToolRequest struct {
	ToolCallID    string          `json:"toolCallId"`
	Name          string          `json:"name"`
	Arguments     json.RawMessage `json:"arguments"`
	MCPServerName string          `json:"mcpServerName"`
	MCPToolName   string          `json:"mcpToolName"`
}

func (c *Copilot) Feed(line []byte) []*pb.AgentBlock {
	c.cx.n++
	var e cpEvent
	if json.Unmarshal(line, &e) != nil || e.Type == "" {
		return nil
	}
	if e.AgentID != "" {
		return nil // a subagent's own work
	}
	var d cpData
	_ = json.Unmarshal(e.Data, &d)
	ms := e.Timestamp.UnixMilli()
	id := e.ID
	if id == "" {
		id = fmt.Sprintf("cp%d", c.cx.n)
	}
	switch e.Type {
	case "session.start", "session.resume":
		if d.Context != nil && c.cx.Cwd == "" {
			c.cx.Cwd = d.Context.Cwd
		}
		if d.SelectedModel != "" {
			c.model = d.SelectedModel
		}
	case "session.model_change":
		if d.NewModel != "" {
			c.model = d.NewModel
		}
	case "session.shutdown":
		if d.CurrentModel != "" {
			c.model = d.CurrentModel
		}
		if d.CurrentTokens > 0 {
			c.tokens = d.CurrentTokens
		}
	case "user.message":
		text := strings.TrimSpace(d.Content)
		switch {
		case text == "":
		case strings.HasPrefix(d.Source, "skill-"):
			// A skill's instructions, injected out of sight.
		case strings.HasPrefix(d.Source, "agent-"):
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "From an agent: " + firstLine(text)})
		default:
			c.cx.turn = id
			c.cx.mentions(text)
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, Text: d.Content, TurnId: id})
		}
	case "assistant.message":
		if d.Model != "" {
			c.model = d.Model
		}
		var out []*pb.AgentBlock
		if s := strings.TrimSpace(d.ReasoningText); s != "" {
			out = append(out, c.add(&pb.AgentBlock{Id: id + ":r", Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: s})...)
		}
		if strings.TrimSpace(d.Content) != "" {
			c.cx.mentions(d.Content)
			out = append(out, c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: ms, Text: d.Content})...)
		}
		// Its tool calls, pending until they run (and while one waits for
		// your permission).
		for _, r := range d.ToolRequests {
			if _, seen := c.cx.byID[r.ToolCallID]; seen || r.ToolCallID == "" {
				continue
			}
			if b := c.tool(r.Name, r.Arguments, r.MCPServerName, r.MCPToolName); b != nil {
				b.Id, b.TimeUnixMs, b.Pending = r.ToolCallID, ms, true
				out = append(out, c.add(b)...)
			}
		}
		return out
	case "assistant.reasoning":
		if s := strings.TrimSpace(d.Content); s != "" {
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: s})
		}
	case "tool.execution_start":
		if d.ToolCallID == "" {
			return nil
		}
		if i, seen := c.cx.byID[d.ToolCallID]; seen && !c.cx.blocks[i].Pending {
			return nil // already done (a resumed session replays)
		}
		b := c.tool(d.ToolName, d.Arguments, d.MCPServerName, d.MCPToolName)
		if b == nil {
			return nil
		}
		b.Id, b.TimeUnixMs, b.Pending = d.ToolCallID, ms, true
		if i, seen := c.cx.byID[d.ToolCallID]; seen {
			b.TimeUnixMs = c.cx.blocks[i].TimeUnixMs
		}
		return c.add(b)
	case "tool.execution_complete":
		return c.complete(d)
	case "abort":
		// Calls that hadn't finished never will (Copilot shows them as
		// cancelled).
		var out []*pb.AgentBlock
		for _, b := range c.cx.blocks {
			if b.Pending && b.TurnId == c.cx.turn {
				blk := cloneBlock(b)
				blk.Pending, blk.Error, blk.Output = false, true, "Cancelled"
				out = append(out, c.add(blk)...)
			}
		}
		return append(out, c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Interrupted"})...)
	case "session.compaction_complete":
		if d.Success {
			if d.PostCompactionToks > 0 {
				c.tokens = d.PostCompactionToks
			}
			if d.TokenLimit > 0 {
				c.window = d.TokenLimit
			}
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted"})
		}
	case "session.error":
		if s := strings.TrimSpace(d.Message); s != "" {
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Error: " + firstLine(s)})
		}
	}
	return nil
}

// tool is a block for one of Copilot's tools (nil: one not worth showing),
// without its id, time or result.
func (c *Copilot) tool(name string, args json.RawMessage, mcpServer, mcpTool string) *pb.AgentBlock {
	var in map[string]any
	var raw string // arguments given as a string (a freeform tool's)
	if json.Unmarshal(args, &in) != nil {
		_ = json.Unmarshal(args, &raw)
	}
	str := func(k string) string { s, _ := in[k].(string); return s }
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	switch name {
	case "report_intent":
		return nil // Copilot's status line ("Exploring the code"), not work
	case "bash", "powershell", "local_shell":
		return c.cx.shell(name, cmpOr(str("command"), raw))
	case "write_bash", "write_powershell":
		if s := str("input"); s != "" {
			b.Title, b.Input = "Typed into a running command", s
		} else {
			b.Title = "Waited for a running command"
		}
	case "read_bash", "read_powershell":
		b.Title = "Read a running command's output"
	case "stop_bash", "stop_powershell":
		b.Title = "Stopped a running command"
	case "list_bash", "list_powershell":
		b.Title = "Listed running commands"
	case "view":
		path := c.cx.path(str("path"))
		b.Title, b.Input = "Read "+path, path
	case "create":
		path := c.cx.path(str("path"))
		content := str("file_text")
		b.Title, b.Input, b.Diff, b.Added = "Wrote "+path, path, clip(addedDiff(content), maxDiff), int32(lines(content))
	case "edit":
		return c.edit(str("path"), str("old_str"), str("new_str"))
	case "str_replace_editor":
		switch str("command") {
		case "view":
			path := c.cx.path(str("path"))
			b.Title, b.Input = "Read "+path, path
		case "create":
			path := c.cx.path(str("path"))
			content := str("file_text")
			b.Title, b.Input, b.Diff, b.Added = "Wrote "+path, path, clip(addedDiff(content), maxDiff), int32(lines(content))
		case "str_replace":
			return c.edit(str("path"), str("old_str"), str("new_str"))
		case "insert":
			path := c.cx.path(str("path"))
			nw := str("new_str")
			b.Title, b.Input, b.Diff, b.Added = "Edited "+path, path, clip(addedDiff(nw), maxDiff), int32(lines(nw))
		default:
			b.Title = "Used " + name
		}
	case "apply_patch":
		patch := raw
		if patch == "" {
			patch = cmpOr(str("input"), str("patch"))
		}
		p := c.cx.patch(patch)
		p.Tool = name
		return p
	case "grep", "rg":
		b.Input = str("pattern")
		b.Title = "Searched for " + quoteTitle(b.Input)
		if p := str("path"); p != "" {
			b.Title += " in " + c.cx.path(p)
		}
	case "glob":
		b.Input = str("pattern")
		b.Title = "Found files matching " + quoteTitle(b.Input)
	case "web_fetch":
		b.Input = str("url")
		b.Title = "Fetched " + b.Input
	case "web_search":
		b.Input = str("query")
		b.Title = "Searched the web for " + quoteTitle(b.Input)
	case "task":
		b.Kind = pb.AgentBlock_KIND_SUBAGENT
		b.Input = str("prompt")
		b.Title = cmpOr(str("description"), "Started an agent")
	case "update_todo":
		b.Title, b.Input = "Updated the plan", str("todos")
	case "ask_user":
		b.Title, b.Input = "Asked you", str("question")
	default:
		switch {
		case mcpServer != "" && mcpTool != "":
			b.Title = "Used " + mcpTool + " (" + mcpServer + ")"
		default:
			b.Title = "Used " + name
		}
		if raw != "" {
			b.Input = clip(raw, maxOutput)
		} else if len(args) > 0 && string(args) != "null" {
			b.Input = clip(string(args), maxOutput)
		}
	}
	return b
}

func (c *Copilot) edit(path, old, nw string) *pb.AgentBlock {
	path = c.cx.path(path)
	return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "edit", Input: path, Title: "Edited " + path,
		Diff: clip(simpleDiff(old, nw), maxDiff), Added: int32(lines(nw)), Removed: int32(lines(old))}
}

// complete fills in a call's result.
func (c *Copilot) complete(d cpData) []*pb.AgentBlock {
	i, ok := c.cx.byID[d.ToolCallID]
	if !ok {
		return nil
	}
	blk := cloneBlock(c.cx.blocks[i])
	blk.Pending = false
	blk.Error = !d.Success || d.ShellExecution != nil && d.ShellExecution.ExitCode != 0
	out := ""
	if d.Result != nil {
		out = d.Result.Content
	}
	if blk.Error {
		var e struct {
			Message string `json:"message"`
		}
		var s string
		if json.Unmarshal(d.Error, &e) == nil && e.Message != "" {
			out = cmpOr(out, e.Message)
		} else if json.Unmarshal(d.Error, &s) == nil && s != "" {
			out = cmpOr(out, s)
		}
	}
	// An edit shows as its diff; its output only when it failed.
	if blk.Diff == "" || blk.Error {
		blk.Output = clip(strings.TrimRight(stripANSI(out), "\n"), maxOutput)
	}
	return c.add(blk)
}

func quoteTitle(s string) string { return "“" + firstLine(s) + "”" }
