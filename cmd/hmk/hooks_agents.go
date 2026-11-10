package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/homegrownsw/hammock/internal/agents"
)

// Each agent Hammock installs hooks for (agents.Agent.Hooks) has a hookTarget:
// the config files it edits, and any step that isn't a file edit (Claude
// Code's MCP server goes in through its own CLI). `hmk hooks install`
// applies them; `--dry-run --json` (hooks_plan.go) describes them.

// configEdit is one config file Hammock changes.
type configEdit struct {
	path     string
	old, new []byte
	changed  bool
	// diffFrom is what the onboarding diff compares new with (old as Hammock
	// would write it, so reformatting doesn't show), else old.
	diffFrom []byte
	// changes say what it does, in plain English, when it changes.
	changes []string
	// remove: the file is Hammock's own (a plugin), deleted to uninstall.
	remove bool
}

type hookTarget struct {
	// edits are the files to change for exe (or with uninstall, to take
	// Hammock's hooks out of), and any notes for the person.
	edits func(home, exe string, uninstall bool) (edits []configEdit, notes []string, err error)
	// apply runs steps that aren't file edits; plan describes them.
	apply func(exe string, uninstall, dry bool) error
	plan  func(binary, exe string, uninstall bool) []string
	// binary finds the agent's CLI (else agentBinary for its command).
	binary func() string
	// used: the agent has been set up here (its own folder is there), for
	// one whose command other programs share (goose, amp, pi), so finding
	// a "goose" isn't enough to set it up unasked (nil: finding it is).
	used func(home string) bool
}

// hookTargets are the agents Hammock installs hooks for, by harness. Each
// agent's hooks_<id>.go adds its own in init.
var hookTargets = map[string]hookTarget{
	"claude": claudeHookTarget,
	"codex":  codexHookTarget,
}

// hookHarnesses are the harnesses with hooks, in the registry's order.
func hookHarnesses() []string {
	var out []string
	for _, a := range agents.All {
		if _, ok := hookTargets[a.ID]; ok && a.Hooks {
			out = append(out, a.ID)
		}
	}
	return out
}

// defaultHookHarnesses are those `hmk hooks install` sets up unasked:
// Claude Code and Codex (whether found or not, as it always has), and the
// other agents with hooks whose CLI is found here (and that have been set
// up here, where another program shares the command), so nothing is
// written for an agent you don't have.
func defaultHookHarnesses() []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, h := range hookHarnesses() {
		if h == "claude" || h == "codex" {
			out = append(out, h)
			continue
		}
		if t := hookTargets[h]; findAgentBinary(h) != "" && (t.used == nil || t.used(home)) {
			out = append(out, h)
		}
	}
	return out
}

// dirAt: path is a folder.
func dirAt(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// configHome is $XDG_CONFIG_HOME, else ~/.config.
func configHome(home string) string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	return filepath.Join(home, ".config")
}

// readConfig reads a config file, a missing one as empty.
func readConfig(path string) ([]byte, error) {
	doc, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return doc, nil
}

var claudeHookTarget = hookTarget{
	edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
		path := filepath.Join(home, ".claude", "settings.json")
		doc, err := readConfig(path)
		if err != nil {
			return nil, nil, err
		}
		e := configEdit{path: path, old: doc, new: doc}
		if uninstall {
			if len(doc) > 0 {
				e.new, e.changed, err = uninstallClaudeHooks(doc)
			}
		} else {
			e.new, e.changed, err = installClaudeHooks(doc, exe)
		}
		if err != nil {
			return nil, nil, err
		}
		// Hammock rewrites settings.json through encoding/json (sorted keys,
		// two-space indent), so diff against the old file in that same
		// form: the diff shows only what Hammock changes, not reformatting.
		e.diffFrom = normalizeJSON(doc)
		where := "~" + strings.TrimPrefix(path, home)
		switch missing := missingClaudeHooks(doc); {
		case uninstall:
			e.changes = []string{"Removes Hammock's hooks from " + where + ", keeping your own"}
		case missing > 0:
			e.changes = []string{fmt.Sprintf("Adds %d hooks to %s so Hammock knows exactly what Claude is doing", missing, where)}
		default:
			e.changes = []string{"Points Hammock's hooks in " + where + " at this copy of Hammock"}
		}
		return []configEdit{e}, nil, nil
	},
	apply: claudeMCP,
	plan: func(binary, exe string, uninstall bool) []string {
		registered, current := claudeMCPState(binary, exe)
		switch {
		case binary == "":
		case uninstall && registered:
			return []string{"Unregisters Hammock's tools (claude mcp remove)"}
		case !uninstall && !current:
			return []string{"Registers Hammock's tools with claude mcp add (user scope)"}
		}
		return nil
	},
	binary: findClaude,
}

var codexHookTarget = hookTarget{
	edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
		hooksPath := filepath.Join(home, ".codex", "hooks.json")
		hdoc, err := readConfig(hooksPath)
		if err != nil {
			return nil, nil, err
		}
		h := configEdit{path: hooksPath, old: hdoc, new: hdoc}
		if uninstall {
			if len(hdoc) > 0 {
				h.new, h.changed, err = uninstallCodexHooks(hdoc)
			}
			h.changes = []string{"Removes Hammock's hooks from ~" + strings.TrimPrefix(hooksPath, home)}
		} else {
			h.new, h.changed, err = installCodexHooks(hdoc, exe)
			h.changes = []string{"Adds Hammock's hooks to ~" + strings.TrimPrefix(hooksPath, home) + " (Codex asks you to trust them once)"}
		}
		if err != nil {
			return nil, nil, err
		}
		if uninstall {
			return []configEdit{h}, nil, nil
		}
		// Turning hooks on and Hammock's MCP server both live in config.toml.
		path := filepath.Join(home, ".codex", "config.toml")
		doc, err := readConfig(path)
		if err != nil {
			return nil, nil, err
		}
		out, warning := planCodexConfig(doc, exe)
		c := configEdit{path: path, old: doc, new: out, changed: !bytes.Equal(doc, out)}
		if _, ch := enableCodexHooks(doc); ch {
			c.changes = append(c.changes, "Turns on Codex's hooks ([features] hooks = true)")
		}
		if _, ch := installCodexMCP(doc, exe); ch {
			c.changes = append(c.changes, "Adds Hammock's tools as [mcp_servers.hammock]")
		}
		var notes []string
		if warning != "" {
			notes = append(notes, warning)
		}
		return []configEdit{h, c}, notes, nil
	},
	binary: findCodex,
}

// findAgentBinary finds harness's CLI, also where installers put it when the
// app (launched without a login shell's PATH) runs this.
func findAgentBinary(harness string) string {
	if t, ok := hookTargets[harness]; ok && t.binary != nil {
		return t.binary()
	}
	if a := agents.Get(harness); a != nil {
		return agentBinary(a.Command)
	}
	return ""
}

// agentBinary looks for command on PATH, then where installers (npm, bun,
// mise, Homebrew, curl scripts) put CLIs, then in extra (relative to home).
func agentBinary(command string, extra ...string) string {
	if p, err := exec.LookPath(command); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, dir := range append(extra, ".local/bin", ".npm-global/bin", ".local/share/mise/shims", ".bun/bin", ".volta/bin", ".cargo/bin", "go/bin") {
		p := filepath.Join(home, dir, command)
		if strings.HasSuffix(dir, "/"+command) {
			p = filepath.Join(home, dir)
		}
		if fileExistsAt(p) {
			return p
		}
	}
	for _, p := range []string{"/opt/homebrew/bin/" + command, "/usr/local/bin/" + command} {
		if fileExistsAt(p) {
			return p
		}
	}
	return ""
}
