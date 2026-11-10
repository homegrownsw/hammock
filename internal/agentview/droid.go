package agentview

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Factory's Droid CLI keeps each session as JSONL in
// ~/.factory/sessions/<project>/<id>.jsonl (format read from droid 1.6.0's
// bundled source; see internal/agents/droid.go): a session_start line, then
// one line per message,
//
//	{"type":"session_start","id":…,"title":…,"version":2,"cwd":…,…}
//	{"type":"message","id":…,"parentId":…,"timestamp":…,"message":{"role":"user"|"assistant","content":[…],…}}
//
// whose content is the Anthropic Messages API's (text, thinking, tool_use,
// tool_result, image blocks), as Claude Code's is; tool results are user
// messages. Its tools have their own names (Execute, Create, Edit with
// old_str/new_str, ApplyPatch, LS, FetchUrl, …), so the Droid parser
// renames them to Claude Code's and lets the Claude parser read the rest.

// DroidEvent is one line of a Droid session file, as far as Hammock reads it.
type DroidEvent struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"`
	Timestamp time.Time `json:"timestamp"`
	// session_start's.
	Title       string `json:"title"`
	ManualTitle bool   `json:"isSessionTitleManuallySet"`
	Cwd         string `json:"cwd"`
	// CallingSessionID: a subagent's session (a Task call's), not yours.
	CallingSessionID string `json:"callingSessionId"`
	// message's.
	Message *DroidMessage `json:"message"`
}

// DroidMessage is a message's body: the API message, with Droid's own
// fields kept beside it.
type DroidMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	// "llm_only": sent to the model, never shown; "user_only": shown to
	// you, never sent (Droid's system notices, saved with role "user").
	Visibility string `json:"visibility"`
	Hidden     bool   `json:"hiddenFromUserViews"`
	Visible    *bool  `json:"isUserVisible"`
	// A hook's record, not a message.
	HookEvent string `json:"hookEventName"`
	ModelID   string `json:"modelId"`
}

// ParseDroid reads one line of a Droid session file.
func ParseDroid(line []byte) (DroidEvent, bool) {
	var e DroidEvent
	if json.Unmarshal(line, &e) != nil {
		return e, false
	}
	return e, e.Type == "session_start" || e.Type == "message" && e.Message != nil
}

// Shown: the message is one Droid shows you (not the model's alone, nor a
// hook's record).
func (e DroidEvent) Shown() bool {
	m := e.Message
	return m != nil && m.Visibility != "llm_only" && !m.Hidden && (m.Visible == nil || *m.Visible) && m.HookEvent == ""
}

// Notice: a notice Droid showed you (role "system" in Droid, saved as
// "user"), not something you typed.
func (e DroidEvent) Notice() bool {
	return e.Message != nil && e.Message.Role == "user" && e.Message.Visibility == "user_only"
}

// Text is the message's text blocks, without the reminders Droid adds.
func (e DroidEvent) Text() string {
	if e.Message == nil {
		return ""
	}
	var s string
	if json.Unmarshal(e.Message.Content, &s) == nil {
		return StripDroidReminders(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	_ = json.Unmarshal(e.Message.Content, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			if t := StripDroidReminders(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

var droidReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// StripDroidReminders takes out the <system-reminder> blocks Droid adds to
// messages and tool results for the model.
func StripDroidReminders(s string) string {
	if !strings.Contains(s, "<system-reminder>") {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(droidReminder.ReplaceAllString(s, ""))
}

// Droid parses a Droid session file for the agent view.
type Droid struct {
	*Claude
	model string
}

var _ Parser = (*Droid)(nil)

// NewDroid reads a session of Droid's in cwd; sub is a subagent's own.
func NewDroid(cwd string, sub bool) *Droid {
	c := NewClaude(cwd)
	c.Sidechain = sub
	return &Droid{Claude: c}
}

// Context is the model of the latest reply. Droid's file doesn't say how
// much of the window a reply took.
func (d *Droid) Context() (string, int64, int64) { return d.model, 0, 0 }

// Feed parses one line and returns the blocks it added or changed.
func (d *Droid) Feed(line []byte) []*pb.AgentBlock {
	e, ok := ParseDroid(line)
	if !ok {
		return nil
	}
	if e.Type == "session_start" {
		if d.Cwd == "" {
			d.Cwd = e.Cwd
		}
		return nil
	}
	if !e.Shown() {
		return nil
	}
	m := e.Message
	if m.ModelID != "" && m.Role == "assistant" {
		d.model = m.ModelID
	}
	ms := e.Timestamp.UnixMilli()
	if e.Notice() {
		if t := e.Text(); t != "" {
			return d.add(&pb.AgentBlock{Id: e.ID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: t})
		}
		return nil
	}
	content, tools := droidContent(m.Content)
	l := ccLine{Type: m.Role, UUID: e.ID, ParentUUID: e.ParentID, Timestamp: e.Timestamp, Message: &ccMsg{Role: m.Role, Content: content}}
	switch m.Role {
	case "user":
		return d.user(l, ms)
	case "assistant":
		out := d.assistant(l, ms)
		for _, t := range tools {
			if b := d.retitle(t); b != nil {
				out = replaceBlock(out, b)
			}
		}
		return out
	}
	return nil
}

// droidTool is a call whose block the Claude parser can't title well.
type droidTool struct {
	id, name string
	input    map[string]any
}

// droidContent is a message's content with Droid's tools under Claude
// Code's names and inputs, and harness reminders taken out; tools are the
// calls to retitle after.
func droidContent(raw json.RawMessage) (json.RawMessage, []droidTool) {
	var blocks []map[string]any
	if json.Unmarshal(raw, &blocks) != nil {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			out, _ := json.Marshal(StripDroidReminders(s))
			return out, nil
		}
		return raw, nil
	}
	var tools []droidTool
	kept := blocks[:0]
	for _, b := range blocks {
		switch b["type"] {
		case "text":
			t, _ := b["text"].(string)
			if t = StripDroidReminders(t); t == "" {
				continue
			}
			b["text"] = t
		case "tool_result":
			switch c := b["content"].(type) {
			case string:
				b["content"] = StripDroidReminders(c)
			case []any:
				for _, p := range c {
					if pm, ok := p.(map[string]any); ok && pm["type"] == "text" {
						t, _ := pm["text"].(string)
						pm["text"] = StripDroidReminders(t)
					}
				}
			}
		case "tool_use":
			name, _ := b["name"].(string)
			in, _ := b["input"].(map[string]any)
			if in == nil {
				in = map[string]any{}
			}
			id, _ := b["id"].(string)
			if n, ok := droidToClaude(name, in); ok {
				b["name"] = n
			} else {
				tools = append(tools, droidTool{id, name, in})
			}
			b["input"] = in
		}
		kept = append(kept, b)
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return raw, nil
	}
	return out, tools
}

// droidToClaude renames a Droid tool to Claude Code's that does the same,
// moving its input's fields to Claude's names (false: it has none).
func droidToClaude(name string, in map[string]any) (string, bool) {
	move := func(from, to string) {
		if v, ok := in[from]; ok {
			in[to] = v
			delete(in, from)
		}
	}
	switch name {
	case "Execute":
		move("summary", "description")
		return "Bash", true
	case "Create":
		return "Write", true
	case "Edit", "MultiEdit":
		if _, ok := in["changes"]; ok {
			return "", false
		}
		move("old_str", "old_string")
		move("new_str", "new_string")
		return "Edit", true
	case "Glob":
		if ps, ok := in["patterns"].([]any); ok {
			var s []string
			for _, p := range ps {
				if p, ok := p.(string); ok {
					s = append(s, p)
				}
			}
			in["pattern"] = strings.Join(s, ", ")
		}
		return "Glob", true
	case "FetchUrl":
		return "WebFetch", true
	case "ExitSpecMode":
		return "ExitPlanMode", true
	case "TodoWrite":
		if _, ok := in["todos"].(string); ok {
			return "", false
		}
		return "TodoWrite", true
	case "AskUser", "ApplyPatch", "LS":
		return "", false
	}
	return name, true // Read, Grep, WebSearch, Task, Skill, mcp__…: as Claude Code's
}

// retitle fixes the block of a call the Claude parser read generically.
func (d *Droid) retitle(t droidTool) *pb.AgentBlock {
	id, ok := d.byTool[t.id]
	if !ok {
		return nil
	}
	old := d.blocks[d.byID[id]]
	str := func(k string) string { s, _ := t.input[k].(string); return s }
	var b *pb.AgentBlock
	switch t.name {
	case "ApplyPatch":
		cx := NewCodex(d.Cwd)
		b = cx.patch(str("input"))
		for f := range cx.files {
			d.files[f] = true
		}
		b.Tool = "Edit"
	case "Edit", "MultiEdit": // several changes to one file
		p := str("file_path")
		if p != "" {
			d.files[p] = true
		}
		var diff strings.Builder
		b = &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "Edit", Input: d.rel(p), Title: "Edited " + d.rel(p)}
		changes, _ := t.input["changes"].([]any)
		for _, c := range changes {
			cm, _ := c.(map[string]any)
			o, _ := cm["old_str"].(string)
			n, _ := cm["new_str"].(string)
			diff.WriteString(simpleDiff(o, n))
			b.Added += int32(lines(n))
			b.Removed += int32(lines(o))
		}
		b.Diff = clip(diff.String(), maxDiff)
	case "LS":
		dir := str("directory_path")
		if dir != "" {
			d.files[dir] = true
		}
		rel := d.rel(dir)
		title := "Listed " + rel
		if rel == "" || rel == "." {
			title = "Listed the folder"
		}
		b = &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "LS", Input: rel, Title: title}
	case "TodoWrite":
		b = &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "TodoWrite", Title: "Updated the to-do list", Input: str("todos")}
	case "AskUser":
		b = &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "AskUser", Title: "Asked you", Input: str("questionnaire")}
	default:
		return nil
	}
	b.Id, b.TimeUnixMs, b.TurnId, b.Pending = old.Id, old.TimeUnixMs, old.TurnId, old.Pending
	return d.add(b)[0]
}

// replaceBlock puts b in place of the block with its id in out.
func replaceBlock(out []*pb.AgentBlock, b *pb.AgentBlock) []*pb.AgentBlock {
	for i, o := range out {
		if o.Id == b.Id {
			out[i] = b
			return out
		}
	}
	return append(out, b)
}
