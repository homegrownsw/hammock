package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Text the daemon hands hmk comes from many hands: Group and tab names from
// a shared Group's host, computer names from a linked computer or one asking
// to link, page titles and URLs from web pages, agents' summaries, session
// transcripts, audit rows. Printed raw, a control sequence in any of them
// would reach the terminal: OSC 52 sets the clipboard, CSI clears the screen
// and moves the cursor (rewriting a warning or a link code above it), and
// bidi overrides make text read differently from what it is. termSafe and
// termSafeText make such text inert before hmk prints it for a person (JSON
// output is escaped already; attach, read and the TUI pass a pane's own
// bytes on by design).

// termSafe is s for one line of a terminal: tabs and line breaks become
// spaces, other control characters (C0, DEL, C1), bidi controls and invalid
// UTF-8 become U+FFFD.
func termSafe(s string) string { return sanitizeTerm(s, false) }

// termSafeText is termSafe for text that's meant to run over several lines:
// it keeps newlines and tabs (a carriage return still goes, as it could
// overwrite a line).
func termSafeText(s string) string { return sanitizeTerm(s, true) }

func sanitizeTerm(s string, multiline bool) string {
	clean := true
	for _, r := range s {
		// (A real U+FFFD is harmless too, but rare enough not to tell
		// apart from invalid UTF-8 here: it's written back as itself.)
		if r == utf8.RuneError || termUnsafe(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			if multiline {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		case r == '\r' && !multiline:
			b.WriteByte(' ')
		case r == utf8.RuneError || termUnsafe(r):
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// termUnsafe: C0 controls, DEL, C1 controls, and the characters that
// reorder text (bidi embeddings, overrides, isolates and marks) or break
// lines (U+2028, U+2029).
func termUnsafe(r rune) bool {
	switch {
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069,
		r == 0x200e, r == 0x200f, r == 0x061c, r == 0x2028, r == 0x2029:
		return true
	}
	return false
}

// printfSafe is fmt.Printf with every string argument (and error) passed
// through termSafe: for lines that show what the daemon sent.
func printfSafe(format string, a ...any) {
	fmt.Print(sprintfSafe(format, a...))
}

// sprintfSafe is fmt.Sprintf with termSafe string arguments.
func sprintfSafe(format string, a ...any) string {
	safe := make([]any, len(a))
	for i, v := range a {
		switch x := v.(type) {
		case string:
			safe[i] = termSafe(x)
		case []string:
			s := make([]string, len(x))
			for j := range x {
				s[j] = termSafe(x[j])
			}
			safe[i] = s
		case error:
			safe[i] = termSafe(x.Error())
		default:
			safe[i] = v
		}
	}
	return fmt.Sprintf(format, safe...)
}

// printlnSafe prints one line of text from the daemon.
func printlnSafe(s string) { fmt.Println(termSafe(s)) }

// printValue prints a single value a script reads (a Group's folder, a new
// id: `cd "$(hmk group folder work)"`): exactly as it is when stdout is a
// pipe or a file, where a path with a tab or a newline must survive, and
// made inert (termSafe) when it's a terminal, where a person reads it.
func printValue(s string) { fmt.Println(valueFor(s, stdoutIsTerminal())) }

func valueFor(s string, terminal bool) string {
	if terminal {
		return termSafe(s)
	}
	return s
}
