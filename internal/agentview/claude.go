// Package agentview turns an agent's session file into the blocks of the
// agent view (a conversation, as in Claude's apps), and reads the prompt the
// agent is showing from its screen. See docs/build-plan.md (Phase 7) and
// docs/spikes/agent-view-keys.md.
package agentview

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/thread"
)

const (
	maxOutput = 16 << 10 // tool output kept per block
	maxDiff   = 32 << 10
)

// Image is an image the session contains, served to clients by ref.
type Image struct {
	MediaType string
	Data      []byte
	// Original is the file Claude Code pasted it from: a PNG, transparency
	// and all, where Data is the JPEG it sent Claude (transparency turned
	// black). Serve it instead while it's still there, and no newer than
	// Sent: Claude Code reuses paste numbers, overwriting the file.
	Original string
	Sent     time.Time
}

// Claude parses a Claude Code session file (JSONL) incrementally: Feed it
// lines as the file grows; it returns the blocks each line added or changed.
type Claude struct {
	Cwd string
	// Sidechain reads a subagent's own transcript, whose lines are all
	// marked sidechain (the main transcript's are skipped).
	Sidechain bool

	blocks  []*pb.AgentBlock
	byID    map[string]int
	byTool  map[string]string // tool_use id -> block id
	images  map[string]Image
	pasted  map[int]string  // a pasted image's number ("[Image #2]") -> its ref
	files   map[string]bool // paths the session referenced
	turn    string
	agentOf map[string]string // tool_use id -> subagent id
	// Where each of your messages branched from (its parent line): one sent
	// from the same point (after interrupting before Claude replied, or a
	// rewind) replaces that branch, as Claude Code shows it.
	branchAt map[string]int // parent uuid -> index of the message's block
	rewound  bool
	// Background shells, and each one's place by its task id.
	shells  []*Shell
	shellAt map[string]int

	// The model of the latest reply, and the context it took (its input,
	// cached or not); the most it has taken, to tell a 1M window.
	model         string
	contextTokens int64
	mostContext   int64

	// Its task list, and the pull requests and artifacts it made (Extras).
	tasks taskList
	links linkList
}

// Tasks is the session's task list (Extras).
func (c *Claude) Tasks() *pb.AgentTaskList { return c.tasks.list() }

// Links is the session's pull requests and artifacts (Extras).
func (c *Claude) Links() []*pb.AgentLink { return c.links.clone() }

// Context is the session's model, the tokens of context its latest reply
// took, and the context window. The transcript doesn't record the window
// (its model is plain "claude-opus-5-5", with no "[1m]"), so it's 1M for
// the models Claude Code gives a 1M window by default, or when the session
// has outgrown 200k; else 200k.
func (c *Claude) Context() (model string, tokens, window int64) {
	window = 200_000
	if oneMillionWindow(c.model) || c.mostContext > window {
		window = 1_000_000
	}
	return c.model, c.contextTokens, window
}

// oneMillionWindow reports whether Claude Code runs model with a 1M context
// window by default: Opus, Sonnet and Fable from Claude 5 on (not Haiku), or
// a model asked for with "[1m]".
func oneMillionWindow(model string) bool {
	if strings.Contains(model, "[1m]") {
		return true
	}
	for _, family := range []string{"claude-opus-", "claude-sonnet-", "claude-fable-"} {
		if rest, ok := strings.CutPrefix(model, family); ok {
			major, _, _ := strings.Cut(rest, "-")
			n, err := strconv.Atoi(major)
			return err == nil && n >= 5
		}
	}
	return false
}

func NewClaude(cwd string) *Claude {
	return &Claude{Cwd: cwd, byID: map[string]int{}, byTool: map[string]string{}, images: map[string]Image{}, pasted: map[int]string{},
		files: map[string]bool{}, agentOf: map[string]string{}, branchAt: map[string]int{}, shellAt: map[string]int{}}
}

// Rewound reports (once) whether a branch was dropped since the last call,
// so what clients show must be replaced.
func (c *Claude) Rewound() bool {
	r := c.rewound
	c.rewound = false
	return r
}

// rewind drops the blocks from index i on: an abandoned branch.
func (c *Claude) rewind(i int) {
	for _, b := range c.blocks[i:] {
		delete(c.byID, b.Id)
	}
	c.blocks = c.blocks[:i]
	for p, j := range c.branchAt {
		if j >= i {
			delete(c.branchAt, p)
		}
	}
	c.rewound = true
}

// Blocks is everything parsed so far, in order.
func (c *Claude) Blocks() []*pb.AgentBlock { return c.blocks }

func (c *Claude) Image(ref string) (Image, bool) { i, ok := c.images[ref]; return i, ok }

// Referenced reports whether the session referenced path (read, wrote,
// edited, searched or mentioned it), which is what may be served to clients.
func (c *Claude) Referenced(path string) bool { return c.files[filepath.Clean(path)] }

// Pending is the newest tool call still waiting for its result.
func (c *Claude) Pending() *pb.AgentBlock {
	for i := len(c.blocks) - 1; i >= 0; i-- {
		b := c.blocks[i]
		if b.Kind == pb.AgentBlock_KIND_USER {
			return nil
		}
		if b.Pending {
			return b
		}
	}
	return nil
}

type ccLine struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype"`
	UUID        string          `json:"uuid"`
	ParentUUID  string          `json:"parentUuid"`
	Timestamp   time.Time       `json:"timestamp"`
	IsMeta      bool            `json:"isMeta"`
	IsCompact   bool            `json:"isCompactSummary"`
	IsSidechain bool            `json:"isSidechain"`
	PasteIDs    []int           `json:"imagePasteIds"`
	SessionID   string          `json:"sessionId"`
	Content     string          `json:"content"` // system lines
	Message     *ccMsg          `json:"message"`
	ToolResult  json.RawMessage `json:"toolUseResult"`
	// A pull request the session opened or pushed to ("pr-link" lines).
	PRNumber   int    `json:"prNumber"`
	PRURL      string `json:"prUrl"`
	PRRepo     string `json:"prRepository"`
	Attachment *struct {
		Type   string `json:"type"`
		Prompt string `json:"prompt"`
	} `json:"attachment"`
}

type ccMsg struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Model   string          `json:"model"`
	Usage   *struct {
		Input         int64 `json:"input_tokens"`
		CacheCreation int64 `json:"cache_creation_input_tokens"`
		CacheRead     int64 `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

type ccBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

var wanted = [][]byte{[]byte(`"type":"user"`), []byte(`"type":"assistant"`), []byte(`"type":"system"`), []byte(`"queued_command"`), []byte(`"type":"pr-link"`)}

// Feed parses one line and returns the blocks it added or changed.
func (c *Claude) Feed(line []byte) []*pb.AgentBlock {
	// Most of a session file is API bookkeeping; skip it before decoding.
	hit := false
	for _, w := range wanted {
		if bytes.Contains(line, w) {
			hit = true
			break
		}
	}
	if !hit {
		return nil
	}
	var l ccLine
	if json.Unmarshal(line, &l) != nil || l.IsSidechain && !c.Sidechain {
		return nil
	}
	ms := l.Timestamp.UnixMilli()
	switch l.Type {
	case "system":
		if l.Subtype == "compact_boundary" {
			return c.add(&pb.AgentBlock{Id: l.UUID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted"})
		}
		return nil
	case "user":
		return c.user(l, ms)
	case "attachment":
		// A message you sent while Claude was working: it reaches Claude as
		// a queued command, not a user message.
		if a := l.Attachment; a != nil && a.Type == "queued_command" {
			return c.userText(l.UUID, a.Prompt, nil, ms)
		}
		return nil
	case "assistant":
		return c.assistant(l, ms)
	case "pr-link":
		if strings.HasPrefix(l.PRURL, "https://") {
			c.links.add(&pb.AgentLink{Kind: pb.AgentLink_KIND_PULL_REQUEST, Url: l.PRURL, Number: int32(l.PRNumber), Repo: l.PRRepo})
		}
		return nil
	}
	return nil
}

func (c *Claude) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.Id == "" {
		b.Id = fmt.Sprintf("b%d", len(c.blocks))
	}
	if b.TurnId == "" {
		b.TurnId = c.turn
	}
	if i, ok := c.byID[b.Id]; ok {
		c.blocks[i] = b
	} else {
		c.byID[b.Id] = len(c.blocks)
		c.blocks = append(c.blocks, b)
	}
	return []*pb.AgentBlock{b}
}

func (c *Claude) user(l ccLine, ms int64) []*pb.AgentBlock {
	if l.Message == nil {
		return nil
	}
	if l.IsMeta {
		c.imageSources(l)
		return nil
	}
	if l.IsCompact {
		return c.compactSummary(l, ms)
	}
	// A typed message is a string; tool results and pasted images are blocks.
	var s string
	if json.Unmarshal(l.Message.Content, &s) == nil {
		return c.typed(l, s, nil, ms)
	}
	var blocks []ccBlock
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return nil
	}
	var out []*pb.AgentBlock
	var text []string
	var imgs []*pb.AgentImage
	for i, b := range blocks {
		switch b.Type {
		case "tool_result":
			out = append(out, c.result(b, l.ToolResult)...)
		case "text":
			text = append(text, b.Text)
		case "image":
			if ref := c.keepImage(fmt.Sprintf("%s:%d", l.UUID, i), b); ref != nil {
				imgs = append(imgs, ref)
			}
		}
	}
	c.notePastes(l, text, imgs)
	if len(text) > 0 || len(imgs) > 0 {
		out = append(out, c.typed(l, strings.Join(text, "\n\n"), imgs, ms)...)
	}
	return out
}

// compactSummary tucks the summary Claude wrote when it compacted the
// conversation under its "Conversation compacted" note, rather than showing
// it as a message you sent.
func (c *Claude) compactSummary(l ccLine, ms int64) []*pb.AgentBlock {
	var text string
	if json.Unmarshal(l.Message.Content, &text) != nil {
		var blocks []ccBlock
		_ = json.Unmarshal(l.Message.Content, &blocks)
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n\n")
	}
	if i, ok := c.byID[l.ParentUUID]; ok && c.blocks[i].Kind == pb.AgentBlock_KIND_NOTE {
		b := proto.Clone(c.blocks[i]).(*pb.AgentBlock)
		b.Output = text
		return c.add(b)
	}
	return c.add(&pb.AgentBlock{Id: l.UUID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted", Output: text})
}

// typed adds a message you typed. One sent from the same point as an
// earlier one replaces that earlier branch.
func (c *Claude) typed(l ccLine, s string, imgs []*pb.AgentImage, ms int64) []*pb.AgentBlock {
	if i, ok := c.branchAt[l.ParentUUID]; ok && l.ParentUUID != "" && i < len(c.blocks) {
		c.rewind(i)
	}
	n := len(c.blocks)
	out := c.userText(l.UUID, s, imgs, ms)
	if l.ParentUUID != "" && len(c.blocks) > n {
		c.branchAt[l.ParentUUID] = n
	}
	return out
}

var (
	commandName = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)
	commandArgs = regexp.MustCompile(`<command-args>([^<]*)</command-args>`)
	localOutput = regexp.MustCompile(`(?s)<local-command-stdout>(.*?)</local-command-stdout>`)
	imageRef    = regexp.MustCompile(`\[Image #\d+\]`)
)

func (c *Claude) userText(id, s string, imgs []*pb.AgentImage, ms int64) []*pb.AgentBlock {
	// Sent from the agent view, it was pasted: shown as typed.
	t := strings.TrimSpace(thread.UnwrapPasted(s))
	switch {
	case strings.HasPrefix(t, "[Request interrupted"):
		return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Interrupted"})
	case strings.Contains(t, "<command-name>"):
		// A slash command you ran: show it as you typed it.
		name := ""
		if m := commandName.FindStringSubmatch(t); m != nil {
			name = m[1]
		}
		if m := commandArgs.FindStringSubmatch(t); m != nil && strings.TrimSpace(m[1]) != "" {
			name += " " + strings.TrimSpace(m[1])
		}
		c.turn = id
		return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, Text: name, TurnId: id})
	case strings.Contains(t, "<local-command-stdout>"):
		out := ""
		if m := localOutput.FindStringSubmatch(t); m != nil {
			out = stripANSI(m[1])
		}
		if strings.TrimSpace(out) == "" {
			return nil
		}
		return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: strings.TrimSpace(out)})
	case strings.HasPrefix(t, "<task-notification>"):
		// A background task (a subagent, a command) reporting back: Claude
		// Code sends it as a user message, but you didn't write it.
		return c.taskNotification(id, t, ms)
	case strings.HasPrefix(t, "<") && strings.Contains(t, "</") && !strings.Contains(t, "\n\n"):
		return nil // system-injected reminders and caveats
	}
	if t == "" && len(imgs) == 0 {
		return nil
	}
	if len(imgs) > 0 {
		t = strings.TrimSpace(imageRef.ReplaceAllString(t, ""))
	}
	c.turn = id
	c.mentions(t)
	return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, Text: t, Images: imgs, TurnId: id})
}

var (
	taskSummary = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	taskStatus  = regexp.MustCompile(`(?s)<status>(.*?)</status>`)
	taskResult  = regexp.MustCompile(`(?s)<result>(.*?)</result>`)
)

// taskNotification: a note saying which task finished, with what it
// reported tucked under it.
func (c *Claude) taskNotification(id, t string, ms int64) []*pb.AgentBlock {
	field := func(re *regexp.Regexp) string {
		if m := re.FindStringSubmatch(t); m != nil {
			return strings.TrimSpace(m[1])
		}
		return ""
	}
	text := field(taskSummary)
	c.endShell(field(taskID), field(taskStatus), text)
	if text == "" {
		text = "Background task " + cmp.Or(field(taskStatus), "finished")
	}
	return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: text, Output: field(taskResult)})
}

func (c *Claude) assistant(l ccLine, ms int64) []*pb.AgentBlock {
	if l.Message == nil {
		return nil
	}
	// Claude Code's own stand-in replies (an error, an interruption) say
	// "<synthetic>": not a model, and no context.
	if m := l.Message.Model; m != "" && m != "<synthetic>" {
		c.model = m
		if u := l.Message.Usage; u != nil {
			if n := u.Input + u.CacheCreation + u.CacheRead; n > 0 {
				c.contextTokens = n
				c.mostContext = max(c.mostContext, n)
			}
		}
	}
	var blocks []ccBlock
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return nil
	}
	var out []*pb.AgentBlock
	for i, b := range blocks {
		id := l.UUID
		if i > 0 {
			id = fmt.Sprintf("%s:%d", l.UUID, i)
		}
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" || strings.TrimSpace(b.Text) == "(no content)" {
				continue
			}
			c.mentions(b.Text)
			out = append(out, c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: ms, Text: b.Text})...)
		case "thinking", "redacted_thinking":
			out = append(out, c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: b.Thinking})...)
		case "tool_use":
			blk := c.toolCall(b)
			blk.Id, blk.TimeUnixMs = id, ms
			c.byTool[b.ID] = id
			out = append(out, c.add(blk)...)
		}
	}
	return out
}

// toolCall describes a call before its result arrives.
func (c *Claude) toolCall(b ccBlock) *pb.AgentBlock {
	var in map[string]any
	_ = json.Unmarshal(b.Input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	blk := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: b.Name, Pending: true}
	path := func(k string) string {
		p := str(k)
		if p != "" {
			c.files[filepath.Clean(p)] = true
		}
		return c.rel(p)
	}
	switch b.Name {
	case "Bash":
		blk.Input = str("command")
		blk.Title = "Ran `" + firstLine(blk.Input) + "`"
		if d := str("description"); d != "" {
			blk.Title = d
		}
	case "Read":
		blk.Input = path("file_path")
		blk.Title = "Read " + blk.Input
	case "Write":
		blk.Input = path("file_path")
		blk.Title = "Wrote " + blk.Input
		content := str("content")
		blk.Diff = clip(addedDiff(content), maxDiff)
		blk.Added = int32(strings.Count(content, "\n") + 1)
	case "Edit", "MultiEdit", "NotebookEdit":
		blk.Input = path("file_path")
		if blk.Input == "" {
			blk.Input = path("notebook_path")
		}
		blk.Title = "Edited " + blk.Input
		if old, nw := str("old_string"), str("new_string"); old != "" || nw != "" {
			blk.Diff = clip(simpleDiff(old, nw), maxDiff)
			blk.Added, blk.Removed = int32(lines(nw)), int32(lines(old))
		}
	case "Grep":
		blk.Input = str("pattern")
		blk.Title = "Searched for `" + blk.Input + "`"
	case "Glob":
		blk.Input = str("pattern")
		blk.Title = "Found files matching `" + blk.Input + "`"
	case "WebFetch":
		blk.Input = str("url")
		blk.Title = "Fetched " + blk.Input
	case "WebSearch":
		blk.Input = str("query")
		blk.Title = "Searched the web for “" + blk.Input + "”"
	case "TodoWrite":
		blk.Title = "Updated the to-do list"
		blk.Input = todos(in["todos"])
		c.tasks.replace(asAny(in["todos"]))
	case "TaskCreate":
		blk.Title = "Added a task"
		blk.Input = str("subject")
		if _, seen := c.byTool[b.ID]; !seen {
			c.tasks.create(b.ID, in)
		}
	case "TaskUpdate":
		blk.Title = "Updated a task"
		if s := str("status"); s != "" {
			blk.Input = "#" + strings.TrimPrefix(fmt.Sprint(in["taskId"]), "#") + " " + strings.ReplaceAll(s, "_", " ")
		}
		c.tasks.update(in)
	case "Task", "Agent":
		blk.Kind = pb.AgentBlock_KIND_SUBAGENT
		blk.Title = str("description")
		blk.Input = str("prompt")
		if t := str("subagent_type"); t != "" {
			blk.Text = t
		}
	case "AskUserQuestion":
		blk.Kind = pb.AgentBlock_KIND_QUESTION
		blk.Questions = Questions(b.Input)
		blk.Title = "Asked you"
	case "ExitPlanMode":
		blk.Kind = pb.AgentBlock_KIND_PLAN
		blk.Text = str("plan")
		blk.Title = "Plan"
	case "Skill":
		blk.Title = "Used the " + str("skill") + " skill"
	case "ToolSearch":
		blk.Title = "Loaded tools"
		blk.Input = str("query")
	case "SendUserFile":
		// Files Claude sent you: shown, not tucked away with tool calls.
		blk.Text = str("caption")
		var names []string
		for _, f := range asStrings(in["files"]) {
			if !filepath.IsAbs(f) && c.Cwd != "" {
				f = filepath.Join(c.Cwd, f)
			}
			f = filepath.Clean(f)
			c.files[f] = true
			names = append(names, filepath.Base(f))
			if mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(f))); strings.HasPrefix(mt, "image/") {
				blk.Images = append(blk.Images, &pb.AgentImage{Ref: "file:" + f, MediaType: mt})
			}
		}
		blk.Input = strings.Join(names, "\n")
		blk.Title = "Sent you " + plural(len(names), "file")
	default:
		name := b.Name
		if strings.HasPrefix(name, "mcp__") {
			parts := strings.SplitN(name, "__", 3)
			if len(parts) == 3 {
				name = parts[2] + " (" + parts[1] + ")"
			}
		}
		blk.Title = "Used " + name
		blk.Input = clip(string(b.Input), 2000)
	}
	return blk
}

// result fills in a call's output.
func (c *Claude) result(b ccBlock, structured json.RawMessage) []*pb.AgentBlock {
	id, ok := c.byTool[b.ToolUseID]
	if !ok {
		return nil
	}
	old := c.blocks[c.byID[id]]
	blk := cloneBlock(old)
	blk.Pending = false
	blk.Error = b.IsError
	var text []string
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		text = append(text, s)
	} else {
		var parts []ccBlock
		_ = json.Unmarshal(b.Content, &parts)
		for i, p := range parts {
			switch p.Type {
			case "text":
				text = append(text, p.Text)
			case "image":
				if ref := c.keepImage(fmt.Sprintf("%s:r%d", id, i), p); ref != nil {
					blk.Images = append(blk.Images, ref)
				}
			}
		}
	}
	out := strings.Join(text, "\n")
	switch blk.Tool {
	case "TaskCreate":
		c.tasks.createdResult(b.ToolUseID, out, b.IsError)
	case "Artifact":
		if l := artifactLink(structured); l != nil && !b.IsError {
			c.links.add(l)
		}
	}
	switch blk.Kind {
	case pb.AgentBlock_KIND_QUESTION:
		var r struct {
			Answers     map[string]string `json:"answers"`
			Annotations map[string]struct {
				Notes string `json:"notes"`
			} `json:"annotations"`
		}
		if json.Unmarshal(structured, &r) == nil && r.Answers != nil {
			for _, q := range blk.Questions {
				q.Answer = r.Answers[q.Question]
				q.Notes = r.Annotations[q.Question].Notes
			}
		}
		if b.IsError || r.Answers == nil {
			blk.Output = clip(out, maxOutput)
		}
	case pb.AgentBlock_KIND_PLAN:
		blk.Output = clip(out, maxOutput)
		blk.Error = b.IsError || strings.Contains(out, "doesn't want to proceed")
	case pb.AgentBlock_KIND_SUBAGENT:
		blk.Output = clip(out, maxOutput)
	default:
		blk.Output = clip(stripANSI(out), maxOutput)
		// Edits carry the real patch in the structured result; a command
		// that changed files (a script, sed) carries theirs too.
		type hunk struct {
			OldStart int      `json:"oldStart"`
			NewStart int      `json:"newStart"`
			Lines    []string `json:"lines"`
		}
		var r struct {
			StructuredPatch []hunk `json:"structuredPatch"`
			BashEditDiff    struct {
				Files []struct {
					FilePath string `json:"filePath"`
					Hunks    []hunk `json:"hunks"`
				} `json:"files"`
				MoreFiles int `json:"moreFiles"`
			} `json:"bashEditDiff"`
		}
		if json.Unmarshal(structured, &r) == nil {
			var d strings.Builder
			added, removed := 0, 0
			write := func(hs []hunk) {
				for _, h := range hs {
					fmt.Fprintf(&d, "@@ -%d +%d @@\n", h.OldStart, h.NewStart)
					for _, ln := range h.Lines {
						d.WriteString(ln + "\n")
						if strings.HasPrefix(ln, "+") {
							added++
						} else if strings.HasPrefix(ln, "-") {
							removed++
						}
					}
				}
			}
			write(r.StructuredPatch)
			for _, f := range r.BashEditDiff.Files {
				c.files[filepath.Clean(f.FilePath)] = true
				// Which file, as a header line (as @@ is): a command doesn't say.
				fmt.Fprintf(&d, "@@ %s\n", c.rel(f.FilePath))
				write(f.Hunks)
			}
			if n := r.BashEditDiff.MoreFiles; n > 0 {
				fmt.Fprintf(&d, "@@ and %d more files\n", n)
			}
			if d.Len() > 0 {
				blk.Diff, blk.Added, blk.Removed = clip(d.String(), maxDiff), int32(added), int32(removed)
			}
		}
	}
	if blk.Tool == "Bash" && blk.Error && strings.Contains(out, "doesn't want to proceed") {
		blk.Output = "Denied"
	}
	if blk.Tool == "Bash" && !blk.Error {
		c.startShell(blk, out, structured, old.TimeUnixMs)
	}
	if (blk.Tool == "KillShell" || blk.Tool == "TaskStop") && !blk.Error {
		c.stoppedShell(blk.Input)
	}
	return c.add(blk)
}

func (c *Claude) keepImage(ref string, b ccBlock) *pb.AgentImage {
	if b.Source == nil || b.Source.Type != "base64" {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(b.Source.Data)
	if err != nil {
		return nil
	}
	ref = "img:" + ref
	c.images[ref] = Image{MediaType: b.Source.MediaType, Data: data}
	return &pb.AgentImage{Ref: ref, MediaType: b.Source.MediaType}
}

var (
	pasteMention = regexp.MustCompile(`\[Image #(\d+)\]`)
	pasteSource  = regexp.MustCompile(`^\[Image: source: (/.+/(\d+)\.png)\]$`)
)

// notePastes numbers a message's pasted images, as "[Image #2]" in its
// text, so the line naming each one's original file can find it.
func (c *Claude) notePastes(l ccLine, text []string, imgs []*pb.AgentImage) {
	ids := l.PasteIDs
	if len(ids) != len(imgs) {
		ids = nil
		for _, m := range pasteMention.FindAllStringSubmatch(strings.Join(text, "\n"), -1) {
			n, _ := strconv.Atoi(m[1])
			ids = append(ids, n)
		}
	}
	if len(ids) != len(imgs) {
		return
	}
	for i, img := range imgs {
		c.pasted[ids[i]] = img.Ref
		if kept, ok := c.images[img.Ref]; ok {
			kept.Sent = l.Timestamp
			c.images[img.Ref] = kept
		}
	}
}

// imageSources reads the hidden line Claude Code writes after a message
// with pasted images ("[Image: source: …/<session>/images/2.png]"), marking
// each image's original PNG to serve in place of the JPEG Claude was sent.
// Only a file in this session's own paste folder counts.
func (c *Claude) imageSources(l ccLine) {
	content := l.Message.Content
	var texts []string
	var s string
	if json.Unmarshal(content, &s) == nil {
		texts = []string{s}
	} else {
		var blocks []ccBlock
		_ = json.Unmarshal(content, &blocks)
		for _, b := range blocks {
			if b.Type == "text" {
				texts = append(texts, b.Text)
			}
		}
	}
	for _, t := range texts {
		m := pasteSource.FindStringSubmatch(strings.TrimSpace(t))
		if m == nil {
			continue
		}
		dir := filepath.Dir(m[1])
		if l.SessionID == "" || filepath.Clean(m[1]) != m[1] || filepath.Base(dir) != "images" || filepath.Base(filepath.Dir(dir)) != l.SessionID {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		ref, ok := c.pasted[n]
		img, has := c.images[ref]
		if !ok || !has || img.MediaType == "image/png" {
			continue
		}
		img.Original = m[1]
		c.images[ref] = img
	}
}

var absPath = regexp.MustCompile(`(?:^|[\s(\x60"'])(/(?:Users|tmp|private|var|opt|Volumes)/[^\s)\x60"'<>]+)`)

// mentions records absolute paths in text as referenced (so links to them
// can be opened from a phone).
func (c *Claude) mentions(s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		c.files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}

func (c *Claude) rel(p string) string {
	if c.Cwd != "" {
		if r, err := filepath.Rel(c.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

// cloneBlock copies a block so updates never mutate one already sent.
func cloneBlock(b *pb.AgentBlock) *pb.AgentBlock { return proto.CloneOf(b) }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…"
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func simpleDiff(old, nw string) string {
	var b strings.Builder
	for _, l := range strings.Split(old, "\n") {
		if old != "" {
			b.WriteString("-" + l + "\n")
		}
	}
	for _, l := range strings.Split(nw, "\n") {
		if nw != "" {
			b.WriteString("+" + l + "\n")
		}
	}
	return b.String()
}

func addedDiff(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

func todos(v any) string {
	items, _ := v.([]any)
	var b strings.Builder
	for _, it := range items {
		m, _ := it.(map[string]any)
		content, _ := m["content"].(string)
		status, _ := m["status"].(string)
		box := "- [ ] "
		switch status {
		case "completed":
			box = "- [x] "
		case "in_progress":
			box = "- [~] "
		}
		b.WriteString(box + content + "\n")
	}
	return b.String()
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// Questions decodes AskUserQuestion's input (from the transcript, or a
// PreToolUse hook while the question is waiting).
func Questions(input json.RawMessage) []*pb.AgentQuestion {
	var q struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
				Preview     string `json:"preview"`
			} `json:"options"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(input, &q)
	var out []*pb.AgentQuestion
	for _, qq := range q.Questions {
		aq := &pb.AgentQuestion{Question: qq.Question, Header: qq.Header, MultiSelect: qq.MultiSelect}
		for _, o := range qq.Options {
			aq.Options = append(aq.Options, &pb.AgentOption{Label: o.Label, Description: o.Description, Preview: o.Preview})
		}
		out = append(out, aq)
	}
	return out
}

func asStrings(v any) []string {
	var out []string
	for _, x := range asAny(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asAny(v any) []any { a, _ := v.([]any); return a }

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
