package agents

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAmpCommands(t *testing.T) {
	if ByProcess("amp") != Amp || ByProcess("amp.exe") != Amp || Parse("Amp") != Amp {
		t.Fatal("not found by process or name")
	}
	id := "T-12345678-0000-4000-8000-000000000abc"
	for argv, want := range map[string]string{
		"amp threads continue " + id:                                             id,
		"amp threads continue https://ampcode.com/threads/" + id:                 id,
		"amp threads continue https://ampcode.com/threads/" + id + "/":           id,
		"amp threads continue --last":                                            "",
		"amp threads continue " + id + " T-22345678-0000-4000-8000-000000000abc": id,
		"amp threads continue":                                                   "",
		"amp threads list":                                                       "",
		"amp -x fix the tests":                                                   "",
		"amp continue " + id:                                                     "",
	} {
		if got := Amp.ResumeID(strings.Fields(argv)); got != want {
			t.Errorf("%s: %q, want %q", argv, got, want)
		}
	}
	if got := Amp.Resume(id); got != "amp threads continue '"+id+"'" {
		t.Errorf("resume: %s", got)
	}
	if got := Amp.StartCommand("fix it's tests"); got != "amp" || !Amp.TypePrompt { // the message is typed in once it's up
		t.Errorf("start: %s", got)
	}
	if got := Amp.StartCommand("  "); got != "amp" {
		t.Errorf("start without a prompt: %s", got)
	}
}

// Events as Hammock's Amp plugin sends them (cmd/hmk/hooks_amp.go): Amp's
// plugin API payloads (ampcode.com/docs/plugin-api), the reply's text for
// agent.end, and the shown thread's state changes.
func TestAmpHooks(t *testing.T) {
	const id = "T-12345678-0000-4000-8000-000000000abc"
	thread := `"thread":{"id":"` + id + `"}`
	for _, c := range []struct {
		event, payload string
		want           Hook
	}{
		{"session.start", `{` + thread + `}`, Hook{Event: "SessionStart"}},
		{"agent.start", `{` + thread + `,"message":"fix the tests"}`, Hook{Event: "UserPromptSubmit", Prompt: "fix the tests"}},
		{"tool.result", `{` + thread + `,"tool":"Read","input":{"path":"/w/a.go"},"status":"done"}`, Hook{Event: "PostToolUse", ToolName: "Read"}},
		{"agent.end", `{` + thread + `,"message":"fix the tests","status":"done","reply":"Fixed."}`, Hook{Event: "Stop", LastMessage: "Fixed."}},
		{"agent.end", `{` + thread + `,"status":"cancelled","reply":""}`, Hook{Event: "Stop"}},
		{"agent.end", `{` + thread + `,"status":"error","reply":"Partial"}`, Hook{Event: "StopFailure", LastMessage: "Partial", ErrorDetails: "Amp stopped with an error"}},
		{"state", `{` + thread + `,"state":"awaiting-approval","previous":"running"}`, Hook{Event: "PermissionRequest", Message: "Amp wants your approval"}},
		{"state", `{` + thread + `,"state":"running","previous":"awaiting-approval"}`, Hook{Event: "PostToolUse"}},
		{"state", `{` + thread + `,"state":"running","previous":""}`, Hook{Event: "PreToolUse"}},
		{"state", `{` + thread + `,"state":"idle","previous":""}`, Hook{Event: "SessionStart"}},
		// Turns' own starts and ends come as events.
		{"state", `{` + thread + `,"state":"running","previous":"idle"}`, Hook{}},
		{"state", `{` + thread + `,"state":"idle","previous":"running"}`, Hook{}},
		{"state", `{` + thread + `,"state":"error","previous":"running"}`, Hook{}},
		{"changes.prompt", `{` + thread + `}`, Hook{}},
	} {
		h := Amp.ReadHook(c.event, []byte(c.payload))
		c.want.SessionID = id
		h.ToolInput = nil
		if !reflect.DeepEqual(h, c.want) {
			t.Errorf("%s %s:\n got %+v\nwant %+v", c.event, c.payload, h, c.want)
		}
	}

	// Tools' inputs keep theirs, with Hammock's names for a command and a file.
	h := Amp.ReadHook("tool.call", []byte(`{`+thread+`,"tool":"Bash","input":{"cmd":"go test ./...","cwd":"/w"}}`))
	var in map[string]any
	_ = json.Unmarshal(h.ToolInput, &in)
	if h.Event != "PreToolUse" || h.ToolName != "Bash" || in["command"] != "go test ./..." || in["cmd"] != "go test ./..." || in["cwd"] != "/w" {
		t.Errorf("Bash: %+v %s", h, h.ToolInput)
	}
	h = Amp.ReadHook("tool.call", []byte(`{`+thread+`,"tool":"edit_file","input":{"path":"/w/a.go","old_str":"a","new_str":"b"}}`))
	in = nil
	_ = json.Unmarshal(h.ToolInput, &in)
	if in["file_path"] != "/w/a.go" {
		t.Errorf("edit_file: %s", h.ToolInput)
	}
	if h := Amp.ReadHook("tool.call", []byte(`{`+thread+`,"tool":"x","input":null}`)); h.ToolInput != nil {
		t.Errorf("no input: %s", h.ToolInput)
	}
	if h := Amp.ReadHook("agent.start", []byte(`not json`)); h.Event != "UserPromptSubmit" || h.SessionID != "" {
		t.Errorf("bad payload: %+v", h)
	}
}

func TestAmpScreen(t *testing.T) {
	for _, s := range []string{"  Allow Once\n  Always Allow for Me\n  Always Allow for Workspace", "Waiting for approval"} {
		if !Amp.NeedsInputRE().MatchString(s) {
			t.Errorf("needs input: %q", s)
		}
	}
	for _, s := range []string{"⠋ Running Tools   Esc cancel", "Auto-Compacting"} {
		if !Amp.WorkingRE().MatchString(s) {
			t.Errorf("working: %q", s)
		}
	}
	if Amp.WorkingRE().MatchString("Running the tools now") || Amp.NeedsInputRE().MatchString("allow once more") {
		t.Error("matched prose")
	}
}
