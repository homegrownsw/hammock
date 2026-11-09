package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A settings.json that's a symlink into a dotfiles repo stays a symlink:
// the file it points at is replaced, with its mode, and nothing else in
// the folder is touched (a planted settings.json.tmp symlink isn't
// followed).
func TestWriteConfigKeepsSymlink(t *testing.T) {
	dir := t.TempDir()
	dotfiles := filepath.Join(dir, "dotfiles")
	claude := filepath.Join(dir, ".claude")
	for _, d := range []string{dotfiles, claude} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(dotfiles, "settings.json")
	if err := os.WriteFile(real, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(claude, "settings.json")
	if err := os.Symlink("../dotfiles/settings.json", link); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, link+".tmp"); err != nil {
		t.Fatal(err)
	}

	if err := writeConfig(link, []byte(`{"old":true}`), []byte(`{"new":true}`), true, false); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings.json isn't a symlink any more: %v %v", st.Mode(), err)
	}
	if b, _ := os.ReadFile(real); string(b) != `{"new":true}` {
		t.Errorf("the linked file has %q", b)
	}
	if st, _ := os.Stat(real); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, want the old file's 0644", st.Mode().Perm())
	}
	if b, _ := os.ReadFile(victim); string(b) != "untouched" {
		t.Errorf("followed the .tmp symlink: victim has %q", b)
	}
	backups, _ := filepath.Glob(link + ".hammock-backup-*")
	if len(backups) != 1 {
		t.Fatalf("backups: %v", backups)
	}
	if b, _ := os.ReadFile(backups[0]); string(b) != `{"old":true}` {
		t.Errorf("backup has %q", b)
	}
	// No temp files left beside the real file.
	ents, _ := os.ReadDir(dotfiles)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".hammock-") {
			t.Errorf("left %s behind", e.Name())
		}
	}

	// Again in the same second: a second backup, not an error.
	if err := writeConfig(link, []byte(`{"new":true}`), []byte(`{"newer":true}`), true, false); err != nil {
		t.Fatal(err)
	}
}

// A new config file (none there yet) is made 0600.
func TestWriteConfigNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "hooks.json")
	if err := writeConfig(path, nil, []byte("{}"), true, false); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		t.Fatalf("got %v %v", st, err)
	}
}
