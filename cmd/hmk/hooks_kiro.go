package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Kiro CLI (3.x, its V3 engine) reads every *.json in ~/.kiro/hooks
// ($KIRO_HOME/hooks) as a global hook file, for every agent and every
// workspace, and reloads them as they change (acp-server.js
// StandaloneHookLoader, 3.1.0). So Hammock's hooks are a file of their own
// there, hammock.json, leaving the person's agents and hook files alone:
// uninstalling deletes it. Its schema is {"version":"v1","hooks":[{name,
// description, trigger, matcher, action: {type: "command", command},
// timeout (seconds), enabled}]} and nothing else, so Hammock's mark is in each
// hook's description.
//
// Kiro runs a command hook with the event's JSON on stdin and waits for it;
// what it does with the result is the reason each command ends in
// `|| true`: exit 2 blocks the prompt or tool call (UserPromptSubmit,
// PreToolUse), exit 1 from a Stop hook makes the agent carry on, and
// SessionStart's and UserPromptSubmit's stdout goes to the model.
// `hmk hook kiro` prints nothing and exits 0, but a hmk that's gone or
// crashed mustn't do any of that.

var kiroHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Stop", "SessionEnd"}

func init() {
	hookTargets["kiro"] = hookTarget{
		edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
			file := kiroHooksFile(home)
			edits, notes, err := pluginFile{file: file, content: kiroHooksJSON}.target().edits(home, exe, uninstall)
			// A hook file, not a plugin: say so.
			for i := range edits {
				switch {
				case len(edits[i].changes) == 0:
				case uninstall:
					edits[i].changes = []string{"Removes Hammock's hooks, ~/" + file}
				default:
					edits[i].changes = []string{"Adds Hammock's hooks as ~/" + file + " so Hammock knows exactly what Kiro is doing"}
				}
			}
			return edits, notes, err
		},
		// Its installer puts kiro-cli in ~/.local/bin (or /usr/local/bin).
		binary: func() string { return agentBinary("kiro-cli") },
	}
}

// kiroHooksFile is Hammock's hook file, relative to home: in $KIRO_HOME, which
// moves Kiro's ~/.kiro, when that's set.
func kiroHooksFile(home string) string {
	if d := os.Getenv("KIRO_HOME"); filepath.IsAbs(d) {
		if rel, err := filepath.Rel(home, filepath.Join(d, "hooks", "hammock.json")); err == nil {
			return rel
		}
	}
	return filepath.Join(".kiro", "hooks", "hammock.json")
}

func kiroHooksJSON(exe string) []byte {
	type action struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hook struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Trigger     string `json:"trigger"`
		Action      action `json:"action"`
		Timeout     int    `json:"timeout"`
	}
	var hooks []hook
	for _, ev := range kiroHookEvents {
		hooks = append(hooks, hook{
			Name:        "hammock-" + ev,
			Description: pluginMarker + ": tells Hammock what Kiro is doing in its panes",
			Trigger:     ev,
			Action:      action{Type: "command", Command: hookCommand(exe, "kiro", ev) + " || true"},
			Timeout:     5,
		})
	}
	out, _ := json.MarshalIndent(map[string]any{"version": "v1", "hooks": hooks}, "", "  ")
	return append(out, '\n')
}
