package daemon

import (
	"testing"

	"github.com/homegrownsw/hammock/internal/textbuf"
)

func TestLineEndsRoundTrip(t *testing.T) {
	for _, file := range []string{
		"", "a", "a\n", "a\nb\n", "a\r\nb\r\n", "a\r\nb", "a\nb\r\nc\n", "a\r\nb\nc\r\nd", "x\ry\r\n", "\r\n\n\r\n",
	} {
		text, e := normaliseText([]byte(file))
		if out, _ := e.render(text, text); string(out) != file {
			t.Errorf("%q: unchanged, saved as %q", file, out)
		}
	}
}

func TestLineEndsMixedKeepsEachLine(t *testing.T) {
	// Mostly CRLF, one LF line: an edit elsewhere leaves that line alone,
	// and new lines take the majority's.
	file := "one\r\ntwo\nthree\r\nfour\r\n"
	text, e := normaliseText([]byte(file))
	edited := textbuf.FromString("one\nNEW\ntwo\nthree changed\nfour\n")
	out, wrote := e.render(edited, text)
	if want := "one\r\nNEW\r\ntwo\nthree changed\r\nfour\r\n"; string(out) != want {
		t.Fatalf("saved %q, want %q", out, want)
	}
	// And again from what was written: unchanged.
	if again, _ := wrote.render(edited, edited); string(again) != string(out) {
		t.Fatalf("second save %q, want %q", again, out)
	}
}

func TestLineEndsUniform(t *testing.T) {
	text, e := normaliseText([]byte("a\r\nb\r\n"))
	out, _ := e.render(textbuf.FromString("a\nnew\nb\n"), text)
	if string(out) != "a\r\nnew\r\nb\r\n" {
		t.Fatalf("got %q", out)
	}
	text, e = normaliseText([]byte("a\nb\n"))
	out, _ = e.render(textbuf.FromString("a\nnew\nb\n"), text)
	if string(out) != "a\nnew\nb\n" {
		t.Fatalf("got %q", out)
	}
}
