package thread

import (
	"regexp"
	"strings"
	"testing"
)

func TestNewIDIsUUIDv7AndOrdered(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a := NewID()
	if !re.MatchString(a) {
		t.Fatalf("not a UUIDv7: %s", a)
	}
	if b := NewID(); b[:13] < a[:13] {
		t.Fatalf("ids not time ordered: %s then %s", a, b)
	}
}

func TestSealChainsHashes(t *testing.T) {
	recs := []Record{{Kind: KindUser, Text: "hi"}, {Kind: KindAssistant, Text: "hello"}}
	Seal(recs)
	if recs[1].Seq != 1 || recs[0].Hash == "" || recs[1].Hash == recs[0].Hash {
		t.Fatalf("%+v", recs)
	}
	h := recs[1].Hash
	recs[0].Text = "changed"
	Seal(recs)
	if recs[1].Hash == h {
		t.Fatal("changing an earlier record didn't change later hashes")
	}
}

func TestClipKeepsEnds(t *testing.T) {
	s := strings.Repeat("a", MaxIO) + "MIDDLE" + strings.Repeat("z", MaxIO)
	c := Clip(s)
	if len(c) > MaxIO+64 || !strings.HasPrefix(c, "aaa") || !strings.HasSuffix(c, "zzz") || strings.Contains(c, "MIDDLE") {
		t.Fatalf("clip len %d", len(c))
	}
}

func TestBrief(t *testing.T) {
	recs := []Record{
		{Kind: KindUser, Text: "Spike the pane holder"},
		{Kind: KindToolCall, Tool: ToolWrite, Edit: &Edit{Path: "holder/ring.go"}},
		{Kind: KindToolCall, Tool: ToolShell, Input: []byte(`{"command":"go test ./holder/..."}`)},
		{Kind: KindUser, Text: "also add a chaos test"},
		{Kind: KindAssistant, Text: "Chaos test written; it passes 20/20."},
	}
	b := Brief(Thread{Harness: "claude", Cwd: "/src/hammock"}, recs)
	for _, want := range []string{"Spike the pane holder", "also add a chaos test", "holder/ring.go", "go test ./holder/...", "passes 20/20", "claude"} {
		if !strings.Contains(b, want) {
			t.Errorf("brief missing %q:\n%s", want, b)
		}
	}
}

func TestUnwrapPasted(t *testing.T) {
	in := "<pasted_content id=\"f757\">\nPlan needs some updates.\n\nBrowsers panes can be marked done.\n</pasted_content id=\"f757\">\n"
	if got := UnwrapPasted(in); strings.TrimSpace(got) != "Plan needs some updates.\n\nBrowsers panes can be marked done." {
		t.Fatalf("got %q", got)
	}
	mixed := "look at this:\n<pasted_content id=\"0a1b\">\nsome log\n</pasted_content id=\"0a1b\">\nthanks"
	if got := UnwrapPasted(mixed); got != "look at this:\nsome log\nthanks" {
		t.Fatalf("mixed: %q", got)
	}
	if s := "a <pasted_content id=\"zz\"> b"; UnwrapPasted(s) != s {
		t.Fatal("not a wrapper, changed")
	}
}
