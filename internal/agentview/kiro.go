package agentview

// Kiro CLI's sessions (~/.kiro/sessions/<workspace>/sess_<uuid>/
// messages.jsonl, its V3 engine's, the default since 3.0) as the agent
// view's blocks.
//
// The format, from Kiro CLI 3.1.0's bundled agent (@kiro/agent 0.66.91,
// acp-server.js: its persisted-message schema and actionToMessages): one
// {"id", "timestamp" (RFC 3339), "payload"} per line, payload.type one of
//
//	user        {content, source: chat|hook|api|steer, images: [{data (base64), mimeType}], _meta}
//	assistant   {content, operationType: Say|Reasoning|Print|Summary, executionId, subExecutionId}
//	tool_call   {toolCallId, toolName, args, status: pending|awaiting_approval|approved|denied|executing|completed|failed, title, kind, actionType}
//	tool_result {toolCallId, content, success, durationMs}
//	turn_start, turn_end {stopReason: end_turn|cancelled…}, error {message},
//	sub_agent_start, sub_agent_complete {subSessionId, response, status},
//	agent_note, session_start (the system prompt), session_metadata,
//	usage_summary, tombstone, steering_inclusion, pending_interaction…
//
// A row is written again, under the same id, as it changes: a reply while
// it streams ("<action>-say", with _meta.kiro.streaming until it's done),
// a tool call at each status ("<action>-call"), then its result
// ("<action>-result"). So a row replaces the block it wrote before. Its
// reasoning is usually encrypted ("..." for content, the text in
// reasoningSignature): only readable reasoning shows. A subagent's own
// work is in sub-executions/<id>.jsonl beside it, not read here.
//
// Its tools as the model names them (tool_call.toolName): execute_bash
// {command}, read_file {path}, fs_write / fs_append {path, text},
// str_replace {path, oldStr, newStr}, delete_file {targetFile},
// grep_search {query, includePattern}, file_search {query},
// list_directory {path}, web_fetch {url}, invoke_sub_agent {name, prompt},
// user_input {question}, and MCP tools.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Kiro)(nil)

// Kiro parses a Kiro CLI session's messages.jsonl.
type Kiro struct {
	Cwd string

	blocks []*pb.AgentBlock
	byID   map[string]int    // block id -> index
	byCall map[string]string // tool call id -> block id
	files  map[string]bool
	images map[string]Image
	turn   string
	n      int
}

func NewKiro(cwd string) *Kiro {
	return &Kiro{Cwd: cwd, byID: map[string]int{}, byCall: map[string]string{}, files: map[string]bool{}, images: map[string]Image{}}
}

func (k *Kiro) Blocks() []*pb.AgentBlock        { return k.blocks }
func (k *Kiro) Image(ref string) (Image, bool)  { i, ok := k.images[ref]; return i, ok }
func (k *Kiro) Referenced(path string) bool     { return k.files[filepath.Clean(path)] }
func (k *Kiro) Rewound() bool                   { return false }
func (k *Kiro) Context() (string, int64, int64) { return "", 0, 0 }
func (k *Kiro) Pending() *pb.AgentBlock         { return pendingAfterUser(k.blocks) }

// KiroPayload is a messages.jsonl row's payload, the fields Hammock reads.
type KiroPayload struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	// user
	Source string `json:"source"`
	Images []struct {
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	} `json:"images"`
	Meta struct {
		Kiro struct {
			Visibility     string `json:"visibility"`
			AgentInitiated bool   `json:"agentInitiated"`
			ServerName     string `json:"serverName"`
		} `json:"kiro"`
	} `json:"_meta"`
	// assistant
	OperationType  string `json:"operationType"`
	SubExecutionID string `json:"subExecutionId"`
	// tool_call, tool_result
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Status     string          `json:"status"`
	Title      string          `json:"title"`
	ActionType string          `json:"actionType"`
	Success    *bool           `json:"success"`
	// turn_end, error, sub_agent_complete
	StopReason string `json:"stopReason"`
	Message    string `json:"message"`
	Response   string `json:"response"`
}

// Shown: a user row is a message you sent (not a hook's, a notification
// Kiro gave itself or one it hides).
func (p *KiroPayload) Shown() bool {
	t := strings.TrimSpace(p.Content)
	return p.Type == "user" && p.Source != "hook" && !p.Meta.Kiro.AgentInitiated && p.Meta.Kiro.Visibility != "hidden" &&
		!strings.HasPrefix(t, "[notification/") && (t != "" || len(p.Images) > 0)
}

type kiroLine struct {
	Type      string      `json:"type"` // hammock_session: agents.Kiro's first line
	Cwd       string      `json:"cwd"`
	ID        string      `json:"id"`
	Timestamp time.Time   `json:"timestamp"`
	Payload   KiroPayload `json:"payload"`
}

func (k *Kiro) Feed(line []byte) []*pb.AgentBlock {
	k.n++
	var l kiroLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	if l.Type == "hammock_session" {
		if k.Cwd == "" {
			k.Cwd = l.Cwd
		}
		return nil
	}
	p := &l.Payload
	if p.SubExecutionID != "" {
		return nil // a subagent's work
	}
	id := l.ID
	if id == "" {
		id = fmt.Sprint("kiro", k.n)
	}
	ms := unixMs(l.Timestamp)
	switch p.Type {
	case "user":
		if !p.Shown() {
			return nil
		}
		text := strings.TrimSpace(p.Content)
		k.mentions(text)
		b := &pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, Text: text, TimeUnixMs: ms, TurnId: id}
		for i, img := range p.Images {
			data, err := base64.StdEncoding.DecodeString(img.Data)
			if err != nil {
				continue
			}
			ref := fmt.Sprintf("%s#%d", id, i)
			k.images[ref] = Image{MediaType: img.MimeType, Data: data}
			b.Images = append(b.Images, &pb.AgentImage{Ref: ref, MediaType: img.MimeType})
		}
		k.turn = id
		return k.put(b)
	case "assistant":
		text := strings.TrimSpace(p.Content)
		switch p.OperationType {
		case "", "Say", "Print":
			if text == "" {
				return nil
			}
			k.mentions(text)
			return k.put(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_TEXT, Text: text, TimeUnixMs: ms})
		case "Reasoning":
			if text == "" || strings.Trim(text, ".…") == "" {
				return nil // encrypted
			}
			return k.put(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_THINKING, Text: text, TimeUnixMs: ms})
		case "Summary":
			return k.put(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, Text: "Compacted the conversation", TimeUnixMs: ms})
		}
	case "tool_call":
		if p.ToolCallID == "" {
			return nil
		}
		bid := "kiro-call-" + p.ToolCallID
		b := k.tool(p.ToolName, p.Args, p.Meta.Kiro.ServerName)
		b.Id, b.TimeUnixMs = bid, ms
		switch p.Status {
		case "pending", "awaiting_approval", "approved", "executing":
			b.Pending = true
		case "denied":
			b.Error, b.Output = true, "Denied"
		case "failed":
			b.Error = true
		}
		if i, ok := k.byID[bid]; ok {
			old := k.blocks[i]
			if old.Output != "" && !b.Pending {
				b.Output, b.Error = old.Output, old.Error || b.Error // its result came first
			}
			if !old.Pending && b.Pending {
				b.Pending = false // a result already ended it
			}
			b.TimeUnixMs = old.TimeUnixMs
		}
		k.byCall[p.ToolCallID] = bid
		return k.put(b)
	case "tool_result":
		bid, ok := k.byCall[p.ToolCallID]
		if !ok {
			return nil
		}
		b := k.blocks[k.byID[bid]]
		b.Pending = false
		b.Output = clip(strings.TrimRight(p.Content, "\n"), maxOutput)
		b.Error = p.Success != nil && !*p.Success
		return []*pb.AgentBlock{b}
	case "sub_agent_complete":
		bid, ok := k.byCall[strings.TrimSuffix(id, "-sub-agent-complete")]
		if !ok {
			return nil
		}
		b := k.blocks[k.byID[bid]]
		b.Pending = false
		if r := strings.TrimSpace(p.Response); r != "" {
			b.Output = clip(r, maxOutput)
		}
		return []*pb.AgentBlock{b}
	case "turn_end":
		// A call it never finished (cancelled, or stopped by an error) isn't
		// waiting any more.
		var out []*pb.AgentBlock
		for i := len(k.blocks) - 1; i >= 0 && k.blocks[i].Kind != pb.AgentBlock_KIND_USER; i-- {
			if b := k.blocks[i]; b.Pending {
				b.Pending = false
				out = append(out, b)
			}
		}
		if p.StopReason == "cancelled" {
			return append(out, k.put(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, Text: "Interrupted", TimeUnixMs: ms})...)
		}
		return out
	case "error":
		if m := firstLine(strings.TrimSpace(p.Message)); m != "" {
			return k.put(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_NOTE, Text: "Error: " + m, TimeUnixMs: ms})
		}
	}
	return nil
}

// put adds b, or replaces the block with its id.
func (k *Kiro) put(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = k.turn
	}
	if i, ok := k.byID[b.Id]; ok {
		b.TurnId = k.blocks[i].TurnId
		k.blocks[i] = b
		return []*pb.AgentBlock{b}
	}
	k.byID[b.Id] = len(k.blocks)
	k.blocks = append(k.blocks, b)
	return []*pb.AgentBlock{b}
}

// tool is one of its tool calls as a block (V3's tool names, and V2's
// and the TUI's spellings of them).
func (k *Kiro) tool(name string, raw json.RawMessage, server string) *pb.AgentBlock {
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	str := func(keys ...string) string {
		for _, key := range keys {
			if s, ok := in[key].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	path := func() string { return k.path(str("path", "targetFile", "file_path", "filePath", "file")) }
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: name}
	switch name {
	case "execute_bash", "execute_pwsh", "execute_shell", "shell", "run_command":
		cmd := str("command")
		k.mentions(cmd)
		b.Input, b.Title = cmd, "Ran `"+firstLine(cmd)+"`"
	case "read_file", "fs_read", "read", "read_code":
		p := path()
		if p == "" {
			if ops, _ := in["operations"].([]any); len(ops) > 0 {
				op, _ := ops[0].(map[string]any)
				s, _ := op["path"].(string)
				p = k.path(s)
			}
		}
		b.Input, b.Title = p, "Read "+p
	case "fs_write", "write_file", "write", "create_file":
		p := path()
		old, nw := str("oldStr", "old_str"), str("newStr", "new_str")
		switch {
		case old != "" || nw != "":
			b.Title, b.Diff, b.Added, b.Removed = "Edited "+p, clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		case str("command") == "append":
			content := strings.TrimSuffix(str("text", "new_str", "content"), "\n")
			b.Title, b.Diff, b.Added = "Appended to "+p, clip(addedDiff(content), maxDiff), int32(lines(content))
		default:
			content := strings.TrimSuffix(str("text", "file_text", "content"), "\n")
			b.Title, b.Diff, b.Added = "Wrote "+p, clip(addedDiff(content), maxDiff), int32(lines(content))
		}
		b.Input = p
	case "fs_append", "append_lines":
		p, content := path(), strings.TrimSuffix(str("text", "content"), "\n")
		b.Input, b.Title, b.Diff, b.Added = p, "Appended to "+p, clip(addedDiff(content), maxDiff), int32(lines(content))
	case "str_replace", "replace_text", "edit_code":
		p, old, nw := path(), str("oldStr", "old_str"), str("newStr", "new_str")
		b.Input, b.Title = p, "Edited "+p
		if old != "" || nw != "" {
			b.Diff, b.Added, b.Removed = clip(simpleDiff(old, nw), maxDiff), int32(lines(nw)), int32(lines(old))
		}
	case "delete_file":
		p := path()
		b.Input, b.Title = p, "Deleted "+p
	case "grep_search", "grep":
		q := str("query", "pattern")
		b.Input, b.Title = q, "Searched for "+strconv.Quote(q)
		if inc := str("includePattern"); inc != "" {
			b.Title += " in " + inc
		}
	case "file_search", "find_files", "glob":
		q := str("query", "pattern")
		b.Input, b.Title = q, "Found files matching "+q
	case "list_directory", "ls":
		p := k.path(str("path"))
		b.Input, b.Title = p, "Listed "+cmpOr(p, "the folder")
	case "web_fetch":
		u := str("url")
		b.Input, b.Title = u, "Fetched "+u
	case "web_search":
		q := str("query")
		b.Input, b.Title = q, "Searched the web for "+strconv.Quote(q)
	case "invoke_sub_agent", "use_subagent", "subagent":
		b.Kind = pb.AgentBlock_KIND_SUBAGENT
		b.Input, b.Title = str("prompt", "query"), "Started an agent"
		if n := str("name", "subAgentName", "agent_name"); n != "" {
			b.Title += ": " + n
		}
	case "user_input", "get_user_input":
		q := str("question")
		b.Input, b.Title = q, "Asked you"
		if q != "" {
			b.Title = "Asked you: " + firstLine(q)
		}
	case "update_session_information":
		b.Input, b.Title = str("title"), "Named the session"
	default:
		b.Title = "Used " + name
		if server != "" {
			b.Title = "Used " + strings.TrimPrefix(name, server+"/") + " (" + server + ")"
		}
		if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
			b.Input = clip(string(raw), maxOutput)
		}
	}
	return b
}

func (k *Kiro) path(p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && k.Cwd != "" {
		p = filepath.Join(k.Cwd, p)
	}
	k.files[filepath.Clean(p)] = true
	if k.Cwd != "" {
		if r, err := filepath.Rel(k.Cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

func (k *Kiro) mentions(s string) {
	for _, m := range absPath.FindAllStringSubmatch(s, 50) {
		k.files[filepath.Clean(strings.TrimRight(m[1], ".,:;"))] = true
	}
}
