package agents

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAiderNewest(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "pkg", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := aiderNewest(sub); ok {
		t.Fatal("found a history that isn't there")
	}
	top := filepath.Join(repo, ".aider.chat.history.md")
	if err := os.WriteFile(top, []byte("\n# aider chat started at 2026-11-07 10:45:50\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, ok := aiderNewest(sub); !ok || p != top {
		t.Fatalf("in a subfolder: %q %v, want the repository's", p, ok)
	}
	// Started with --no-git in the subfolder (newer): its own.
	own := filepath.Join(sub, ".aider.chat.history.md")
	if err := os.WriteFile(own, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(top, old, old)
	if p, _ := aiderNewest(sub); p != own {
		t.Fatalf("newer in cwd: %q", p)
	}
	// A link (a repository could point it anywhere) isn't read.
	other := t.TempDir()
	secret := filepath.Join(other, "secret")
	_ = os.WriteFile(secret, []byte("x"), 0o600)
	_ = os.Remove(own)
	_ = os.Remove(top)
	if err := os.Symlink(secret, top); err != nil {
		t.Fatal(err)
	}
	if p, ok := aiderNewest(sub); ok {
		t.Fatalf("followed a link: %q", p)
	}
	if _, ok := aiderNewest("relative/dir"); ok {
		t.Fatal("a relative folder")
	}
}

func TestAiderRegistered(t *testing.T) {
	a := Get("aider")
	if a == nil || ByProcess("aider") != a || Parse("Aider") != a {
		t.Fatal("aider isn't registered")
	}
	// It takes no first message on its command line: started bare.
	if got := a.StartCommand("fix the tests"); got != "aider" {
		t.Errorf("start: %q", got)
	}
	if got := a.Resume("ignored"); got != "aider --restore-chat-history" {
		t.Errorf("resume: %q", got)
	}
	k := a.Kind(true)
	if !k.AgentView || k.Hooks || k.Sessions || k.Usage || !k.Schedulable || !k.CanResume {
		t.Errorf("kind: %+v", k)
	}
	if len(a.SlashCommands("")) == 0 {
		t.Error("no commands")
	}
}
