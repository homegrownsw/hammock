package agentview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Claude Code shows every prompt (its question tool, permissions, plan
// approval) as a numbered menu above a footer of key hints:
//
//	 Do you want to proceed?
//	 ❯ 1. Yes
//	   2. Yes, and always allow …
//	   3. No
//
//	 Esc to cancel · Tab to amend
//
// The question tool shows one question at a time, and doesn't write its call
// to the session file until it's answered, so the screen is the source for
// prompts; the transcript's pending call (a command, a plan) is context.

var (
	// ❯ marks Claude Code's selected option, › Codex's.
	optionLine = regexp.MustCompile(`^\s*([❯›])?\s*(\d+)\.\s+(.*?)\s*$`)
	checkbox   = regexp.MustCompile(`^\[([ ✔✓xX])\]\s*`)
	// Claude Code's spinner: a frame glyph, a capitalised word (or a few,
	// when it shows the current task) ending in an ellipsis, then its
	// timer in brackets or nothing. Once done it says "✻ Worked for 23s",
	// with no ellipsis, so that isn't read as working.
	spinnerLine   = regexp.MustCompile(`^\s*[✻✳✶✢✽·*+]\s+(\p{Lu}[^…()]{0,60}?…)\s*(\(.*\))?\s*$`)
	interruptLine = regexp.MustCompile(`^\s*[✻✳✶✢✽·*+•●]\s+\p{Lu}.*\(.*esc to (interrupt|stop).*\)\s*$`)
	// The footer under the input box while it works: "⏵⏵ auto mode on
	// (shift+tab to cycle) · esc to interrupt".
	footerInterrupt = regexp.MustCompile(`(^|·)\s*esc to interrupt\s*$`)
)

// Screen is what the prompt reader found on a pane's screen.
type Screen struct {
	Options      []string // the menu's options, numbered from 1
	Descriptions []string
	Checked      []bool
	Multi        bool
	Checkboxes   int    // multi-select: options 1 to this are checkboxes
	Selected     int    // 1-based
	Title        string // the paragraph above the menu
	Tabs         string // the question tool's tab bar (several questions)
	Arrows       bool   // options aren't numbered: picked with ↑↓ and Enter
	Working      bool   // the agent is busy ("esc to interrupt")
	Status       string // the spinner line
	// A question with previews (see previewBeside): picked with a number
	// and Enter, and notes can go with it ("n", the text, Enter).
	Previews     bool
	NotesEditing bool // its notes field is open (typing goes there, Esc closes it)
	// Body is the prompt's whole text above its options, as shown (the
	// command a permission prompt asks about, every line of it), and Key
	// which prompt it is: a digest of that text and the options, the same
	// however the selection moves, what's ticked, or the terminal's width
	// wraps it. Empty with no menu.
	Body string
	Key  string
	// The question tool's Submit step: what it lists as answered.
	Review []*pb.AgentReviewItem
}

// promptBody is the text of the dialog above a menu whose first option is
// on line start: up to its top rule (Claude's), or with none, the
// conversation above it (Codex's • and └, Claude's ⏺ and ⎿), with box
// sides and dashed rules dropped, indentation shared by every line taken
// off, and runs of blank lines made one. A dialog that runs off the top of
// the screen starts with "…": its first lines aren't there to show.
func promptBody(lines []string, start int) string {
	// Claude's top rule: a line of ─ across the screen.
	rule := func(t string) bool { return utf8.RuneCountInString(t) >= 10 && strings.Trim(t, "─━") == "" }
	ruled := false // everything under it is the dialog's
	for i := start - 1; i >= 0 && !ruled; i-- {
		ruled = rule(strings.TrimSpace(lines[i]))
	}
	var body []string
	whole := false
	for i := start - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if rule(t) || !ruled && (strings.HasPrefix(t, "⏺") || strings.HasPrefix(t, "⎿") || strings.HasPrefix(t, "•") ||
			strings.HasPrefix(t, "└") || strings.HasPrefix(t, "●") || strings.HasPrefix(t, "✔") ||
			strings.HasPrefix(t, "╭") || strings.HasPrefix(t, "╰") || strings.HasPrefix(t, "┌")) {
			whole = true
			break
		}
		if strings.Trim(t, "╌┄ ") == "" && t != "" {
			continue // a dashed rule around the command
		}
		l := strings.TrimRight(lines[i], " ")
		// Box sides: "│ text │".
		if s := strings.TrimLeft(l, " "); strings.HasPrefix(s, "│") || strings.HasPrefix(s, "┃") {
			l = strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(s[len("│"):], " "), "│"), "┃"), " ")
		}
		body = append(body, l)
	}
	slices.Reverse(body)
	indent := -1
	for _, l := range body {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " ")); indent < 0 || n < indent {
			indent = n
		}
	}
	var out []string
	for _, l := range body {
		if strings.TrimSpace(l) == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, l[indent:])
	}
	if !whole && len(out) > 0 {
		out = append([]string{"…"}, out...)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// promptKey is the digest naming a prompt: its text and options, with
// every space taken out, so wrapping it differently doesn't change it.
func promptKey(body string, options []string) string {
	nospace := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	h := sha256.New()
	h.Write([]byte(nospace(body)))
	for _, o := range options {
		h.Write([]byte{0})
		h.Write([]byte(nospace(o)))
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// A question whose options have previews puts each option's beside the
// list, boxed, with notes under it and its own footer:
//
//	❯ 1. Cmd-P quick open             ┌────────────────────────┐
//	    (Recommended)                 │ ⌘P  Quick Open         │
//	  2. Cmd-O quick open             └────────────────────────┘
//
//	                                  Notes: press n to add notes
//	──────────────────────────────────────────────────────────────
//	  Chat about this
//
//	Enter to select · ↑/↓ to navigate · n to add notes · Esc to cancel
//
// A number only moves the selection there; labels wrap onto the lines under
// them (no descriptions are shown), and the answered option says ✔.
var (
	previewBeside = regexp.MustCompile(`\s{2,}[┌│└├╭╰].*$`)
	notesLine     = regexp.MustCompile(`^\s*Notes:\s*(.*?)\s*$`)
)

// ReadScreen finds a menu prompt at the bottom of the screen.
func ReadScreen(text string) Screen {
	var sc Screen
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	// A spinner that says how to interrupt it (older Claude Code; Codex's
	// "• Working (12s • esc to interrupt)"). Only a spinner line: the
	// conversation above can quote the words.
	for _, l := range lines {
		if interruptLine.MatchString(l) {
			sc.Working = true
			sc.Status = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "✻✳✶✢✽·*+•● "))
		}
	}
	// Claude Code's spinner line leads with the word it picked for working
	// ("✽ Percolating… (23s · ↓ 1.9k tokens)"): that word is the status. The
	// last such line, as the spinner is under the conversation; none, and
	// the app says "Working…".
	// It's working when the footer under it offers to interrupt (newer
	// Claude Code puts "esc to interrupt" there): no hook fires for /compact,
	// so that's all that says "Compacting conversation…" is working.
	for i := len(lines) - 1; i >= 0; i-- {
		if m := spinnerLine.FindStringSubmatch(lines[i]); m != nil {
			sc.Status = m[1]
			for _, l := range lines[i+1:] {
				if footerInterrupt.MatchString(l) {
					sc.Working = true
				}
			}
			break
		}
	}
	// The footer hints under a menu: "Esc to cancel" (questions, permissions)
	// or "ctrl+g to edit" (plans); Codex's "Press enter to confirm or esc to
	// cancel" (approvals) and "enter select · esc back" (its pickers).
	footer := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-10 && footer < 0; i-- {
		for _, f := range []string{"Esc to cancel", "ctrl+g to edit", "Enter to select", " to confirm or ", "esc back"} {
			if strings.Contains(lines[i], f) {
				footer = i
				break
			}
		}
	}
	// The question tool's Submit tab has no footer: its menu ends the screen.
	if footer < 0 && len(lines) > 0 && optionLine.MatchString(lines[len(lines)-1]) {
		footer = len(lines)
	}
	if footer < 0 {
		return sc
	}
	if footer < len(lines) && strings.Contains(lines[footer], "n to add notes") {
		sc.Previews = true
		// The notes field is open: its footer offers the editor (ctrl+g),
		// or it shows its placeholder. (Its text alone doesn't say: closed,
		// the notes stay shown.)
		sc.NotesEditing = strings.Contains(lines[footer], "ctrl+g to edit")
		lines = slices.Clone(lines)
		for i := max(0, footer-60); i < footer; i++ {
			l := previewBeside.ReplaceAllString(lines[i], "")
			if m := notesLine.FindStringSubmatch(l); m != nil {
				sc.NotesEditing = sc.NotesEditing || strings.HasPrefix(m[1], "Add notes on this")
				l = ""
			}
			lines[i] = l
		}
	}
	// Options are the numbered lines above the footer, read upward; the lines
	// between them are their descriptions.
	type opt struct {
		label, desc  string
		checked, box bool
	}
	opts := map[int]*opt{}
	start, sel := -1, 0
	var desc []string
	for i := footer - 1; i >= 0 && i >= footer-60; i-- {
		t := strings.TrimSpace(lines[i])
		m := optionLine.FindStringSubmatch(lines[i])
		if m == nil {
			if start >= 0 && (t == "" || strings.HasSuffix(t, "?")) && i < start-1 {
				break
			}
			// (A multi-select question's "Submit" line under its options is
			// the form's, not a description.)
			if t != "" && t != "Submit" && !strings.HasPrefix(t, "─") && !strings.HasPrefix(t, "╌") {
				desc = append([]string{t}, desc...)
			} else {
				desc = nil
			}
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if _, dup := opts[n]; dup {
			break
		}
		o := &opt{label: strings.TrimSpace(m[3])}
		if sc.Previews {
			// Its label wraps onto the lines under it; ✔ marks the answer.
			o.label = strings.TrimSpace(strings.TrimSuffix(o.label, "✔"))
			if len(desc) > 0 {
				o.label += " " + strings.Join(desc, " ")
			}
			desc = nil
		}
		// Codex's pickers put an option's description beside it, after a gap.
		if l, d, ok := strings.Cut(o.label, "  "); ok && !sc.Previews && strings.TrimSpace(d) != "" {
			o.label, desc = strings.TrimSpace(l), []string{strings.TrimSpace(d)}
		}
		if cb := checkbox.FindStringSubmatch(o.label); cb != nil {
			o.checked, o.box = cb[1] != " ", true
			o.label = strings.TrimSpace(o.label[len(cb[0]):])
			sc.Multi = true
		}
		if len(desc) > 0 && !strings.Contains(strings.Join(desc, " "), "to approve with this feedback") {
			o.desc = strings.Join(desc, " ")
		}
		desc = nil
		opts[n] = o
		if m[1] != "" {
			sel = n
		}
		start = i
	}
	for n := 1; ; n++ {
		o, ok := opts[n]
		if !ok {
			break
		}
		sc.Options = append(sc.Options, o.label)
		sc.Descriptions = append(sc.Descriptions, o.desc)
		sc.Checked = append(sc.Checked, o.checked)
		if o.box {
			sc.Checkboxes = n
		}
	}
	sc.Selected = sel
	if len(sc.Options) == 0 {
		readArrowMenu(&sc, lines, footer)
		return sc
	}
	// The title is the paragraph above the menu (wrapped lines joined), and
	// above that, the question tool's tab bar when there are several.
	if start > 0 {
		i := start - 1
		for i >= 0 && strings.TrimSpace(lines[i]) == "" {
			i--
		}
		var para []string
		for ; i >= 0; i-- {
			t := strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[i]), "│┃|"))
			if t == "" || strings.HasPrefix(t, "─") || strings.HasPrefix(t, "╌") || strings.HasPrefix(t, "←") ||
				strings.HasPrefix(t, "•") || strings.HasPrefix(t, "✔") || strings.HasPrefix(t, "⏺") || strings.HasPrefix(t, "⎿") {
				break // the conversation above it (Codex's • and ✔, Claude's ⏺)
			}
			para = append([]string{t}, para...)
		}
		sc.Title = strings.Join(para, " ")
		for j := i; j >= 0 && j >= i-3; j-- {
			if t := strings.TrimSpace(lines[j]); strings.HasPrefix(t, "←") {
				sc.Tabs = t
				break
			}
		}
		sc.Body = promptBody(lines, start)
		if r, ask := readReview(lines, start); len(r) > 0 {
			sc.Review, sc.Title = r, ask
		}
	}
	sc.Key = promptKey(sc.Body, sc.Options)
	return sc
}

// readReview reads the question tool's Submit step, the questions and their
// answers between its heading and the menu (each wraps under itself):
//
//	Review your answers
//
//	 ● Pick a number.
//	   → 2
//	 ● Pick seasons.
//	   → Spring, Autumn
//
//	Ready to submit your answers?
//
// and what it says besides them (that line, a warning), or its heading when
// nothing: the card's title.
func readReview(lines []string, start int) ([]*pb.AgentReviewItem, string) {
	head := -1
	for i := start - 1; i >= 0 && i >= start-80; i-- {
		if strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[i]), "│┃|")) == "Review your answers" {
			head = i
			break
		}
	}
	if head < 0 {
		return nil, ""
	}
	var items []*pb.AgentReviewItem
	var rest []string
	var field *string // what a wrapped line continues
	bullet := 0       // the ● line's indent: a wrapped line is further in
	for _, l := range lines[head+1 : start] {
		l = strings.TrimRight(strings.Trim(l, "│┃"), " ")
		t := strings.TrimSpace(l)
		in := len(l) - len(strings.TrimLeft(l, " "))
		switch {
		case strings.HasPrefix(t, "●"):
			items = append(items, &pb.AgentReviewItem{Question: strings.TrimSpace(strings.TrimPrefix(t, "●"))})
			field, bullet = &items[len(items)-1].Question, in
		case strings.HasPrefix(t, "→") && len(items) > 0:
			a := &items[len(items)-1].Answer
			*a = strings.TrimSpace(strings.TrimPrefix(t, "→"))
			field = a
		case t == "":
			field = nil
		case field != nil && in > bullet:
			*field += " " + t
		default:
			field = nil
			rest = append(rest, t)
		}
	}
	if len(rest) == 0 {
		return items, "Review your answers"
	}
	return items, strings.Join(rest, " ")
}

// markedLine is a menu's selected option without a number: "❯ No, exit".
var markedLine = regexp.MustCompile(`^( *)[❯›] (\S.*?)\s*$`)

// readArrowMenu reads a menu whose options have no numbers, right above the
// footer, as Claude Code's workspace trust dialog shows one:
//
//	Accessing workspace:
//
//	/Users/me
//
//	Quick safety check: Is this a project you created or one you trust? …
//
//	❯ No, exit
//	  Yes, I trust this folder
//
//	Enter to confirm · Esc to cancel
//
// One option is marked, the rest are indented to its label's column. The
// title is the whole dialog above it, paragraph by paragraph.
//
// Only a dialog that fills the screen counts: its "Enter to confirm" footer
// is the last line, so a menu quoted in the conversation (above the input
// box) or a draft in an input box is never read as one.
func readArrowMenu(sc *Screen, lines []string, footer int) {
	if footer != len(lines)-1 || !strings.Contains(lines[footer], "Enter to confirm") {
		return
	}
	end := footer - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	start := end
	for start > 0 && strings.TrimSpace(lines[start-1]) != "" {
		start--
	}
	// A paragraph of its own, with the dialog's text above it.
	if end < 0 || end-start > 20 || start == 0 {
		return
	}
	col, sel := -1, 0
	for i := start; i <= end; i++ {
		if m := markedLine.FindStringSubmatch(lines[i]); m != nil {
			if col >= 0 {
				return // two marked lines: not a menu
			}
			col, sel = len(m[1])+2, i-start+1
		}
	}
	if col < 0 || end == start {
		return
	}
	var opts []string
	for i := start; i <= end; i++ {
		l := lines[i]
		if i-start+1 == sel {
			opts = append(opts, markedLine.FindStringSubmatch(l)[2])
			continue
		}
		if len(l) <= col || strings.TrimSpace(l[:col]) != "" || l[col] == ' ' {
			return // not lined up with the marked option
		}
		opts = append(opts, strings.TrimSpace(l))
	}
	sc.Options, sc.Selected, sc.Arrows = opts, sel, true
	sc.Descriptions, sc.Checked = make([]string, len(opts)), make([]bool, len(opts))
	var paras []string
	var para []string
	flush := func() {
		if len(para) > 0 {
			paras = append([]string{strings.Join(para, " ")}, paras...)
			para = nil
		}
	}
	for i := start - 1; i >= 0 && i >= start-40; i-- {
		t := strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[i]), "│┃|"))
		if strings.HasPrefix(t, "─") || strings.HasPrefix(t, "╌") || strings.HasPrefix(t, "⏺") || strings.HasPrefix(t, "⎿") {
			break // the dialog's top rule, or the conversation above it
		}
		if t == "" {
			flush()
			continue
		}
		para = append([]string{t}, para...)
	}
	flush()
	sc.Title = strings.Join(paras, "\n\n")
	sc.Body, sc.Key = sc.Title, promptKey(sc.Title, sc.Options)
}

// otherOption is the option that takes typed text, if any.
func otherOption(opts []string) int {
	for i, o := range opts {
		if strings.HasPrefix(o, "Type something") || strings.HasPrefix(o, "Tell Claude what to change") {
			return i + 1
		}
	}
	return 0
}

// Prompt is the screen's menu as the agent view offers it, with the pending
// call (if the transcript has it) deciding what kind of prompt it is.
func Prompt(sc Screen, pending *pb.AgentBlock) *pb.AgentPrompt {
	if len(sc.Options) == 0 {
		return &pb.AgentPrompt{}
	}
	p := &pb.AgentPrompt{Kind: pb.AgentPrompt_KIND_QUESTION, Title: sc.Title, Options: sc.Options, Selected: int32(sc.Selected),
		Descriptions: sc.Descriptions, MultiSelect: sc.Multi, Tabs: sc.Tabs, Other: int32(otherOption(sc.Options)), Notes: sc.Previews,
		PromptId: sc.Key, Body: sc.Body, Review: sc.Review}
	if sc.Multi {
		p.Checked, p.Checkboxes = sc.Checked, int32(sc.Checkboxes)
	}
	if pending != nil {
		p.BlockId = pending.Id
		switch pending.Kind {
		case pb.AgentBlock_KIND_PLAN:
			p.Kind = pb.AgentPrompt_KIND_PLAN
		case pb.AgentBlock_KIND_QUESTION:
			// The screen cuts long text to the terminal's width: take the
			// question and descriptions from the call itself.
			if q := matchQuestion(pending.Questions, sc.Options); q != nil {
				p.Title = q.Question
				for i, o := range q.Options {
					if i < len(p.Descriptions) && o.Description != "" {
						p.Descriptions[i] = o.Description
					}
					if o.Preview != "" && i < len(p.Options) {
						if p.Previews == nil {
							p.Previews = make([]string, len(p.Options))
						}
						p.Previews[i] = o.Preview
					}
				}
			}
		default:
			p.Kind = pb.AgentPrompt_KIND_PERMISSION
		}
	}
	return p
}

// matchQuestion is the question whose options the screen is showing (the
// screen adds its own after them, and may cut labels short with "…").
func matchQuestion(qs []*pb.AgentQuestion, shown []string) *pb.AgentQuestion {
	for _, q := range qs {
		if len(q.Options) == 0 || len(q.Options) > len(shown) {
			continue
		}
		ok := true
		for i, o := range q.Options {
			s := strings.TrimSuffix(strings.TrimSpace(shown[i]), "…")
			if s == "" || !strings.HasPrefix(o.Label, s) {
				ok = false
				break
			}
		}
		if ok {
			return q
		}
	}
	return nil
}

// InputLines is how many lines the agent's input box holds on screen (Claude
// Code's ❯, Codex's ›, and the lines continuing it), at least 1: what to
// clear before typing a message into it.
func InputLines(text string) int {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	start := inputStart(lines, "❯", "›")
	if start < 0 {
		return 1
	}
	n := 1
	for _, l := range lines[start+1:] {
		if !strings.HasPrefix(l, "  ") || strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "─") {
			break
		}
		n++
	}
	return min(n, 40)
}

// inputStart is the line the agent's input box starts on (the last that
// starts with one of marks, Claude Code's ❯ or Codex's ›), -1 if none: not
// a row of Claude Code's agent list under it, which ❯ marks when it's
// selected.
func inputStart(lines []string, marks ...string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimLeft(lines[i], " ")
		for _, m := range marks {
			if strings.HasPrefix(t, m) && !agentRow.MatchString(lines[i]) {
				return i
			}
		}
	}
	return -1
}

// agentRow is a row of the agent list Claude Code shows under its input
// box while subagents run, ⏺ marking the conversation the terminal shows
// (Enter on a row shows that one, and the input box then messages it), ❯
// the row selected (↓ from the input box selects; typing goes back to it):
//
//	❯ ◯ main
//	  ⏺ general-purpose  Run the tests                 19s · ↓ 42.5k tokens
var agentRow = regexp.MustCompile(`^(❯ |  )([⏺◯]) (\S.*)$`)

// AgentList is Claude Code's agent list, as ReadAgentList finds it.
type AgentList struct {
	Rows     int // 0: none shown
	Main     int // the main conversation's row
	Shown    int // the row the terminal shows (⏺)
	Selected int // the row selected, -1 when the input box has the keys
}

// ViewingSubagent: the terminal shows a subagent's conversation, not the
// main one, so what's typed goes to the subagent.
func (l AgentList) ViewingSubagent() bool { return l.Rows > 0 && l.Shown != l.Main }

// ReadAgentList finds Claude Code's agent list at the bottom of the screen
// (its last rows, after the input box's footer).
func ReadAgentList(text string) AgentList {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	l := AgentList{Main: -1, Shown: -1, Selected: -1}
	end := len(lines)
	start := end
	for start > 0 && agentRow.MatchString(lines[start-1]) {
		start--
	}
	for i, line := range lines[start:end] {
		m := agentRow.FindStringSubmatch(line)
		if m[1] == "❯ " {
			l.Selected = i
		}
		if m[2] == "⏺" {
			l.Shown = i
		}
		if strings.TrimSpace(m[3]) == "main" {
			l.Main = i
		}
	}
	if l.Main < 0 || l.Shown < 0 {
		return AgentList{Main: -1, Shown: -1, Selected: -1} // not the list
	}
	l.Rows = end - start
	return l
}

// ClearInput is the keys that empty an agent's input box of n lines (Ctrl+E
// to the end of a line, Ctrl+U to clear it, Backspace to join the line
// above), which do nothing to an empty one, in Claude Code and Codex alike.
func ClearInput(n int) []byte {
	return bytes.Repeat([]byte{0x05, 0x15, 0x7F}, max(n, 1))
}

// InputDraft is what's typed in the agent's input box, from the screen with
// faint text (the box's placeholder) left out: its lines after the ❯ or ›,
// continuation lines' indent dropped.
// HasInput reports whether an agent's message box (its ❯ or › prompt) is on
// screen.
func HasInput(text string) bool {
	return inputStart(strings.Split(strings.TrimRight(text, "\n "), "\n"), "❯", "›") >= 0
}

func InputDraft(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n "), "\n")
	start := inputStart(lines, "❯", "›")
	if start < 0 {
		return ""
	}
	first := strings.TrimLeft(lines[start], " ")
	first = strings.TrimPrefix(strings.TrimPrefix(first, "❯"), "›")
	out := []string{strings.TrimPrefix(first, " ")}
	for _, l := range lines[start+1:] {
		if !strings.HasPrefix(l, "  ") || strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "─") {
			break
		}
		out = append(out, strings.TrimPrefix(l, "  "))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// bangFooter: lines Claude Code shows under a running "!" command that
// aren't its output: how long it's run, and the key to background it.
var bangFooter = regexp.MustCompile(`^\((\d+[hms] ?)+\)$|^\(ctrl\+b to run in background\)$|^Running…$`)

// BangOutput is what a "!" command running at Claude Code's prompt has
// printed so far, from the screen (its transcript only has it when it's
// done): the lines under the last "! cmd" above the input box, after its
// ⎿. Running: one is (its timer or Running… shows: Claude Code's spinner
// doesn't, nor do its hooks say it's working).
func BangOutput(text string) (out string, running bool) {
	lines := strings.Split(text, "\n")
	end := len(lines)
	if i := inputStart(lines, "❯"); i >= 0 {
		end = i
	}
	// The input box's top rule, and blank lines above it.
	for end > 0 && (strings.TrimSpace(lines[end-1]) == "" || strings.HasPrefix(strings.TrimSpace(lines[end-1]), "─")) {
		end--
	}
	start := -1
	for i := end - 1; i >= 0; i-- {
		l := lines[i]
		if strings.HasPrefix(l, "! ") {
			start = i
			break
		}
		// Only the command's own lines: its output, indented, or its
		// command's wrapped lines.
		if !strings.HasPrefix(l, "  ") {
			return "", false
		}
	}
	if start < 0 {
		return "", false
	}
	var printed []string
	in := false
	for _, l := range lines[start+1 : end] {
		if t, ok := strings.CutPrefix(l, "  ⎿"); ok {
			// Its first line: ⎿ then a space and a no-break space.
			in = true
			l = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(t, " "), "\u00a0"), " ")
		} else if !in {
			continue // the command, wrapped
		} else {
			l = strings.TrimPrefix(l, "     ")
		}
		if bangFooter.MatchString(strings.TrimSpace(l)) {
			running = true
			continue
		}
		printed = append(printed, strings.TrimRight(l, " "))
	}
	if !running {
		return "", false // done: its transcript has it
	}
	return strings.Trim(strings.Join(printed, "\n"), "\n"), true
}
