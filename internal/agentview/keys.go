package agentview

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Answering a prompt is a short script of keystrokes, the first checked
// against the screen (it must still show the options the client saw), so a
// changed or vanished prompt never gets stray keys. See
// docs/internals/agent-view-keys.md for what Claude Code expects.

// Step types Keys once Ready reports the screen is right for them.
type Step struct {
	Ready func(Screen) bool // nil: no check
	Keys  []byte
	What  string // for errors and logs
}

// ErrMismatch: the screen didn't show the prompt an answer was for.
var ErrMismatch = errors.New("couldn't match Claude's prompt on screen")

// ErrChanged: the screen shows a prompt, but not the one the answer was
// for (Screen.Key): a stale card, or one prompt after another with the
// same options.
var ErrChanged = errors.New("the request changed: look again")

var (
	keyRight = []byte("\x1b[C")
	keyUp    = []byte("\x1b[A")
	keyDown  = []byte("\x1b[B")
	keyEsc   = []byte("\x1b")
)

// Enter is the Enter key as the program wants it: CSI u under the kitty
// keyboard protocol (which Claude Code turns on), else CR.
func Enter(kitty bool) []byte {
	if kitty {
		return []byte("\x1b[13u")
	}
	return []byte("\r")
}

// CtrlEnter is Ctrl+Enter under the kitty keyboard protocol: Claude Code's
// Send Now (chat:sendNow) for messages it's holding while it works.
var CtrlEnter = []byte("\x1b[13;5u")

// ToMainInput is the next key that takes the terminal from a subagent's
// conversation back to the main one's input box, in Claude Code's agent
// list l: ↓ into the list, ↑ or ↓ to main's row, Enter, then ↑ until the
// input box has the keys again. Nil: it has them, in the main one.
func ToMainInput(l AgentList, kitty bool) []byte {
	switch {
	case !l.ViewingSubagent():
		if l.Selected >= 0 {
			return keyUp
		}
		return nil
	case l.Selected < 0, l.Selected < l.Main:
		return keyDown
	case l.Selected > l.Main:
		return keyUp
	}
	return Enter(kitty)
}

func digit(n int) []byte { return []byte(fmt.Sprint(n)) }

func sameOptions(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AnswerSteps scripts an answer to the menu on screen. cur is the prompt as
// last read (arrows: its options have no numbers); a.OptionsSeen is what the
// client answered, and a.PromptId, when it says, which prompt (an older
// client doesn't: its options alone are checked).
func AnswerSteps(cur *pb.AgentPrompt, a *pb.AgentAnswer, arrows, kitty bool) ([]Step, error) {
	if cur == nil || len(cur.Options) == 0 {
		return nil, ErrMismatch
	}
	if a.PromptId != "" && a.PromptId != cur.PromptId {
		return nil, ErrChanged
	}
	if !sameOptions(cur.Options, a.OptionsSeen) {
		return nil, ErrMismatch
	}
	showing := func(sc Screen) bool {
		return sameOptions(sc.Options, a.OptionsSeen) && (a.PromptId == "" || sc.Key == a.PromptId)
	}
	n := len(a.OptionsSeen)
	valid := func(o int32) bool { return o >= 1 && int(o) <= n }
	switch {
	case cur.MultiSelect && (a.Option == 0 || a.Text != "" && a.Option == cur.Other):
		return multiSteps(cur, a, showing)
	case !valid(a.Option):
		return nil, fmt.Errorf("no option %d", a.Option)
	case a.Notes != "" && !cur.Notes:
		return nil, errors.New("this prompt doesn't take notes")
	case cur.Notes:
		// A question with previews: a number only moves the selection, and
		// Enter picks the selected option. Notes go first: "n" opens them,
		// and Esc closes them, keeping what was typed. Not Enter: from the
		// notes, it picks only an option a number just moved to (the one
		// already selected is left unpicked, "notes only").
		o := int(a.Option)
		selected := func(sc Screen) bool { return sameOptions(sc.Options, a.OptionsSeen) && sc.Selected == o }
		steps := []Step{{Ready: showing, Keys: digit(o), What: cur.Options[o-1]}}
		if note := oneLine(a.Notes); note != "" {
			editing := func(sc Screen) bool { return selected(sc) && sc.NotesEditing }
			steps = append(steps,
				Step{Ready: selected, Keys: []byte("n"), What: "notes"},
				Step{Ready: editing, Keys: []byte(note), What: "notes text"},
				// (Right after the text, which was typed only once the field
				// showed open: its footer may not say so once there's text.)
				Step{Keys: keyEsc, What: "close notes"})
			return append(steps, Step{Ready: func(sc Screen) bool { return selected(sc) && !sc.NotesEditing }, Keys: Enter(kitty), What: "enter"}), nil
		}
		return append(steps, Step{Ready: selected, Keys: Enter(kitty), What: "enter"}), nil
	case arrows && a.Text == "":
		return arrowSteps(int(cur.Selected), int(a.Option), a.OptionsSeen, kitty), nil
	case arrows:
		return nil, errors.New("this prompt doesn't take typed text")
	case a.Text != "":
		// The "other" option takes text typed into it, then Enter.
		o := int(a.Option)
		return []Step{
			{Ready: showing, Keys: digit(o), What: cur.Options[o-1]},
			{Ready: func(sc Screen) bool { return sc.Selected == o }, Keys: []byte(oneLine(a.Text)), What: "text"},
			{Keys: Enter(kitty), What: "enter"},
		}, nil
	default:
		return []Step{{Ready: showing, Keys: optionKey(cur.Options[a.Option-1], int(a.Option)), What: cur.Options[a.Option-1]}}, nil
	}
}

// multiSteps answers a multi-select question: a number toggles its box
// (leaving the selection where it is), so the chosen options are toggled
// to match, then → moves on. Typed text goes in the "other" option, which
// takes it only while selected: ↑ or ↓ to it, the text (which ticks it),
// then ↑ out of its field, as → in it only moves the cursor.
func multiSteps(cur *pb.AgentPrompt, a *pb.AgentAnswer, showing func(Screen) bool) ([]Step, error) {
	n := len(a.OptionsSeen)
	other := int(cur.Other)
	text := oneLine(a.Text)
	if text != "" && (other == 0 || cur.Selected < 1) {
		return nil, errors.New("this prompt doesn't take typed text")
	}
	want := map[int32]bool{}
	for _, o := range a.Toggles {
		if o < 1 || int(o) > n {
			return nil, fmt.Errorf("no option %d", o)
		}
		want[o] = true
	}
	var steps []Step
	for i := range cur.Options {
		o := int32(i + 1)
		if text != "" && int(o) == other {
			continue // ticked by typing into it
		}
		checked := i < len(cur.Checked) && cur.Checked[i]
		if want[o] != checked {
			steps = append(steps, Step{Keys: digit(int(o)), What: cur.Options[i]})
		}
	}
	if text != "" {
		at := func(k int) func(Screen) bool {
			return func(sc Screen) bool { return sameOptions(sc.Options, a.OptionsSeen) && sc.Selected == k }
		}
		move, d := keyDown, 1
		if other < int(cur.Selected) {
			move, d = keyUp, -1
		}
		for k := int(cur.Selected); k != other; k += d {
			steps = append(steps, Step{Ready: at(k), Keys: move, What: "move to " + cur.Options[k+d-1]})
		}
		steps = append(steps,
			Step{Ready: at(other), Keys: []byte(text), What: "text"},
			// (Once typed, the option shows the text instead of its label.)
			Step{Keys: keyUp, What: "leave the text"},
			Step{Ready: func(sc Screen) bool { return sc.Selected == other-1 }, Keys: keyRight, What: "next"})
	} else {
		steps = append(steps, Step{Keys: keyRight, What: "next"})
	}
	if steps[0].Ready == nil {
		steps[0].Ready = showing
	}
	return steps, nil
}

// optionShortcut is the key a Codex option names at its end: "(y)", "(esc)".
var optionShortcut = regexp.MustCompile(`\((\w|esc)\)$`)

// optionKey picks option n: its own key if it names one (Codex's, where a
// number only moves the selection), else its number (Claude Code's).
func optionKey(label string, n int) []byte {
	if m := optionShortcut.FindStringSubmatch(strings.TrimSpace(label)); m != nil {
		if m[1] == "esc" {
			return keyEsc
		}
		return []byte(m[1])
	}
	return digit(n)
}

// arrowSteps picks option o of a menu without numbers: ↑ or ↓ a line at a
// time from the selected one, each once the screen shows the selection
// where the last left it, then Enter once it shows o selected.
func arrowSteps(sel, o int, seen []string, kitty bool) []Step {
	at := func(n int) func(Screen) bool {
		return func(sc Screen) bool { return sameOptions(sc.Options, seen) && sc.Selected == n }
	}
	move, d := keyDown, 1
	if o < sel {
		move, d = keyUp, -1
	}
	var steps []Step
	for n := sel; n != o; n += d {
		steps = append(steps, Step{Ready: at(n), Keys: move, What: "move to " + seen[n+d-1]})
	}
	return append(steps, Step{Ready: at(o), Keys: Enter(kitty), What: seen[o-1]})
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// ErrNotAsking: no prompt on screen for an Allow or Deny to answer.
var ErrNotAsking = errors.New("the agent isn't asking any more")

// DecisionSteps scripts Needs You's Allow (the first option: Enter while
// it's selected, else its number; in a menu without numbers, the option
// starting "Yes", as the trust dialog's first is "No, exit") or Deny (Esc)
// for the prompt on screen, typed only while the screen still shows that
// prompt. An Allow given promptID (the card's AttentionItem.prompt_id) is
// only for that prompt: any other on screen is ErrChanged. Without one (an
// older client) it's for whichever is showing, as is a Deny: Esc only
// turns a request down.
func DecisionSteps(sc Screen, promptID string, allow, kitty bool) ([]Step, error) {
	if len(sc.Options) == 0 {
		return nil, ErrNotAsking
	}
	seen, key := sc.Options, sc.Key
	if !allow {
		asking := func(s Screen) bool { return sameOptions(s.Options, seen) }
		return []Step{{Ready: asking, Keys: keyEsc, What: "deny"}}, nil
	}
	showing := func(s Screen) bool { return sameOptions(s.Options, seen) && s.Key == key }
	if promptID != "" && promptID != key {
		return nil, ErrChanged
	}
	if sc.Arrows {
		for i, o := range seen {
			if strings.HasPrefix(o, "Yes") {
				steps := arrowSteps(sc.Selected, i+1, seen, kitty)
				first := steps[0].Ready
				steps[0].Ready = func(s Screen) bool { return first(s) && s.Key == key }
				return steps, nil
			}
		}
		return nil, errors.New("answer this prompt in the agent view")
	}
	keys := digit(1)
	if sc.Selected == 1 {
		keys = Enter(kitty)
	}
	return []Step{{Ready: showing, Keys: keys, What: seen[0]}}, nil
}

// InterruptKeys stops the agent (Esc).
func InterruptKeys() []byte { return keyEsc }
