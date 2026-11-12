package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// testdata/pi/session.jsonl is a real pi 1.5.0 session (npm
// @earendil-works/pi-coding-agent), its system prompt shortened and its
// folder renamed: pi in RPC mode against a stand-in OpenAI-compatible
// server (bash, write, edit, read and a failing bash, your own !command,
// /name, a second prompt), then, through pi's own SessionManager, a label,
// a /tree branch back to the first answer with a summary, a message with
// an image, a reply with thinking from another model, a compaction and a
// failed reply.
func piBlocks(t *testing.T, p *Pi) []string {
	t.Helper()
	var got []string
	for _, b := range p.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Output != "" {
			s += " → " + oneLine(b.Output)
		}
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Error {
			s += " (failed)"
		}
		if b.Pending {
			s += " (pending)"
		}
		if len(b.Images) > 0 {
			s += " [image]"
		}
		got = append(got, s)
	}
	return got
}

func feedPi(t *testing.T, p *Pi, path string) (rewound bool) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		p.Feed(sc.Bytes())
		if p.Rewound() {
			rewound = true
		}
	}
	return rewound
}

func TestPiBlocks(t *testing.T) {
	p := NewPi("")
	if !feedPi(t, p, "testdata/pi/session.jsonl") {
		t.Error("the /tree branch didn't replace what's shown")
	}
	want := []string{
		"USER make notes",
		"TEXT Let me check.",
		"TOOL Ran `echo hi` → hi",
		"TEXT Writing it.",
		"TOOL Wrote notes.txt | +one / +two",
		"TOOL Edited notes.txt | one / -two / +three",
		"TOOL Read notes.txt → one three",
		"TOOL Ran `exit 3` → (no output) Command exited with code 3 (failed)",
		"TEXT Done: notes.txt now says one and three.",
		// The branch left behind (your !command, /name, "again please") is gone.
		"NOTE Summarized the branch left behind: Asked again; got the same answer.",
		"USER try another way [image]",
		"THINKING Consider sed.",
		"TEXT Using sed instead.",
		"NOTE Conversation compacted",
		"NOTE Error: 429 rate limit exceeded",
	}
	got := piBlocks(t, p)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if p.Cwd != "/Users/me/proj" || !p.Referenced("/Users/me/proj/notes.txt") {
		t.Errorf("cwd %q, notes.txt referenced %v", p.Cwd, p.Referenced("/Users/me/proj/notes.txt"))
	}
	// The failed reply's usage doesn't count; the last good one's does.
	if m, tok, _ := p.Context(); m != "fake-2" || tok != 2510 {
		t.Errorf("context %s %d", m, tok)
	}
	var img string
	for _, b := range p.Blocks() {
		if len(b.Images) > 0 {
			img = b.Images[0].Ref
		}
	}
	if i, ok := p.Image(img); !ok || i.MediaType != "image/png" || len(i.Data) == 0 {
		t.Errorf("image %q: %v %v", img, ok, i.MediaType)
	}
	// Every block of a turn belongs to its message.
	for _, b := range p.Blocks()[:9] {
		if b.TurnId != p.Blocks()[0].Id {
			t.Errorf("%s%s: turn %q", b.Title, b.Text, b.TurnId)
		}
	}
}

// Before the branch: the whole line of the conversation, with your own
// command and the second prompt.
func TestPiBlocksBeforeBranch(t *testing.T) {
	data, err := os.ReadFile("testdata/pi/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	p := NewPi("")
	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, `"type":"label"`) {
			break
		}
		p.Feed([]byte(l))
	}
	got := piBlocks(t, p)
	tail := strings.Join(got[len(got)-3:], "\n")
	want := "TOOL You ran `echo from me` → from me\nUSER again please\nTEXT Done: notes.txt now says one and three."
	if tail != want {
		t.Fatalf("got\n%s\nwant\n%s", tail, want)
	}
	if p.Rewound() {
		t.Error("rewound without a branch")
	}
}

func TestPiPendingAndLegacyEdit(t *testing.T) {
	p := NewPi("/work")
	p.Feed([]byte(`{"type":"session","version":3,"id":"s1","timestamp":"2026-11-07T10:00:00.000Z","cwd":"/work"}`))
	p.Feed([]byte(`{"type":"message","id":"a1","parentId":null,"timestamp":"2026-11-07T10:00:01.000Z","message":{"role":"user","content":"fix it","timestamp":1}}`))
	p.Feed([]byte(`{"type":"message","id":"a2","parentId":"a1","timestamp":"2026-11-07T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"t1","name":"edit","arguments":{"path":"/work/x.go","oldText":"a\nb","newText":"c"}}],"model":"m","usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":12},"stopReason":"toolUse","timestamp":2}}`))
	pend := p.Pending()
	if pend == nil || pend.Title != "Edited x.go" || pend.Diff != "-a\n-b\n+c\n" || pend.Added != 1 || pend.Removed != 2 {
		t.Fatalf("pending: %v", pend)
	}
	p.Feed([]byte(`{"type":"message","id":"a3","parentId":"a2","timestamp":"2026-11-07T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"t1","toolName":"edit","content":[{"type":"text","text":"Could not find the text"}],"isError":true,"timestamp":3}}`))
	if p.Pending() != nil {
		t.Fatal("still pending")
	}
	if b := p.Blocks()[1]; !b.Error || b.Output != "Could not find the text" {
		t.Fatalf("result: %v", b)
	}
	// Aborted (Esc): noted, its usage not counted.
	p.Feed([]byte(`{"type":"message","id":"a4","parentId":"a3","timestamp":"2026-11-07T10:00:04.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Trying"}],"model":"m","usage":{"input":99,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":99},"stopReason":"aborted","timestamp":4}}`))
	got := piBlocks(t, p)
	if got[len(got)-1] != "NOTE Interrupted" {
		t.Fatalf("got %v", got)
	}
	if _, tok, _ := p.Context(); tok != 12 {
		t.Fatalf("tokens %d", tok)
	}
}
