package agentview

import (
	"bytes"
	"testing"
)

// Claude Code's input box with its agent list under it, as a subagent runs.
func agentListScreen(input, list string) string {
	rule := "────────────────────────────────────────"
	return "✻ Waiting for 1 background agent to finish\n\n" + rule + "\n" + input + "\n" + rule + "\n" +
		"   ~/Developer/app\n  ◆ Opus 5.5 medium  ·  ctx ▰▱▱▱▱▱▱▱▱▱ 7%\n  ↑/↓ to select\n\n" + list
}

const (
	listMainSelected = "❯ ⏺ main\n  ◯ general-purpose  Run the tests                    8s · ↓ 42.5k tokens"
	listSubSelected  = "  ⏺ main\n❯ ◯ general-purpose  Run the tests                    13s · ↓ 42.5k tokens"
	listViewingSub   = "  ◯ main\n  ⏺ general-purpose  Run the tests                    25s · ↓ 42.5k tokens"
	listBackToMain   = "❯ ◯ main\n  ⏺ general-purpose  Run the tests                    30s · ↓ 42.5k tokens"
)

// A row selected in the agent list isn't what's typed in the input box.
func TestInputDraftUnderAgentList(t *testing.T) {
	for name, tc := range map[string]struct{ screen, want string }{
		"main selected":     {agentListScreen("❯ ", listMainSelected), ""},
		"subagent selected": {agentListScreen("❯ ", listSubSelected), ""},
		"typed":             {agentListScreen("❯ fix it", listSubSelected), "fix it"},
	} {
		if got := InputDraft(tc.screen); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
		if n := InputLines(tc.screen); n != 1 {
			t.Errorf("%s: %d input lines", name, n)
		}
	}
}

func TestReadAgentList(t *testing.T) {
	for name, tc := range map[string]struct {
		list     string
		viewing  bool
		selected int
		toMain   []byte
	}{
		"main, input has the keys": {"  ⏺ main\n  ◯ general-purpose  Run the tests", false, -1, nil},
		"main selected":            {listMainSelected, false, 0, keyUp},
		"subagent selected":        {listSubSelected, false, 1, keyUp},
		"viewing a subagent":       {listViewingSub, true, -1, keyDown},
		"main's row selected":      {listBackToMain, true, 0, []byte("\x1b[13u")},
		"subagent's row selected":  {"  ◯ main\n❯ ⏺ general-purpose  Run the tests", true, 1, keyUp},
	} {
		l := ReadAgentList(agentListScreen("❯ ", tc.list))
		if l.Rows != 2 || l.ViewingSubagent() != tc.viewing || l.Selected != tc.selected {
			t.Errorf("%s: %+v", name, l)
		}
		if got := ToMainInput(l, true); !bytes.Equal(got, tc.toMain) {
			t.Errorf("%s: key %q, want %q", name, got, tc.toMain)
		}
	}
	// No list: nothing to do.
	if l := ReadAgentList(agentListScreen("❯ ", "")); l.Rows != 0 || ToMainInput(l, true) != nil {
		t.Errorf("no list: %+v", l)
	}
	// Rows above the input box (the conversation quoting them) aren't the list.
	if l := ReadAgentList(listViewingSub + "\n" + agentListScreen("❯ ", "")); l.Rows != 0 {
		t.Errorf("quoted list: %+v", l)
	}
}
