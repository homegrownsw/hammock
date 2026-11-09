package agentview

// OpenCode's sessions (and Kilo CLI's, a fork of it) as the agent view's
// blocks. OpenCode keeps every session in one SQLite database, not a file
// each, so agents.OpenCode's Open reads one session's rows from it and
// writes them as the lines this reads: OpenCodeLine, Hammock's own shape, the
// same whichever of OpenCode's two schemas (V1's message and part tables,
// V2's session_message and session_part) the rows came from.

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// OpenCodeLine is one line of an OpenCode session as Open writes it: the
// session itself first, then each message in order.
type OpenCodeLine struct {
	Session *OpenCodeSession `json:"session,omitempty"`

	ID   string `json:"id,omitempty"`
	Role string `json:"role,omitempty"` // user, assistant, shell, note
	Time int64  `json:"time,omitempty"` // unix ms
	// Text is what you sent (user), a note's text, or a shell's command.
	Text   string `json:"text,omitempty"`
	Model  string `json:"model,omitempty"`
	Tokens int64  `json:"tokens,omitempty"` // the context a reply took
	Error  string `json:"error,omitempty"`
	// Output and Status are a shell's (you ran "!cmd").
	Output string         `json:"output,omitempty"`
	Status string         `json:"status,omitempty"`
	Parts  []OpenCodePart `json:"parts,omitempty"`
}

// OpenCodeSession is a session's own details.
type OpenCodeSession struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd,omitempty"`
	Title   string `json:"title,omitempty"`
	Created int64  `json:"created,omitempty"` // unix ms
	Parent  string `json:"parent,omitempty"`  // a subagent's session: the one that started it
}

// OpenCodePart is part of a reply: text, reasoning or a tool call.
type OpenCodePart struct {
	ID     string          `json:"id,omitempty"`
	Type   string          `json:"type"`
	Text   string          `json:"text,omitempty"`
	Tool   string          `json:"tool,omitempty"`
	Status string          `json:"status,omitempty"` // pending, running, completed, error
	Input  json.RawMessage `json:"input,omitempty"`
	Output string          `json:"output,omitempty"`
	Error  string          `json:"error,omitempty"`
}

var _ Parser = (*OpenCode)(nil)

// OpenCode reads OpenCodeLines. It borrows a Codex parser for paths and
// patches: OpenCode's patch tool takes the same "*** Begin Patch" text as
// Codex's apply_patch.
type OpenCode struct {
	cx     *Codex
	blocks []*pb.AgentBlock
	byID   map[string]int
	turn   string

	model  string
	tokens int64
}

func NewOpenCode(cwd string) *OpenCode {
	return &OpenCode{cx: NewCodex(cwd), byID: map[string]int{}}
}

func (o *OpenCode) Blocks() []*pb.AgentBlock        { return o.blocks }
func (o *OpenCode) Image(string) (Image, bool)      { return Image{}, false }
func (o *OpenCode) Referenced(path string) bool     { return o.cx.Referenced(path) }
func (o *OpenCode) Rewound() bool                   { return false }
func (o *OpenCode) Context() (string, int64, int64) { return o.model, o.tokens, 0 }

func (o *OpenCode) Pending() *pb.AgentBlock {
	for i := len(o.blocks) - 1; i >= 0; i-- {
		b := o.blocks[i]
		if b.Kind == pb.AgentBlock_KIND_USER {
			return nil
		}
		if b.Pending {
			return b
		}
	}
	return nil
}

func (o *OpenCode) Feed(line []byte) []*pb.AgentBlock {
	var l OpenCodeLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	if s := l.Session; s != nil {
		if o.cx.Cwd == "" {
			o.cx.Cwd = s.Cwd
		}
		return nil
	}
	if l.Model != "" {
		o.model = l.Model
	}
	switch l.Role {
	case "user":
		if strings.TrimSpace(l.Text) == "" {
			return nil
		}
		o.turn = l.ID
		o.cx.mentions(l.Text)
		return o.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: l.Time, Text: l.Text, TurnId: l.ID})
	case "assistant":
		return o.assistant(l)
	case "shell":
		b := o.cx.shell("shell", l.Text)
		b.Id, b.TimeUnixMs = l.ID, l.Time
		b.Output = clip(strings.TrimRight(stripANSI(l.Output), "\n"), maxOutput)
		b.Pending = l.Status == "running"
		b.Error = l.Status == "timeout" || l.Status == "killed"
		return o.add(b)
	case "note":
		if l.Text == "" {
			return nil
		}
		return o.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: l.Time, Text: l.Text})
	}
	return nil
}

func (o *OpenCode) assistant(l OpenCodeLine) []*pb.AgentBlock {
	if l.Tokens > 0 {
		o.tokens = l.Tokens
	}
	var out []*pb.AgentBlock
	for i, p := range l.Parts {
		id := l.ID + "/" + p.ID
		if p.ID == "" {
			id = fmt.Sprintf("%s/%d", l.ID, i)
		}
		var b *pb.AgentBlock
		switch p.Type {
		case "text":
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			o.cx.mentions(p.Text)
			b = &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TEXT, Text: p.Text}
		case "reasoning":
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			b = &pb.AgentBlock{Kind: pb.AgentBlock_KIND_THINKING, Text: p.Text}
		case "tool":
			b = o.tool(p)
		default:
			continue
		}
		b.Id, b.TimeUnixMs = id, l.Time
		out = append(out, o.add(b)...)
	}
	if l.Error != "" {
		out = append(out, o.add(&pb.AgentBlock{Id: l.ID + "/error", Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: l.Time, Text: l.Error})...)
	}
	return out
}

// tool is a tool call as a block, by what OpenCode's tools take: V1's
// (and Kilo's) bash, read, write, edit, apply_patch, task; V2's shell,
// patch, subagent, which take path where V1's took filePath.
func (o *OpenCode) tool(p OpenCodePart) *pb.AgentBlock {
	var in map[string]any
	_ = json.Unmarshal(p.Input, &in)
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, _ := in[k].(string); s != "" {
				return s
			}
		}
		return ""
	}
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Tool}
	switch p.Tool {
	case "bash", "shell":
		b = o.cx.shell(p.Tool, str("command"))
		if d := str("description"); d != "" {
			b.Title = d
		}
	case "read":
		b.Input = o.cx.path(str("filePath", "path"))
		b.Title = "Read " + cmpOr(b.Input, "a file")
	case "write":
		b.Input = o.cx.path(str("filePath", "path"))
		content := str("content")
		b.Title, b.Diff, b.Added = "Wrote "+cmpOr(b.Input, "a file"), clip(addedDiff(content), maxDiff), int32(lines(content))
	case "edit":
		b.Input = o.cx.path(str("filePath", "path"))
		old, nw := str("oldString"), str("newString")
		b.Title = "Edited " + cmpOr(b.Input, "a file")
		if old != "" || nw != "" {
			b.Diff, b.Added, b.Removed = clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		}
	case "patch", "apply_patch":
		b = o.cx.patch(str("patchText"))
		b.Tool = p.Tool
	case "glob":
		b.Input = str("pattern")
		b.Title = "Found files matching `" + b.Input + "`"
	case "grep":
		b.Input = str("pattern")
		b.Title = "Searched for `" + b.Input + "`"
	case "list":
		b.Input = o.cx.path(str("path"))
		b.Title = "Listed " + cmpOr(b.Input, "files")
	case "webfetch":
		b.Input = str("url")
		b.Title = "Fetched " + b.Input
	case "websearch":
		b.Input = str("query")
		b.Title = "Searched the web for `" + b.Input + "`"
	case "task", "subagent":
		b.Input = str("prompt")
		b.Title = "Started an agent: " + cmpOr(str("description"), cmpOr(str("subagent_type"), str("agent")))
	case "todowrite":
		b.Title, b.Input = "Updated the plan", strings.TrimRight(todos(in["todos"]), "\n")
	case "question":
		b.Kind, b.Questions = pb.AgentBlock_KIND_QUESTION, openCodeQuestions(in["questions"])
		b.Title = "Asked you"
		if len(b.Questions) > 0 {
			b.Title = firstLine(b.Questions[0].Question)
		}
	case "skill":
		b.Input = str("name")
		b.Title = "Loaded the " + b.Input + " skill"
	default:
		b.Title = "Used " + p.Tool
		if len(p.Input) > 0 && string(p.Input) != "{}" {
			b.Input = clip(string(p.Input), maxOutput)
		}
	}
	switch p.Status {
	case "pending", "running", "streaming", "":
		b.Pending = true
	case "error":
		b.Error = true
		b.Output = clip(strings.TrimRight(stripANSI(cmpOr(p.Error, p.Output)), "\n"), maxOutput)
	default:
		// An edit's diff says what it did; others show what they printed.
		if b.Diff == "" || p.Tool == "bash" || p.Tool == "shell" {
			b.Output = clip(strings.TrimRight(stripANSI(p.Output), "\n"), maxOutput)
		}
	}
	return b
}

// openCodeQuestions reads the question tool's questions (OpenCode's own
// shape: multiple rather than Claude's multiSelect).
func openCodeQuestions(v any) []*pb.AgentQuestion {
	var out []*pb.AgentQuestion
	for _, it := range asAny(v) {
		m, _ := it.(map[string]any)
		q, _ := m["question"].(string)
		h, _ := m["header"].(string)
		multi, _ := m["multiple"].(bool)
		aq := &pb.AgentQuestion{Question: q, Header: h, MultiSelect: multi}
		for _, op := range asAny(m["options"]) {
			om, _ := op.(map[string]any)
			label, _ := om["label"].(string)
			desc, _ := om["description"].(string)
			aq.Options = append(aq.Options, &pb.AgentOption{Label: label, Description: desc})
		}
		out = append(out, aq)
	}
	return out
}

func (o *OpenCode) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = o.turn
	}
	if i, ok := o.byID[b.Id]; ok {
		o.blocks[i] = b
	} else {
		o.byID[b.Id] = len(o.blocks)
		o.blocks = append(o.blocks, b)
	}
	return []*pb.AgentBlock{b}
}
