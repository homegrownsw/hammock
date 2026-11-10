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
// client answered.
func AnswerSteps(cur *pb.AgentPrompt, a *pb.AgentAnswer, arrows, kitty bool) ([]Step, error) {
	if cur == nil || len(cur.Options) == 0 || !sameOptions(cur.Options, a.OptionsSeen) {
		return nil, ErrMismatch
	}
	showing := func(sc Screen) bool { return sameOptions(sc.Options, a.OptionsSeen) }
	n := len(a.OptionsSeen)
	valid := func(o int32) bool { return o >= 1 && int(o) <= n }
	switch {
	case cur.MultiSelect && a.Text == "" && a.Option == 0:
		// Tick exactly the chosen options, then move on.
		want := map[int32]bool{}
		for _, o := range a.Toggles {
			if !valid(o) {
				return nil, fmt.Errorf("no option %d", o)
			}
			want[o] = true
		}
		var steps []Step
		for i := range cur.Options {
			o := int32(i + 1)
			checked := i < len(cur.Checked) && cur.Checked[i]
			if want[o] != checked {
				steps = append(steps, Step{Keys: digit(int(o)), What: cur.Options[i]})
			}
		}
		steps = append(steps, Step{Keys: keyRight, What: "next"})
		steps[0].Ready = showing
		return steps, nil
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
		// The "other" option takes text typed into it, then Enter (→ for a
		// multi-select question).
		o := int(a.Option)
		next := Enter(kitty)
		if cur.MultiSelect {
			next = keyRight
		}
		return []Step{
			{Ready: showing, Keys: digit(o), What: cur.Options[o-1]},
			{Ready: func(sc Screen) bool { return sc.Selected == o }, Keys: []byte(oneLine(a.Text)), What: "text"},
			{Keys: next, What: "enter"},
		}, nil
	default:
		return []Step{{Ready: showing, Keys: optionKey(cur.Options[a.Option-1], int(a.Option)), What: cur.Options[a.Option-1]}}, nil
	}
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
// prompt.
func DecisionSteps(sc Screen, allow, kitty bool) ([]Step, error) {
	if len(sc.Options) == 0 {
		return nil, ErrNotAsking
	}
	seen := sc.Options
	showing := func(s Screen) bool { return sameOptions(s.Options, seen) }
	if !allow {
		return []Step{{Ready: showing, Keys: keyEsc, What: "deny"}}, nil
	}
	if sc.Arrows {
		for i, o := range seen {
			if strings.HasPrefix(o, "Yes") {
				return arrowSteps(sc.Selected, i+1, seen, kitty), nil
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
