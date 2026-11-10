package agentview

// Pi's sessions (~/.pi/agent/sessions/--<cwd>--/<time>_<id>.jsonl) as the
// agent view's blocks. The format is pi-mono's packages/coding-agent
// docs/session-format.md and message-types.md (session version 3, pi
// 1.5.0): a "session" header line, then entries with an id and a parentId
// forming a tree. Pi appends every entry as a child of the current leaf;
// going back in /tree (or /fork's in-place branch) makes the next entry a
// child of an earlier one, and the view then shows that branch: the
// entries from the root to it, as Pi's own screen does.
//
// Messages: "user" (a string or text and image parts), "assistant" (text,
// thinking and toolCall parts, with its model and usage), "toolResult"
// (by toolCallId), "bashExecution" (your own !command), "custom" (an
// extension's). Its tools: bash {command}, read {path}, write {path,
// content}, edit {path, edits: [{oldText, newText}]} (older: oldText,
// newText), grep {pattern, path}, find {pattern, path}, ls {path}.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Pi)(nil)

type Pi struct {
	Cwd string

	blocks []*pb.AgentBlock
	byID   map[string]int
	byCall map[string]string // toolCallId -> block id
	files  map[string]bool
	images map[string]Image
	turn   string

	model  string
	tokens int64

	// The tree: every entry read (its parent and line), and the leaf.
	entries map[string]piStored
	leaf    string
	rewound bool
}

type piStored struct {
	parent string
	line   []byte
}

func NewPi(cwd string) *Pi {
	p := &Pi{Cwd: cwd, entries: map[string]piStored{}}
	p.reset()
	return p
}

func (p *Pi) reset() {
	p.blocks, p.byID, p.byCall, p.turn = nil, map[string]int{}, map[string]string{}, ""
	if p.files == nil {
		p.files, p.images = map[string]bool{}, map[string]Image{}
	}
	p.model, p.tokens = "", 0
}

func (p *Pi) Blocks() []*pb.AgentBlock        { return p.blocks }
func (p *Pi) Image(ref string) (Image, bool)  { i, ok := p.images[ref]; return i, ok }
func (p *Pi) Referenced(path string) bool     { return p.files[filepath.Clean(path)] }
func (p *Pi) Context() (string, int64, int64) { return p.model, p.tokens, 0 }
func (p *Pi) Rewound() bool                   { r := p.rewound; p.rewound = false; return r }
func (p *Pi) Pending() *pb.AgentBlock         { return piPendingAfterUser(p.blocks) }
func (p *Pi) blockByCall(id string) (int, bool) {
	i, ok := p.byID[p.byCall[id]]
	return i, ok && p.byCall[id] != ""
}

// piPendingAfterUser is the newest pending call since the last user block.
func piPendingAfterUser(blocks []*pb.AgentBlock) *pb.AgentBlock {
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

type piEntry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  *string         `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Cwd       string          `json:"cwd"`
	Message   *piMessage      `json:"message"`
	ModelID   string          `json:"modelId"`
	Summary   string          `json:"summary"`
	Content   json.RawMessage `json:"content"`
	Display   bool            `json:"display"`
	Custom    string          `json:"customType"`
}

type piMessage struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	Model        string          `json:"model"`
	Usage        *piUsage        `json:"usage"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	ToolCallID   string          `json:"toolCallId"`
	ToolName     string          `json:"toolName"`
	IsError      bool            `json:"isError"`
	Details      *struct {
		Patch string `json:"patch"`
	} `json:"details"`
	Timestamp int64 `json:"timestamp"`
	// bashExecution
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode *int   `json:"exitCode"`
	Canceled bool   `json:"cancelled"`
	// custom
	CustomType string `json:"customType"`
	Display    bool   `json:"display"`
}

type piUsage struct {
	Input, Output, CacheRead, CacheWrite, TotalTokens int64
}

func (u *piUsage) UnmarshalJSON(b []byte) error {
	var v struct {
		Input       int64 `json:"input"`
		Output      int64 `json:"output"`
		CacheRead   int64 `json:"cacheRead"`
		CacheWrite  int64 `json:"cacheWrite"`
		TotalTokens int64 `json:"totalTokens"`
	}
	err := json.Unmarshal(b, &v)
	*u = piUsage{v.Input, v.Output, v.CacheRead, v.CacheWrite, v.TotalTokens}
	return err
}

type piPart struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Thinking  string         `json:"thinking"`
	Redacted  bool           `json:"redacted"`
	Data      string         `json:"data"`
	MimeType  string         `json:"mimeType"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// piParts reads content that's a string or a list of parts.
func piParts(raw json.RawMessage) []piPart {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []piPart{{Type: "text", Text: s}}
	}
	var parts []piPart
	_ = json.Unmarshal(raw, &parts)
	return parts
}

// PiText is a message's text parts, joined (content a string or parts).
func PiText(raw json.RawMessage) string {
	var out []string
	for _, pt := range piParts(raw) {
		if pt.Type == "text" && pt.Text != "" {
			out = append(out, pt.Text)
		}
	}
	return strings.Join(out, "\n")
}

func (p *Pi) Feed(line []byte) []*pb.AgentBlock {
	var e piEntry
	if json.Unmarshal(line, &e) != nil {
		return nil
	}
	if e.Type == "session" {
		if p.Cwd == "" {
			p.Cwd = e.Cwd
		}
		return nil
	}
	if e.ID == "" {
		// A version 1 session: no tree, entries in order.
		return p.apply(e)
	}
	parent := ""
	if e.ParentID != nil {
		parent = *e.ParentID
	}
	p.entries[e.ID] = piStored{parent: parent, line: append([]byte(nil), line...)}
	var out []*pb.AgentBlock
	if p.leaf != "" && parent != p.leaf {
		// Another branch: show the path to this entry instead.
		p.rebuild(parent)
		p.rewound = true
	}
	p.leaf = e.ID
	out = append(out, p.apply(e)...)
	return out
}

// rebuild replays the entries from the root to id (inclusive).
func (p *Pi) rebuild(id string) {
	var chain []string
	seen := map[string]bool{}
	for id != "" && !seen[id] {
		s, ok := p.entries[id]
		if !ok {
			break
		}
		seen[id] = true
		chain = append(chain, id)
		id = s.parent
	}
	p.reset()
	for i := len(chain) - 1; i >= 0; i-- {
		var e piEntry
		if json.Unmarshal(p.entries[chain[i]].line, &e) == nil {
			p.apply(e)
		}
	}
}

func (p *Pi) apply(e piEntry) []*pb.AgentBlock {
	ms := int64(0)
	if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		ms = t.UnixMilli()
	}
	id := "pi" + e.ID
	switch e.Type {
	case "message":
		if e.Message == nil {
			return nil
		}
		if e.Message.Timestamp > 0 {
			ms = e.Message.Timestamp
		}
		return p.message(id, ms, e.Message)
	case "model_change":
		if e.ModelID != "" {
			p.model = e.ModelID
		}
	case "compaction":
		return p.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted"})
	case "branch_summary":
		text := "Summarized the branch left behind"
		if s := strings.TrimSpace(e.Summary); s != "" {
			text += ": " + firstLine(s)
		}
		return p.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: text})
	case "custom_message":
		if e.Display {
			return p.custom(id, ms, e.Custom, e.Content)
		}
	}
	return nil
}

func (p *Pi) message(id string, ms int64, m *piMessage) []*pb.AgentBlock {
	switch m.Role {
	case "user":
		blk := &pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, TurnId: id}
		var texts []string
		for i, pt := range piParts(m.Content) {
			switch pt.Type {
			case "text":
				texts = append(texts, pt.Text)
			case "image":
				if img := p.keepImage(fmt.Sprintf("%s.%d", id, i), pt); img != nil {
					blk.Images = append(blk.Images, img)
				}
			}
		}
		blk.Text = strings.Join(texts, "\n")
		if strings.TrimSpace(blk.Text) == "" && len(blk.Images) == 0 {
			return nil
		}
		p.turn = id
		p.mentions(blk.Text)
		return p.add(blk)
	case "assistant":
		if m.Model != "" {
			p.model = m.Model
		}
		if u := m.Usage; u != nil && m.StopReason != "aborted" && m.StopReason != "error" {
			// compaction.ts calculateContextTokens.
			if t := cmpInt(u.TotalTokens, u.Input+u.Output+u.CacheRead+u.CacheWrite); t > 0 {
				p.tokens = t
			}
		}
		var out []*pb.AgentBlock
		for i, pt := range piParts(m.Content) {
			bid := fmt.Sprintf("%s.%d", id, i)
			switch pt.Type {
			case "text":
				if strings.TrimSpace(pt.Text) != "" {
					p.mentions(pt.Text)
					out = append(out, p.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: ms, Text: pt.Text})...)
				}
			case "thinking":
				if s := strings.TrimSpace(pt.Thinking); s != "" && !pt.Redacted {
					out = append(out, p.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: s})...)
				}
			case "toolCall":
				blk := p.tool(pt.Name, pt.Arguments)
				blk.Id, blk.TimeUnixMs, blk.Pending = bid, ms, true
				if pt.ID != "" {
					p.byCall[pt.ID] = bid
				}
				out = append(out, p.add(blk)...)
			}
		}
		switch m.StopReason {
		case "aborted":
			out = append(out, p.add(&pb.AgentBlock{Id: id + ".end", Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Interrupted"})...)
		case "error":
			text := "Error"
			if s := strings.TrimSpace(m.ErrorMessage); s != "" {
				text += ": " + firstLine(s)
			}
			out = append(out, p.add(&pb.AgentBlock{Id: id + ".end", Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: text})...)
		}
		return out
	case "toolResult":
		i, ok := p.blockByCall(m.ToolCallID)
		if !ok {
			return nil
		}
		blk := cloneBlock(p.blocks[i])
		blk.Pending, blk.Error = false, m.IsError
		var texts []string
		for j, pt := range piParts(m.Content) {
			switch pt.Type {
			case "text":
				texts = append(texts, pt.Text)
			case "image":
				if img := p.keepImage(fmt.Sprintf("%s.%d", id, j), pt); img != nil {
					blk.Images = append(blk.Images, img)
				}
			}
		}
		// The edit tool's result has the unified diff it made, with context.
		if blk.Tool == "edit" && !m.IsError && m.Details != nil && m.Details.Patch != "" {
			blk.Diff, blk.Added, blk.Removed = patchHunks(m.Details.Patch)
		}
		// An edit's or a write's own confirmation adds nothing to its diff.
		if (blk.Tool != "edit" && blk.Tool != "write") || m.IsError {
			blk.Output = clip(strings.TrimRight(stripANSI(strings.Join(texts, "\n")), "\n"), maxOutput)
		}
		return p.add(blk)
	case "bashExecution":
		blk := &pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TOOL, TimeUnixMs: ms, Tool: "bash", Input: m.Command,
			Title: "You ran `" + firstLine(m.Command) + "`", Output: clip(strings.TrimRight(stripANSI(m.Output), "\n"), maxOutput)}
		blk.Error = m.Canceled || (m.ExitCode != nil && *m.ExitCode != 0)
		p.mentions(m.Command)
		return p.add(blk)
	case "custom", "hookMessage":
		if m.Display {
			return p.custom(id, ms, m.CustomType, m.Content)
		}
	}
	return nil
}

// custom is an extension's message, shown as Pi shows it.
func (p *Pi) custom(id string, ms int64, kind string, content json.RawMessage) []*pb.AgentBlock {
	text := strings.TrimSpace(PiText(content))
	if text == "" {
		return nil
	}
	if kind != "" {
		text = kind + ": " + text
	}
	return p.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: firstLine(text)})
}

// tool is a call to one of Pi's tools, by its arguments.
func (p *Pi) tool(name string, in map[string]any) *pb.AgentBlock {
	str := func(k string) string { s, _ := in[k].(string); return s }
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	switch name {
	case "bash":
		cmd := str("command")
		p.mentions(cmd)
		b.Input, b.Title = cmd, "Ran `"+firstLine(cmd)+"`"
	case "read":
		path := p.path(str("path"))
		b.Input, b.Title = path, "Read "+path
	case "write":
		path, content := p.path(str("path")), str("content")
		b.Input, b.Title = path, "Wrote "+path
		b.Diff, b.Added = clip(addedDiff(content), maxDiff), int32(lines(content))
	case "edit":
		path := p.path(str("path"))
		b.Input, b.Title = path, "Edited "+path
		var diff strings.Builder
		edits := asAny(in["edits"])
		if len(edits) == 0 && in["oldText"] != nil {
			edits = []any{map[string]any{"oldText": in["oldText"], "newText": in["newText"]}}
		}
		for _, x := range edits {
			m, _ := x.(map[string]any)
			old, _ := m["oldText"].(string)
			nw, _ := m["newText"].(string)
			diff.WriteString(simpleDiff(old, nw))
			b.Added += int32(lines(nw))
			b.Removed += int32(lines(old))
		}
		b.Diff = clip(diff.String(), maxDiff)
	case "grep":
		b.Input, b.Title = str("pattern"), "Searched for `"+str("pattern")+"`"
		if d := str("path"); d != "" {
			b.Title += " in " + p.path(d)
		}
	case "find":
		b.Input, b.Title = str("pattern"), "Found files matching `"+str("pattern")+"`"
		if d := str("path"); d != "" {
			b.Title += " in " + p.path(d)
		}
	case "ls":
		d := p.path(str("path"))
		b.Input, b.Title = d, "Listed "+cmpOr(d, "files")
	case "codemode":
		// A JavaScript script calling its other tools (and MCP servers').
		b.Input, b.Title = clip(str("code"), maxOutput), "Ran a script"
	default:
		b.Title = "Used " + name
		if server, tool, ok := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__"); ok && strings.HasPrefix(name, "mcp__") {
			b.Title = "Used " + tool + " (" + server + ")"
		}
		if len(in) > 0 {
			raw, _ := json.Marshal(in)
			b.Input = clip(string(raw), maxOutput)
		}
	}
	return b
}

func (p *Pi) keepImage(ref string, pt piPart) *pb.AgentImage {
	data, err := base64.StdEncoding.DecodeString(pt.Data)
	if err != nil || len(data) == 0 {
		return nil
	}
	ref = "img:" + ref
	p.images[ref] = Image{MediaType: pt.MimeType, Data: data}
	return &pb.AgentImage{Ref: ref, MediaType: pt.MimeType}
}

func (p *Pi) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = p.turn
	}
	if i, ok := p.byID[b.Id]; ok {
		p.blocks[i] = b
	} else {
		p.byID[b.Id] = len(p.blocks)
		p.blocks = append(p.blocks, b)
	}
	return []*pb.AgentBlock{b}
}

func (p *Pi) path(s string) string {
	if s == "" {
		return ""
	}
	abs := s
	if !filepath.IsAbs(abs) && p.Cwd != "" {
		abs = filepath.Join(p.Cwd, abs)
	}
	p.files[filepath.Clean(abs)] = true
	if p.Cwd != "" {
		if r, err := filepath.Rel(p.Cwd, abs); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return s
}

func (p *Pi) mentions(s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		p.files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}

// patchHunks is a unified diff's changed and context lines, without its
// file and hunk headers, and the lines it adds and removes.
func patchHunks(patch string) (string, int32, int32) {
	var b strings.Builder
	var added, removed int32
	hunk := false
	for _, l := range strings.Split(strings.TrimRight(patch, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			hunk = true
		case !hunk: // the file headers
		case strings.HasPrefix(l, "+"):
			added++
			b.WriteString(l + "\n")
		case strings.HasPrefix(l, "-"):
			removed++
			b.WriteString(l + "\n")
		case strings.HasPrefix(l, " "):
			b.WriteString(l + "\n")
		}
	}
	return clip(b.String(), maxDiff), added, removed
}

func cmpInt(a, b int64) int64 {
	if a != 0 {
		return a
	}
	return b
}
