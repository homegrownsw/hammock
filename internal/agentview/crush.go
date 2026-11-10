package agentview

// Crush's sessions as the agent view's blocks. Crush keeps them in a SQLite
// database, which agents.Crush reads into lines (crushOpen): a
// {"type":"hammock_session"} line for the session (its title, working folder
// and the context its last reply took, prompt_tokens), then one
// {"type":"message"} line per messages row, in order: id, role (user,
// assistant or tool), parts, model, created_at (Unix seconds) and summary
// (is_summary_message). The whole session is read again whenever it
// changes, as Crush rewrites a message while it streams.
//
// parts is Crush's own JSON (internal/message/message.go marshalParts and
// content.go, v0.98.1): a list of {"type", "data"}, the types text {text,
// hidden}, reasoning {thinking}, tool_call {id, name, input (a JSON string,
// partial until finished), finished}, tool_result {tool_call_id, name,
// content, metadata (a JSON string), is_error} (in a "tool" message),
// finish {reason: end_turn, max_tokens, tool_use, canceled, error,
// content_filter…, message, details}, binary {Path, MIMEType, Data
// (base64)}, image_url {url} and shell_command {command, output,
// exit_code} (your own !command). Its tools (internal/agent/tools): bash
// {command, description}, view {file_path}, write {file_path, content},
// edit {file_path, old_string, new_string}, multiedit {file_path, edits:
// [{old_string, new_string}]}, glob/grep {pattern, path}, ls {path},
// fetch/web_fetch {url}, agentic_fetch {url, prompt}, web_search {query},
// download {url, file_path}, todos {todos: [{content, status}]}, question
// {questions}, agent {prompt} (a subagent, in a session of its own), and
// MCP tools, named mcp_<server>_<tool>.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Crush)(nil)

// Crush parses a Crush session, as agents.Crush's lines.
type Crush struct {
	Cwd string

	blocks []*pb.AgentBlock
	byID   map[string]int
	byCall map[string]string // tool call id -> block id
	files  map[string]bool
	images map[string]Image
	turn   string

	model  string
	tokens int64
}

func NewCrush(cwd string) *Crush {
	return &Crush{Cwd: cwd, byID: map[string]int{}, byCall: map[string]string{}, files: map[string]bool{}, images: map[string]Image{}}
}

func (c *Crush) Blocks() []*pb.AgentBlock        { return c.blocks }
func (c *Crush) Image(ref string) (Image, bool)  { i, ok := c.images[ref]; return i, ok }
func (c *Crush) Referenced(path string) bool     { return c.files[filepath.Clean(path)] }
func (c *Crush) Context() (string, int64, int64) { return c.model, c.tokens, 0 }
func (c *Crush) Rewound() bool                   { return false }
func (c *Crush) Pending() *pb.AgentBlock         { return pendingAfterUser(c.blocks) }

// CrushPart is one of a message's parts, its data's fields flattened.
type CrushPart struct {
	Type string
	// text
	Text   string `json:"text"`
	Hidden bool   `json:"hidden"`
	// reasoning
	Thinking string `json:"thinking"`
	// tool_call
	ID       string `json:"id"`
	Name     string `json:"name"`
	Input    string `json:"input"`
	Finished bool   `json:"finished"`
	// tool_result
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	Metadata   string `json:"metadata"`
	IsError    bool   `json:"is_error"`
	MediaType  string `json:"mime_type"` // with its image in Data
	// finish
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Details string `json:"details"`
	// binary (Go's field names: it has no tags); Data is also a
	// tool_result's data, base64 too
	Path     string `json:"Path"`
	MIMEType string `json:"MIMEType"`
	Data     []byte `json:"Data"`
	// image_url
	URL string `json:"url"`
	// shell_command
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// CrushParts reads a message's parts.
func CrushParts(raw json.RawMessage) []CrushPart {
	var wrapped []struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &wrapped) != nil {
		return nil
	}
	out := make([]CrushPart, 0, len(wrapped))
	for _, w := range wrapped {
		var p CrushPart
		_ = json.Unmarshal(w.Data, &p)
		p.Type = w.Type
		out = append(out, p)
	}
	return out
}

type crushLine struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	Cwd          string          `json:"cwd"`
	PromptTokens int64           `json:"prompt_tokens"`
	Role         string          `json:"role"`
	Parts        json.RawMessage `json:"parts"`
	Model        string          `json:"model"`
	Created      int64           `json:"created_at"`
	Summary      bool            `json:"summary"`
}

func (c *Crush) Feed(line []byte) []*pb.AgentBlock {
	var l crushLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	if l.Type == "hammock_session" {
		if c.Cwd == "" {
			c.Cwd = l.Cwd
		}
		c.tokens = l.PromptTokens
		return nil
	}
	if l.Type != "message" || l.ID == "" {
		return nil
	}
	at := l.Created * 1000
	parts := CrushParts(l.Parts)
	var out []*pb.AgentBlock
	switch l.Role {
	case "user":
		var text []string
		var images []*pb.AgentImage
		for i, p := range parts {
			switch p.Type {
			case "text":
				if !p.Hidden && strings.TrimSpace(p.Text) != "" {
					text = append(text, p.Text)
				}
			case "binary":
				if strings.HasPrefix(p.MIMEType, "image/") && len(p.Data) > 0 {
					ref := fmt.Sprintf("img:%s:%d", l.ID, i)
					c.images[ref] = Image{MediaType: p.MIMEType, Data: p.Data}
					images = append(images, &pb.AgentImage{Ref: ref, MediaType: p.MIMEType})
				} else if p.Path != "" {
					c.mention(p.Path)
					text = append(text, "📎 "+p.Path)
				}
			case "image_url":
				if p.URL != "" && !strings.HasPrefix(p.URL, "data:") {
					text = append(text, p.URL)
				}
			case "shell_command":
				out = append(out, c.add(&pb.AgentBlock{Id: l.ID + ":" + fmt.Sprint(i), Kind: pb.AgentBlock_KIND_TOOL, TimeUnixMs: at,
					Tool: "bash", Title: "Ran `" + firstLine(p.Command) + "`", Input: p.Command,
					Output: clip(stripANSI(p.Output), maxOutput), Error: p.ExitCode != 0})...)
			}
		}
		if len(text) > 0 || len(images) > 0 {
			c.turn = l.ID
			out = append(out, c.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: at, Text: strings.Join(text, "\n"), Images: images, TurnId: l.ID})...)
		}
	case "assistant":
		if l.Model != "" {
			c.model = l.Model
		}
		if l.Summary {
			var text []string
			for _, p := range parts {
				if p.Type == "text" {
					text = append(text, p.Text)
				}
			}
			return c.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: at, Title: "Summarized the conversation", Text: strings.Join(text, "\n")})
		}
		// Its text, joined, where its first text part is (after its
		// thinking, before its tool calls, as Crush shows it).
		var texts []string
		for _, p := range parts {
			if p.Type == "text" {
				texts = append(texts, p.Text)
			}
		}
		text := strings.TrimSpace(strings.Join(texts, ""))
		for i, p := range parts {
			id := fmt.Sprintf("%s:%d", l.ID, i)
			switch p.Type {
			case "reasoning":
				if s := strings.TrimSpace(p.Thinking); s != "" {
					out = append(out, c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: at, Text: s})...)
				}
			case "text":
				if text != "" {
					mentionPaths(c.files, text)
					out = append(out, c.add(&pb.AgentBlock{Id: l.ID, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: at, Text: text})...)
					text = ""
				}
			case "tool_call":
				b := c.crushTool(p)
				b.Id, b.TimeUnixMs, b.Pending = "call:"+cmpOr(p.ID, id), at, true
				if old, ok := c.blockByCall(p.ID); ok {
					// Seen before (as it streamed): keep its result.
					prev := c.blocks[old]
					b.Pending, b.Output, b.Error = prev.Pending, prev.Output, prev.Error
					if prev.Diff != "" && b.Diff == "" {
						b.Diff, b.Added, b.Removed = prev.Diff, prev.Added, prev.Removed
					}
				}
				if p.ID != "" {
					c.byCall[p.ID] = b.Id
				}
				out = append(out, c.add(b)...)
			case "finish":
				if p.Reason == "canceled" || p.Reason == "error" {
					// Its calls that never got a result never will.
					for _, q := range parts {
						if i, ok := c.blockByCall(q.ID); ok && q.Type == "tool_call" && c.blocks[i].Pending {
							b := cloneBlock(c.blocks[i])
							b.Pending, b.Error = false, true
							c.blocks[i] = b
							out = append(out, b)
						}
					}
				}
				switch p.Reason {
				case "canceled":
					out = append(out, c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: at, Title: "Canceled"})...)
				case "error", "content_filter", "max_tokens":
					title := map[string]string{"error": "Error", "content_filter": "Refused", "max_tokens": "Ran out of output tokens"}[p.Reason]
					out = append(out, c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: at, Title: cmpOr(firstLine(p.Message), title),
						Text: p.Details, Error: p.Reason != "max_tokens"})...)
				}
			}
		}
	case "tool":
		for _, p := range parts {
			if p.Type != "tool_result" {
				continue
			}
			i, ok := c.blockByCall(p.ToolCallID)
			if !ok {
				continue
			}
			b := cloneBlock(c.blocks[i])
			c.crushResult(b, p)
			c.blocks[i] = b
			out = append(out, b)
		}
	}
	return out
}

func (c *Crush) blockByCall(id string) (int, bool) {
	if id == "" || c.byCall[id] == "" {
		return 0, false
	}
	i, ok := c.byID[c.byCall[id]]
	return i, ok
}

// add puts b in the view, replacing the block with its id if there's one
// (a message read again).
func (c *Crush) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = c.turn
	}
	if i, ok := c.byID[b.Id]; ok {
		c.blocks[i] = b
		return []*pb.AgentBlock{b}
	}
	c.byID[b.Id] = len(c.blocks)
	c.blocks = append(c.blocks, b)
	return []*pb.AgentBlock{b}
}

func (c *Crush) mention(p string) {
	if p == "" {
		return
	}
	if !filepath.IsAbs(p) && c.Cwd != "" {
		p = filepath.Join(c.Cwd, p)
	}
	c.files[filepath.Clean(p)] = true
}

// rel is p as shown: relative to the session's folder when it's in it.
func (c *Crush) rel(p string) string {
	if p == "" {
		return ""
	}
	c.mention(p)
	if c.Cwd != "" && filepath.IsAbs(p) {
		if r, err := filepath.Rel(c.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

// crushTool is the block for a call to one of Crush's tools.
func (c *Crush) crushTool(p CrushPart) *pb.AgentBlock {
	var args map[string]any
	_ = json.Unmarshal([]byte(p.Input), &args) // partial while it streams: nothing yet
	str := func(k string) string { s, _ := args[k].(string); return s }
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name}
	switch p.Name {
	case "bash":
		b.Input = str("command")
		mentionPaths(c.files, b.Input)
		b.Title = "Ran `" + firstLine(b.Input) + "`"
		if d := str("description"); d != "" {
			b.Title = d
		}
	case "view":
		b.Input = c.rel(str("file_path"))
		b.Title = "Read " + b.Input
	case "write":
		b.Input = c.rel(str("file_path"))
		content := str("content")
		b.Title = "Wrote " + b.Input
		b.Diff, b.Added = clip(addedDiff(content), maxDiff), int32(lines(content))
	case "edit":
		b.Input = c.rel(str("file_path"))
		b.Title = "Edited " + b.Input
		if old, nw := str("old_string"), str("new_string"); old != "" || nw != "" {
			b.Diff = clip(simpleDiff(old, nw), maxDiff)
			b.Added, b.Removed = int32(lines(nw)), int32(lines(old))
		}
	case "multiedit":
		b.Input = c.rel(str("file_path"))
		b.Title = "Edited " + b.Input
		var d strings.Builder
		for _, e := range asAny(args["edits"]) {
			m, _ := e.(map[string]any)
			old, _ := m["old_string"].(string)
			nw, _ := m["new_string"].(string)
			d.WriteString(simpleDiff(old, nw))
			b.Added += int32(lines(nw))
			b.Removed += int32(lines(old))
		}
		b.Diff = clip(d.String(), maxDiff)
	case "glob":
		b.Input = str("pattern")
		b.Title = "Found files matching `" + b.Input + "`"
	case "grep", "rg":
		b.Input = str("pattern")
		b.Title = "Searched for `" + b.Input + "`"
	case "ls":
		b.Input = cmpOr(c.rel(str("path")), ".")
		b.Title = "Listed " + b.Input
	case "fetch", "web_fetch", "agentic_fetch":
		b.Input = cmpOr(str("url"), str("prompt"))
		b.Title = "Fetched " + firstLine(b.Input)
	case "web_search", "sourcegraph":
		b.Input = str("query")
		b.Title = "Searched the web for “" + b.Input + "”"
		if p.Name == "sourcegraph" {
			b.Title = "Searched Sourcegraph for “" + b.Input + "”"
		}
	case "download":
		b.Input = c.rel(str("file_path"))
		b.Title = "Downloaded " + str("url")
	case "todos":
		b.Title = "Updated the to-do list"
		b.Input = todos(args["todos"])
	case "agent":
		b.Kind = pb.AgentBlock_KIND_SUBAGENT
		b.Input = str("prompt")
		b.Title = firstLine(b.Input)
	case "question":
		b.Kind = pb.AgentBlock_KIND_QUESTION
		b.Title = "Asked you"
		for _, q := range asAny(args["questions"]) {
			m, _ := q.(map[string]any)
			text, _ := m["question"].(string)
			label, _ := m["label"].(string)
			aq := &pb.AgentQuestion{Question: text, Header: label, MultiSelect: m["type"] == "multi_choice"}
			for _, ch := range append(asAny(m["choices"]), asAny(m["options"])...) {
				switch v := ch.(type) {
				case string:
					aq.Options = append(aq.Options, &pb.AgentOption{Label: v})
				case map[string]any:
					l, _ := v["label"].(string)
					d, _ := v["description"].(string)
					aq.Options = append(aq.Options, &pb.AgentOption{Label: l, Description: d})
				}
			}
			b.Questions = append(b.Questions, aq)
		}
	default:
		shown := p.Name
		// An MCP tool: mcp_<server>_<tool>.
		if rest, ok := strings.CutPrefix(p.Name, "mcp_"); ok {
			if server, tool, ok := strings.Cut(rest, "_"); ok {
				shown = tool + " (" + server + ")"
			}
		}
		b.Title = "Used " + shown
		if p.Input != "" && p.Input != "{}" {
			b.Input = clip(p.Input, 2000)
		}
	}
	return b
}

// crushResult fills in a call's result: what the tool told the model, or
// for a write the diff it made.
func (c *Crush) crushResult(b *pb.AgentBlock, p CrushPart) {
	b.Pending, b.Error = false, p.IsError
	out := p.Content
	switch b.Tool {
	case "bash":
		// Its content ends with the folder it ran in (<cwd>…</cwd>), for
		// the model; its metadata has the output alone.
		var md struct {
			Output *string `json:"output"`
		}
		if json.Unmarshal([]byte(p.Metadata), &md) == nil && md.Output != nil && !p.IsError {
			out = *md.Output
		} else if i := strings.LastIndex(out, "<cwd>"); i >= 0 && strings.HasSuffix(strings.TrimSpace(out), "</cwd>") {
			out = strings.TrimSpace(out[:i])
		}
	case "edit", "multiedit":
		// "Content replaced in file: …": the diff says it.
		if !p.IsError {
			out = ""
		}
	case "write":
		var md struct {
			Diff string `json:"diff"`
		}
		if json.Unmarshal([]byte(p.Metadata), &md) == nil && md.Diff != "" {
			if d, added, removed := unifiedDiff(md.Diff); d != "" {
				b.Diff, b.Added, b.Removed = clip(d, maxDiff), added, removed
			}
		}
		if !p.IsError {
			out = ""
		}
	case "view":
		// The file's text, not worth showing again.
		if !p.IsError {
			out = ""
		}
	case "question":
		if len(b.Questions) > 0 {
			b.Questions[0].Answer = strings.TrimSpace(out)
		}
	}
	if strings.HasPrefix(p.MediaType, "image/") && len(p.Data) > 0 {
		ref := "img:" + b.Id
		c.images[ref] = Image{MediaType: p.MediaType, Data: p.Data}
		b.Images = []*pb.AgentImage{{Ref: ref, MediaType: p.MediaType}}
	}
	mentionPaths(c.files, out)
	b.Output = clip(stripANSI(out), maxOutput)
}
