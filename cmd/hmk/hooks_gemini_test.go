package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hammock's hooks go into Gemini CLI's and Qwen Code's settings.json beside
// the person's own settings and hooks, once, and come out leaving theirs.
func TestInstallGeminiAndQwenHooks(t *testing.T) {
	for _, c := range []struct {
		harness, file, existing string
		events                  int
		timeout                 float64
	}{
		{"gemini", ".gemini/settings.json",
			`{"security":{"auth":{"selectedType":"oauth-personal"}},"hooks":{"BeforeTool":[{"matcher":"write_file","hooks":[{"type":"command","command":"./lint.sh","timeout":30000}]}]}}`,
			7, 5000},
		{"qwen", ".qwen/settings.json",
			`{"model":{"name":"qwen3-coder-plus"},"hooks":{"SessionStart":[{"matcher":"*","hooks":[{"type":"command","command":"bash herdr-agent-session.sh session","timeout":10000}]}]}}`,
			10, 5},
	} {
		home := t.TempDir()
		path := filepath.Join(home, c.file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(c.existing), 0o644); err != nil {
			t.Fatal(err)
		}
		target := hookTargets[c.harness]
		edits, _, err := target.edits(home, "/Applications/Hammock.app/Contents/Helpers/hmk", false)
		if err != nil || len(edits) != 1 || !edits[0].changed || edits[0].path != path {
			t.Fatalf("%s: %v %+v", c.harness, err, edits)
		}
		out := edits[0].new
		var doc struct {
			Hooks map[string][]struct {
				Matcher string
				Hooks   []struct {
					Type    string
					Command string
					Timeout float64
				}
			}
		}
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatal(err)
		}
		hmk := 0
		for ev, entries := range doc.Hooks {
			for _, e := range entries {
				for _, h := range e.Hooks {
					if strings.HasSuffix(h.Command, "Helpers/hmk hook "+c.harness) {
						hmk++
						if h.Type != "command" || h.Timeout != c.timeout || e.Matcher != "" {
							t.Errorf("%s %s: %+v (matcher %q)", c.harness, ev, h, e.Matcher)
						}
					}
				}
			}
		}
		if hmk != c.events {
			t.Errorf("%s: %d of Hammock's hooks, want %d: %s", c.harness, hmk, c.events, out)
		}
		for _, keep := range []string{"oauth-personal", "lint.sh", "qwen3-coder-plus", "herdr-agent-session.sh"} {
			if strings.Contains(c.existing, keep) && !strings.Contains(string(out), keep) {
				t.Errorf("%s: lost %s", c.harness, keep)
			}
		}
		// Installing again changes nothing.
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
		again, _, _ := target.edits(home, "/Applications/Hammock.app/Contents/Helpers/hmk", false)
		if again[0].changed {
			t.Errorf("%s: second install changed the file", c.harness)
		}
		// Uninstalling takes out only Hammock's.
		gone, _, err := target.edits(home, "", true)
		if err != nil || !gone[0].changed {
			t.Fatalf("%s uninstall: %v", c.harness, err)
		}
		if s := string(gone[0].new); strings.Contains(s, "hook "+c.harness) || !strings.Contains(s, strings.Split(strings.Split(c.existing, `"command":"`)[1], `"`)[0]) {
			t.Errorf("%s uninstall: %s", c.harness, s)
		}
	}
}
