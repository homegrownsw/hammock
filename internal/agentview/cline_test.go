package agentview

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// clineFixture is a session folder in Cline CLI 3.0.74's layout
// (~/.cline/data/sessions/<id>/): its manifest and its messages file, as
// sdk/packages/core/src/session writes them, shaped after the CLI's own
// fixtures (sdk/packages/core/fixtures/session) and its tools' schemas
// (sdk/packages/core/src/extensions/tools/schemas.ts). Hand-made.
const clineFixture = "testdata/cline/1794050000000_k3x9q/1794050000000_k3x9q"

// clineLines is the session as agents.Cline's Open gives it: the session's
// line, then a message a line.
func clineLines(t *testing.T) [][]byte {
	t.Helper()
	b, err := os.ReadFile(clineFixture + ".messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	head, _ := json.Marshal(map[string]any{"$hammockSession": ClineSession{ID: "1794050000000_k3x9q", Cwd: "/work", Model: "claude-sonnet-5-5"}})
	out := [][]byte{head}
	for _, m := range doc.Messages {
		var line bytes.Buffer
		_ = json.Compact(&line, m)
		out = append(out, line.Bytes())
	}
	return out
}

func TestClineBlocks(t *testing.T) {
	c := NewCline("")
	for _, l := range clineLines(t) {
		c.Feed(l)
	}
	var got []string
	for _, b := range c.Blocks() {
		s := strings.TrimPrefix(b.Kind.String(), "KIND_") + " " + b.Title + b.Text
		if b.Diff != "" {
			s += " | " + strings.ReplaceAll(strings.TrimSpace(b.Diff), "\n", " / ")
		}
		if b.Output != "" {
			s += " > " + strings.ReplaceAll(b.Output, "\n", " / ")
		}
		if b.Error {
			s += " (error)"
		}
		if b.Pending {
			s += " (pending)"
		}
		got = append(got, s)
	}
	want := []string{
		"USER Add a --top N flag to @tally.sh",
		"THINKING Read the script, then take the count from $2.",
		"TEXT I'll read the script first.",
		"TOOL Read tally.sh > 1 | #!/bin/sh / 2 | wc -w *.md",
		`TOOL Edited tally.sh | -wc -w *.md / +top="$2" / +wc -w *.md | head -n "$top"`,
		"TEXT Added --top N: it prints the N biggest files.",
		"USER Now run it, and write a README",
		"TOOL Ran `./tally.sh --top 2` and 1 more > sh: ./tally.sh: Permission denied / README.md / tally.sh (error)",
		"TOOL Wrote README.md | +# tally / +",
		"NOTE Conversation compacted",
		"TEXT It needs to be executable.",
		"TOOL Edited tally.sh | @@ tally.sh / -#!/bin/sh / +#!/bin/bash (pending)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	turn := ""
	seen := map[string]bool{}
	for _, b := range c.Blocks() {
		if seen[b.Id] {
			t.Errorf("id %s twice", b.Id)
		}
		seen[b.Id] = true
		if b.Kind.String() == "KIND_USER" {
			turn = b.Id
		}
		if b.TurnId != turn {
			t.Errorf("%s: turn %q, want %q", b.Title+b.Text, b.TurnId, turn)
		}
	}
	if p := c.Pending(); p == nil || p.Title != "Edited tally.sh" {
		t.Errorf("pending: %v", p)
	}
	for _, p := range []string{"/work/tally.sh", "/work/README.md"} {
		if !c.Referenced(p) {
			t.Errorf("%s not referenced", p)
		}
	}
	if m, tok, _ := c.Context(); m != "claude-sonnet-5-5" || tok != 18234 {
		t.Errorf("context %q %d", m, tok)
	}
	if c.Blocks()[0].TimeUnixMs != 1794050001000 {
		t.Errorf("time %d", c.Blocks()[0].TimeUnixMs)
	}

	// Read again from the start (its file is rewritten whole), the blocks
	// keep their ids.
	again := NewCline("")
	for _, l := range clineLines(t) {
		again.Feed(l)
	}
	for i, b := range again.Blocks() {
		if b.Id != c.Blocks()[i].Id {
			t.Errorf("block %d: id %s, then %s", i, c.Blocks()[i].Id, b.Id)
		}
	}
}

func TestClineTools(t *testing.T) {
	c := NewCline("/work")
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"read_files", `{"files":[{"path":"/work/a.go"},{"path":"/work/b.go"}]}`, "Read a.go and 1 other file"},
		{"read_files", `["/work/a.go"]`, "Read a.go"},
		{"read_files", `{"file_paths":["/work/c.go"]}`, "Read c.go"},
		{"run_commands", `{"commands":[{"command":"go","args":["test","./..."]}]}`, "Ran `go test ./...`"},
		{"run_commands", `"make"`, "Ran `make`"},
		{"search_codebase", `{"queries":["TODO","FIXME"]}`, "Searched for `TODO` and 1 more"},
		{"fetch_web_content", `{"requests":[{"url":"https://x.test","prompt":"summarize"}]}`, "Fetched https://x.test"},
		{"editor", `{"path":"/work/a.go","new_text":"x","insert_line":3}`, "Edited a.go"},
		{"skills", `{"skill":"release","args":null}`, "Used the release skill"},
		{"ask_question", `{"question":"Which one?","options":["a","b"]}`, "Asked “Which one?”"},
		{"submit_and_exit", `{"summary":"Done it all","verified":true}`, "Finished the task"},
		{"spawn_agent", `{"systemPrompt":"You review.","task":"Review the diff"}`, "Started an agent: Review the diff"},
		{"team_status", `{}`, "Used team_status"},
	} {
		if got := c.tool(tc.name, clineInput(tc.name, json.RawMessage(tc.in))).Title; got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestClineDisplayText(t *testing.T) {
	for in, want := range map[string]string{
		`<user_input mode="plan">look around</user_input>`:                                           "look around",
		`<mode_notice>switched</mode_notice><user_input mode="act">go</user_input>`:                  "go",
		`<user_command slash="review">the diff</user_command>`:                                       "/review the diff",
		`<user_command slash="team">spawn a team of agents for the following task: x</user_command>`: "/team x",
		"plain": "plain",
	} {
		if got := ClineDisplayText(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if got := ClineUserText([]ClineBlock{{Type: "text", Text: "[TASK RESUMPTION] carry on"}}); got != "" {
		t.Errorf("resumption: %q", got)
	}
}
