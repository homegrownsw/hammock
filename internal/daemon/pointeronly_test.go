package daemon

import "testing"

// Clicking into a waiting agent's pane, scrolling it or moving the pointer
// over it isn't answering it: only keys take it back to working.
func TestPointerOnly(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"\x1b[<0;12;5M", true},                  // SGR press
		{"\x1b[<0;12;5m", true},                  // SGR release
		{"\x1b[<35;40;20M\x1b[<35;41;20M", true}, // motion, two reports at once
		{"\x1b[<64;3;3M", true},                  // wheel
		{"\x1b[32;12;5M", true},                  // urxvt
		{"\x1b[M !!", true},                      // X10
		{"\x1b[I", true},                         // focus in
		{"\x1b[O\x1b[<0;1;1M", true},
		{"", false},
		{"1", false},
		{"\r", false},
		{"\x1b", false},                   // Escape
		{"\x1b[A", false},                 // up arrow
		{"\x1b[1;2A", false},              // shift-up
		{"\x1b[13;2u", false},             // kitty shift-enter
		{"\x1b[<0;12;5Mx", false},         // a click, then a key
		{"\x1b[<0;12M", false},            // cut short
		{"\x1b[200~text\x1b[201~", false}, // paste
	} {
		if got := pointerOnly([]byte(c.in)); got != c.want {
			t.Errorf("pointerOnly(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
