package main

import (
	"slices"
	"testing"
)

func TestTypingWrites(t *testing.T) {
	got, err := typingWrites("/quit", []string{"ctrl-c", "Escape", "F2", "up"}, true)
	want := []string{"/quit", "\x03", "\x1b", "\x1bOQ", "\x1b[A", "\r"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %q %v", got, err)
	}
	// Enter alone, and text with Enter, are separate writes.
	if got, _ := typingWrites("hi", nil, true); !slices.Equal(got, []string{"hi", "\r"}) {
		t.Fatalf("got %q", got)
	}
	if got, _ := typingWrites("", nil, true); !slices.Equal(got, []string{"\r"}) {
		t.Fatalf("got %q", got)
	}
	for _, bad := range []string{"ctrl-", "ctrl-1", "hyper-x", "f13"} {
		if _, err := typingWrites("", []string{bad}, false); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
