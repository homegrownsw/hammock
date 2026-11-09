package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Goose's hooks are Open Plugins ones (goose 1.58.0,
// documentation/docs/guides/context-engineering/hooks.md and
// crates/goose/src/plugins): every folder in ~/.agents/plugins is a plugin,
// its hooks/hooks.json mapping events to commands (Claude Code's shape,
// run with `sh -c`, the event's JSON on stdin) and its .mcp.json adding MCP
// servers. Hammock's is its own folder, hammock-goose (other agents may read
// ~/.agents/plugins too: internal/agents/goose.go ignores their payloads),
// with Hammock's hooks and Hammock's tools. Goose runs each hook and waits for it;
// for PreToolUse it reads stdout as a decision, and `hmk hook goose`
// prints nothing (an allow) and exits 0 at once.

const goosePluginDir = ".agents/plugins/hammock-goose"

// gooseHookEvents are the events Hammock's plugin forwards, by goose's names.
var gooseHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "SessionEnd"}

func init() {
	why := "so Hammock knows exactly what Goose is doing"
	files := []hookTarget{
		pluginFile{file: filepath.Join(goosePluginDir, "plugin.json"), content: goosePluginManifest, why: why}.target(),
		pluginFile{file: filepath.Join(goosePluginDir, "hooks", "hooks.json"), content: gooseHooksJSON, why: why}.target(),
		pluginFile{file: filepath.Join(goosePluginDir, ".mcp.json"), content: gooseMCPJSON, why: "with Hammock's tools"}.target(),
	}
	hookTargets["goose"] = hookTarget{
		edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
			var edits []configEdit
			var notes []string
			for _, f := range files {
				e, n, err := f.edits(home, exe, uninstall)
				if err != nil {
					return nil, nil, err
				}
				edits, notes = append(edits, e...), append(notes, n...)
			}
			// One plugin: say so once.
			for i := range edits {
				if i > 0 {
					edits[i].changes = nil
				} else if uninstall {
					edits[i].changes = []string{"Removes Hammock's plugin ~/" + goosePluginDir}
				} else {
					edits[i].changes = []string{"Adds Hammock's plugin ~/" + goosePluginDir + " (hooks and Hammock's tools) " + why}
				}
			}
			return edits, notes, nil
		},
		// Uninstalled, the plugin's folders go too (os.Remove leaves them if
		// anything else is in them), or goose would find an empty plugin.
		apply: func(_ string, uninstall, dry bool) error {
			if home, err := os.UserHomeDir(); err == nil && uninstall && !dry {
				dir := filepath.Join(home, goosePluginDir)
				_ = os.Remove(filepath.Join(dir, "hooks"))
				_ = os.Remove(dir)
			}
			return nil
		},
		// Its install script puts it in ~/.local/bin; Homebrew's
		// block-goose-cli in Homebrew's.
		binary: func() string { return agentBinary("goose") },
		// The database migration tool is "goose" too.
		used: func(home string) bool { return dirAt(filepath.Join(configHome(home), "goose")) },
	}
}

func gooseJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return append(b, '\n')
}

func goosePluginManifest(string) []byte {
	return gooseJSON(map[string]any{
		"name":        "hammock-goose",
		"version":     "1.0.0",
		"description": pluginMarker + ": tells Hammock what Goose is doing in its panes, and gives Goose Hammock's tools.",
	})
}

func gooseHooksJSON(exe string) []byte {
	hooks := map[string]any{}
	for _, ev := range gooseHookEvents {
		hooks[ev] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hookCommand(exe, "goose", ev), "timeout": 5}}}}
	}
	return gooseJSON(map[string]any{"_hammock": pluginMarker, "hooks": hooks})
}

func gooseMCPJSON(exe string) []byte {
	return gooseJSON(map[string]any{
		"_hammock":   pluginMarker,
		"mcpServers": map[string]any{"hammock": map[string]any{"command": exe, "args": []string{"mcp"}}},
	})
}
