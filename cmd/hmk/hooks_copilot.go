package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
)

// GitHub Copilot CLI reads every *.json in ~/.copilot/hooks (Copilot
// 1.0.12+), so Hammock's hooks are a file of their own there, hammock.json,
// leaving your settings.json and other hook files alone: uninstalling
// deletes it. The events are named as Claude Code's (Copilot accepts
// either), which makes Copilot send Claude-shaped, snake_case payloads
// (hook_event_name, session_id, tool_name, tool_input, transcript_path):
// agents.Copilot reads them.
//
// Copilot fails a preToolUse hook closed: a non-zero exit (a hmk that has
// since moved, say) would deny every tool call. So each command ends in
// `|| true`; a timeout fails open.

var copilotHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "Stop", "ErrorOccurred", "SessionEnd"}

const copilotHookMarker = " hook copilot"

func init() {
	hookTargets["copilot"] = copilotHookTarget
}

var copilotHookTarget = hookTarget{
	edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
		rel := filepath.Join(".copilot", "hooks", "hammock.json")
		path := filepath.Join(home, rel)
		doc, err := readConfig(path)
		if err != nil {
			return nil, nil, err
		}
		where := "~/" + rel
		if len(bytes.TrimSpace(doc)) > 0 && !bytes.Contains(doc, []byte(copilotHookMarker)) {
			return nil, []string{where + " isn't Hammock's: left as it is"}, nil
		}
		e := configEdit{path: path, old: doc, diffFrom: normalizeJSON(doc)}
		if uninstall {
			if len(doc) > 0 {
				e.remove, e.changed = true, true
				e.changes = []string{"Removes Hammock's hooks, " + where}
			}
			return []configEdit{e}, nil, nil
		}
		e.new = copilotHooksFile(exe)
		e.changed = !bytes.Equal(normalizeJSON(doc), normalizeJSON(e.new))
		e.changes = []string{"Adds Hammock's hooks as " + where + " so Hammock knows exactly what Copilot is doing"}
		return []configEdit{e}, nil, nil
	},
}

// copilotHooksFile is ~/.copilot/hooks/hammock.json for exe: one command per
// event, naming the event.
func copilotHooksFile(exe string) []byte {
	type entry struct {
		Type       string `json:"type"`
		Bash       string `json:"bash"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	hooks := map[string][]entry{}
	for _, ev := range copilotHookEvents {
		hooks[ev] = []entry{{Type: "command", Bash: copilotHookCommand(exe, ev), TimeoutSec: 5}}
	}
	out, _ := json.MarshalIndent(map[string]any{"version": 1, "hooks": hooks}, "", "  ")
	return append(out, '\n')
}

func copilotHookCommand(exe, event string) string {
	return hookCommand(exe, "copilot", event) + " || true"
}
