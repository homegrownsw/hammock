package vt

import (
	"strings"
	"testing"
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
		"file://":                   "",
		"http:///tmp":               "",
	} {
		if got := fileURLPath(u); got != want {
			t.Errorf("%s: got %q, want %q", u, got, want)
		}
	}
}
