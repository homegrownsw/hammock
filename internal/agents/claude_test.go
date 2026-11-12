package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeSessionURL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	sessions := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("100.json", `{"pid":100,"sessionId":"aaa","updatedAt":1,"bridgeSessionId":"session_01Old"}`)
	write("200.json", `{"pid":200,"sessionId":"aaa","updatedAt":5,"bridgeSessionId":"session_01New"}`)
	write("300.json", `{"pid":300,"sessionId":"bbb","updatedAt":9}`)
	write("400.json", `{"pid":400,"sessionId":"ccc","bridgeSessionId":"session_01/../x"}`)
	write("500.json", `not json`)

	for id, want := range map[string]string{
		"aaa": "https://claude.ai/code/session_01New", // the newest file
		"bbb": "",                                     // Remote Control off
		"ccc": "",                                     // not an id a link can have
		"zzz": "",
		"":    "",
	} {
		if got := claudeSessionURL(id); got != want {
			t.Errorf("claudeSessionURL(%q) = %q, want %q", id, got, want)
		}
	}
}
