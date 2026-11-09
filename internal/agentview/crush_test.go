package agentview

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// testdata/crush/session.jsonl is a Crush session as agents.Crush reads it
// out of Crush's database: a hammock_session line, then its messages rows,
// their parts made as Crush v0.98.1 marshals them
// (internal/message/message.go marshalParts, content.go; a bash call and
// its result shaped as a real crush v0.98.1 run against a stand-in
// OpenAI-compatible server wrote them): text with an image attached,
// thinking, bash, view, edit, write (with its diff in the result's
// metadata), an MCP tool, and a turn canceled mid-call.
func TestCrushBlocks(t *testing.T) {
	c := NewCrush("")
	f, err := os.Open("testdata/crush/session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c.Feed(sc.Bytes())
	}
	var got []string
	for _, b := range c.Blocks() {
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
	want := []string{
		"USER The test in parse_test.go fails, fix it [image]",
		"THINKING Let me run the tests first.",
		"TEXT I'll run the tests.",
		"TOOL Run the tests → --- FAIL: TestParse (0.00s) parse_test.go:12: got 2, want 3 FAIL",
		"TOOL Read parse.go",
		"TOOL Edited parse.go | -return n - 1 / +return n",
		"TOOL Wrote NOTES.md | +fixed",
		"TOOL Used create_issue (github) → Created issue #1",
		"TEXT Fixed: `parse` was off by one.",
		"USER Now run them all",
		"TOOL Ran `go test -race ./...` (failed)",
		"NOTE Canceled",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if m, tokens, _ := c.Context(); m != "claude-sonnet-4-5" || tokens != 18234 {
		t.Errorf("context = %q, %d", m, tokens)
	}
	if !c.Referenced("/Users/me/app/parse.go") || !c.Referenced("/Users/me/app/NOTES.md") {
		t.Error("the files it edited aren't referenced")
	}
	if c.Cwd != "/Users/me/app" {
		t.Errorf("cwd = %q", c.Cwd)
	}
	if img, ok := c.Image("img:m1:1"); !ok || img.MediaType != "image/png" || len(img.Data) == 0 {
		t.Errorf("image = %+v, %v", img, ok)
	}
}

// A reply read while it streams: its call's input is still partial JSON.
func TestCrushStreaming(t *testing.T) {
	c := NewCrush("/w")
	c.Feed([]byte(`{"type":"message","id":"u","role":"user","parts":[{"type":"text","data":{"text":"go"}}],"created_at":1}`))
	c.Feed([]byte(`{"type":"message","id":"a","role":"assistant","parts":[{"type":"text","data":{"text":"Look"}},{"type":"tool_call","data":{"id":"t1","name":"bash","input":"{\"comm","finished":false}}],"created_at":2}`))
	if p := c.Pending(); p == nil || p.Title != "Ran ``" {
		t.Fatalf("pending = %v", p)
	}
	// The hidden continuation Crush sends itself isn't yours.
	c.Feed([]byte(`{"type":"message","id":"h","role":"user","parts":[{"type":"text","data":{"text":"continue","hidden":true}}],"created_at":3}`))
	if n := len(c.Blocks()); n != 3 {
		t.Errorf("%d blocks, want 3", n)
	}
}
