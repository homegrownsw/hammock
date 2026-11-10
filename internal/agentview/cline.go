package agentview

// Cline CLI's sessions as the agent view's blocks. Cline 3.x (github.com/
// cline/cline, apps/cli on the SDK in sdk/packages; read at CLI 3.0.74,
// 94eecbb, 2026-11-06) keeps each session in a folder of its own,
// ~/.cline/data/sessions/<id>/: <id>.json, its manifest (session_id, cwd,
// workspace_root, started_at, model, metadata.title…), and
// <id>.messages.json, rewritten whole after each of its replies:
//
//	{"version": 1, "updated_at": …, "agent": "lead", "sessionId": …,
//	 "system_prompt": …, "messages": [MessageWithMetadata…]}
//
// agents.Cline's Open turns the two into lines, which this reads: a line
// with the manifest's fields under "$hammockSession", then each message as
// it is. A message (sdk/packages/shared/src/llms/messages.ts) is {id,
// role: "user"|"assistant", content: a string or blocks, ts (ms), metadata,
// modelInfo {id, provider}, metrics {inputTokens, outputTokens, …}}. Its
// blocks: text {text}, thinking {thinking}, redacted_thinking, image {data,
// mediaType}, file {path, content} (a file you @-mentioned), tool_use {id,
// name, input}, and, in user messages, tool_result {tool_use_id, name,
// content, is_error}, whose content is a string (often a JSON
// ToolOperationResult {query, result, error, success}) or a list of those
// or of text and image blocks. What you type is stored wrapped,
// <user_input mode="act">…</user_input>, sometimes after a <mode_notice>.
// metadata.displayRole "system" marks context Cline injected, "status" and
// "error" its notices; metadata.kind its own synthetic messages
// (compaction summaries, reminders).
//
// Its tools (sdk/packages/core/src/extensions/tools/schemas.ts):
// read_files {files: [{path, start_line, end_line}]}, search_codebase
// {queries}, run_commands {commands: [string | {command, args}]},
// fetch_web_content {requests: [{url, prompt}]}, editor {path, old_text,
// new_text, insert_line}, apply_patch {input} (Codex's patch format), skills
// {skill, args}, ask_question {question, options}, submit_and_exit
// {summary, verified}, spawn_agent {systemPrompt, task} and team_* tools.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Cline)(nil)

type Cline struct {
	cx *Codex // paths, mentions, patches and the blocks, as Codex's

	images map[string]Image
	n      int // messages read, for those without an id

	model  string
	tokens int64
}

func NewCline(cwd string) *Cline {
	return &Cline{cx: NewCodex(cwd), images: map[string]Image{}}
}

func (c *Cline) Blocks() []*pb.AgentBlock        { return c.cx.blocks }
func (c *Cline) Image(ref string) (Image, bool)  { i, ok := c.images[ref]; return i, ok }
func (c *Cline) Referenced(path string) bool     { return c.cx.Referenced(path) }
func (c *Cline) Context() (string, int64, int64) { return c.model, c.tokens, 0 }
func (c *Cline) Rewound() bool                   { return false }

// Pending is the newest tool call without its result since your last
// message: what a permission prompt on its screen is about.
func (c *Cline) Pending() *pb.AgentBlock { return pendingAfterUser(c.cx.blocks) }

// ClineSession is the line agents.Cline's Open puts before a session's
// messages: what its manifest says.
type ClineSession struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd,omitempty"`
	Title   string `json:"title,omitempty"`
	Started string `json:"started,omitempty"` // RFC 3339
	Model   string `json:"model,omitempty"`
}

// ClineMessage is one of a session's messages.
type ClineMessage struct {
	Session *ClineSession `json:"$hammockSession"`

	ID       string          `json:"id"`
	Role     string          `json:"role"`
	Content  json.RawMessage `json:"content"`
	Ts       int64           `json:"ts"`
	Metadata struct {
		DisplayRole string `json:"displayRole"`
		DisplayOnly bool   `json:"displayOnly"`
		Kind        string `json:"kind"`
	} `json:"metadata"`
	ModelInfo *struct {
		ID string `json:"id"`
	} `json:"modelInfo"`
	Metrics *struct {
		InputTokens  int64 `json:"inputTokens"`
		OutputTokens int64 `json:"outputTokens"`
	} `json:"metrics"`
}

// ClineBlock is one of a message's content blocks.
type ClineBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Data      string          `json:"data"`
	MediaType string          `json:"mediaType"`
	Path      string          `json:"path"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ParseClineLine reads one line, with its message's blocks (a string
// content as one text block).
func ParseClineLine(line []byte) (ClineMessage, []ClineBlock, bool) {
	var m ClineMessage
	if json.Unmarshal(line, &m) != nil {
		return m, nil, false
	}
	var blocks []ClineBlock
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		blocks = []ClineBlock{{Type: "text", Text: s}}
	} else {
		_ = json.Unmarshal(m.Content, &blocks)
	}
	return m, blocks, true
}

// clineSynthetic are the metadata kinds of user messages Cline writes
// itself (sdk/packages/core/src/session/user-run-messages.ts).
var clineSynthetic = map[string]bool{
	"auto_compaction": true, "compaction_budget_emergency": true, "completion_reminder": true,
	"loop_detection_notice": true, "manual_compaction": true, "mistake_stop_notice": true, "recovery_notice": true,
}

// Shown: you see the message in Cline as yours or its own (not context it
// injected, nor one of its notices).
func (m ClineMessage) Shown() bool {
	switch strings.ToLower(m.Metadata.DisplayRole) {
	case "system", "status", "error":
		return false
	}
	return !clineSynthetic[m.Metadata.Kind] && !m.Compaction()
}

// Compaction: the message is a summary of the conversation before it.
func (m ClineMessage) Compaction() bool {
	return m.Metadata.Kind == "compaction" || m.Metadata.Kind == "compaction_summary"
}

// ClineUserText is what you wrote in a user message, as Cline shows it
// (formatDisplayUserInput, sdk/packages/shared/src/prompt/format.ts):
// without its <user_input> wrapper and <mode_notice>s, a /command as one.
func ClineUserText(blocks []ClineBlock) string {
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" {
			if s := ClineDisplayText(b.Text); s != "" {
				texts = append(texts, s)
			}
		}
	}
	s := strings.TrimSpace(strings.Join(texts, "\n"))
	// Its own continuation prompts.
	if strings.HasPrefix(s, "[TASK RESUMPTION]") || s == "The user approved switching to act mode. Continue with the approved plan now." {
		return ""
	}
	return s
}

// ClineDisplayText is one text as Cline shows what you typed.
func ClineDisplayText(s string) string {
	slash := ""
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "<user_command") && strings.HasSuffix(t, "</user_command>") {
		if _, rest, ok := strings.Cut(t, `slash="`); ok {
			slash, _, _ = strings.Cut(rest, `"`)
		}
	}
	for _, tag := range []string{"user_input", "user_command"} {
		s = clineUntag(s, tag)
	}
	for {
		i := strings.Index(s, "<mode_notice>")
		j := strings.Index(s, "</mode_notice>")
		if i < 0 || j < i {
			break
		}
		s = s[:i] + s[j+len("</mode_notice>"):]
	}
	s = strings.TrimSpace(s)
	if slash != "" {
		if strings.EqualFold(slash, "team") {
			s = strings.TrimSpace(strings.TrimPrefix(s, "spawn a team of agents for the following task:"))
		}
		return strings.TrimSpace("/" + slash + " " + s)
	}
	return s
}

// clineUntag drops <tag …> and </tag>, keeping what's between.
func clineUntag(s, tag string) string {
	for {
		i := strings.Index(s, "<"+tag)
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			break
		}
		s = s[:i] + s[i+j+1:]
	}
	return strings.ReplaceAll(s, "</"+tag+">", "")
}

func (c *Cline) Feed(line []byte) []*pb.AgentBlock {
	m, blocks, ok := ParseClineLine(line)
	if !ok {
		return nil
	}
	if s := m.Session; s != nil {
		if c.cx.Cwd == "" {
			c.cx.Cwd = s.Cwd
		}
		if c.model == "" {
			c.model = s.Model
		}
		return nil
	}
	if m.Role != "user" && m.Role != "assistant" {
		return nil
	}
	c.n++
	id := "c" + m.ID
	if m.ID == "" {
		id = fmt.Sprintf("c#%d", c.n)
	}
	if m.Role == "assistant" {
		if m.ModelInfo != nil && m.ModelInfo.ID != "" {
			c.model = m.ModelInfo.ID
		}
		if m.Metrics != nil && m.Metrics.InputTokens > 0 {
			c.tokens = m.Metrics.InputTokens + m.Metrics.OutputTokens
		}
	}
	var out []*pb.AgentBlock
	// Tool results go to their calls, whatever else the message is.
	for _, b := range blocks {
		if b.Type == "tool_result" {
			out = append(out, c.result(id, b)...)
		}
	}
	if m.Compaction() {
		return append(out, c.cx.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: m.Ts, Text: "Conversation compacted"})...)
	}
	if !m.Shown() || m.Metadata.DisplayOnly {
		return out
	}
	if m.Role == "user" {
		blk := &pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: m.Ts, TurnId: id, Text: ClineUserText(blocks)}
		for i, b := range blocks {
			switch b.Type {
			case "image":
				if img := c.keepImage(fmt.Sprintf("%s.%d", id, i), b.Data, b.MediaType); img != nil {
					blk.Images = append(blk.Images, img)
				}
			case "file":
				c.cx.path(b.Path)
			}
		}
		if blk.Text == "" && len(blk.Images) == 0 {
			return out
		}
		c.cx.turn = id
		c.cx.mentions(blk.Text)
		return append(out, c.cx.add(blk)...)
	}
	for i, b := range blocks {
		bid := fmt.Sprintf("%s.%d", id, i)
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				c.cx.mentions(b.Text)
				out = append(out, c.cx.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: m.Ts, Text: b.Text})...)
			}
		case "thinking":
			if s := strings.TrimSpace(b.Thinking); s != "" {
				out = append(out, c.cx.add(&pb.AgentBlock{Id: bid, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: m.Ts, Text: s})...)
			}
		case "tool_use":
			blk := c.tool(b.Name, clineInput(b.Name, b.Input))
			blk.Id, blk.TimeUnixMs, blk.Pending = bid, m.Ts, true
			if b.ID != "" {
				c.cx.byCall[b.ID] = bid
			}
			out = append(out, c.cx.add(blk)...)
		}
	}
	return out
}

// result puts a tool_result's output on its call's block.
func (c *Cline) result(msgID string, r ClineBlock) []*pb.AgentBlock {
	bid, ok := c.cx.byCall[r.ToolUseID]
	if !ok {
		return nil
	}
	i, ok := c.cx.byID[bid]
	if !ok {
		return nil
	}
	blk := cloneBlock(c.cx.blocks[i])
	blk.Pending = false
	text, failed, imgs := c.output(r.Content, msgID+"."+bid)
	blk.Error = r.IsError || failed
	blk.Images = append(blk.Images, imgs...)
	// An edit's own confirmation adds nothing to its diff.
	if blk.Diff == "" || blk.Error {
		blk.Output = clip(strings.TrimRight(stripANSI(text), "\n"), maxOutput)
	}
	return c.cx.add(blk)
}

// output is a tool result's text, whether it failed, and its images: a
// string (a ToolOperationResult's JSON, or plain text), or a list of
// ToolOperationResults or of text and image blocks.
func (c *Cline) output(raw json.RawMessage, ref string) (string, bool, []*pb.AgentImage) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		t := strings.TrimSpace(s)
		if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
			return s, false, nil
		}
		raw = json.RawMessage(t)
	}
	type item struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Data      string          `json:"data"`
		MediaType string          `json:"mediaType"`
		Query     *string         `json:"query"`
		Result    json.RawMessage `json:"result"`
		Error     string          `json:"error"`
		Success   *bool           `json:"success"`
	}
	var items []item
	var one item
	if json.Unmarshal(raw, &items) != nil {
		if json.Unmarshal(raw, &one) != nil {
			return s, false, nil
		}
		items = []item{one}
	}
	var texts []string
	var imgs []*pb.AgentImage
	failed, known := false, false
	for i, it := range items {
		switch {
		case it.Query != nil || it.Success != nil || it.Error != "":
			known = true
			if it.Error != "" || (it.Success != nil && !*it.Success) {
				failed = true
			}
			var r string
			if json.Unmarshal(it.Result, &r) != nil && len(it.Result) > 0 && string(it.Result) != "null" {
				r = string(it.Result)
			}
			if t := strings.TrimSpace(r); t != "" {
				texts = append(texts, r)
			}
			if it.Error != "" {
				texts = append(texts, it.Error)
			}
		case it.Type == "text":
			known = true
			texts = append(texts, it.Text)
		case it.Type == "image":
			known = true
			if img := c.keepImage(fmt.Sprintf("%s.%d", ref, i), it.Data, it.MediaType); img != nil {
				imgs = append(imgs, img)
			}
		}
	}
	if !known {
		return string(raw), false, nil
	}
	return strings.Join(texts, "\n"), failed, imgs
}

// tool is a call to one of Cline's tools, by its input.
func (c *Cline) tool(name string, in map[string]any) *pb.AgentBlock {
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := in[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	switch name {
	case "read_files":
		var paths []string
		for _, p := range clinePaths(in) {
			paths = append(paths, c.cx.path(p))
		}
		b.Input = strings.Join(paths, "\n")
		switch len(paths) {
		case 0:
			b.Title = "Read files"
		case 1:
			b.Title = "Read " + paths[0]
		default:
			b.Title = fmt.Sprintf("Read %s and %s", paths[0], plural(len(paths)-1, "other file"))
		}
	case "run_commands":
		cmds := clineCommands(in)
		for _, cmd := range cmds {
			c.cx.mentions(cmd)
		}
		b.Input = strings.Join(cmds, "\n")
		switch len(cmds) {
		case 0:
			b.Title = "Ran commands"
		case 1:
			b.Title = "Ran `" + firstLine(cmds[0]) + "`"
		default:
			b.Title = "Ran `" + firstLine(cmds[0]) + "` and " + fmt.Sprint(len(cmds)-1, " more")
		}
	case "search_codebase":
		qs := clineStrings(in["queries"])
		b.Input = strings.Join(qs, "\n")
		if len(qs) > 0 {
			b.Title = "Searched for `" + firstLine(qs[0]) + "`"
			if len(qs) > 1 {
				b.Title += " and " + fmt.Sprint(len(qs)-1, " more")
			}
		} else {
			b.Title = "Searched the code"
		}
	case "fetch_web_content":
		var urls []string
		for _, r := range asAny(in["requests"]) {
			if m, ok := r.(map[string]any); ok {
				if u, _ := m["url"].(string); u != "" {
					urls = append(urls, u)
				}
			}
		}
		b.Input = strings.Join(urls, "\n")
		switch len(urls) {
		case 0:
			b.Title = "Fetched a page"
		case 1:
			b.Title = "Fetched " + urls[0]
		default:
			b.Title = "Fetched " + urls[0] + " and " + fmt.Sprint(len(urls)-1, " more")
		}
	case "editor":
		p := c.cx.path(str("path"))
		nw := str("new_text")
		b.Input = p
		_, insert := in["insert_line"].(float64)
		switch old := str("old_text"); {
		case old != "":
			b.Title = "Edited " + p
			b.Diff, b.Added, b.Removed = clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		case insert:
			b.Title = "Edited " + p
			b.Diff, b.Added = clip(addedDiff(nw), maxDiff), int32(lines(nw))
		default:
			// A file that isn't there yet, made with new_text.
			b.Title = "Wrote " + p
			b.Diff, b.Added = clip(addedDiff(nw), maxDiff), int32(lines(nw))
		}
	case "apply_patch":
		b = c.cx.patch(str("input"))
	case "skills":
		b.Input, b.Title = str("args"), "Used the "+cmpOr(str("skill"), "a")+" skill"
	case "ask_question":
		q := str("question")
		b.Input, b.Title = q, "Asked "+quoteTitle(q)
	case "submit_and_exit":
		b.Input, b.Title = str("summary"), "Finished the task"
	case "spawn_agent":
		task := str("task")
		b.Input, b.Title = task, "Started an agent"
		if task != "" {
			b.Title += ": " + firstLine(task)
		}
	default:
		b.Title = "Used " + name
		if len(in) > 0 {
			raw, _ := json.Marshal(in)
			b.Input = clip(string(raw), maxOutput)
		}
	}
	return b
}

// clineInput is a tool call's input as an object: its tools also take a
// bare string or list in place of their usual field (the *UnionSchemas).
func clineInput(name string, raw json.RawMessage) map[string]any {
	var v any
	_ = json.Unmarshal(raw, &v)
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if v == nil {
		return nil
	}
	key := map[string]string{"read_files": "files", "run_commands": "commands", "search_codebase": "queries", "apply_patch": "input"}[name]
	if key == "" {
		key = "input"
	}
	return map[string]any{key: v}
}

// clinePaths are a read_files call's paths, in any of the shapes its tool
// accepts (ReadFilesInputUnionSchema).
func clinePaths(in map[string]any) []string {
	var out []string
	add := func(v any) {
		switch x := v.(type) {
		case string:
			if x != "" {
				out = append(out, x)
			}
		case map[string]any:
			for _, k := range []string{"path", "file_path", "filePath"} {
				if s, _ := x[k].(string); s != "" {
					out = append(out, s)
					return
				}
			}
		}
	}
	for _, k := range []string{"files", "file_paths", "paths"} {
		switch v := in[k].(type) {
		case []any:
			for _, e := range v {
				add(e)
			}
		default:
			add(v)
		}
	}
	if len(out) == 0 {
		add(in)
	}
	return out
}

// clineCommands are a run_commands call's commands: strings, or
// {command, args} run without a shell.
func clineCommands(in map[string]any) []string {
	var out []string
	add := func(v any) {
		switch x := v.(type) {
		case string:
			if x != "" {
				out = append(out, x)
			}
		case map[string]any:
			cmd, _ := x["command"].(string)
			if cmd == "" {
				cmd, _ = x["cmd"].(string)
			}
			if cmd != "" {
				out = append(out, strings.Join(append([]string{cmd}, asStrings(x["args"])...), " "))
			}
		}
	}
	switch v := in["commands"].(type) {
	case []any:
		for _, e := range v {
			add(e)
		}
	case nil:
		add(in)
	default:
		add(v)
	}
	return out
}

func clineStrings(v any) []string {
	if s, ok := v.(string); ok && s != "" {
		return []string{s}
	}
	return asStrings(v)
}

func (c *Cline) keepImage(ref, data, mime string) *pb.AgentImage {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(b) == 0 {
		return nil
	}
	ref = "img:" + ref
	c.images[ref] = Image{MediaType: mime, Data: b}
	return &pb.AgentImage{Ref: ref, MediaType: mime}
}
