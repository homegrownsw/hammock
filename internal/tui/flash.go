package tui

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// How long a flash stays at the bottom right: an error longer, to be read.
const (
	flashFor      = 3 * time.Second
	errorFlashFor = 7 * time.Second
)

// rpcPrefix is how gRPC words an error ("rpc error: code = NotFound desc =
// no such pane"): only the desc is for people.
var rpcPrefix = regexp.MustCompile(`rpc error: code = \w+ desc = `)

// readableFlash is msg as a flash shows it, the gRPC wording taken out (its
// description capitalised where it starts the message, or follows "⚠ "),
// and whether it's an error: a "⚠" one, or one that came from gRPC.
func readableFlash(msg string) (string, bool) {
	isErr := strings.HasPrefix(msg, "⚠")
	loc := rpcPrefix.FindStringIndex(msg)
	if loc == nil {
		return msg, isErr
	}
	lead := strings.TrimSpace(strings.TrimPrefix(msg[:loc[0]], "⚠")) == ""
	msg = rpcPrefix.ReplaceAllString(msg, "") // (wrapped ones too)
	if r, n := utf8.DecodeRuneInString(msg[loc[0]:]); lead && r != utf8.RuneError {
		msg = msg[:loc[0]] + string(unicode.ToUpper(r)) + msg[loc[0]+n:]
	}
	return msg, true
}
