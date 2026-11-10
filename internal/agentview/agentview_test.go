package agentview

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func feedFile(t *testing.T, path string) *Claude {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c := NewClaude("/private/tmp/agentview-spike")
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		c.Feed(sc.Bytes())
	}
	return c
}

// The spike session: a two-question AskUserQuestion, a Bash permission, a
// plan rejected with feedback, and a free-text answer.
func TestClaudeSpikeSession(t *testing.T) {
	c := feedFile(t, "testdata/spike.jsonl")
	var kinds []string
	for _, b := range c.Blocks() {
		kinds = append(kinds, strings.TrimPrefix(b.Kind.String(), "KIND_"))
	}
	t.Log(strings.Join(kinds, " "))
	var q, bash, plan *pb.AgentBlock
	users := 0
	for _, b := range c.Blocks() {
		switch {
		case b.Kind == pb.AgentBlock_KIND_QUESTION && q == nil:
			q = b
		case b.Kind == pb.AgentBlock_KIND_TOOL && b.Tool == "Bash":
			bash = b
		case b.Kind == pb.AgentBlock_KIND_PLAN:
			plan = b
		case b.Kind == pb.AgentBlock_KIND_USER:
			users++
		}
	}
	if users != 4 {
		t.Errorf("%d user messages, want 4", users)
	}
	if q == nil || len(q.Questions) != 2 || !q.Questions[1].MultiSelect || q.Questions[0].Answer != "Green" || q.Questions[1].Answer != "Apple, Plum" {
		t.Errorf("question block: %v", q)
	}
	if bash == nil || bash.Pending || bash.Input != "touch /tmp/agentview-spike/hello.txt" {
		t.Errorf("bash block: %v", bash)
	}
	if plan == nil || !strings.Contains(plan.Text, "README") || !plan.Error {
		t.Errorf("plan block (rejected with feedback): %v", plan)
	}
	if c.Pending() != nil {
		t.Errorf("nothing should be pending at the end")
	}
	if !c.Referenced("/private/tmp/agentview-spike/README.md") && !c.Referenced("/tmp/agentview-spike/hello.txt") {
		t.Log("note: no referenced paths recorded")
	}
}

func TestReadScreen(t *testing.T) {
	for _, tc := range []struct {
		file     string
		options  int
		first    string
		selected int
		title    string
	}{
		{"permission.txt", 4, "Yes", 1, "Do you want to proceed?"},
		{"questions.txt", 5, "Red", 1, "What is your favourite colour?"},
		{"submit.txt", 2, "Submit answers", 1, "Ready to submit your answers?"},
		{"plan.txt", 3, "Yes, and use auto mode", 1, "Claude has written up a plan and is ready to execute. Would you like to proceed?"},
		{"codex-permission.txt", 3, "Yes, proceed (y)", 1, "$ touch hello.txt"},
	} {
		b, err := os.ReadFile("testdata/" + tc.file)
		if err != nil {
			t.Fatal(err)
		}
		sc := ReadScreen(string(b))
		if len(sc.Options) != tc.options || sc.Options[0] != tc.first || sc.Selected != tc.selected || sc.Title != tc.title {
			t.Errorf("%s: %+v", tc.file, sc)
		}
	}
	if sc := ReadScreen("❯ hello\n"); len(sc.Options) != 0 {
		t.Errorf("no menu expected: %+v", sc)
	}
}

// The working row shows the word Claude Code picked ("Hatching…"), or nothing
// (the app's "Working…") when there's no spinner line to read it from.
func TestSpinnerStatus(t *testing.T) {
	bar := strings.Repeat("─", 40)
	for _, c := range []struct{ screen, want string }{
		{"⏺ Reading files\n\n✽ Hatching… (23s · ↓ 1.9k tokens · thought for 2s)\n\n" + bar + "\n❯ \n" + bar + "\n  ⏵⏵ auto mode on\n", "Hatching…"},
		{"✻ Percolating… (12s · esc to interrupt)\n> ", "Percolating…"},
		{"· Running the tests… (1m 4s · ↑ 312 tokens)\n" + bar + "\n❯ \n", "Running the tests…"},
		{"✶ Clauding…\n" + bar + "\n❯ \n", "Clauding…"},
		{"⏺ Done.\n\n✻ Worked for 23s\n\n" + bar + "\n❯ \n", ""},
		{"⏺ Writing files…\n" + bar + "\n❯ \n", ""},
		{"❯ hello\n", ""},
		{"• Working (12s • esc to interrupt)\n› ", "Working (12s • esc to interrupt)"},
		// Words from the conversation aren't the spinner, even quoting it.
		{"⏺ It only read the line when it contained \"esc to interrupt\". Current\n  status line when it contained \"esc to interrupt\" (it's now `✽ Hatching… (23s)`)\n\n" + bar + "\n❯ \n", ""},
	} {
		if got := ReadScreen(c.screen).Status; got != c.want {
			t.Errorf("status of %q = %q, want %q", c.screen, got, c.want)
		}
	}
	if sc := ReadScreen("  the status line when it contained \"esc to interrupt\"\n❯ \n"); sc.Working {
		t.Error("quoted words read as working")
	}
}

func TestAnswerSteps(t *testing.T) {
	b, _ := os.ReadFile("testdata/questions.txt")
	sc := ReadScreen(string(b))
	cur := Prompt(sc, nil)
	if cur.Kind != pb.AgentPrompt_KIND_QUESTION || cur.Other != 4 || cur.Descriptions[0] != "Red" || cur.Tabs == "" {
		t.Fatalf("prompt %+v", cur)
	}
	steps, err := AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: cur.Options, Option: 2}, false, true)
	if err != nil || len(steps) != 1 || string(steps[0].Keys) != "2" || !steps[0].Ready(sc) {
		t.Fatalf("steps %v, %v", steps, err)
	}
	steps, _ = AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: cur.Options, Option: 4, Text: "hot chocolate"}, false, true)
	if len(steps) != 3 || string(steps[1].Keys) != "hot chocolate" || string(steps[2].Keys) != "\x1b[13u" {
		t.Errorf("typed answer: %v", steps)
	}
	// Multi-select: tick 1 and 3 when 1 is already ticked.
	multi := &pb.AgentPrompt{Options: []string{"Apple", "Pear", "Plum", "Type something"}, MultiSelect: true, Checked: []bool{true, false, false, false}}
	steps, _ = AnswerSteps(multi, &pb.AgentAnswer{OptionsSeen: multi.Options, Toggles: []int32{1, 3}}, false, true)
	var keys []string
	for _, s := range steps {
		keys = append(keys, string(s.Keys))
	}
	if strings.Join(keys, " ") != "3 \x1b[C" {
		t.Errorf("multi keys %q", keys)
	}
	// A prompt that changed since the client saw it is refused.
	if _, err := AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: []string{"Yes", "No"}, Option: 1}, false, true); err != ErrMismatch {
		t.Errorf("want ErrMismatch, got %v", err)
	}
}

// Needs You's Allow / Deny: only while a prompt shows, and only into that one.
func TestDecisionSteps(t *testing.T) {
	for _, f := range []string{"permission.txt", "codex-permission.txt"} {
		b, _ := os.ReadFile("testdata/" + f)
		sc := ReadScreen(string(b))
		allow, err := DecisionSteps(sc, true, true)
		if err != nil || len(allow) != 1 || string(allow[0].Keys) != "\x1b[13u" || !allow[0].Ready(sc) {
			t.Errorf("%s allow: %v, %v", f, allow, err)
		}
		deny, _ := DecisionSteps(sc, false, false)
		if len(deny) != 1 || string(deny[0].Keys) != "\x1b" || !deny[0].Ready(sc) || deny[0].Ready(Screen{}) {
			t.Errorf("%s deny: %v", f, deny)
		}
	}
	// Moved off the first option: its number instead of Enter.
	if s, _ := DecisionSteps(Screen{Options: []string{"Yes", "No"}, Selected: 2}, true, false); string(s[0].Keys) != "1" {
		t.Errorf("allow with No selected: %q", s[0].Keys)
	}
	if _, err := DecisionSteps(ReadScreen("$ ls\nfoo bar\n$ "), true, false); err != ErrNotAsking {
		t.Errorf("want ErrNotAsking, got %v", err)
	}
}

// Claude Code's workspace trust dialog: a menu without numbers (#47).
func TestTrustDialog(t *testing.T) {
	b, err := os.ReadFile("testdata/trust.txt")
	if err != nil {
		t.Fatal(err)
	}
	sc := ReadScreen(string(b))
	if !sc.Arrows || len(sc.Options) != 2 || sc.Options[0] != "No, exit" || sc.Options[1] != "Yes, I trust this folder" || sc.Selected != 1 {
		t.Fatalf("screen %+v", sc)
	}
	if !strings.HasPrefix(sc.Title, "Accessing workspace:\n\n/Users/me\n\nQuick safety check: Is this a project you created or one you trust? (Like your own code, a well-known open source project, or work") {
		t.Errorf("title %q", sc.Title)
	}
	cur := Prompt(sc, nil)
	if cur.Kind != pb.AgentPrompt_KIND_QUESTION || len(cur.Options) != 2 {
		t.Fatalf("prompt %+v", cur)
	}
	// Picking "Yes": ↓, then Enter once the screen shows it selected.
	steps, err := AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: cur.Options, Option: 2}, sc.Arrows, true)
	if err != nil || len(steps) != 2 || string(steps[0].Keys) != "\x1b[B" || string(steps[1].Keys) != "\x1b[13u" || !steps[0].Ready(sc) || steps[1].Ready(sc) {
		t.Fatalf("steps %v, %v", steps, err)
	}
	moved := sc
	moved.Selected = 2
	if !steps[1].Ready(moved) {
		t.Error("Enter not ready with Yes selected")
	}
	// Picking the selected one is just Enter.
	if steps, _ := AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: cur.Options, Option: 1}, sc.Arrows, false); len(steps) != 1 || string(steps[0].Keys) != "\r" {
		t.Errorf("enter only: %v", steps)
	}
	// Needs You's Allow trusts the folder, not option 1 ("No, exit").
	if allow, err := DecisionSteps(sc, true, true); err != nil || len(allow) != 2 || string(allow[0].Keys) != "\x1b[B" {
		t.Errorf("allow %v, %v", allow, err)
	}
	// Typed text has nowhere to go.
	if _, err := AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: cur.Options, Option: 1, Text: "hi"}, sc.Arrows, false); err == nil {
		t.Error("typed text accepted")
	}
	// ↑ from the third of three, a line at a time, each gated on the last.
	up := arrowSteps(3, 1, []string{"a", "b", "c"}, false)
	if len(up) != 3 || string(up[0].Keys) != "\x1b[A" || string(up[1].Keys) != "\x1b[A" ||
		!up[1].Ready(Screen{Options: []string{"a", "b", "c"}, Selected: 2}) || up[1].Ready(Screen{Options: []string{"a", "b", "c"}, Selected: 3}) {
		t.Errorf("up steps %v", up)
	}
	// The input box, and the dialog quoted in a reply above it, aren't menus.
	quoted := "⏺ It shows:\n\n  Security guide\n\n  ❯ No, exit\n    Yes, I trust this folder\n\n  Enter to confirm · Esc to cancel\n\n───\n❯ \n───\n  ? for shortcuts\n"
	for _, s := range []string{"❯ hello\n", "❯ hello\n  world\n\n Esc to cancel\n", "text\n\n› hello\n  world\n\n Esc to cancel\n", "\n ❯ one\n ❯ two\n\n Enter to confirm\n", quoted} {
		if sc := ReadScreen(s); len(sc.Options) != 0 {
			t.Errorf("%q: no menu expected: %+v", s, sc)
		}
	}
}

func TestClaudeMidTurnMessage(t *testing.T) {
	c := NewClaude("/tmp")
	c.Feed([]byte(`{"type":"user","uuid":"u1","timestamp":"2026-10-30T17:00:00Z","message":{"role":"user","content":"Build the devices screen"}}`))
	c.Feed([]byte(`{"type":"attachment","uuid":"a1","timestamp":"2026-10-30T17:01:00Z","attachment":{"type":"queued_command","prompt":"Make it a tab instead"}}`))
	c.Feed([]byte(`{"type":"attachment","uuid":"a2","timestamp":"2026-10-30T17:01:00Z","attachment":{"type":"file","filename":"x.go"}}`))
	bs := c.Blocks()
	if len(bs) != 2 || bs[1].Kind != pb.AgentBlock_KIND_USER || bs[1].Text != "Make it a tab instead" {
		t.Fatalf("blocks %v", bs)
	}
}

func TestClaudeInterruptedMessageIsReplaced(t *testing.T) {
	c := NewClaude("/tmp")
	c.Feed([]byte(`{"type":"user","uuid":"u0","parentUuid":"p","timestamp":"2026-10-30T17:00:00Z","message":{"role":"user","content":"First"}}`))
	c.Feed([]byte(`{"type":"assistant","uuid":"a0","parentUuid":"u0","timestamp":"2026-10-30T17:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"Done"}]}}`))
	// Sent, then interrupted before Claude answered: the next message
	// branches from the same point and Claude Code shows only it.
	c.Feed([]byte(`{"type":"user","uuid":"u1","parentUuid":"a0","timestamp":"2026-10-30T17:01:00Z","message":{"role":"user","content":"Some iOS notes:"}}`))
	c.Feed([]byte(`{"type":"user","uuid":"u2","parentUuid":"a0","timestamp":"2026-10-30T17:01:05Z","message":{"role":"user","content":"Some iOS notes: the whole list"}}`))
	if !c.Rewound() {
		t.Fatal("not marked rewound")
	}
	var texts []string
	for _, b := range c.Blocks() {
		texts = append(texts, b.Text)
	}
	if strings.Join(texts, "|") != "First|Done|Some iOS notes: the whole list" {
		t.Fatalf("blocks %q", texts)
	}
	if c.Rewound() {
		t.Fatal("rewound reported twice")
	}
}

func TestPromptTakesFullTextFromTheCall(t *testing.T) {
	screen := "│ How does a guest's Mac reach yours? A shared Group runs on your\n│ Mac, so it needs a path.\n\n❯ 1. Tailscale only\n     Ships now. Colleagues on your tailnet just work; others ne…\n  2. Build a relay now\n     Works with anyone, anywhere, but means building and ho…\n  3. Type something.\n\nEnter to select · Esc to cancel\n"
	sc := ReadScreen(screen)
	if !strings.HasPrefix(sc.Title, "How does a guest's Mac") || strings.Contains(sc.Title, "│") {
		t.Fatalf("title %q", sc.Title)
	}
	call := &pb.AgentBlock{Id: "q", Kind: pb.AgentBlock_KIND_QUESTION, Questions: []*pb.AgentQuestion{{
		Question: "How does a guest's Mac reach yours? (the whole question)",
		Options: []*pb.AgentOption{
			{Label: "Tailscale only", Description: "Ships now. Colleagues on your tailnet just work; others need you to share the Mac."},
			{Label: "Build a relay now", Description: "Works with anyone, anywhere, but means building and hosting a server."},
		},
	}}}
	p := Prompt(sc, call)
	if p.Title != "How does a guest's Mac reach yours? (the whole question)" || !strings.HasSuffix(p.Descriptions[0], "share the Mac.") || !strings.HasSuffix(p.Descriptions[1], "a server.") {
		t.Fatalf("prompt %+v", p)
	}
}

func TestClaudeTaskNotificationIsANote(t *testing.T) {
	c := NewClaude("/tmp")
	// As Claude Code writes it: a user message, from a background subagent.
	msg := "<task-notification>\n<task-id>a1</task-id>\n<status>completed</status>\n<summary>Agent \"Review the diff\" finished</summary>\n<note>It may notify again.</note>\n<result>I found 2 issues.\n\n- One\n- Two</result>\n</task-notification>"
	line, _ := json.Marshal(map[string]any{"type": "user", "uuid": "n1", "timestamp": "2026-11-02T17:00:00Z",
		"origin":  map[string]any{"kind": "task-notification"},
		"message": map[string]any{"role": "user", "content": msg}})
	c.Feed(line)
	bs := c.Blocks()
	if len(bs) != 1 || bs[0].Kind != pb.AgentBlock_KIND_NOTE || bs[0].Text != `Agent "Review the diff" finished` || bs[0].Output != "I found 2 issues.\n\n- One\n- Two" {
		t.Fatalf("got %v", bs)
	}
}

func TestClaudeCompactionSummaryIsTuckedUnderTheNote(t *testing.T) {
	c := NewClaude("/tmp")
	c.Feed([]byte(`{"type":"system","subtype":"compact_boundary","uuid":"b1","content":"Conversation compacted","timestamp":"2026-10-30T01:00:00Z"}`))
	c.Feed([]byte(`{"type":"user","uuid":"s1","parentUuid":"b1","isCompactSummary":true,"timestamp":"2026-10-30T01:00:01Z","message":{"role":"user","content":"This session is being continued…"}}`))
	bs := c.Blocks()
	if len(bs) != 1 || bs[0].Kind != pb.AgentBlock_KIND_NOTE || bs[0].Output != "This session is being continued…" {
		t.Fatalf("got %v", bs)
	}
}

func TestClaudeSentFilesShowTheirImages(t *testing.T) {
	c := NewClaude("/work")
	c.Feed([]byte(`{"type":"assistant","uuid":"a1","timestamp":"2026-10-31T09:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"SendUserFile","input":{"files":["/tmp/shot.png","notes.md"],"caption":"Before and after"}}]}}`))
	bs := c.Blocks()
	if len(bs) != 1 || bs[0].Title != "Sent you 2 files" || bs[0].Text != "Before and after" {
		t.Fatalf("block %v", bs)
	}
	if len(bs[0].Images) != 1 || bs[0].Images[0].Ref != "file:/tmp/shot.png" || bs[0].Images[0].MediaType != "image/png" {
		t.Fatalf("images %v", bs[0].Images)
	}
	if !c.Referenced("/tmp/shot.png") || !c.Referenced("/work/notes.md") {
		t.Fatal("sent files should be servable")
	}
}

// A pasted screenshot reaches Claude as a JPEG (its transparency black); the
// hidden line after it names the original PNG, served instead.
func TestClaudePastedImageKeepsItsOriginal(t *testing.T) {
	c := NewClaude("/tmp")
	jpeg := `{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"/9j/"}}`
	meta := func(path string) []byte {
		return []byte(`{"type":"user","uuid":"m","isMeta":true,"sessionId":"s1","timestamp":"2026-11-04T09:00:00Z","message":{"role":"user","content":[{"type":"text","text":"[Image: source: ` + path + `]"}]}}`)
	}
	dir := "/private/tmp/claude-501/proj/s1/images/"
	// Two in one message, each named on its own line.
	c.Feed([]byte(`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-11-04T09:00:00Z","imagePasteIds":[2,3],"message":{"role":"user","content":[{"type":"text","text":"Look [Image #2] [Image #3]"},` + jpeg + `,` + jpeg + `]}}`))
	c.Feed(meta(dir + "2.png"))
	c.Feed(meta(dir + "3.png"))
	// Not this session's paste folder, or not a clean path: ignored.
	c.Feed([]byte(`{"type":"user","uuid":"u2","parentUuid":"x","sessionId":"s1","timestamp":"2026-11-04T09:05:00Z","message":{"role":"user","content":[{"type":"text","text":"Again [Image #4] [Image #5]"},` + jpeg + `,` + jpeg + `]}}`))
	c.Feed(meta("/private/tmp/claude-501/proj/other/images/4.png"))
	c.Feed(meta(dir + "../images/5.png"))
	bs := c.Blocks()
	if len(bs) != 2 || len(bs[0].Images) != 2 || len(bs[1].Images) != 2 {
		t.Fatalf("blocks %v", bs)
	}
	for i, want := range []string{dir + "2.png", dir + "3.png"} {
		img, ok := c.Image(bs[0].Images[i].Ref)
		if !ok || img.Original != want || img.MediaType != "image/jpeg" || !img.Sent.Equal(time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)) {
			t.Fatalf("image %d %+v", i, img)
		}
	}
	for i := range 2 {
		if img, _ := c.Image(bs[1].Images[i].Ref); img.Original != "" {
			t.Fatalf("foreign path kept: %q", img.Original)
		}
	}
}

// A command that changed files (a script, sed) shows their diff, as an edit
// does, each under its file's name.
func TestClaudeBashEditShowsItsDiff(t *testing.T) {
	c := NewClaude("/work")
	c.Feed([]byte(`{"type":"assistant","uuid":"a1","timestamp":"2026-10-31T09:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"python3 fix.py","description":"Fix the thing"}}]}}`))
	c.Feed([]byte(`{"type":"user","uuid":"u1","timestamp":"2026-10-31T09:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]},` +
		`"toolUseResult":{"stdout":"ok","bashEditDiff":{"files":[{"filePath":"/work/a.go","hunks":[{"oldStart":3,"oldLines":2,"newStart":3,"newLines":2,"lines":[" keep","-old","+new"]}]}],"moreFiles":0}}}`))
	bs := c.Blocks()
	if len(bs) != 1 {
		t.Fatalf("blocks %v", bs)
	}
	if want := "@@ a.go\n@@ -3 +3 @@\n keep\n-old\n+new\n"; bs[0].Diff != want || bs[0].Added != 1 || bs[0].Removed != 1 {
		t.Fatalf("diff %q +%d -%d", bs[0].Diff, bs[0].Added, bs[0].Removed)
	}
	if !c.Referenced("/work/a.go") {
		t.Fatal("the changed file should be servable")
	}
}

// The model and context come from the latest reply's usage; Claude 5's
// Opus, Sonnet and Fable are on a 1M window, as is a session past 200k, and
// Claude Code's stand-in replies don't count.
func TestClaudeContext(t *testing.T) {
	c := NewClaude("/work")
	reply := func(id, model string, in, created, read int) {
		c.Feed([]byte(fmt.Sprintf(`{"type":"assistant","uuid":"%s","timestamp":"2026-10-31T09:00:00Z","message":{"role":"assistant","model":"%s","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":5}}}`, id, model, in, created, read)))
	}
	reply("a1", "claude-haiku-5-5", 10, 1000, 49000)
	if m, n, w := c.Context(); m != "claude-haiku-5-5" || n != 50010 || w != 200_000 {
		t.Fatalf("%s %d %d", m, n, w)
	}
	reply("a2", "claude-opus-5-5", 10, 1000, 153000)
	if m, n, w := c.Context(); m != "claude-opus-5-5" || n != 154010 || w != 1_000_000 {
		t.Fatalf("%s %d %d", m, n, w)
	}
	reply("a3", "<synthetic>", 0, 0, 0)
	reply("a4", "claude-sonnet-4-5", 2, 500, 250000)
	if m, n, w := c.Context(); m != "claude-sonnet-4-5" || n != 250502 || w != 1_000_000 {
		t.Fatalf("%s %d %d", m, n, w)
	}
	for model, want := range map[string]bool{
		"claude-opus-5-5": true, "claude-opus-5": true, "claude-fable-5-1": true, "claude-sonnet-5-5": true,
		"claude-sonnet-4-5[1m]": true, "claude-opus-4-1": false, "claude-sonnet-4-5": false,
		"claude-haiku-5-5": false, "claude-3-7-sonnet-20250219": false, "": false,
	} {
		if oneMillionWindow(model) != want {
			t.Errorf("oneMillionWindow(%q) = %v", model, !want)
		}
	}
}

// TestClaudeBackgroundShells: a Bash call run in the background is listed
// with where its output goes, until its task notification says it ended.
func TestClaudeBackgroundShells(t *testing.T) {
	c := NewClaude("/work")
	c.Feed([]byte(`{"type":"assistant","uuid":"a1","timestamp":"2026-11-03T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"npm run dev","description":"Start the dev server","run_in_background":true}}]}}`))
	c.Feed([]byte(`{"type":"user","uuid":"u1","timestamp":"2026-11-03T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Command running in background with ID: b12ab. Output is being written to: /tmp/claude-501/p/s/tasks/b12ab.output. You will be notified."}]},"toolUseResult":{"stdout":"","backgroundTaskId":"b12ab"}}`))
	sh := c.Shells()
	if len(sh) != 1 || sh[0].Id != "b12ab" || sh[0].Command != "npm run dev" || sh[0].Description != "Start the dev server" ||
		sh[0].Output != "/tmp/claude-501/p/s/tasks/b12ab.output" || sh[0].Finished || sh[0].StartedUnixMs == 0 {
		t.Fatalf("shells %+v", sh)
	}
	c.Feed([]byte(`{"type":"user","uuid":"u2","timestamp":"2026-11-03T10:05:00Z","message":{"role":"user","content":"<task-notification>\n<task-id>b12ab</task-id>\n<status>failed</status>\n<summary>Background command \"Start the dev server\" failed with exit code 1</summary>\n</task-notification>"}}`))
	sh = c.Shells()
	if !sh[0].Finished || !sh[0].Failed || !strings.Contains(sh[0].Summary, "exit code 1") {
		t.Fatalf("ended %+v", sh[0])
	}
	// A foreground command isn't one.
	c.Feed([]byte(`{"type":"assistant","uuid":"a2","timestamp":"2026-11-03T10:06:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"ls"}}]}}`))
	c.Feed([]byte(`{"type":"user","uuid":"u3","timestamp":"2026-11-03T10:06:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"a\nb"}]},"toolUseResult":{"stdout":"a\nb"}}`))
	if len(c.Shells()) != 1 {
		t.Fatalf("foreground listed: %+v", c.Shells())
	}
}

// A question with previews (the preview boxed beside the list, notes under
// it): options read without the box, labels joined across their wrapped
// lines, the answered ✔ dropped.
func TestPreviewQuestion(t *testing.T) {
	for _, f := range []string{"preview.txt", "preview-tabs.txt", "preview-answered.txt"} {
		b, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		sc := ReadScreen(string(b))
		if !sc.Previews || sc.NotesEditing || !slices.Equal(sc.Options, []string{"Cmd-P quick open (Recommended)", "Cmd-O quick open"}) ||
			sc.Selected != 1 || sc.Title != "Which shortcut set?" || sc.Descriptions[0] != "" || sc.Descriptions[1] != "" {
			t.Errorf("%s: %+v", f, sc)
		}
		if f == "preview-tabs.txt" && sc.Tabs == "" {
			t.Errorf("%s: no tabs", f)
		}
	}
	b, _ := os.ReadFile("testdata/preview.txt")
	sc := ReadScreen(string(b))
	pending := &pb.AgentBlock{Id: "toolu_1", Kind: pb.AgentBlock_KIND_QUESTION, Questions: []*pb.AgentQuestion{{
		Question: "Which shortcut set?",
		Options: []*pb.AgentOption{
			{Label: "Cmd-P quick open (Recommended)", Description: "Cmd-P opens files", Preview: "┌──┐\n│⌘P│\n└──┘"},
			{Label: "Cmd-O quick open", Description: "Cmd-O opens files", Preview: "⌘O"},
		},
	}}}
	cur := Prompt(sc, pending)
	if !cur.Notes || len(cur.Previews) != 2 || cur.Previews[1] != "⌘O" || cur.Descriptions[0] != "Cmd-P opens files" || cur.Other != 0 {
		t.Fatalf("prompt %+v", cur)
	}
	// A number only selects here: then Enter, once it shows selected.
	steps, err := AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: cur.Options, Option: 2}, sc.Arrows, true)
	if err != nil || len(steps) != 2 || string(steps[0].Keys) != "2" || !steps[0].Ready(sc) ||
		string(steps[1].Keys) != "\x1b[13u" || steps[1].Ready(sc) {
		t.Fatalf("steps %v, %v", steps, err)
	}
	moved := sc
	moved.Selected = 2
	if !steps[1].Ready(moved) {
		t.Error("Enter not ready once 2 is selected")
	}
	// With notes: n, the text once the notes field is open, Esc to close
	// it (Enter there leaves the option unpicked), then Enter once closed.
	steps, err = AnswerSteps(cur, &pb.AgentAnswer{OptionsSeen: cur.Options, Option: 2, Notes: "prefer\nvim keys"}, sc.Arrows, true)
	if err != nil || len(steps) != 5 || string(steps[1].Keys) != "n" || !steps[1].Ready(moved) || string(steps[2].Keys) != "prefer vim keys" ||
		string(steps[3].Keys) != "\x1b" || string(steps[4].Keys) != "\x1b[13u" || !steps[4].Ready(moved) {
		t.Fatalf("notes steps %v, %v", steps, err)
	}
	if steps[2].Ready(moved) {
		t.Error("typed before the notes field opened")
	}
	open := ReadScreen(strings.Replace(strings.Replace(string(b), "Notes: press n to add notes", "Notes: Add notes on this design…", 1), "❯ 1.", "  1.", 1))
	// Open with text typed (the footer offers ctrl+g), then closed with the
	// notes still shown.
	typed := strings.Replace(strings.Replace(strings.Replace(string(b), "Notes: press n to add notes", "Notes: prefer", 1), "❯ 1.", "  1.", 1), "  2. Cmd-O", "❯ 2. Cmd-O", 1)
	open2 := ReadScreen(strings.Replace(typed, "n to add notes · Esc", "n to add notes · ctrl+g to edit in VS Code · Esc", 1))
	closed := ReadScreen(typed)
	if !open.NotesEditing || !open2.NotesEditing || closed.NotesEditing || !steps[2].Ready(open2) || steps[3].Ready != nil || steps[4].Ready(open2) || !steps[4].Ready(closed) {
		t.Errorf("notes field open: %+v / %+v / %+v", open, open2, closed)
	}
	// Notes for a prompt that doesn't take them are refused.
	q, _ := os.ReadFile("testdata/questions.txt")
	plain := Prompt(ReadScreen(string(q)), nil)
	if _, err := AnswerSteps(plain, &pb.AgentAnswer{OptionsSeen: plain.Options, Option: 1, Notes: "x"}, false, true); err == nil {
		t.Error("notes typed into a prompt without them")
	}
}

// A "!" command typed at Claude Code's prompt: the command and its output
// are user lines, shown as the Bash tool it ran, not as messages.
func TestClaudeBangCommandIsATool(t *testing.T) {
	c := NewClaude("/tmp")
	feed := func(uuid, parent, content string) {
		line, _ := json.Marshal(map[string]any{"type": "user", "uuid": uuid, "parentUuid": parent, "timestamp": "2026-11-09T12:00:00Z",
			"message": map[string]any{"role": "user", "content": content}})
		c.Feed(line)
	}
	feed("i1", "p0", "<bash-input>git status</bash-input>")
	if bs := c.Blocks(); len(bs) != 1 || bs[0].Kind != pb.AgentBlock_KIND_TOOL || bs[0].Title != "Ran `git status`" || !bs[0].Pending {
		t.Fatalf("input: %v", bs)
	}
	feed("o1", "i1", "<bash-stdout>On branch main\n\nnothing to commit</bash-stdout><bash-stderr></bash-stderr>")
	bs := c.Blocks()
	if len(bs) != 1 || bs[0].Pending || bs[0].Error || bs[0].Output != "On branch main\n\nnothing to commit" {
		t.Fatalf("output: %v", bs)
	}

	feed("i2", "o1", "<bash-input>sleep</bash-input>")
	feed("o2", "i2", "<bash-stdout></bash-stdout><bash-stderr>usage: sleep number[unit] [...]\n</bash-stderr>")
	feed("i3", "o2", "<bash-input>true</bash-input>")
	feed("o3", "i3", "<bash-stdout>(Bash completed with no output)</bash-stdout><bash-stderr></bash-stderr>")
	bs = c.Blocks()
	// No exit status recorded, and stderr alone isn't failure.
	if len(bs) != 3 || bs[1].Error || bs[1].Output != "usage: sleep number[unit] [...]" || bs[2].Output != "" || bs[2].Pending {
		t.Fatalf("blocks: %v", bs)
	}
	for _, b := range bs {
		if b.Kind == pb.AgentBlock_KIND_USER || b.Kind == pb.AgentBlock_KIND_TEXT {
			t.Fatalf("a message: %v", b)
		}
	}

	// Output holding the very tags (a diff of this parser): still output.
	diff := "+\t<bash-input>x</bash-input>\n+\t<command-name>/x</command-name><local-command-stdout>y</local-command-stdout>\n+</bash-stdout><bash-stderr>"
	feed("i4", "o3", "<bash-input>git diff</bash-input>")
	feed("o4", "i4", "<bash-stdout>"+diff+"</bash-stdout><bash-stderr>warn</bash-stderr>")
	bs = c.Blocks()
	if len(bs) != 4 || bs[3].Title != "Ran `git diff`" || bs[3].Pending || bs[3].Output != diff+"\nwarn" {
		t.Fatalf("tags in output: %v", bs)
	}

	// The command and its output in one line.
	feed("i5", "o4", "<bash-input>pwd</bash-input>\n\n<bash-stdout>/tmp</bash-stdout><bash-stderr></bash-stderr>")
	// Interrupted, no output: settled once something else is typed.
	feed("i6", "i5", "<bash-input>sleep 100</bash-input>")
	feed("u7", "i6", "Never mind")
	bs = c.Blocks()
	if len(bs) != 7 || bs[4].Output != "/tmp" || bs[4].Pending || bs[5].Title != "Ran `sleep 100`" || bs[5].Pending || bs[6].Text != "Never mind" {
		t.Fatalf("one line, interrupted: %v", bs)
	}
}
