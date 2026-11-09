package simshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileName(t *testing.T) {
	at := time.Date(2026, 11, 4, 10, 15, 32, 0, time.Local)
	for device, want := range map[string]string{
		"iPhone 17 Pro": "Simulator Screenshot - iPhone 17 Pro - 2026-11-04 at 10.15.32.png",
		"a/b:c":         "Simulator Screenshot - a-b-c - 2026-11-04 at 10.15.32.png",
		"":              "Simulator Screenshot - Simulator - 2026-11-04 at 10.15.32.png",
	} {
		if got := FileName(device, at); got != want {
			t.Errorf("FileName(%q) = %q, want %q", device, got, want)
		}
	}
}

func TestSaveRefusesEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := Save(dir, "shot.png", nil); err == nil {
		t.Fatal("Save of no picture succeeded")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left %d files behind", len(entries))
	}
}

func TestSaveDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	p1, err := Save(dir, "shot.png", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Save(dir, "shot.png", []byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if p1 != filepath.Join(dir, "shot.png") || p2 != filepath.Join(dir, "shot 2.png") {
		t.Fatalf("paths = %q, %q", p1, p2)
	}
	if b, _ := os.ReadFile(p1); string(b) != "one" {
		t.Errorf("first file = %q, want it untouched", b)
	}
	if b, _ := os.ReadFile(p2); string(b) != "two" {
		t.Errorf("second file = %q, want %q", b, "two")
	}
}
