package agentview

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Aider (github.com/Aider-AI/aider) keeps no session files of its own: it
// appends every session in a project to one Markdown file,
// .aider.chat.history.md at the top of the git repository (else in the
// folder it was started in). The format, from aider/io.py (0.86.2) and
// checked against a file it wrote:
//
//	# aider chat started at 2026-11-07 10:45:50   a session starts (local time)
//	#### make it greet by name␠␠                    what you sent, a line each
//	I'll make it greet by name. …                   its reply, as it streamed it
//	> Applied edit to hello.py␠␠                    its own output (tool_output)
//
// Your lines and its output always end in two spaces (a Markdown line
// break), which a reply's lines almost never do. A message of its own of
// several lines is quoted on its first line only and ends at the next line
// ending in two spaces. Replies in its "diff" edit formats carry their
// edits as SEARCH/REPLACE blocks, shown here as edits.

// AiderHistoryFile is the name of Aider's chat history file.
const AiderHistoryFile = ".aider.chat.history.md"

// AiderRoot is the folder Aider keeps cwd's history in: the top of the git
// repository cwd is in (where .git is, a folder or a worktree's file),
// else cwd itself.
func AiderRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	for d := cwd; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return cwd
		}
		d = up
	}
}

const (
	aiderHeader = "# aider chat started at "
	// aiderRestored is what it says when --restore-chat-history loaded the
	// whole file back into the conversation.
	aiderRestored = "Restored previous conversation history."
	// aiderKeep is how many blocks of earlier sessions are kept, for a
	// session that restores them.
	aiderKeep = 2000
)

var (
	aiderHead    = regexp.MustCompile(`^<{5,9} SEARCH>?\s*$`)
	aiderDivider = regexp.MustCompile(`^={5,9}\s*$`)
	aiderUpdated = regexp.MustCompile(`^>{5,9} REPLACE\s*$`)
	aiderModel   = regexp.MustCompile(`^(?:Main model|Model): (\S+) with `)
	aiderTokens  = regexp.MustCompile(`^Tokens: ([\d.]+)(k?) sent`)
	aiderTouched = regexp.MustCompile(`^(?:Applied edit to|Added) (\S+?)(?: to the chat.*)?$`)
)

// Aider parses Aider's chat history file for the agent view: the latest
// session in it, or every session when the latest restored them.
type Aider struct {
	Cwd  string
	root string

	blocks []*pb.AgentBlock
	from   int // the first block of what's shown
	n      int // lines read: blocks' ids
	at     int64
	turn   string
	last   string // the kind of the last line: "user", "note", "reply", "" (a blank, a header)
	open   bool   // a message of its own goes on until a line ending in two spaces

	reply   []string // the reply being read, and where its blocks start
	replyAt int
	replyID string

	model   string
	tokens  int64
	files   map[string]bool
	rewound bool
}

var _ Parser = (*Aider)(nil)

// NewAider reads Aider's history for a pane in cwd.
func NewAider(cwd string) *Aider {
	return &Aider{Cwd: cwd, root: AiderRoot(cwd), files: map[string]bool{}}
}

func (a *Aider) Blocks() []*pb.AgentBlock        { return a.blocks[a.from:] }
func (a *Aider) Pending() *pb.AgentBlock         { return nil }
func (a *Aider) Image(string) (Image, bool)      { return Image{}, false }
func (a *Aider) Referenced(path string) bool     { return a.files[filepath.Clean(path)] }
func (a *Aider) Context() (string, int64, int64) { return a.model, a.tokens, 0 }

func (a *Aider) Rewound() bool {
	r := a.rewound
	a.rewound = false
	return r
}

// Feed parses one line and returns the blocks it added or changed.
func (a *Aider) Feed(b []byte) []*pb.AgentBlock {
	a.n++
	line := strings.TrimSuffix(string(b), "\r")
	if rest, ok := strings.CutPrefix(line, aiderHeader); ok {
		a.session(rest)
		return nil
	}
	if a.open {
		a.open = !strings.HasSuffix(line, "  ")
		return a.note(strings.TrimSuffix(line, "  "), true)
	}
	if text, ok := strings.CutPrefix(line, "#### "); ok && strings.HasSuffix(line, "  ") {
		return a.user(strings.TrimSuffix(text, "  "))
	}
	if (line == ">" || strings.HasPrefix(line, "> ")) && (a.last != "reply" || strings.HasSuffix(line, "  ")) {
		// Its own output; quoted in a reply (no line break) it's the reply's.
		a.open = !strings.HasSuffix(line, "  ")
		text := strings.TrimPrefix(strings.TrimPrefix(line, ">"), " ")
		return a.note(strings.TrimSuffix(text, "  "), a.last == "note")
	}
	if a.last == "reply" {
		a.reply = append(a.reply, line)
		return a.replyBlocks()
	}
	if strings.TrimSpace(line) == "" {
		a.last = ""
		return nil
	}
	a.last = "reply"
	a.reply, a.replyAt, a.replyID = []string{line}, len(a.blocks), a.id()
	return a.replyBlocks()
}

func (a *Aider) id() string { return "aider-" + strconv.Itoa(a.n) }

// session starts a new session (at: its local time): what's shown starts
// here unless it restores the earlier ones.
func (a *Aider) session(at string) {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(at), time.Local); err == nil {
		a.at = t.UnixMilli()
	}
	if drop := len(a.blocks) - aiderKeep; drop > 0 {
		a.blocks = append([]*pb.AgentBlock(nil), a.blocks[drop:]...)
	}
	a.from, a.rewound = len(a.blocks), true
	a.turn, a.last, a.open, a.reply = "", "", false, nil
	a.model, a.tokens = "", 0
}

func (a *Aider) add(b *pb.AgentBlock) []*pb.AgentBlock {
	if b.TurnId == "" {
		b.TurnId = a.turn
	}
	b.TimeUnixMs = a.at
	a.blocks = append(a.blocks, b)
	return []*pb.AgentBlock{b}
}

// replace puts b in place of the last block.
func (a *Aider) replace(b *pb.AgentBlock) []*pb.AgentBlock {
	a.blocks[len(a.blocks)-1] = b
	return []*pb.AgentBlock{b}
}

// user is a line of what you sent: one message, however many lines.
func (a *Aider) user(text string) []*pb.AgentBlock {
	if text == "<blank>" {
		text = ""
	}
	if a.last == "user" && len(a.blocks) > a.from {
		b := cloneBlock(a.blocks[len(a.blocks)-1])
		b.Text += "\n" + text
		return a.replace(b)
	}
	a.last = "user"
	id := a.id()
	a.turn = id
	return a.add(&pb.AgentBlock{Id: id, Kind: pb.AgentBlock_KIND_USER, Text: text, TurnId: id})
}

// note is a line of its own output: lines in a row are one note.
func (a *Aider) note(text string, more bool) []*pb.AgentBlock {
	a.learn(text)
	if more && len(a.blocks) > a.from && a.blocks[len(a.blocks)-1].Kind == pb.AgentBlock_KIND_NOTE {
		b := cloneBlock(a.blocks[len(a.blocks)-1])
		b.Text += "\n" + text
		a.last = "note"
		return a.replace(b)
	}
	a.last = "note"
	return a.add(&pb.AgentBlock{Id: a.id(), Kind: pb.AgentBlock_KIND_NOTE, Text: text})
}

// learn takes what its output says: the model, the context sent, the
// files it edited or added, and whether it restored earlier sessions.
func (a *Aider) learn(text string) {
	text = strings.TrimSpace(text)
	if m := aiderModel.FindStringSubmatch(text); m != nil {
		a.model = m[1]
	}
	if m := aiderTokens.FindStringSubmatch(text); m != nil {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			if m[2] == "k" {
				f *= 1000
			}
			a.tokens = int64(f)
		}
	}
	if m := aiderTouched.FindStringSubmatch(text); m != nil {
		a.ref(m[1])
	}
	if text == aiderRestored && a.from > 0 {
		a.from, a.rewound = 0, true
	}
}

// ref notes a file the session named, relative to the repository's top,
// if it's inside it.
func (a *Aider) ref(p string) {
	if p == "" || a.root == "" {
		return
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(a.root, p)
	}
	p = filepath.Clean(p)
	if r, err := filepath.Rel(a.root, p); err == nil && r != "." && !strings.HasPrefix(r, "..") {
		a.files[p] = true
	}
}

// replyBlocks are the reply read so far, as text and edits, in place of
// what it was before.
func (a *Aider) replyBlocks() []*pb.AgentBlock {
	segs := a.segments(a.reply)
	if len(a.blocks)-a.replyAt > len(segs) {
		a.rewound = true // fewer blocks than before: clients drop the rest
	}
	a.blocks = a.blocks[:a.replyAt]
	for i, b := range segs {
		b.Id = a.replyID
		if i > 0 {
			b.Id += "." + strconv.Itoa(i)
		}
		b.TurnId, b.TimeUnixMs = a.turn, a.at
		a.blocks = append(a.blocks, b)
	}
	return segs
}

// segments splits a reply into its text and its SEARCH/REPLACE edits
// (aider/coders/editblock_coder.py's find_original_update_blocks: an edit
// is the file's name, an optional fence, then the blocks).
func (a *Aider) segments(lines []string) []*pb.AgentBlock {
	var out []*pb.AgentBlock
	var text []string
	flush := func() {
		if t := strings.TrimSpace(strings.Join(text, "\n")); t != "" {
			out = append(out, &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TEXT, Text: t})
		}
		text = nil
	}
	file := ""
	for i := 0; i < len(lines); {
		if !aiderHead.MatchString(lines[i]) {
			text = append(text, lines[i])
			i++
			continue
		}
		div := i + 1
		for div < len(lines) && !aiderDivider.MatchString(lines[div]) {
			div++
		}
		end := div + 1
		for end < len(lines) && !aiderUpdated.MatchString(lines[end]) {
			end++
		}
		if end >= len(lines) {
			// Not all read yet (or not an edit): text until it is.
			text = append(text, lines[i:]...)
			break
		}
		name, drop := aiderFilename(text)
		text = text[:len(text)-drop]
		if name != "" {
			file = name
		}
		flush()
		out = append(out, a.edit(file, lines[i+1:div], lines[div+1:end]))
		i = end + 1
		if i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			i++ // the fence it opened
		}
	}
	flush()
	return out
}

func (a *Aider) edit(file string, old, nw []string) *pb.AgentBlock {
	a.ref(file)
	o, n := strings.Join(old, "\n"), strings.Join(nw, "\n")
	b := &pb.AgentBlock{Kind: pb.AgentBlock_KIND_TOOL, Tool: "edit", Input: file, Title: "Edited " + file}
	if file == "" {
		b.Title = "Edited a file"
	}
	b.Diff = clip(simpleDiff(o, n), maxDiff)
	b.Added, b.Removed = int32(len(nw)), int32(len(old))
	return b
}

// aiderFilename is the file an edit's name lines before it give (its
// find_filename: up to three lines back, past fences), and how many of
// text's last lines they are.
func aiderFilename(text []string) (string, int) {
	name, drop := "", 0
	for d := 1; d <= 3 && d <= len(text); d++ {
		l := strings.TrimSpace(text[len(text)-d])
		fence := strings.HasPrefix(l, "```")
		if n := aiderStripFilename(l); n != "" && name == "" {
			name, drop = n, d
		} else if fence && name == "" {
			drop = d
		}
		if !fence {
			break
		}
	}
	// The fence a name inside it opened (```\nname\n<<<<<<< SEARCH).
	if drop < len(text) && strings.HasPrefix(strings.TrimSpace(text[len(text)-drop-1]), "```") && name != "" {
		drop++
	}
	return name, drop
}

// aiderStripFilename is a line as a file's name, as its strip_filename
// reads one ("" when it isn't).
func aiderStripFilename(l string) string {
	l = strings.TrimSpace(l)
	if l == "" || l == "..." {
		return ""
	}
	if rest, ok := strings.CutPrefix(l, "```"); ok {
		if rest != "" && strings.ContainsAny(rest, "./") {
			return rest
		}
		return ""
	}
	l = strings.TrimRight(l, ":")
	l = strings.TrimLeft(l, "#")
	l = strings.TrimSpace(l)
	l = strings.Trim(l, "`")
	l = strings.Trim(l, "*")
	return l
}
