package agents

import "testing"

func TestParseHook(t *testing.T) {
	// Claude Code's own shape, its event named in the payload.
	h := ParseHook("", []byte(`{"hook_event_name":"PreToolUse","session_id":"s1","transcript_path":"/t.jsonl","tool_name":"Bash","tool_input":{"command":"ls"}}`), nil)
	if h.Event != "PreToolUse" || h.SessionID != "s1" || h.TranscriptPath != "/t.jsonl" || h.ToolName != "Bash" || string(h.ToolInput) != `{"command":"ls"}` {
		t.Errorf("claude: %+v", h)
	}
	// Camel case, a tool's arguments as a JSON string (Copilot's), and the
	// event named on the command line, mapped to Hammock's.
	h = ParseHook("preToolUse", []byte(`{"sessionId":"s2","toolName":"bash","toolArgs":"{\"command\":\"make\"}"}`), map[string]string{"preToolUse": "PreToolUse"})
	if h.Event != "PreToolUse" || h.SessionID != "s2" || h.ToolName != "bash" || string(h.ToolInput) != `{"command":"make"}` {
		t.Errorf("camel: %+v", h)
	}
	// An event mapped to "" is ignored by classify; unmapped ones pass through.
	if h := ParseHook("afterAgentThought", nil, map[string]string{"afterAgentThought": ""}); h.Event != "" {
		t.Errorf("ignored: %+v", h)
	}
	// Codex's older notify.
	h = ParseHook("", []byte(`{"type":"agent-turn-complete","thread-id":"t1","last-assistant-message":"Done."}`), nil)
	if h.Event != "agent-turn-complete" || h.SessionID != "t1" || h.LastMessage != "Done." {
		t.Errorf("notify: %+v", h)
	}
	// An agent with its own reader.
	a := &Agent{ParseHook: func(ev string, _ []byte) Hook { return Hook{Event: "Stop", SessionID: ev} }}
	if h := a.ReadHook("x", nil); h.Event != "Stop" || h.SessionID != "x" {
		t.Errorf("own: %+v", h)
	}
	// A harness Hammock doesn't know reads generically.
	var none *Agent
	if h := none.ReadHook("Stop", nil); h.Event != "Stop" {
		t.Errorf("nil: %+v", h)
	}
}

func TestRegistry(t *testing.T) {
	if All[0] != Claude || All[1] != Codex {
		t.Fatalf("order: %v %v", All[0].ID, All[1].ID)
	}
	if ByProcess("Claude") != Claude || ByProcess("codex") != Codex || ByProcess("vim") != nil {
		t.Error("by process")
	}
	if Parse("Claude Code") != Claude || Parse("codex") != Codex || Parse("nope") != nil {
		t.Error("parse")
	}
	if Other("claude") != "codex" || Other("codex") != "claude" || Other("nope") != "" {
		t.Error("other")
	}
	if got := Claude.StartCommand("it's done"); got != `claude 'it'\''s done'` {
		t.Errorf("start: %s", got)
	}
	if got := Codex.ResumeID([]string{"codex", "resume", "019c0000-0000-7000-8000-00000000c0de"}); got != "019c0000-0000-7000-8000-00000000c0de" {
		t.Errorf("resume id: %q", got)
	}
}
