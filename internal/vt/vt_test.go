package vt

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOSCTitleAndCwdAcrossChunks(t *testing.T) {
	var o OSC
	stream := "hello\x1b]2;my title\x07more\x1b]7;file:///Users/me/My%20Dir\x1b\\done"
	for i := 0; i < len(stream); i += 3 { // feed in awkward chunks
		end := min(i+3, len(stream))
		o.Write([]byte(stream[i:end]))
	}
	if o.Title != "my title" || o.Cwd != "/Users/me/My Dir" || !o.Changed {
		t.Fatalf("got %+v", o)
	}
}

// A title that isn't UTF-8 (Latin-1, or cut mid-character at maxOSC) is
// kept as the UTF-8 it has: protobuf can't send anything else.
func TestOSCTitleIsUTF8(t *testing.T) {
	var o OSC
	o.Write([]byte("\x1b]0;caf\xe9\x07"))
	if o.Title != "caf" {
		t.Fatalf("Latin-1 title: %q", o.Title)
	}
	long := "\x1b]2;x" + strings.Repeat("é", maxOSC) + "\x07" // cut mid-é
	o.Write([]byte(long))
	if !utf8.ValidString(o.Title) || !strings.HasPrefix(o.Title, "xéé") {
		t.Fatalf("long title isn't UTF-8: %q…", o.Title[:8])
	}
}

func TestMirrorTextAndSnapshot(t *testing.T) {
	m := New(40, 10)
	defer m.Close()
	m.Write([]byte("hello \x1b[1;32mworld\x1b[0m\r\nsecond line\r\n"))
	text := m.Text(false)
	if !strings.Contains(text, "hello world") || !strings.Contains(text, "second line") {
		t.Fatalf("text %q", text)
	}
	m2 := New(40, 10)
	defer m2.Close()
	m2.Write(m.Snapshot())
	if got := m2.Text(false); !strings.Contains(got, "hello world") || !strings.Contains(got, "second line") {
		t.Fatalf("replayed text %q", got)
	}
}

func TestOSC7OnlyAcceptsLocalCleanPaths(t *testing.T) {
	localHostname = func() string { return "My-Mac.local" }
	for u, want := range map[string]string{
		"file:///tmp/x":             "/tmp/x",
		"file://localhost/tmp/x":    "/tmp/x",
		"file://my-mac/tmp/x/":      "/tmp/x",
		"file://My-Mac.local/tmp/x": "/tmp/x",
		"file://otherhost/tmp/x":    "",
		"file:///tmp/../etc":        "",
		"file:///tmp/a%0Ab":         "",
		"file:///tmp/a%1b[31m":      "",
		"file:///tmp/caf%E9":        "", // Latin-1, not UTF-8
		"file:///tmp/caf%C3%A9":     "/tmp/café",
		"file://":                   "",
		"http:///tmp":               "",
	} {
		if got := fileURLPath(u); got != want {
			t.Errorf("%s: got %q, want %q", u, got, want)
		}
	}
}
