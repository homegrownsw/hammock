package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInstallClaudeHooksKeepsExistingAndIsIdempotent(t *testing.T) {
	existing := `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}],` +
		`"PreToolUse":[{"matcher":"^Bash$","hooks":[{"type":"command","command":"/Users/a/.atuin/bin/atuin hook claude-code"}]}]}}`
	out, changed, err := installClaudeHooks([]byte(existing), "/usr/local/bin/hmk")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model"] != "opus" {
		t.Fatal("lost unrelated settings")
	}
	stop := doc["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 || !strings.Contains(string(out), "say done") {
		t.Fatalf("existing Stop hook not kept: %s", out)
	}
	// Another tool's similar-looking hook isn't mistaken for Hammock's.
	if pre := doc["hooks"].(map[string]any)["PreToolUse"].([]any); len(pre) != 2 {
		t.Fatalf("Hammock's PreToolUse hook not added next to atuin's: %s", out)
	}
	again, changed, _ := installClaudeHooks(out, "/usr/local/bin/hmk")
	if changed || string(again) != string(out) {
		t.Fatal("second install changed the file")
	}
	// After the app moves, a reinstall repoints the entries, without duplicating them.
	moved, changed, _ := installClaudeHooks(out, "/Applications/Hammock.app/Contents/Helpers/hmk")
	if !changed || strings.Contains(string(moved), "/usr/local/bin/hmk") || strings.Count(string(moved), "hmk hook claude\"") != len(claudeHookEvents) ||
		!strings.Contains(string(moved), "atuin hook claude-code") {
		t.Fatalf("repoint: %s", moved)
	}
	removed, changed, _ := uninstallClaudeHooks(out)
	if !changed || strings.Contains(string(removed), "hmk hook") || !strings.Contains(string(removed), "say done") || !strings.Contains(string(removed), "atuin hook claude-code") {
		t.Fatalf("uninstall: %s", removed)
	}
}

func TestInstallCodexHooks(t *testing.T) {
	// herdr's and atuin's hooks stay; Hammock's are added once, for every tool.
	existing := `{"hooks":{"SessionStart":[{"hooks":[{"command":"bash herdr.sh session","type":"command"}]}],
"PreToolUse":[{"matcher":"^Bash$","hooks":[{"type":"command","command":"atuin hook codex"}]}]}}`
	out, changed, err := installCodexHooks([]byte(existing), "/Applications/Hammock β.app/Contents/Helpers/hmk")
	if err != nil || !changed {
		t.Fatal(err, changed)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Command string }
		}
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	for _, ev := range codexHookEvents {
		found := false
		for _, e := range doc.Hooks[ev] {
			for _, h := range e.Hooks {
				if h.Command == "'/Applications/Hammock β.app/Contents/Helpers/hmk' hook codex" {
					found = true
					if e.Matcher != "" {
						t.Errorf("%s: matcher %q (Codex's are regexes: none matches every tool)", ev, e.Matcher)
					}
				}
			}
		}
		if !found {
			t.Errorf("%s: no Hammock hook", ev)
		}
	}
	if !strings.Contains(string(out), "herdr.sh") || !strings.Contains(string(out), "atuin hook codex") {
		t.Fatalf("others' hooks lost: %s", out)
	}
	if _, changed, _ := installCodexHooks(out, "/Applications/Hammock β.app/Contents/Helpers/hmk"); changed {
		t.Fatal("second install changed it")
	}
	if !strings.Contains(string(out), `"timeout": 3`) {
		t.Fatal("SessionEnd's timeout isn't Codex's 3 s limit")
	}
	// An older install's 5 s SessionEnd is corrected.
	older := strings.Replace(string(out), `"timeout": 3`, `"timeout": 5`, 1)
	if fixed, changed, _ := installCodexHooks([]byte(older), "/Applications/Hammock β.app/Contents/Helpers/hmk"); !changed || string(fixed) != string(out) {
		t.Fatalf("older SessionEnd timeout not corrected: %v", changed)
	}
	removed, changed, _ := uninstallCodexHooks(out)
	if !changed || strings.Contains(string(removed), "hook codex\"") && strings.Contains(string(removed), "hmk") || !strings.Contains(string(removed), "atuin hook codex") {
		t.Fatalf("uninstall: %s", removed)
	}
}

func TestPlanCodexConfigTurnsOnHooksAndMCP(t *testing.T) {
	for _, in := range []string{"model = \"o5\"\n", "[features]\njs_repl = false\n", "[features]\nhooks = false\n"} {
		out, _ := planCodexConfig([]byte(in), "/bin/hmk")
		if !codexHooksOnRE.Match(out) || !strings.Contains(string(out), "[mcp_servers.hammock]") || strings.Count(string(out), "[features]") != 1 {
			t.Fatalf("%q → %q", in, out)
		}
		again, _ := planCodexConfig(out, "/bin/hmk")
		if string(again) != string(out) {
			t.Fatalf("%q: second plan changed the file", in)
		}
	}
}

func TestLineDiff(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	new := "a\nb\nc\nd\ne\nf\ng\nh\nX\ni\nj\n"
	d := lineDiff([]byte(old), []byte(new))
	if d.Added != 1 || d.Removed != 0 {
		t.Fatalf("added %d removed %d", d.Added, d.Removed)
	}
	var got []string
	for _, l := range d.Lines {
		got = append(got, l.Op+l.Text)
	}
	want := "…| f| g| h|+X| i| j"
	if strings.Join(got, "|") != want {
		t.Fatalf("got %q want %q", strings.Join(got, "|"), want)
	}
	if d := lineDiff(nil, []byte("x\n")); d.Added != 1 || len(d.Lines) != 1 {
		t.Fatalf("from empty: %+v", d)
	}
}

func TestMCPPointsAt(t *testing.T) {
	out := []byte("hammock:\n  Scope: User config (available in all your projects)\n  Status: ✔ Connected\n  Type: stdio\n" +
		"  Command: /Applications/Hammock.app/Contents/Helpers/hmk\n  Args: mcp\n  Environment:\n")
	if !mcpPointsAt(out, "/Applications/Hammock.app/Contents/Helpers/hmk") {
		t.Fatal("same binary not recognised")
	}
	if mcpPointsAt(out, "/elsewhere/hammock") {
		t.Fatal("a moved app counted as registered")
	}
}

func TestClaudeHookCommandQuotesAndReplacesUnquoted(t *testing.T) {
	exe := "/Users/a/dist/Hammock β.app/Contents/Helpers/hmk"
	if got := claudeHookCommand(exe); got != "'"+exe+"' hook claude" {
		t.Fatalf("got %q", got)
	}
	if got := shellQuote("/a/it's"); got != `'/a/it'\''s'` {
		t.Fatalf("got %q", got)
	}
	if got := claudeHookCommand("/usr/local/bin/hmk"); got != "/usr/local/bin/hmk hook claude" {
		t.Fatalf("plain path changed: %q", got)
	}
	// An older install wrote the path unquoted: reinstalling replaces it.
	old := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + exe + ` hook claude"}]}]}}`
	out, changed, err := installClaudeHooks([]byte(old), exe)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var doc map[string]any
	_ = json.Unmarshal(out, &doc)
	if stop := doc["hooks"].(map[string]any)["Stop"].([]any); len(stop) != 1 || strings.Contains(string(out), `"`+exe+` hook claude"`) {
		t.Fatalf("old entry not replaced: %s", out)
	}
	if _, changed, _ := installClaudeHooks(out, exe); changed {
		t.Fatal("reinstall not idempotent")
	}
	if removed, changed, _ := uninstallClaudeHooks(out); !changed || strings.Contains(string(removed), "hook claude") {
		t.Fatalf("uninstall: %s", removed)
	}
}
