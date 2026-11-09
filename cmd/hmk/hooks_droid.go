package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Factory's Droid reads hooks in Claude Code's shape from ~/.factory/hooks.json
// (keyed by event, no "hooks" wrapper), or, only when there's no hooks.json,
// from the "hooks" key of ~/.factory/settings.json (docs.factory.com's hooks
// reference; droid 1.6.0). So Hammock's go where Droid will read them: into
// hooks.json if you have one, else settings.json, beside your own. Its
// payloads name their event (hook_event_name), and a matcher left out
// matches every tool. Hammock's tools go in ~/.factory/mcp.json.

var droidHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop", "SessionEnd"}

const droidHookMarker = " hook droid"

func init() { hookTargets["droid"] = droidHookTarget }

var droidHookTarget = hookTarget{
	edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
		dir := filepath.Join(home, ".factory")
		hooksPath, settingsPath, mcpPath := filepath.Join(dir, "hooks.json"), filepath.Join(dir, "settings.json"), filepath.Join(dir, "mcp.json")
		_, err := os.Stat(hooksPath)
		hasHooksFile := err == nil
		var edits []configEdit
		for _, f := range []struct {
			path    string
			wrapped bool // settings.json: under "hooks"
		}{{hooksPath, false}, {settingsPath, true}} {
			doc, err := readConfig(f.path)
			if err != nil {
				return nil, nil, err
			}
			e := configEdit{path: f.path, old: doc, new: doc, diffFrom: normalizeJSON(doc)}
			where := "~/.factory/" + filepath.Base(f.path)
			// Install where Droid reads hooks; take Hammock's out of the other.
			if !uninstall && f.wrapped != hasHooksFile {
				e.new, e.changed, err = editDroidHooks(doc, f.wrapped, exe)
				e.changes = []string{"Adds Hammock's hooks to " + where + " so Hammock knows exactly what Droid is doing"}
			} else if len(doc) > 0 {
				e.new, e.changed, err = editDroidHooks(doc, f.wrapped, "")
				e.changes = []string{"Removes Hammock's hooks from " + where + ", keeping your own"}
			}
			if err != nil {
				return nil, nil, err
			}
			if e.changed || !uninstall && f.wrapped != hasHooksFile {
				edits = append(edits, e)
			}
		}
		doc, err := readConfig(mcpPath)
		if err != nil {
			return nil, nil, err
		}
		m := configEdit{path: mcpPath, old: doc, new: doc, diffFrom: normalizeJSON(doc)}
		m.new, m.changed, err = editDroidMCP(doc, exe, uninstall)
		if err != nil {
			return nil, nil, err
		}
		if uninstall {
			m.changes = []string{"Removes Hammock's tools from ~/.factory/mcp.json"}
		} else {
			m.changes = []string{"Adds Hammock's tools to ~/.factory/mcp.json"}
		}
		return append(edits, m), nil, nil
	},
}

// editDroidHooks puts Hammock's hooks into a hooks document (exe "": takes
// them out), keeping everything else: settings.json's "hooks" key when
// wrapped, else hooks.json's top level.
func editDroidHooks(doc []byte, wrapped bool, exe string) ([]byte, bool, error) {
	in := doc
	if !wrapped && len(bytes.TrimSpace(doc)) > 0 {
		var events map[string]any
		if err := json.Unmarshal(doc, &events); err != nil {
			return nil, false, fmt.Errorf("parse hooks.json: %w", err)
		}
		in, _ = json.Marshal(map[string]any{"hooks": events})
	}
	var out []byte
	var changed bool
	var err error
	switch {
	case exe != "":
		out, changed, err = installHooksJSONEach(in, func(string) string { return hookCommand(exe, "droid", "") },
			droidHookMarker, droidHookEvents, func(string) string { return "" }, func(string) int { return 5 })
	case len(bytes.TrimSpace(in)) > 0:
		out, changed, err = uninstallHooksJSON(in, droidHookMarker)
	default:
		return doc, false, nil
	}
	switch {
	case err != nil:
		return nil, false, err
	case !changed:
		return doc, false, nil
	case wrapped:
		// No hooks left: no "hooks" key either (Droid takes one, even
		// empty, as hooks to move into hooks.json).
		var settings map[string]any
		if json.Unmarshal(out, &settings) == nil {
			if h, ok := settings["hooks"].(map[string]any); ok && len(h) == 0 {
				delete(settings, "hooks")
				out, err = json.MarshalIndent(settings, "", "  ")
				out = append(out, '\n')
			}
		}
		return out, true, err
	}
	var settings struct {
		Hooks map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(out, &settings); err != nil {
		return nil, false, err
	}
	if settings.Hooks == nil {
		settings.Hooks = map[string]any{}
	}
	out, err = json.MarshalIndent(settings.Hooks, "", "  ")
	return append(out, '\n'), true, err
}

// editDroidMCP adds Hammock's MCP server to mcp.json ({"mcpServers": {"hammock":
// {"type": "stdio", "command": exe, "args": ["mcp"]}}}), or with uninstall
// removes it if it's Hammock's.
func editDroidMCP(doc []byte, exe string, uninstall bool) ([]byte, bool, error) {
	cfg := map[string]any{}
	if len(bytes.TrimSpace(doc)) > 0 {
		if err := json.Unmarshal(doc, &cfg); err != nil {
			return nil, false, fmt.Errorf("parse mcp.json: %w", err)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	cur, _ := servers["hammock"].(map[string]any)
	cmd, _ := cur["command"].(string)
	ours := cur != nil && filepath.Base(cmd) == "hmk"
	if uninstall {
		if !ours {
			return doc, false, nil
		}
		delete(servers, "hammock")
	} else {
		if cur != nil && !ours {
			return doc, false, nil // a server of yours named hammock
		}
		if ours && cmd == exe {
			return doc, false, nil
		}
		if servers == nil {
			servers = map[string]any{}
			cfg["mcpServers"] = servers
		}
		servers["hammock"] = map[string]any{"type": "stdio", "command": exe, "args": []any{"mcp"}}
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	return append(out, '\n'), true, err
}
