package main

import (
	"fmt"
	"strings"
	"time"
)

// Typing into a pane (hmk send, the send_to_pane tool): text, then named
// keys, then Enter, each written on its own. Agents can't put control
// characters in tool arguments, so keys are named; and a TUI (Claude Code,
// Codex) takes text and Enter arriving together as a paste, typing the Enter
// rather than submitting.

// keyGap separates the writes, long enough that a TUI sees separate keys.
const keyGap = 80 * time.Millisecond

var terminalKeys = map[string]string{
	"enter": "\r", "return": "\r", "tab": "\t", "shift-tab": "\x1b[Z", "space": " ",
	"escape": "\x1b", "esc": "\x1b", "backspace": "\x7f", "delete": "\x1b[3~",
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"arrowup": "\x1b[A", "arrowdown": "\x1b[B", "arrowright": "\x1b[C", "arrowleft": "\x1b[D", // as browser_press_key names them
	"home": "\x1b[H", "end": "\x1b[F", "pageup": "\x1b[5~", "pagedown": "\x1b[6~",
	"f1": "\x1bOP", "f2": "\x1bOQ", "f3": "\x1bOR", "f4": "\x1bOS",
	"f5": "\x1b[15~", "f6": "\x1b[17~", "f7": "\x1b[18~", "f8": "\x1b[19~",
	"f9": "\x1b[20~", "f10": "\x1b[21~", "f11": "\x1b[23~", "f12": "\x1b[24~",
}

// keyNames lists the names keyBytes knows, for usage text and errors.
const keyNames = "enter, tab, shift-tab, space, escape, backspace, delete, up, down, left, right, home, end, pageup, pagedown, f1–f12, ctrl-a … ctrl-z"

// keyBytes is what a named key sends: one of terminalKeys, or ctrl-<letter>.
func keyBytes(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if b, ok := terminalKeys[n]; ok {
		return b, nil
	}
	if l, ok := strings.CutPrefix(n, "ctrl-"); ok && len(l) == 1 && l[0] >= 'a' && l[0] <= 'z' {
		return string(rune(l[0] - 'a' + 1)), nil
	}
	return "", fmt.Errorf("unknown key %q (known: %s)", name, keyNames)
}

// typingWrites is what to write, in order: the text, each key, then Enter.
func typingWrites(text string, keys []string, enter bool) ([]string, error) {
	var out []string
	if text != "" {
		out = append(out, text)
	}
	for _, k := range keys {
		b, err := keyBytes(k)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if enter {
		out = append(out, "\r")
	}
	return out, nil
}

// typeInto writes each piece with send, keyGap apart.
func typeInto(writes []string, send func(string) error) error {
	for i, w := range writes {
		if i > 0 {
			time.Sleep(keyGap)
		}
		if err := send(w); err != nil {
			return err
		}
	}
	return nil
}
