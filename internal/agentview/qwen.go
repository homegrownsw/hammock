package agentview

// Qwen Code's sessions (~/.qwen/projects/<cwd, sanitised>/chats/<id>.jsonl)
// as the agent view's blocks.
//
// The format, from qwen-code's packages/core/src/services/
// chatRecordingService.ts (0.26, November 2026): append-only JSONL, one
// record per line, chained like Claude Code's by uuid and parentUuid:
// {uuid, parentUuid, sessionId, timestamp, type: user|assistant|
// tool_result|system, subtype, cwd, message: {role, parts}, …}. Its
// messages are the Gemini API's (parts with text, thought, functionCall,
// functionResponse), as Gemini CLI's are, so it shares that reading with
// gemini.go: assistant records carry function calls, tool_result records
// their responses (with toolCallResult: status, resultDisplay). /rewind
// re-roots the chain: a "rewind" system record whose parent is where the
// rewound turn began.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

var _ Parser = (*Qwen)(nil)

// Qwen parses a Qwen Code session file.
type Qwen struct {
	Cwd string

	blocks []*pb.AgentBlock
	byID   map[string]int
	byCall map[string]string // functionCall id -> block id
	files  map[string]bool
	turn   string
	// branchAt: where each message you typed began, by its parent record:
	// a rewind to that parent drops it and everything after.
	branchAt map[string]int
	rewound  bool

	model          string
	tokens, window int64
}

func NewQwen(cwd string) *Qwen {
	return &Qwen{Cwd: cwd, byID: map[string]int{}, byCall: map[string]string{}, files: map[string]bool{}, branchAt: map[string]int{}}
}

func (q *Qwen) Blocks() []*pb.AgentBlock        { return q.blocks }
func (q *Qwen) Image(string) (Image, bool)      { return Image{}, false }
func (q *Qwen) Referenced(path string) bool     { return q.files[filepath.Clean(path)] }
func (q *Qwen) Context() (string, int64, int64) { return q.model, q.tokens, q.window }
func (q *Qwen) Pending() *pb.AgentBlock         { return pendingAfterUser(q.blocks) }

func (q *Qwen) Rewound() bool {
	r := q.rewound
	q.rewound = false
	return r
}

type qwLine struct {
	UUID        string    `json:"uuid"`
	ParentUUID  string    `json:"parentUuid"`
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"`
	Subtype     string    `json:"subtype"`
	IsSidechain bool      `json:"isSidechain"`
	Message     *struct {
		Parts json.RawMessage `json:"parts"`
	} `json:"message"`
	Model         string `json:"model"`
	ContextWindow int64  `json:"contextWindowSize"`
	Usage         *struct {
		Prompt int64 `json:"promptTokenCount"`
	} `json:"usageMetadata"`
	ToolCallResult *struct {
		CallID        string          `json:"callId"`
		Status        string          `json:"status"`
		ResultDisplay json.RawMessage `json:"resultDisplay"`
	} `json:"toolCallResult"`
	SystemPayload json.RawMessage `json:"systemPayload"`
}

func (q *Qwen) Feed(line []byte) []*pb.AgentBlock {
	var l qwLine
	if json.Unmarshal(line, &l) != nil || l.UUID == "" || l.IsSidechain {
		return nil
	}
	ms := l.Timestamp.UnixMilli()
	var parts []gemPart
	if l.Message != nil {
		parts = gemParts(l.Message.Parts)
	}
	var payload struct {
		DisplayText      *string `json:"displayText"`
		Phase            string  `json:"phase"`
		RawCommand       string  `json:"rawCommand"`
		HiddenInvocation bool    `json:"hiddenInvocation"`
	}
	_ = json.Unmarshal(l.SystemPayload, &payload)
	text := partsText(parts)
	if payload.DisplayText != nil {
		text = *payload.DisplayText // what you typed, before expansion
	}
	text = strings.TrimSpace(text)
	switch l.Type {
	case "user":
		switch l.Subtype {
		case "", "mid_turn_user_message", "realtime_message":
			return q.typed(l, text, ms)
		case "notification", "cron", "agent_message", "agent_mention":
			// Not something you typed: a background task, a scheduled
			// prompt or another agent reporting in.
			if text != "" {
				return q.add(&pb.AgentBlock{Id: l.UUID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: firstLine(text)})
			}
		}
	case "assistant":
		return q.assistant(l, parts, ms)
	case "tool_result":
		return q.result(l, parts)
	case "system":
		switch l.Subtype {
		case "chat_compression":
			return q.add(&pb.AgentBlock{Id: l.UUID, Kind: pb.AgentBlock_KIND_NOTE, TimeUnixMs: ms, Text: "Conversation compacted"})
		case "slash_command":
			if payload.Phase == "invocation" && !payload.HiddenInvocation && payload.RawCommand != "" {
				return q.typed(l, strings.TrimSpace(payload.RawCommand), ms)
			}
		case "rewind":
			if i, ok := q.branchAt[l.ParentUUID]; ok && l.ParentUUID != "" && i < len(q.blocks) {
				q.rewind(i)
			}
		}
	}
	return nil
}

// typed adds a message you sent. One sent from the same point as an
// earlier one replaces that earlier branch.
func (q *Qwen) typed(l qwLine, text string, ms int64) []*pb.AgentBlock {
	if text == "" {
		return nil
	}
	if i, ok := q.branchAt[l.ParentUUID]; ok && l.ParentUUID != "" && i < len(q.blocks) {
		q.rewind(i)
	}
	if l.ParentUUID != "" {
		q.branchAt[l.ParentUUID] = len(q.blocks)
	}
	q.turn = l.UUID
	mentionPaths(q.files, text)
	return q.add(&pb.AgentBlock{Id: l.UUID, Kind: pb.AgentBlock_KIND_USER, TimeUnixMs: ms, Text: text, TurnId: l.UUID})
}

func (q *Qwen) assistant(l qwLine, parts []gemPart, ms int64) []*pb.AgentBlock {
	if l.Model != "" {
		q.model = l.Model
	}
	if l.ContextWindow > 0 {
		q.window = l.ContextWindow
	}
	if l.Usage != nil && l.Usage.Prompt > 0 {
		q.tokens = l.Usage.Prompt
	}
	var out []*pb.AgentBlock
	var text, thought strings.Builder
	flush := func(i int) {
		if t := strings.TrimSpace(thought.String()); t != "" {
			out = append(out, q.add(&pb.AgentBlock{Id: fmt.Sprintf("%s/t%d", l.UUID, i), Kind: pb.AgentBlock_KIND_THINKING, TimeUnixMs: ms, Text: t})...)
		}
		if t := strings.TrimSpace(text.String()); t != "" {
			mentionPaths(q.files, t)
			out = append(out, q.add(&pb.AgentBlock{Id: fmt.Sprintf("%s/%d", l.UUID, i), Kind: pb.AgentBlock_KIND_TEXT, TimeUnixMs: ms, Text: t})...)
		}
		thought.Reset()
		text.Reset()
	}
	for i, p := range parts {
		switch {
		case p.FunctionCall != nil:
			flush(i)
			var args map[string]any
			_ = json.Unmarshal(p.FunctionCall.Args, &args)
			b := gemToolBlock(p.FunctionCall.Name, args, q.Cwd, q.files)
			b.Id, b.TimeUnixMs, b.Pending = fmt.Sprintf("%s/c%d", l.UUID, i), ms, true
			if p.FunctionCall.ID != "" {
				q.byCall[p.FunctionCall.ID] = b.Id
			}
			out = append(out, q.add(b)...)
		case p.Thought:
			thought.WriteString(p.Text)
		default:
			text.WriteString(p.Text)
		}
	}
	flush(len(parts))
	return out
}

// result fills in the calls a tool_result record answers.
func (q *Qwen) result(l qwLine, parts []gemPart) []*pb.AgentBlock {
	var out []*pb.AgentBlock
	for _, p := range parts {
		if p.FunctionResponse == nil {
			continue
		}
		id, ok := q.byCall[p.FunctionResponse.ID]
		if !ok && l.ToolCallResult != nil {
			id, ok = q.byCall[l.ToolCallResult.CallID]
		}
		if !ok {
			continue
		}
		b := cloneBlock(q.blocks[q.byID[id]])
		var display json.RawMessage
		failed := false
		if r := l.ToolCallResult; r != nil {
			display = r.ResultDisplay
			failed = r.Status == "error" || r.Status == "cancelled"
		}
		if sr, ok := shellResult(display); ok && (sr.Outcome != "completed" || sr.ExitCode != nil && *sr.ExitCode != 0) {
			failed = true
		}
		raw, _ := json.Marshal([]gemPart{p})
		gemResult(b, failed, display, raw)
		out = append(out, q.add(b)...)
	}
	return out
}

func (q *Qwen) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = q.turn
	}
	if i, ok := q.byID[b.Id]; ok {
		q.blocks[i] = b
	} else {
		q.byID[b.Id] = len(q.blocks)
		q.blocks = append(q.blocks, b)
	}
	return []*pb.AgentBlock{b}
}

// rewind drops the blocks from index i on: an abandoned branch.
func (q *Qwen) rewind(i int) {
	for _, b := range q.blocks[i:] {
		delete(q.byID, b.Id)
	}
	q.blocks = q.blocks[:i]
	for p, j := range q.branchAt {
		if j >= i {
			delete(q.branchAt, p)
		}
	}
	q.turn = ""
	for j := len(q.blocks) - 1; j >= 0; j-- {
		if q.blocks[j].Kind == pb.AgentBlock_KIND_USER {
			q.turn = q.blocks[j].Id
			break
		}
	}
	q.rewound = true
}
