// Package thread is Hammock's vendor-neutral record of agent
// conversations (ADR-0006). A Thread is an append-only list of Records that
// can be decoded from one harness's native session files and encoded into
// another's, which is what agent switching is built on.
package thread

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Kind is the type of a record.
type Kind string

const (
	KindUser       Kind = "user"        // a user message
	KindAssistant  Kind = "assistant"   // assistant text
	KindToolCall   Kind = "tool_call"   // the assistant invoking a tool
	KindToolResult Kind = "tool_result" // the tool's output
	KindNote       Kind = "note"        // something kept as text when it has no equivalent (e.g. a provider web search)
	KindReasoning  Kind = "reasoning"   // marker only: reasoning happened (content is never carried across harnesses)
)

// Tool is a harness-neutral tool name. Codecs map their native tools to
// these where there is an equivalent; anything else stays native and is
// rendered as a note when encoded for a harness that lacks it.
type Tool string

const (
	ToolShell    Tool = "shell"      // run a command
	ToolRead     Tool = "read_file"  // read a file
	ToolWrite    Tool = "write_file" // create/overwrite a file
	ToolEdit     Tool = "edit_file"  // modify a file (diff in Edit)
	ToolSearch   Tool = "search"     // grep/glob style search
	ToolWebFetch Tool = "web_fetch"
	ToolTodo     Tool = "todo"
	ToolUnmapped Tool = ""
)

// Record is one entry in a thread.
type Record struct {
	Seq    int       `json:"seq"`
	Kind   Kind      `json:"kind"`
	Time   time.Time `json:"time,omitzero"`
	Text   string    `json:"text,omitempty"`    // message text, tool output, or note
	CallID string    `json:"call_id,omitempty"` // links a tool_result to its tool_call

	Tool       Tool            `json:"tool,omitempty"`        // neutral tool, when mapped
	NativeTool string          `json:"native_tool,omitempty"` // the harness's own tool name
	Input      json.RawMessage `json:"input,omitempty"`       // tool input (neutral fields for mapped tools)
	IsError    bool            `json:"is_error,omitempty"`
	Edit       *Edit           `json:"edit,omitempty"` // for write/edit calls

	Hash string `json:"hash,omitempty"` // content hash, chained with the previous record
}

// Edit describes a file change.
type Edit struct {
	Path string `json:"path"`
	Old  string `json:"old,omitempty"` // replaced text (edit) — may be clipped
	New  string `json:"new,omitempty"` // new text / full content (write) — may be clipped
}

// Thread is a conversation's identity and provenance.
type Thread struct {
	ID       string    `json:"id"`        // UUIDv7, stable across harnesses and machines
	Harness  string    `json:"harness"`   // harness it was captured from
	NativeID string    `json:"native_id"` // that harness's session id
	Cwd      string    `json:"cwd"`
	Title    string    `json:"title,omitempty"`
	Named    bool      `json:"named,omitempty"` // Title is the person's own (/rename), not generated
	Created  time.Time `json:"created"`
	Parent   string    `json:"parent,omitempty"` // thread this was converted/forked from
	Source   string    `json:"source,omitempty"` // harness the conversation came from, when converted
}

// MaxIO bounds tool input/output carried in a record; longer values keep
// their start and end.
const MaxIO = 64 << 10

// Clip keeps the start and end of s within MaxIO bytes.
func Clip(s string) string {
	if len(s) <= MaxIO {
		return s
	}
	half := MaxIO/2 - 32
	return s[:half] + fmt.Sprintf("\n… [%d bytes omitted] …\n", len(s)-2*half) + s[len(s)-half:]
}

// Seal numbers records and chains their hashes so a thread's history is
// tamper-evident and cheap to compare/sync.
func Seal(recs []Record) {
	prev := ""
	for i := range recs {
		recs[i].Seq = i
		recs[i].Hash = ""
		b, _ := json.Marshal(recs[i])
		sum := sha256.Sum256(append([]byte(prev), b...))
		recs[i].Hash = hex.EncodeToString(sum[:16])
		prev = recs[i].Hash
	}
}

// NewID returns a UUIDv7 (time-ordered) string.
func NewID() string {
	var b [16]byte
	_, _ = io.ReadFull(rand.Reader, b[:])
	ms := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint16(b[0:2], uint16(ms>>32))
	binary.BigEndian.PutUint32(b[2:6], uint32(ms))
	b[6] = (b[6] & 0x0F) | 0x70 // version 7
	b[8] = (b[8] & 0x3F) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Codec reads and writes one harness's native session format.
type Codec interface {
	// Harness names the agent CLI, e.g. "claude" or "codex".
	Harness() string
	// Decode reads a native session into neutral records.
	Decode(r io.Reader) (Thread, []Record, error)
	// Encode writes records as a new native session for this harness,
	// returning the session id to resume and the file it belongs at.
	Encode(t Thread, recs []Record) (nativeID string, path string, data []byte, err error)
	// ResumeCommand is the argv that resumes a session in cwd.
	ResumeCommand(nativeID string) []string
}

// Locator is implemented by codecs that can find a session file by id.
type Locator interface {
	Locate(nativeID string) (string, bool)
}

// pastedContent is Claude Code's wrapper around text pasted into its prompt
// (as Hammock sends messages typed in the agent view): `<pasted_content
// id="1a2b">` and `</pasted_content id="1a2b">`, each on a line of its own.
var pastedContent = regexp.MustCompile(`(?s)<pasted_content id="[0-9a-f]{4}">\n(.*?)\n?</pasted_content id="[0-9a-f]{4}">\n?`)

// UnwrapPasted is a prompt with Claude Code's pasted-content wrappers
// taken off, leaving what was pasted.
func UnwrapPasted(s string) string {
	if !strings.Contains(s, "<pasted_content id=") {
		return s
	}
	return pastedContent.ReplaceAllString(s, "$1\n")
}
