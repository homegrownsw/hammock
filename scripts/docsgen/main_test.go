package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A note's image is copied into the site and shown under its note, from a released version's notes and from
// a version's summary alike.
func TestWhatsNewImages(t *testing.T) {
	root := t.TempDir()
	write := func(path, s string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 2026.2.0: notes only.
	write("mac/v2026.2.0/windows.md", "tag: NEW\ndate: 2026-11-10\nimage: windows.gif Two windows\nWindows of\ntheir own.\n")
	write("mac/v2026.2.0/windows.gif", "GIF89a")
	write("mac/v2026.2.0/fix.md", "tag: FIXED\ndate: 2026-11-10\nA fix.\n")
	// 2026.1.0: its summary wins over its notes.
	write("mac/v2026.1.0/tabs.md", "tag: NEW\ndate: 2026-11-01\nimage: tabs.png Tabs\nTabs.\n")
	write("mac/v2026.1.0/tabs.png", "PNG")
	write("mac/v2026.1.0.md", "# Mac 2026.1.0\n\n## New\n\n- Tabs, summarised.\n  ![Tabs [wide]](v2026.1.0/tabs.png)\n- Plain.\n")
	write("ios/v2026.2.0/x.md", "tag: FIXED\ndate: 2026-11-10\nOn iPhone.\n")

	public := filepath.Join(t.TempDir(), "whats-new")
	if err := os.MkdirAll(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(public, "stale.png"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	page, err := whatsNewPage(root, public)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"- Windows of their own.\n  ![Two windows](/hammock/docs/whats-new/mac-2026.2.0-windows.gif)\n",
		"- A fix.\n",
		"- Tabs, summarised.\n  ![Tabs \\[wide\\]](/hammock/docs/whats-new/mac-2026.1.0-tabs.png)\n- Plain.\n",
		"- On iPhone.\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "image:") {
		t.Errorf("an image: line leaked into the text:\n%s", page)
	}
	for _, f := range []string{"mac-2026.2.0-windows.gif", "mac-2026.1.0-tabs.png"} {
		if _, err := os.Stat(filepath.Join(public, f)); err != nil {
			t.Errorf("%s wasn't copied: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(public, "stale.png")); err == nil {
		t.Error("an image from an earlier run was left behind")
	}
}

// A version's notes are shown by their order lines, then those with a picture, then by file name.
func TestWhatsNewOrder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ios", "v2026.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]string{
		"a-plain.md":   "tag: NEW\ndate: 2026-11-10\nPlain.\n",
		"b-picture.md": "tag: NEW\ndate: 2026-11-10\nimage: b.gif A picture\nWith a picture.\n",
		"c-placed.md":  "tag: NEW\ndate: 2026-11-10\norder: 1\nPlaced first.\n",
		"d-fixed.md":   "tag: FIXED\ndate: 2026-11-10\norder: 2\nFixed.\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	notes, err := appNotes(root, "ios")
	if err != nil || len(notes) != 1 {
		t.Fatalf("appNotes: %v, %d versions", err, len(notes))
	}
	var got []string
	for _, n := range append(notes[0].newNotes, notes[0].fixed...) {
		got = append(got, n.text)
	}
	if want := "Placed first.|With a picture.|Plain.|Fixed."; strings.Join(got, "|") != want {
		t.Errorf("order %q, want %q", strings.Join(got, "|"), want)
	}
}
