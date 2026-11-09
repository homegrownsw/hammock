package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Droid reads ~/.factory/hooks.json if there is one, else settings.json's
// "hooks": Hammock's go where Droid will read them, beside yours (herdr's,
// say), once, and come out again leaving yours.
func TestDroidHookTarget(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".factory")
	_ = os.MkdirAll(dir, 0o755)
	settings := `{"model":"x","hooks":{"SessionStart":[{"hooks":[{"command":"bash herdr-agent-state.sh session","timeout":10,"type":"command"}]}]}}`
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(exe string, uninstall bool) map[string]configEdit {
		edits, _, err := droidHookTarget.edits(home, exe, uninstall)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]configEdit{}
		for _, e := range edits {
			out[filepath.Base(e.path)] = e
			if e.changed {
				write(filepath.Base(e.path), string(e.new))
			}
		}
		return out
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	exe := "/Applications/Hammock β.app/Contents/Helpers/hmk"
	cmd := "'" + exe + "' hook droid"

	// No hooks.json: settings.json.
	write("settings.json", settings)
	e := apply(exe, false)
	if !e["settings.json"].changed || !e["mcp.json"].changed {
		t.Fatalf("edits: %v", e)
	}
	var doc struct {
		Model string
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct {
				Command string
				Timeout int
			}
		}
	}
	if err := json.Unmarshal([]byte(read("settings.json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Model != "x" || !strings.Contains(read("settings.json"), "herdr-agent-state.sh") {
		t.Fatalf("lost your settings: %s", read("settings.json"))
	}
	for _, ev := range droidHookEvents {
		found := false
		for _, m := range doc.Hooks[ev] {
			for _, h := range m.Hooks {
				if h.Command == cmd {
					found = true
					if m.Matcher != "" || h.Timeout != 5 {
						t.Errorf("%s: matcher %q timeout %d", ev, m.Matcher, h.Timeout)
					}
				}
			}
		}
		if !found {
			t.Errorf("%s: no Hammock hook", ev)
		}
	}
	var mcp struct {
		MCPServers map[string]struct {
			Type, Command string
			Args          []string
		}
	}
	if err := json.Unmarshal([]byte(read("mcp.json")), &mcp); err != nil || mcp.MCPServers["hammock"].Command != exe || mcp.MCPServers["hammock"].Args[0] != "mcp" {
		t.Fatalf("mcp: %s", read("mcp.json"))
	}
	for name, e := range apply(exe, false) {
		if e.changed {
			t.Errorf("second install changed %s", name)
		}
	}

	// You start using hooks.json (Droid's /hooks): Hammock's move there, out
	// of settings.json, which Droid no longer reads for hooks.
	write("hooks.json", `{"PreToolUse":[{"matcher":"Execute","commandRegex":"^git ","hooks":[{"type":"command","command":"/usr/local/bin/audit.sh","timeout":30}]}]}`)
	apply(exe, false)
	if strings.Contains(read("settings.json"), "hook droid") || !strings.Contains(read("settings.json"), "herdr-agent-state.sh") {
		t.Errorf("settings.json: %s", read("settings.json"))
	}
	hooks := map[string]any{}
	if err := json.Unmarshal([]byte(read("hooks.json")), &hooks); err != nil {
		t.Fatal(err)
	}
	if _, wrapped := hooks["hooks"]; wrapped || len(hooks) != len(droidHookEvents) || !strings.Contains(read("hooks.json"), `"commandRegex": "^git "`) ||
		strings.Count(read("hooks.json"), "hook droid") != len(droidHookEvents) {
		t.Errorf("hooks.json: %s", read("hooks.json"))
	}

	// Uninstall leaves only yours, and no empty "hooks" behind.
	apply(exe, true)
	if strings.Contains(read("hooks.json"), "hmk") || !strings.Contains(read("hooks.json"), "audit.sh") || strings.Contains(read("mcp.json"), "hammock") {
		t.Errorf("uninstall: %s %s", read("hooks.json"), read("mcp.json"))
	}
	write("settings.json", `{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/hmk hook droid"}]}]}}`)
	apply(exe, true)
	if s := read("settings.json"); strings.Contains(s, "hooks") || !strings.Contains(s, `"model"`) {
		t.Errorf("settings.json after uninstall: %s", s)
	}
	// A server of yours called hmk is yours.
	write("mcp.json", `{"mcpServers":{"hammock":{"type":"http","url":"https://hammock.example/mcp"}}}`)
	if e := apply(exe, false); e["mcp.json"].changed {
		t.Error("replaced your own hmk server")
	}
}
