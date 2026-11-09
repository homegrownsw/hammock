//go:build ghostty

package vt

import (
	"strings"
	"testing"
)

// A client attaching mid-session learns bracketed paste from the snapshot.
func TestSnapshotKeepsBracketedPaste(t *testing.T) {
	m := New(80, 24)
	defer m.Close()
	m.Write([]byte("\x1b[?2004hhello"))
	if s := string(m.Snapshot()); !strings.Contains(s, "?2004h") {
		t.Fatalf("snapshot lacks ?2004h: %q", s)
	}
}
