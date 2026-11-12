package tui

import "testing"

// A value wider than its field scrolls to keep the cursor in view, with a
// cell left for the cursor at the end.
func TestFieldWindowScrollsToCursor(t *testing.T) {
	f := &field{value: "~/Developer/app/some/long/folder"}
	f.cursor = len([]rune(f.value))
	shown, cx := f.window(10)
	if shown != "ong/folder"[1:] || cx != 9 {
		t.Fatalf("at the end: shown %q, cursor %d", shown, cx)
	}
	f.cursor = 0
	shown, cx = f.window(10)
	if cx != 0 || shown[:2] != "~/" {
		t.Fatalf("at home: shown %q, cursor %d", shown, cx)
	}
	// Moving right within the window doesn't scroll it.
	f.cursor = 5
	if _, cx = f.window(10); cx != 5 || f.off != 0 {
		t.Fatalf("inside the window: off %d, cursor %d", f.off, cx)
	}
	// A short value never scrolls.
	f = &field{value: "abc", cursor: 3, off: 2}
	if shown, cx = f.window(10); shown != "abc" || cx != 3 {
		t.Fatalf("short: shown %q, cursor %d", shown, cx)
	}
}

// Wide characters count as two cells.
func TestFieldWindowWide(t *testing.T) {
	f := &field{value: "日本語のフォルダ"}
	f.cursor = len([]rune(f.value))
	shown, cx := f.window(7)
	if shown != "フォルダ"[len("フ"):] || cx != 6 {
		t.Fatalf("shown %q, cursor %d", shown, cx)
	}
	if got := f.runeAt(2); got != f.off+1 {
		t.Fatalf("runeAt(2) = %d, want %d", got, f.off+1)
	}
}
