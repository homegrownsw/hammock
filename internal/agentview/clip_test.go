package agentview

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClipKeepsUTF8Valid(t *testing.T) {
	s := strings.Repeat("a", 9) + "─────" // 3-byte runes from byte 9
	for n := 9; n < 14; n++ {
		if got := clip(s, n); !utf8.ValidString(got) {
			t.Fatalf("clip(%d) = %q: invalid UTF-8", n, got)
		}
	}
}
