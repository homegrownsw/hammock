package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// StopFailure fires in place of Stop when an API error (a dropped
// connection, a rate limit) ends the turn: without it the pane would show
// working until the next prompt.
var claudeHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "StopFailure", "SessionEnd"}

const hookMarker = " hook claude"

// Codex's lifecycle hooks (~/.codex/hooks.json, with [features] hooks):
// the same shape and events as Claude's, PermissionRequest in place of
// Notification.
var codexHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "SessionEnd"}

const hookMarkerCodexJSON = " hook codex"

// installClaudeHooks merges Hammock's hook entries into Claude Code's
// settings JSON, keeping every existing hook. It returns the new document and
// whether anything changed. Hammock entries pointing at another hmk binary
// (the app moved) are repointed at exe.
func installClaudeHooks(doc []byte, exe string) ([]byte, bool, error) {
	return installHooksJSON(doc, claudeHookCommand(exe), hookMarker, claudeHookEvents, "*", func(string) int { return 5 })
}

// installCodexHooks does the same for Codex's hooks.json (whose matchers are
// regular expressions: none matches every tool).
func installCodexHooks(doc []byte, exe string) ([]byte, bool, error) {
	return installHooksJSON(doc, shellQuote(exe)+hookMarkerCodexJSON, hookMarkerCodexJSON, codexHookEvents, "", codexHookTimeout)
}

// codexHookTimeout: Codex clamps SessionEnd hooks to 3 s (and warns).
func codexHookTimeout(ev string) int {
	if ev == "SessionEnd" {
		return 3
	}
	return 5
}

func uninstallClaudeHooks(doc []byte) ([]byte, bool, error) {
	return uninstallHooksJSON(doc, hookMarker)
}
func uninstallCodexHooks(doc []byte) ([]byte, bool, error) {
	return uninstallHooksJSON(doc, hookMarkerCodexJSON)
}

// installHooksJSON adds cmd to each event's hooks (tool events with
// matcher, if any), keeping everything else.
func installHooksJSON(doc []byte, cmd, marker string, events []string, matcher string, timeout func(string) int) ([]byte, bool, error) {
	return installHooksJSONEach(doc, func(string) string { return cmd }, marker, events, func(ev string) string {
		if ev == "PreToolUse" || ev == "PostToolUse" {
			return matcher
		}
		return ""
	}, timeout)
}

// installHooksJSONEach is installHooksJSON with a command and matcher per
// event (cmdFor, matcherFor; "" for none).
func installHooksJSONEach(doc []byte, cmdFor func(string) string, marker string, events []string, matcherFor func(string) string, timeout func(string) int) ([]byte, bool, error) {
	settings := map[string]any{}
	if len(bytes.TrimSpace(doc)) > 0 {
		if err := json.Unmarshal(doc, &settings); err != nil {
			return nil, false, fmt.Errorf("parse settings: %w", err)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	for _, ev := range events {
		cmd := cmdFor(ev)
		entries, _ := hooks[ev].([]any)
		present := false
		for _, e := range entries {
			m, _ := e.(map[string]any)
			for _, h := range asSlice(m["hooks"]) {
				if hm, ok := h.(map[string]any); ok && isHookFor(hm["command"], marker) {
					present = true
					// An older install, from where the app used to be.
					if hm["command"] != cmd {
						hm["command"] = cmd
						changed = true
					}
					if t, _ := hm["timeout"].(float64); int(t) != timeout(ev) {
						hm["timeout"] = timeout(ev)
						changed = true
					}
				}
			}
		}
		if present {
			continue
		}
		entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": timeout(ev)}}}
		if m := matcherFor(ev); m != "" {
			entry["matcher"] = m
		}
		hooks[ev] = append(entries, entry)
		changed = true
	}
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	return append(out, '\n'), changed, err
}

// uninstallHooksJSON removes Hammock's entries, leaving others intact.
func uninstallHooksJSON(doc []byte, marker string) ([]byte, bool, error) {
	settings := map[string]any{}
	if err := json.Unmarshal(doc, &settings); err != nil {
		return nil, false, err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	changed := false
	for ev, v := range hooks {
		var kept []any
		for _, e := range asSlice(v) {
			m, _ := e.(map[string]any)
			var hs []any
			for _, h := range asSlice(m["hooks"]) {
				if hm, ok := h.(map[string]any); ok && isHookFor(hm["command"], marker) {
					changed = true
					continue
				}
				hs = append(hs, h)
			}
			if len(hs) > 0 {
				m["hooks"] = hs
				kept = append(kept, m)
			}
		}
		if len(kept) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = kept
		}
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	return append(out, '\n'), changed, err
}

// claudeHookCommand is the shell command Claude Code runs for Hammock's hooks:
// exe, quoted when it has anything a shell would split or expand (an app in
// "Hammock β.app", say), so an older unquoted entry is replaced on reinstall.
func claudeHookCommand(exe string) string { return shellQuote(exe) + hookMarker }

// shellQuote quotes s for a POSIX shell, leaving plain paths as they are.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+,:@%=", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isHammockHook reports whether a hook command is Hammock's ("<hmk> hook claude"),
// and not merely similar: atuin's is "atuin hook claude-code".
func isHammockHook(cmd any) bool { return isHookFor(cmd, hookMarker) }

// isHookFor reports whether cmd is "<hmk> <marker>", or that and an event
// name ("<hmk> hook gemini BeforeAgent"): the program must be hmk itself,
// as atuin's Codex hook is "atuin hook codex".
func isHookFor(cmd any, marker string) bool {
	c := strings.TrimSpace(fmt.Sprint(cmd))
	prog, ok := strings.CutSuffix(c, marker)
	if !ok {
		i := strings.LastIndex(c, marker+" ")
		if i < 0 || strings.ContainsAny(c[i+len(marker)+1:], " '\"$;&|") {
			return false
		}
		prog = c[:i]
	}
	prog = strings.TrimSpace(prog)
	if len(prog) >= 2 && prog[0] == '\'' && prog[len(prog)-1] == '\'' {
		prog = strings.ReplaceAll(prog[1:len(prog)-1], `'\''`, "'")
	}
	return filepath.Base(prog) == "hmk"
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// runHooksInstall installs (or with --uninstall removes) Hammock's hooks and
// MCP registration for each harness picked with --harness claude,codex
// (default: Claude Code's and Codex's, as always, and any other agent with
// hooks whose CLI is found here). --dry-run prints what would change;
// --dry-run --json prints a plan for the app's onboarding (see hooks_plan.go).
func runHooksInstall(args []string) error {
	dry := hasFlag(args, "--dry-run")
	uninstall := hasFlag(args, "--uninstall")
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	harnesses := defaultHookHarnesses()
	for i, a := range args {
		if a == "--exe" && i+1 < len(args) {
			exe = args[i+1] // hooks for another copy of hammock (e.g. the app's, from a dev build)
		}
		if a == "--harness" && i+1 < len(args) {
			harnesses = strings.Split(args[i+1], ",")
		}
	}
	for _, h := range harnesses {
		if _, ok := hookTargets[h]; !ok {
			return fmt.Errorf("unknown harness %q (want %s)", h, strings.Join(hookHarnesses(), ", "))
		}
	}
	if hasFlag(args, "--json") {
		return printHooksPlan(exe, harnesses, uninstall)
	}
	home, _ := os.UserHomeDir()
	for _, h := range harnesses {
		t := hookTargets[h]
		edits, notes, err := t.edits(home, exe, uninstall)
		if err != nil {
			return err
		}
		for _, n := range notes {
			fmt.Fprintln(os.Stderr, "note:", n)
		}
		for _, e := range edits {
			if e.remove {
				if err := removeConfig(e.path, dry); err != nil {
					return err
				}
				continue
			}
			if err := writeConfig(e.path, e.old, e.new, e.changed, dry); err != nil {
				return err
			}
		}
		if t.apply != nil {
			if err := t.apply(exe, uninstall, dry); err != nil {
				return err
			}
		}
	}
	return nil
}

// planCodexConfig applies Hammock's config.toml edits (hooks turned on, the
// MCP server) and returns the new document. Hooks themselves are in
// hooks.json; an older install's notify line is left as it is (its events
// agree with the hooks').
func planCodexConfig(doc []byte, exe string) ([]byte, string) {
	out, _ := enableCodexHooks(doc)
	out, _ = installCodexMCP(out, exe)
	return out, ""
}

var (
	codexFeaturesRE = regexp.MustCompile(`(?m)^\[features\][ \t]*\n`)
	codexHooksOnRE  = regexp.MustCompile(`(?m)^\[features\][^\[]*^\s*hooks\s*=\s*true`)
	codexHooksKeyRE = regexp.MustCompile(`(?m)^(\[features\][^\[]*^\s*hooks\s*=\s*)\S+`)
)

// enableCodexHooks turns on Codex's hooks feature ([features] hooks = true).
func enableCodexHooks(doc []byte) ([]byte, bool) {
	switch {
	case codexHooksOnRE.Match(doc):
		return doc, false
	case codexHooksKeyRE.Match(doc):
		return codexHooksKeyRE.ReplaceAll(doc, []byte("${1}true")), true
	case codexFeaturesRE.Match(doc):
		loc := codexFeaturesRE.FindIndex(doc)
		out := append([]byte{}, doc[:loc[1]]...)
		out = append(out, "hooks = true\n"...)
		return append(out, doc[loc[1]:]...), true
	}
	out := append([]byte{}, doc...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, "\n[features]\nhooks = true\n"...), true
}

// claudeMCP registers hammock's MCP server (terminal and browser tools) with
// Claude Code for every project, through its own CLI rather than editing
// ~/.claude.json, which running sessions rewrite. Outside a Hammock pane the
// server offers no tools, so registering it everywhere is harmless.
func claudeMCP(exe string, uninstall, dry bool) error {
	claude := findClaude()
	want := []string{"mcp", "add", "--scope", "user", "hammock", "--", exe, "mcp"}
	if claude == "" {
		if !uninstall {
			fmt.Printf("Claude Code MCP: claude not found; register Hammock's tools with:\n  claude %s\n", strings.Join(want, " "))
		}
		return nil
	}
	cur, err := exec.Command(claude, "mcp", "get", "hammock").CombinedOutput()
	registered := err == nil
	if uninstall {
		if !registered {
			return nil
		}
		if dry {
			fmt.Println("Claude Code MCP: would run claude mcp remove --scope user hammock")
			return nil
		}
		return runClaude(claude, "mcp", "remove", "--scope", "user", "hammock")
	}
	if registered && mcpPointsAt(cur, exe) {
		fmt.Println("Claude Code MCP: already registered")
		return nil
	}
	if dry {
		fmt.Printf("Claude Code MCP: would run claude %s\n", strings.Join(want, " "))
		return nil
	}
	if registered { // an older install, from where the app used to be
		_ = runClaude(claude, "mcp", "remove", "--scope", "user", "hammock")
	}
	return runClaude(claude, want...)
}

// mcpPointsAt reports whether claude mcp get's output is for exe: it prints
// "Command: <exe>" and "Args: mcp" on lines of their own.
func mcpPointsAt(out []byte, exe string) bool {
	return bytes.Contains(out, []byte(exe+" mcp")) ||
		bytes.Contains(out, []byte("Command: "+exe+"\n")) && bytes.Contains(out, []byte("Args: mcp\n"))
}

func runClaude(claude string, args ...string) error {
	out, err := exec.Command(claude, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude %s: %v: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	fmt.Println("Claude Code MCP: " + strings.TrimSpace(string(out)))
	return nil
}

// findClaude finds the claude CLI, also where installers put it when the
// app (launched without a login shell's PATH) runs this.
func findClaude() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{".local/bin/claude", ".claude/local/claude", ".local/share/mise/shims/claude", ".npm-global/bin/claude"} {
		if fileExistsAt(filepath.Join(home, p)) {
			return filepath.Join(home, p)
		}
	}
	for _, p := range []string{"/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
		if fileExistsAt(p) {
			return p
		}
	}
	return ""
}

func fileExistsAt(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// installCodexMCP adds Hammock's MCP server to Codex's config.toml.
func installCodexMCP(doc []byte, exe string) ([]byte, bool) {
	if bytes.Contains(doc, []byte("[mcp_servers.hammock]")) {
		return doc, false
	}
	out := append([]byte{}, doc...)
	if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	out = append(out, []byte(fmt.Sprintf("\n[mcp_servers.hammock]\ncommand = %q\nargs = [\"mcp\"]\n", exe))...)
	return out, true
}

// writeConfig backs up and replaces path (or prints the result when dry).
func writeConfig(path string, old, new []byte, changed, dry bool) error {
	if !changed {
		fmt.Printf("%s: already up to date\n", path)
		return nil
	}
	if dry {
		fmt.Printf("--- %s (would write)\n%s\n", path, new)
		return nil
	}
	if len(old) > 0 {
		backup := fmt.Sprintf("%s.hammock-backup-%s", path, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return err
		}
		fmt.Printf("%s: backed up to %s\n", path, backup)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, new, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	fmt.Printf("%s: updated\n", path)
	return nil
}
