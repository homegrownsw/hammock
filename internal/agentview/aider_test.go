package agentview

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// testdata/aider/chat.history.md is a real .aider.chat.history.md, written
// by aider-chat 0.86.2 against a stand-in OpenAI-compatible server (its
// command lines' paths shortened): three sessions in one repository, the
// first an edit in two SEARCH/REPLACE blocks (with the .gitignore question
// answered), the second started with --restore-chat-history and answered
// in Markdown with a quoted line, the third a fresh one sent a message of
// two lines whose edits failed to match (its multi-line error messages, and
// its "Add file to the chat?" question).

func aiderBlocks(a *Aider) []string {
	var got []string
	for _, b := range a.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + strings.SplitN(b.Text, "\n", 2)[0]
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		got = append(got, s)
	}
	return got
}

// feedAider feeds the fixture's lines up to (not including) the line
// starting with stop ("" for all), and says whether it was rewound.
func feedAider(t *testing.T, a *Aider, stop string, nth int) (rewound bool) {
	t.Helper()
	b, err := os.ReadFile("testdata/aider/chat.history.md")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if stop != "" && strings.HasPrefix(l, stop) {
			if seen++; seen == nth {
				break
			}
		}
		a.Feed([]byte(l))
		if a.Rewound() {
			rewound = true
		}
	}
	return rewound
}

func TestAiderFirstSession(t *testing.T) {
	a := NewAider("")
	feedAider(t, a, "# aider chat started at", 2)
	want := []string{
		"NOTE You can skip this check with --no-gitignore",
		"USER make it greet by name",
		"TEXT I'll make it greet by name.",
		`TOOL Edited hello.py | -def hello(): / -    print("hello") / +def hello(name): / +    print(f"hello {name}")`,
		"TEXT And add a caller:",
		`TOOL Edited hello.py | + / +if __name__ == "__main__": / +    hello("world")`,
		"NOTE Tokens: 2.4k sent, 77 received.",
	}
	if got := aiderBlocks(a); !slices.Equal(got, want) {
		t.Fatalf("blocks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	bs := a.Blocks()
	if n := bs[0].Text; !strings.Contains(n, "(Y)es/(N)o [Yes]: y") || !strings.Contains(n, "Added hello.py to the chat.") {
		t.Errorf("first note: %q", n)
	}
	if bs[1].TurnId != bs[1].Id || bs[3].TurnId != bs[1].Id || bs[0].TurnId != "" {
		t.Errorf("turns: %q %q %q", bs[0].TurnId, bs[1].TurnId, bs[3].TurnId)
	}
	if e := bs[3]; e.Added != 2 || e.Removed != 2 || e.Input != "hello.py" || e.Pending {
		t.Errorf("edit: %+v", e)
	}
	if !strings.Contains(bs[6].Text, "Commit ") {
		t.Errorf("last note: %q", bs[6].Text)
	}
	if m, tok, _ := a.Context(); m != "openai/fake" || tok != 2400 {
		t.Errorf("context: %q %d", m, tok)
	}
	if bs[1].TimeUnixMs == 0 {
		t.Error("no time from the session's header")
	}
}

func TestAiderRestoredSessionShowsEarlierOnes(t *testing.T) {
	a := NewAider("")
	if !feedAider(t, a, "# aider chat started at", 3) {
		t.Error("a new session didn't rewind")
	}
	got := aiderBlocks(a)
	// The first session, then the second, which restored it.
	if len(got) < 10 || got[1] != "USER make it greet by name" {
		t.Fatalf("blocks:\n%s", strings.Join(got, "\n"))
	}
	tail := got[len(got)-4:]
	want := []string{
		"NOTE /Users/you/.local/bin/aider --openai-api-base http://127.0.0.1:18765/v1 --openai-api-key ...x --model openai/fake --no-check-update --no-show-model-warnings --analytics-disable --no-show-release-notes --yes-always --map-tokens 0 --edit-format diff --restore-chat-history --message what does it do?",
		"USER what does it do?",
		"TEXT #### Summary",
		"NOTE Tokens: 2.4k sent, 22 received.",
	}
	if !slices.Equal(tail, want) {
		t.Fatalf("tail:\n%s\nwant:\n%s", strings.Join(tail, "\n"), strings.Join(want, "\n"))
	}
	reply := a.Blocks()[len(a.Blocks())-2].Text
	if !strings.Contains(reply, "> A quoted line in the reply.") || !strings.HasSuffix(reply, "- two") {
		t.Errorf("a line quoted in a reply isn't the reply's: %q", reply)
	}
}

func TestAiderFreshSessionAfterRestored(t *testing.T) {
	a := NewAider("")
	feedAider(t, a, "", 0)
	got := aiderBlocks(a)
	if got[0] != "NOTE /Users/you/.local/bin/aider --openai-api-base http://127.0.0.1:18765/v1 --openai-api-key ...x --model openai/fake --no-check-update --no-show-model-warnings --analytics-disable --no-show-release-notes --yes-always --map-tokens 0 --edit-format diff --message thanks" {
		t.Fatalf("the latest session doesn't start the view:\n%s", strings.Join(got, "\n"))
	}
	bs := a.Blocks()
	if bs[0].Text != "/Users/you/.local/bin/aider --openai-api-base http://127.0.0.1:18765/v1 --openai-api-key ...x --model openai/fake --no-check-update --no-show-model-warnings --analytics-disable --no-show-release-notes --yes-always --map-tokens 0 --edit-format diff --message thanks\nnow e...xplain it\nAider v0.86.2\nModel: openai/fake with diff edit format\nGit repo: .git with 1 files\nRepo-map: disabled" {
		t.Errorf("a command line of two lines: %q", bs[0].Text)
	}
	if bs[1].Kind != pb.AgentBlock_KIND_USER || bs[1].Text != "thanks\nnow explain it" {
		t.Errorf("a message of two lines: %q", bs[1].Text)
	}
	// Its error of several lines, quoted on its first line only, is one
	// note with the SEARCH/REPLACE it quotes, not an edit.
	var errNote *pb.AgentBlock
	for _, b := range bs {
		if strings.Contains(b.Text, "SEARCH/REPLACE block failed to match!") {
			errNote = b
			break
		}
	}
	if errNote == nil || errNote.Kind != pb.AgentBlock_KIND_NOTE || !strings.HasPrefix(errNote.Text, "Tokens: 2.5k sent, 77 received.\nThe LLM did not conform") ||
		!strings.Contains(errNote.Text, "<<<<<<< SEARCH") || !strings.HasSuffix(errNote.Text, "Just reply with fixed versions of the block above that failed to match.\nCommit 13459ca feat: Greet by name") {
		t.Fatalf("error note: %+v", errNote)
	}
	edits := 0
	for _, b := range bs {
		if b.Kind == pb.AgentBlock_KIND_TOOL {
			edits++
		}
	}
	if edits != 8 { // four replies of two edits
		t.Errorf("%d edits, want 8", edits)
	}
	last := bs[len(bs)-1]
	if last.Kind != pb.AgentBlock_KIND_NOTE || !strings.HasSuffix(last.Text, "Only 3 reflections allowed, stopping.") {
		t.Errorf("last: %+v", last)
	}
	if _, tok, _ := a.Context(); tok != 3100 {
		t.Errorf("context tokens %d, want 3100 (its last reply's)", tok)
	}
}

func TestAiderReferencedFilesInRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	_ = os.Mkdir(sub, 0o755)
	if AiderRoot(sub) != dir {
		t.Fatalf("root of %s: %s", sub, AiderRoot(sub))
	}
	a := NewAider(sub)
	for _, l := range []string{
		"# aider chat started at 2026-11-07 10:45:50", "", "#### fix it  ", "",
		"src/x.go", "```go", "<<<<<<< SEARCH", "a", "=======", "b", ">>>>>>> REPLACE", "```", "",
		"../../etc/passwd", "```", "<<<<<<< SEARCH", "=======", "x", ">>>>>>> REPLACE", "```", "",
		"> Applied edit to src/x.go  ", "> Added docs/y.md to the chat  ",
	} {
		a.Feed([]byte(l))
	}
	for p, want := range map[string]bool{
		filepath.Join(dir, "src/x.go"): true, filepath.Join(dir, "docs/y.md"): true, "/etc/passwd": false,
		filepath.Join(dir, "../etc/passwd"): false,
	} {
		if a.Referenced(p) != want {
			t.Errorf("Referenced(%s) = %v", p, !want)
		}
	}
	if got := aiderBlocks(a); !slices.Equal(got, []string{
		"USER fix it", "TOOL Edited src/x.go | -a / +b", "TOOL Edited ../../etc/passwd | +x", "NOTE Applied edit to src/x.go",
	}) {
		t.Errorf("blocks:\n%s", strings.Join(got, "\n"))
	}
}

func TestAiderFilenames(t *testing.T) {
	for _, c := range []struct {
		text []string
		name string
		drop int
	}{
		{[]string{"Change it:", "", "hello.py", "```python"}, "hello.py", 2},
		{[]string{"Change it:", "```", "hello.py"}, "hello.py", 2},                        // diff-fenced
		{[]string{"Change it:", "```python", "src/app.py", "```python"}, "src/app.py", 3}, // a fence before the name too
		{[]string{"Change it:", "", "```python"}, "", 1},                                  // the last file's
		{[]string{"**hello.py**", "```python"}, "hello.py", 2},
		{[]string{"```hello.py"}, "hello.py", 1},
	} {
		if name, drop := aiderFilename(c.text); name != c.name || drop != c.drop {
			t.Errorf("%q: %q %d, want %q %d", c.text, name, drop, c.name, c.drop)
		}
	}
}

// A reply arrives a line at a time: an edit not finished yet is text until
// it is, and then the text's block stays where it was.
func TestAiderReplyAsItArrives(t *testing.T) {
	a := NewAider("")
	for _, l := range []string{"# aider chat started at 2026-11-07 10:45:50", "#### go  ", "", "Here:", "", "a.py", "```python", "<<<<<<< SEARCH", "x", "======="} {
		a.Feed([]byte(l))
	}
	if got := aiderBlocks(a); len(got) != 2 || !strings.HasPrefix(got[1], "TEXT Here:") {
		t.Fatalf("unfinished: %q", got)
	}
	textID := a.Blocks()[1].Id
	for _, l := range []string{"y", ">>>>>>> REPLACE", "```", "", "Done."} {
		a.Feed([]byte(l))
	}
	got := aiderBlocks(a)
	if !slices.Equal(got, []string{"USER go", "TEXT Here:", "TOOL Edited a.py | -x / +y", "TEXT Done."}) {
		t.Fatalf("blocks: %q", got)
	}
	if a.Blocks()[1].Id != textID || a.Blocks()[1].Text != "Here:" {
		t.Errorf("text block %q %q", a.Blocks()[1].Id, a.Blocks()[1].Text)
	}
	ids := map[string]bool{}
	for _, b := range a.Blocks() {
		if ids[b.Id] {
			t.Errorf("id %s twice", b.Id)
		}
		ids[b.Id] = true
	}
}
