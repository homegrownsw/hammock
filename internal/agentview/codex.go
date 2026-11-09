package agentview

// Codex's rollouts (~/.codex/sessions/…/rollout-*.jsonl) as the agent view's
// blocks, like Claude's (claude.go): your messages, its replies, its
// commands and edits with their output, and its model and context.
//
// Codex 0.16x runs most tools from small scripts given to its "exec" tool:
// tools.exec_command({cmd: …}) to run commands, apply_patch with a patch to
// edit. Those show as the commands and edits they are.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/thread"
)

// Parser reads one agent's session file, a line at a time, into blocks.
type Parser interface {
	// Feed parses one line and returns the blocks it added or changed.
	Feed(line []byte) []*pb.AgentBlock
	Blocks() []*pb.AgentBlock
	// Pending is the newest tool call still waiting for its result.
	Pending() *pb.AgentBlock
	Image(ref string) (Image, bool)
	// Referenced: the session read, wrote or mentioned path (so it may be
	// served to clients).
	Referenced(path string) bool
	// Context: the model, the context its latest reply took, and the window.
	Context() (model string, tokens, window int64)
	// Rewound reports (once) that what clients show must be replaced.
	Rewound() bool
}

var (
	_ Parser = (*Claude)(nil)
	_ Parser = (*Codex)(nil)
)

type Codex struct {
	Cwd string

	blocks []*pb.AgentBlock
	byID   map[string]int
	byCall map[string]string // call_id -> block id
	files  map[string]bool
	turn   string
	n      int // lines read, for blocks with no id of their own

	model          string
	tokens, window int64

	tasks taskList // its plan (update_plan), as a task list
}

// Tasks is the session's plan as a task list (Extras). Codex has no links.
func (c *Codex) Tasks() *pb.AgentTaskList { return c.tasks.list() }
func (c *Codex) Links() []*pb.AgentLink   { return nil }

func NewCodex(cwd string) *Codex {
	return &Codex{Cwd: cwd, byID: map[string]int{}, byCall: map[string]string{}, files: map[string]bool{}}
}

func (c *Codex) Blocks() []*pb.AgentBlock        { return c.blocks }
func (c *Codex) Image(string) (Image, bool)      { return Image{}, false }
func (c *Codex) Referenced(path string) bool     { return c.files[filepath.Clean(path)] }
func (c *Codex) Rewound() bool                   { return false }
func (c *Codex) Context() (string, int64, int64) { return c.model, c.tokens, c.window }

func (c *Codex) Pending() *pb.AgentBlock {
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

type cxLine struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type cxPayload struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   []cxContent     `json:"content"`
	Summary   []cxContent     `json:"summary"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	CallID    string          `json:"call_id"`
	Output    json.RawMessage `json:"output"`
	Action    json.RawMessage `json:"action"`
	Author    string          `json:"author"`
	Cwd       string          `json:"cwd"`
	Model     string          `json:"model"`
	Reason    string          `json:"reason"`
	Info      *struct {
		Last struct {
			Total int64 `json:"total_tokens"`
		} `json:"last_token_usage"`
		Window int64 `json:"model_context_window"`
	} `json:"info"`
	Window   int64 `json:"model_context_window"`
	Settings *struct {
		Model string `json:"model"`
	} `json:"thread_settings"`
}

type cxContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c *Codex) Feed(line []byte) []*pb.AgentBlock {
	c.n++
	var l cxLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	var p cxPayload
	if json.Unmarshal(l.Payload, &p) != nil {
		return nil
	}
	ms := l.Timestamp.UnixMilli()
	switch l.Type {
	case "session_meta":
		if c.Cwd == "" {
			c.Cwd = p.Cwd
		}
		return nil
	case "turn_context":
		if p.Model != "" {
			c.model = p.Model
		}
		return nil
	case "event_msg":
		return c.event(p, ms)
	case "response_item":
	default:
		return nil
	}
	id := p.ID
	if id == "" {
		id = fmt.Sprintf("cx%d", c.n)
	}
	switch p.Type {
	case "message":
		text := joinText(p.Content)
		switch {
		case strings.TrimSpace(text) == "":
		case p.Role == "user" && strings.HasPrefix(strings.TrimSpace(text), "<user_shell_command>"):
			return c.userShell(id, text, ms)
		case p.Role == "user" && !thread.CodexInjected(text):
			// Codex sometimes records a message twice, back to back; it shows
			// it once.
			if n := len(c.blocks); n > 0 && c.blocks[n-1].Kind == pb.AgentBlock_KIND_USER && c.blocks[n-1].Text == text {
				return nil
			}
			c.turn = id
			c.mentions(text)
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, Text: text, TurnId: id})
		case p.Role == "assistant":
			c.mentions(text)
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: ms, Text: text})
		}
	case "reasoning":
		// Usually encrypted; a summary, when there is one, is worth showing.
		if s := strings.TrimSpace(joinText(p.Summary)); s != "" {
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: s})
		}
	case "agent_message":
		// A message between Codex's own agents.
		if s := strings.TrimSpace(joinText(p.Content)); s != "" {
			return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "From " + p.Author + ": " + firstLine(s)})
		}
	case "function_call":
		return c.call(id, p.CallID, ms, c.functionCall(p))
	case "custom_tool_call":
		return c.call(id, p.CallID, ms, c.customCall(p))
	case "local_shell_call":
		var a struct {
			Command []string `json:"command"`
		}
		_ = json.Unmarshal(p.Action, &a)
		return c.call(id, p.CallID, ms, c.shell("local_shell", unwrapShell(a.Command)))
	case "web_search_call":
		var a struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal(p.Action, &a)
		return c.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TOOL, TimeUnixMs: ms, Tool: "web_search", Input: a.Query, Title: "Searched the web for " + strconv.Quote(a.Query)})
	case "function_call_output", "custom_tool_call_output":
		return c.result(p)
	}
	return nil
}

func (c *Codex) event(p cxPayload, ms int64) []*pb.AgentBlock {
	switch p.Type {
	case "task_started":
		if p.Window > 0 {
			c.window = p.Window
		}
	case "thread_settings_applied":
		if p.Settings != nil && p.Settings.Model != "" {
			c.model = p.Settings.Model
		}
	case "token_count":
		if p.Info != nil {
			c.tokens = p.Info.Last.Total
			if p.Info.Window > 0 {
				c.window = p.Info.Window
			}
		}
	case "turn_aborted":
		return c.add(&pb.AgentBlock{Id: fmt.Sprintf("cx%d", c.n), Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Interrupted"})
	case "context_compacted":
		return c.add(&pb.AgentBlock{Id: fmt.Sprintf("cx%d", c.n), Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted"})
	}
	return nil
}

func (c *Codex) call(id, callID string, ms int64, b *pb.AgentBlock) []*pb.AgentBlock {
	b.Id, b.TimeUnixMs, b.Pending = id, ms, true
	if callID != "" {
		c.byCall[callID] = id
	}
	return c.add(b)
}

// functionCall: a tool called with JSON arguments.
func (c *Codex) functionCall(p cxPayload) *pb.AgentBlock {
	var in map[string]any
	_ = json.Unmarshal([]byte(p.Arguments), &in)
	str := func(k string) string { s, _ := in[k].(string); return s }
	switch p.Name {
	case "exec_command", "shell", "shell_command", "local_shell":
		cmd := str("cmd")
		if cmd == "" {
			cmd = str("command")
		}
		if cmd == "" {
			if argv, ok := in["command"].([]any); ok {
				cmd = unwrapShell(asStrings(argv))
			}
		}
		return c.shell(p.Name, cmd)
	case "write_stdin":
		if s := str("chars"); s != "" {
			return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Input: s, Title: "Typed into a running command"}
		}
		return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Title: "Waited for a running command"}
	case "wait", "wait_agent", "sleep":
		return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Title: "Waited"}
	case "update_plan":
		c.tasks.replace(asAny(in["plan"]))
		return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Title: "Updated the plan", Input: planText(in["plan"])}
	case "view_image":
		path := c.path(str("path"))
		return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Input: path, Title: "Viewed " + path}
	case "apply_patch":
		return c.patch(str("input"))
	case "spawn_agent", "send_message", "followup_task":
		return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Input: str("message"), Title: map[string]string{
			"spawn_agent": "Started an agent", "send_message": "Messaged an agent", "followup_task": "Gave an agent a follow-up"}[p.Name]}
	}
	// Claude's tools, in a session Hammock converted from Claude Code.
	switch p.Name {
	case "Write", "Edit", "MultiEdit", "Read":
		path := c.path(str("file_path"))
		b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Input: path}
		switch p.Name {
		case "Read":
			b.Title = "Read " + path
		case "Write":
			content := cmpOr(str("content"), str("new_string"))
			b.Title, b.Diff, b.Added = "Wrote "+path, clip(addedDiff(content), maxDiff), int32(lines(content))
		default:
			old, nw := str("old_string"), str("new_string")
			b.Title, b.Diff, b.Added, b.Removed = "Edited "+path, clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		}
		return b
	case "Bash":
		return c.shell(p.Name, str("command"))
	}
	title := "Used " + p.Name
	if server, tool, ok := strings.Cut(strings.TrimPrefix(p.Name, "mcp__"), "__"); ok && strings.HasPrefix(p.Name, "mcp__") {
		title = "Used " + tool + " (" + server + ")"
	}
	return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Title: title, Input: clip(p.Arguments, maxOutput)}
}

var (
	scriptTool = regexp.MustCompile(`tools\.([a-z_]+)\(`)
	stdinChars = regexp.MustCompile(`"?chars"?\s*:\s*("(?:[^"\\]|\\.)*")`)
)

var execCommandRE = regexp.MustCompile(`exec_command\(\{\s*"?cmd"?\s*:\s*("(?:[^"\\]|\\.)*")`)

// customCall: a tool given free text: a patch, or an exec script.
func (c *Codex) customCall(p cxPayload) *pb.AgentBlock {
	switch p.Name {
	case "apply_patch":
		return c.patch(p.Input)
	case "exec":
		if strings.Contains(p.Input, "*** Begin Patch") {
			patch := strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(p.Input)
			if i, j := strings.Index(patch, "*** Begin Patch"), strings.Index(patch, "*** End Patch"); i >= 0 && j > i {
				return c.patch(patch[i : j+len("*** End Patch")])
			}
		}
		var cmds []string
		for _, m := range execCommandRE.FindAllStringSubmatch(p.Input, -1) {
			if s, err := strconv.Unquote(m[1]); err == nil {
				cmds = append(cmds, s)
			}
		}
		if len(cmds) > 0 {
			b := c.shell("exec_command", strings.Join(cmds, "\n"))
			if len(cmds) > 1 {
				b.Title = fmt.Sprintf("Ran `%s` and %s", firstLine(cmds[0]), plural(len(cmds)-1, "more command"))
			}
			return b
		}
		// Another of its tools, by the first the script calls.
		title := "Ran a script"
		if m := scriptTool.FindStringSubmatch(p.Input); m != nil {
			switch m[1] {
			case "write_stdin":
				title = "Waited for a running command"
				if typed := stdinChars.FindStringSubmatch(p.Input); typed != nil && typed[1] != `""` {
					title = "Typed into a running command"
				}
			case "wait", "wait_agent", "sleep":
				title = "Waited"
			case "view_image":
				title = "Viewed an image"
			case "update_plan":
				title = "Updated the plan"
			case "apply_patch":
				title = "Edited files"
			}
		}
		return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "exec", Title: title, Input: clip(p.Input, maxOutput)}
	}
	return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: p.Name, Title: "Used " + p.Name, Input: clip(p.Input, maxOutput)}
}

func (c *Codex) shell(tool, cmd string) *pb.AgentBlock {
	c.mentions(cmd)
	return &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: tool, Input: cmd, Title: "Ran `" + firstLine(cmd) + "`"}
}

// patch is an apply_patch edit as a diff: each file under an "@@ path"
// header, as the views show a command's edits to several files.
func (c *Codex) patch(text string) *pb.AgentBlock {
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "apply_patch"}
	var diff strings.Builder
	var paths []string
	for _, l := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(l, "*** Update File: "), strings.HasPrefix(l, "*** Add File: "), strings.HasPrefix(l, "*** Delete File: "):
			_, p, _ := strings.Cut(l, ": ")
			p = c.path(strings.TrimSpace(p))
			paths = append(paths, p)
			diff.WriteString("@@ " + p + "\n")
			if strings.HasPrefix(l, "*** Delete File: ") {
				diff.WriteString("-(deleted)\n")
				b.Removed++
			}
		case strings.HasPrefix(l, "*** "), strings.HasPrefix(l, "@@"):
		case strings.HasPrefix(l, "+"):
			diff.WriteString(l + "\n")
			b.Added++
		case strings.HasPrefix(l, "-"):
			diff.WriteString(l + "\n")
			b.Removed++
		case strings.HasPrefix(l, " "):
			diff.WriteString(l + "\n")
		}
	}
	b.Diff = clip(diff.String(), maxDiff)
	switch len(paths) {
	case 0:
		b.Title = "Edited files"
	case 1:
		b.Title, b.Input = "Edited "+paths[0], paths[0]
	default:
		b.Title, b.Input = fmt.Sprintf("Edited %s and %s", paths[0], plural(len(paths)-1, "other file")), strings.Join(paths, "\n")
	}
	return b
}

// result fills in a call's output.
func (c *Codex) result(p cxPayload) []*pb.AgentBlock {
	id, ok := c.byCall[p.CallID]
	if !ok {
		return nil
	}
	blk := cloneBlock(c.blocks[c.byID[id]])
	blk.Pending = false
	out, failed := codexOutput(p.Output)
	blk.Error = failed
	if blk.Tool != "apply_patch" || failed {
		blk.Output = clip(strings.TrimRight(stripANSI(out), "\n"), maxOutput)
	}
	return c.add(blk)
}

var exitCode = regexp.MustCompile(`(?m)^(?:Process exited with code|Exit code:)\s*(-?\d+)`)

// codexOutput is what a tool printed, and whether it failed: a string, a
// JSON {"output", "metadata": {"exit_code"}}, or content items (an exec
// script's, after its "Script completed … Output:" header).
func codexOutput(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		var obj struct {
			Output   string `json:"output"`
			Metadata struct {
				ExitCode int `json:"exit_code"`
			} `json:"metadata"`
		}
		if strings.HasPrefix(strings.TrimSpace(s), "{") && json.Unmarshal([]byte(s), &obj) == nil && obj.Output != "" {
			return obj.Output, obj.Metadata.ExitCode != 0
		}
		return trimExecHeader(s)
	}
	var items []cxContent
	if json.Unmarshal(raw, &items) == nil {
		var parts []string
		for i, it := range items {
			if i == 0 && len(items) > 1 && strings.HasPrefix(it.Text, "Script completed") && strings.HasSuffix(strings.TrimSpace(it.Text), "Output:") {
				continue
			}
			parts = append(parts, it.Text)
		}
		return trimExecHeader(strings.Join(parts, "\n"))
	}
	return string(bytes.TrimSpace(raw)), false
}

// trimExecHeader drops exec_command's own header (chunk ID, wall time, exit
// code) above what the command printed, noting a non-zero exit.
func trimExecHeader(s string) (string, bool) {
	failed := false
	if m := exitCode.FindStringSubmatch(s); m != nil && m[1] != "0" {
		failed = true
	}
	if i := strings.Index(s, "\nOutput:\n"); i >= 0 && i < 400 {
		s = s[i+len("\nOutput:\n"):]
	}
	return s, failed
}

func (c *Codex) add(b *pb.AgentBlock) []*pb.AgentBlock {
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

func (c *Codex) path(p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && c.Cwd != "" {
		p = filepath.Join(c.Cwd, p)
	}
	c.files[filepath.Clean(p)] = true
	if c.Cwd != "" {
		if r, err := filepath.Rel(c.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

func (c *Codex) mentions(s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		c.files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}

func joinText(cs []cxContent) string {
	var parts []string
	for _, c := range cs {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// planText is update_plan's steps as a checklist.
func planText(v any) string {
	var b strings.Builder
	for _, it := range asAny(v) {
		m, _ := it.(map[string]any)
		step, _ := m["step"].(string)
		status, _ := m["status"].(string)
		mark := "[ ]"
		switch status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			mark = "[~]"
		}
		b.WriteString(mark + " " + step + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// unwrapShell turns ["bash","-lc","X"] into X, other argv into a command line.
func unwrapShell(argv []string) string {
	if len(argv) == 3 && (argv[1] == "-lc" || argv[1] == "-c") && strings.HasSuffix(argv[0], "sh") {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var (
	cxShellCommand = regexp.MustCompile(`(?s)<command>\n?(.*?)\n?</command>`)
	cxShellExit    = regexp.MustCompile(`(?m)^Exit code: (-?\d+)`)
	cxShellOutput  = regexp.MustCompile(`(?s)\nOutput:\n?(.*?)\n?</result>`)
)

// userShell is a "!" command you ran at Codex's prompt: Codex records it,
// once it's finished, as a user message of its own,
//
//	<user_shell_command>
//	<command>
//	ls
//	</command>
//	<result>
//	Exit code: 0
//	Duration: 0.0123 seconds
//	Output:
//	…
//	</result>
//	</user_shell_command>
//
// shown as the shell tool it is, "Ran `ls`", with its output.
func (c *Codex) userShell(id, text string, ms int64) []*pb.AgentBlock {
	m := cxShellCommand.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	cmd := strings.TrimSpace(m[1])
	c.turn = id
	// What you ran, as you typed it: not read as a file read or a search,
	// as Codex's own commands are.
	b := &pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TOOL, Tool: "Bash", TimeUnixMs: ms, Input: cmd,
		Title: "Ran `" + firstLine(cmd) + "`", TurnId: id}
	if e := cxShellExit.FindStringSubmatch(text); e != nil {
		b.Error = e[1] != "0"
	}
	if o := cxShellOutput.FindStringSubmatch(text); o != nil {
		b.Output = clip(stripANSI(strings.TrimRight(o[1], "\n")), maxOutput)
	}
	return c.add(b)
}
