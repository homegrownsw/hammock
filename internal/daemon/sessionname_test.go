package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexjsp/hammock/internal/agents"
)

func TestNameTranscript(t *testing.T) {
	path := filepath.Join(allowTranscripts(t), "s.jsonl")
	os.WriteFile(path, []byte(`{"type":"user","message":"hi"}`+"\n"+`{"type":"custom-title","customTitle":"Old","sessionId":"s1"}`+"\n"), 0o600)
	if err := nameTranscript(path, "s1", "Playback Stall"); err != nil {
		t.Fatal(err)
	}
	// Already the latest: nothing more is written.
	if err := nameTranscript(path, "s1", "Playback Stall"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[2] != `{"type":"custom-title","customTitle":"Playback Stall","sessionId":"s1"}` {
		t.Fatalf("transcript:\n%s", b)
	}
	// Claude re-writing its old name puts it back to Hammock's on the next pass.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"custom-title","customTitle":"Old","sessionId":"s1"}` + "\n")
	f.Close()
	nameTranscript(path, "s1", "Playback Stall")
	if f, _ := os.Open(path); latestCustomTitle(f) != "Playback Stall" {
		t.Fatal("name not restored")
	}
}

// allowTranscripts makes a temporary folder count as an agent's transcript
// folder for the test.
func allowTranscripts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range agents.All {
		addAgentRoots(t, a.ID, dir)
	}
	return dir
}

func TestSafeTranscript(t *testing.T) {
	dir := allowTranscripts(t)
	good := filepath.Join(dir, "proj", "s.jsonl")
	os.MkdirAll(filepath.Dir(good), 0o700)
	os.WriteFile(good, nil, 0o600)
	if safeTranscript(good) == "" {
		t.Fatal("transcript in an agent folder refused")
	}
	outside := filepath.Join(t.TempDir(), "x.jsonl")
	os.WriteFile(outside, nil, 0o600)
	link := filepath.Join(dir, "link.jsonl")
	os.Symlink(outside, link)
	os.WriteFile(filepath.Join(dir, "notes.txt"), nil, 0o600)
	os.Mkdir(filepath.Join(dir, "d.jsonl"), 0o700)
	for _, p := range []string{outside, link, filepath.Join(dir, "notes.txt"), filepath.Join(dir, "d.jsonl"), filepath.Join(dir, "..", "x.jsonl"), "rel.jsonl", ""} {
		if safeTranscript(p) != "" {
			t.Errorf("%q accepted", p)
		}
	}
	if err := nameTranscript(link, "s", "x"); err == nil {
		t.Fatal("named a transcript outside the agent folders")
	}
	if b, _ := os.ReadFile(outside); len(b) != 0 {
		t.Fatal("wrote outside the agent folders")
	}
}

// An agent started with CLAUDE_CONFIG_DIR keeps its transcripts there: the
// daemon learns the folder from its hook's environment.
func TestLearnedTranscriptRoot(t *testing.T) {
	cfg := t.TempDir()
	dir := filepath.Join(cfg, "projects", "p")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if safeTranscript(path) != "" {
		t.Fatal("taken before it was learned")
	}
	learnTranscriptRoots([]string{"HOME=/x", "CLAUDE_CONFIG_DIR=" + cfg, "CODEX_HOME=relative"})
	defer learnedRoots.Delete(filepath.Join(cfg, "projects"))
	if safeTranscript(path) == "" {
		t.Fatal("not taken once learned")
	}
	if outside := filepath.Join(cfg, "other.jsonl"); os.WriteFile(outside, []byte("{}\n"), 0o600) == nil && safeTranscript(outside) != "" {
		t.Fatal("took a file outside projects")
	}
	// Learned as Claude's, it's not another agent's, and only Claude's are
	// named by appending.
	if safeTranscriptOf("codex", path) != "" || safeTranscriptOf("claude", path) == "" {
		t.Fatal("taken as another agent's")
	}
}
