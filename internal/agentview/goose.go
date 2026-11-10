package agentview

// Goose's sessions as the agent view's blocks. Goose (github.com/block/goose,
// now aaif-goose; read at 1.58.0) keeps every session in one SQLite
// database, <data>/sessions/sessions.db: a sessions table (id, name,
// working_dir, created_at, total_tokens, model_config_json…) and a messages
// table, one row per message (role "user" or "assistant", created_timestamp,
// content_json, metadata_json). agents.Goose's Open turns one session into
// lines, which this reads:
//
//	{"type":"session","id":"20261107_1","cwd":"/work","name":"…","created_ms":…,"model":"…","tokens":1234}
//	{"type":"message","row":7,"role":"assistant","time_ms":…,"content":[…],"metadata":{…}}
//
// content is the row's content_json as it is: goose's MessageContent list
// (crates/goose-provider-types/src/conversation/message.rs), tagged by
// "type": text {text}, image {data, mimeType}, toolRequest {id, toolCall:
// {status, value: {name, arguments}} or {status: "error", error}},
// toolResponse {id, toolResult: {status, value: {content, isError,
// structuredContent}} (older: value a content list) or {status: "error",
// error}}, thinking {thinking}, redactedThinking, toolConfirmationRequest,
// actionRequired, systemNotification {notificationType, msg}, error {kind,
// message}. Tool results come back in "user" messages. metadata is its
// MessageMetadata: userVisible (false: only the model sees it), agentVisible,
// compactionSummary, inference {requestedModel, resolvedModel}.
//
// Its own tools (the developer extension's, sent without a prefix): shell
// {command}, write {path, content}, edit {path, before, after}, tree {path,
// depth}, read_image {source}; older versions' developer__shell and
// developer__text_editor {command: view|write|str_replace|insert|undo_edit,
// path, file_text, old_str, new_str}. Other extensions' are
// "<extension>__<tool>" (todo__todo_write {content}); summon's delegate
// {instructions} starts a subagent.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Goose)(nil)

type Goose struct {
	Cwd string

	blocks []*pb.AgentBlock
	byID   map[string]int
	byCall map[string]string // toolRequest id -> block id
	files  map[string]bool
	images map[string]Image
	turn   string

	model  string
	tokens int64
}

func NewGoose(cwd string) *Goose {
	return &Goose{Cwd: cwd, byID: map[string]int{}, byCall: map[string]string{}, files: map[string]bool{}, images: map[string]Image{}}
}

func (g *Goose) Blocks() []*pb.AgentBlock        { return g.blocks }
func (g *Goose) Image(ref string) (Image, bool)  { i, ok := g.images[ref]; return i, ok }
func (g *Goose) Referenced(path string) bool     { return g.files[filepath.Clean(path)] }
func (g *Goose) Context() (string, int64, int64) { return g.model, g.tokens, 0 }
func (g *Goose) Rewound() bool                   { return false }

// Pending is the newest tool call without its result since your last
// message: what a permission prompt on its screen is about.
func (g *Goose) Pending() *pb.AgentBlock {
	for i := len(g.blocks) - 1; i >= 0; i-- {
		switch b := g.blocks[i]; {
		case b.Kind == pb.AgentBlock_KIND_USER:
			return nil
		case b.Pending:
			return b
		}
	}
	return nil
}

// GooseLine is one line of a goose session, as agents.Goose's Open writes
// them: the session's header, or one of its messages.
type GooseLine struct {
	Type string `json:"type"` // "session" or "message"

	// The session.
	ID        string `json:"id,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Name      string `json:"name,omitempty"`
	Named     bool   `json:"named,omitempty"` // a name you gave it, not one goose made up
	CreatedMs int64  `json:"created_ms,omitempty"`
	UpdatedMs int64  `json:"updated_ms,omitempty"`
	Model     string `json:"model,omitempty"`
	Tokens    int64  `json:"tokens,omitempty"` // its context, as goose last counted it

	// A message.
	Row      int64           `json:"row,omitempty"`
	Role     string          `json:"role,omitempty"`
	TimeMs   int64           `json:"time_ms,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
}

// GoosePart is one of a message's content items.
type GoosePart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
	Name     string `json:"name"` // a document's
	ID       string `json:"id"`
	// toolRequest's call and toolResponse's result.
	ToolCall   *GooseResult `json:"toolCall"`
	ToolResult *GooseResult `json:"toolResult"`
	// systemNotification, error.
	NotificationType string `json:"notificationType"`
	Msg              string `json:"msg"`
	Kind             string `json:"kind"`
	Message          string `json:"message"`
}

// GooseResult is goose's ToolResult: a value, or an error.
type GooseResult struct {
	Status string          `json:"status"`
	Value  json.RawMessage `json:"value"`
	Error  string          `json:"error"`
}

// Call is a toolRequest's tool and arguments.
func (r *GooseResult) Call() (string, map[string]any) {
	var v struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if r != nil {
		_ = json.Unmarshal(r.Value, &v)
	}
	return v.Name, v.Arguments
}

// GooseMeta is a message's metadata.
type GooseMeta struct {
	UserVisible       *bool `json:"userVisible"`
	CompactionSummary bool  `json:"compactionSummary"`
	Steer             bool  `json:"steer"`
	Inference         *struct {
		RequestedModel string `json:"requestedModel"`
		ResolvedModel  string `json:"resolvedModel"`
	} `json:"inference"`
}

// ParseGooseLine reads one line, with its message's content and metadata.
func ParseGooseLine(line []byte) (GooseLine, []GoosePart, GooseMeta, bool) {
	var l GooseLine
	var parts []GoosePart
	var meta GooseMeta
	if json.Unmarshal(line, &l) != nil {
		return l, nil, meta, false
	}
	if len(l.Content) > 0 {
		_ = json.Unmarshal(l.Content, &parts)
	}
	if len(l.Metadata) > 0 {
		_ = json.Unmarshal(l.Metadata, &meta)
	}
	return l, parts, meta, true
}

// Shown: you see the message in goose (it isn't only for the model).
func (m GooseMeta) Shown() bool { return m.UserVisible == nil || *m.UserVisible }

// GooseUserText is a user message's text: what you wrote, not tool results.
func GooseUserText(parts []GoosePart) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func (g *Goose) Feed(line []byte) []*pb.AgentBlock {
	l, parts, meta, ok := ParseGooseLine(line)
	if !ok {
		return nil
	}
	if l.Type == "session" {
		if g.Cwd == "" {
			g.Cwd = l.Cwd
		}
		if l.Model != "" {
			g.model = l.Model
		}
		g.tokens = l.Tokens
		return nil
	}
	if l.Type != "message" {
		return nil
	}
	if inf := meta.Inference; inf != nil && l.Role == "assistant" {
		g.model = cmpOr(inf.ResolvedModel, cmpOr(inf.RequestedModel, g.model))
	}
	id := fmt.Sprintf("g%d", l.Row)
	var out []*pb.AgentBlock
	// Tool results go to their calls, whether or not the message is shown.
	for _, p := range parts {
		if p.Type == "toolResponse" {
			out = append(out, g.result(id, p)...)
		}
	}
	if !meta.Shown() {
		return out
	}
	if meta.CompactionSummary {
		return append(out, g.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: l.TimeMs, Text: "Conversation compacted"})...)
	}
	if l.Role == "user" {
		blk := &pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: l.TimeMs, TurnId: id, Text: GooseUserText(parts)}
		for i, p := range parts {
			if p.Type == "image" {
				if img := g.keepImage(fmt.Sprintf("%s.%d", id, i), p.Data, p.MimeType); img != nil {
					blk.Images = append(blk.Images, img)
				}
			}
		}
		if blk.Text == "" && len(blk.Images) == 0 {
			return out
		}
		g.turn = id
		g.mentions(blk.Text)
		return append(out, g.add(blk)...)
	}
	for i, p := range parts {
		bid := fmt.Sprintf("%s.%d", id, i)
		switch p.Type {
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				g.mentions(p.Text)
				out = append(out, g.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: l.TimeMs, Text: p.Text})...)
			}
		case "thinking":
			if s := strings.TrimSpace(p.Thinking); s != "" {
				out = append(out, g.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: l.TimeMs, Text: s})...)
			}
		case "toolRequest":
			var blk *pb.AgentBlock
			if p.ToolCall != nil && p.ToolCall.Status == "error" {
				// The model asked for a tool in a way goose couldn't read.
				blk = &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Title: "Couldn't call a tool", Output: clip(p.ToolCall.Error, maxOutput), Error: true}
			} else {
				blk = g.tool(p.ToolCall.Call())
				blk.Pending = true
			}
			blk.Id, blk.TimeUnixMs = bid, l.TimeMs
			if p.ID != "" {
				g.byCall[p.ID] = bid
			}
			out = append(out, g.add(blk)...)
		case "systemNotification":
			// Its "thinking" and progress messages come and go on its screen.
			if p.NotificationType == "inlineMessage" || p.NotificationType == "creditsExhausted" {
				if s := strings.TrimSpace(p.Msg); s != "" {
					out = append(out, g.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: l.TimeMs, Text: firstLine(s)})...)
				}
			}
		case "error":
			text := "Error"
			if s := strings.TrimSpace(p.Message); s != "" {
				text += ": " + firstLine(s)
			}
			out = append(out, g.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: l.TimeMs, Text: text})...)
		}
	}
	return out
}

// result puts a toolResponse's output on its call's block.
func (g *Goose) result(msgID string, p GoosePart) []*pb.AgentBlock {
	bid, ok := g.byCall[p.ID]
	if !ok || p.ToolResult == nil {
		return nil
	}
	i, ok := g.byID[bid]
	if !ok {
		return nil
	}
	blk := cloneBlock(g.blocks[i])
	blk.Pending = false
	r := p.ToolResult
	if r.Status == "error" {
		blk.Error, blk.Output = true, clip(strings.TrimSpace(r.Error), maxOutput)
		return g.add(blk)
	}
	// A CallToolResult ({content, isError}); older ones are the content list.
	var v struct {
		Content []GoosePart `json:"content"`
		IsError bool        `json:"isError"`
	}
	if json.Unmarshal(r.Value, &v) != nil {
		_ = json.Unmarshal(r.Value, &v.Content)
	}
	var texts []string
	for j, c := range v.Content {
		switch c.Type {
		case "text":
			texts = append(texts, c.Text)
		case "image":
			if img := g.keepImage(fmt.Sprintf("%s.%s.%d", msgID, bid, j), c.Data, c.MimeType); img != nil {
				blk.Images = append(blk.Images, img)
			}
		}
	}
	blk.Error = v.IsError
	// A write's or an edit's own confirmation adds nothing to its diff.
	if blk.Diff == "" || blk.Error {
		blk.Output = clip(strings.TrimRight(stripANSI(strings.Join(texts, "\n")), "\n"), maxOutput)
	}
	return g.add(blk)
}

// tool is a call to one of goose's tools, by its arguments.
func (g *Goose) tool(name string, in map[string]any) *pb.AgentBlock {
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	ext, base, prefixed := strings.Cut(name, "__")
	if !prefixed {
		ext, base = "", name
	}
	dev := ext == "" || ext == "developer"
	switch {
	case dev && base == "shell":
		cmd := str("command")
		g.mentions(cmd)
		b.Input, b.Title = cmd, "Ran `"+firstLine(cmd)+"`"
	case dev && base == "write":
		p, content := g.path(str("path")), str("content")
		b.Input, b.Title = p, "Wrote "+p
		b.Diff, b.Added = clip(addedDiff(content), maxDiff), int32(lines(content))
	case dev && base == "edit":
		p, old, nw := g.path(str("path")), str("before"), str("after")
		b.Input, b.Title = p, "Edited "+p
		b.Diff, b.Added, b.Removed = clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
	case dev && base == "text_editor":
		p := g.path(str("path"))
		b.Input = p
		switch str("command") {
		case "view":
			b.Title = "Read " + p
		case "write":
			content := str("file_text")
			b.Title, b.Diff, b.Added = "Wrote "+p, clip(addedDiff(content), maxDiff), int32(lines(content))
		case "str_replace":
			old, nw := str("old_str"), str("new_str")
			b.Title = "Edited " + p
			b.Diff, b.Added, b.Removed = clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		case "insert":
			nw := str("new_str")
			b.Title, b.Diff, b.Added = "Edited "+p, clip(addedDiff(nw), maxDiff), int32(lines(nw))
		case "undo_edit":
			b.Title = "Undid the last edit to " + p
		default:
			b.Title = "Used the editor on " + p
		}
	case dev && base == "tree":
		p := g.path(str("path"))
		b.Input, b.Title = p, "Listed "+cmpOr(p, "the folder")
	case dev && base == "read_image":
		src := str("source")
		if !strings.Contains(src, "://") {
			src = g.path(src)
		}
		b.Input, b.Title = src, "Looked at "+src
	case base == "todo_write":
		b.Input, b.Title = str("content"), "Updated the plan"
	case ext == "" && (base == "delegate" || base == "subagent"):
		task := str("instructions", "source")
		b.Input, b.Title = task, "Started an agent"
		if task != "" {
			b.Title += ": " + firstLine(task)
		}
	default:
		b.Title = "Used " + name
		if prefixed {
			b.Title = "Used " + base + " (" + ext + ")"
		}
		if len(in) > 0 {
			raw, _ := json.Marshal(in)
			b.Input = clip(string(raw), maxOutput)
		}
	}
	return b
}

func (g *Goose) keepImage(ref, data, mime string) *pb.AgentImage {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(b) == 0 {
		return nil
	}
	ref = "img:" + ref
	g.images[ref] = Image{MediaType: mime, Data: b}
	return &pb.AgentImage{Ref: ref, MediaType: mime}
}

func (g *Goose) add(b *pb.AgentBlock) []*pb.AgentBlock {
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

func (g *Goose) path(s string) string {
	if s == "" {
		return ""
	}
	abs := s
	if !filepath.IsAbs(abs) && g.Cwd != "" {
		abs = filepath.Join(g.Cwd, abs)
	}
	g.files[filepath.Clean(abs)] = true
	if g.Cwd != "" {
		if r, err := filepath.Rel(g.Cwd, abs); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return s
}

func (g *Goose) mentions(s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		g.files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}
