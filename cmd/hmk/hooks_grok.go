package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// xAI's Grok CLI reads every *.json in ~/.grok/hooks, in Claude Code's
// shape and with its event names, so Hammock's hooks are a file of their own
// there, hammock.json, leaving your config.toml and other hook files alone:
// uninstalling deletes it. Each command names its event. Grok runs a
// failed hook as if it had passed (only an explicit deny blocks, and hmk
// hook never gives one), so a hmk that has moved can't stop it working.
// Like Claude Code, Grok holds back your own hooks in a folder you haven't
// trusted (/hooks-trust, or --trust).
//
// Grok also runs Claude Code's hooks from ~/.claude/settings.json, Hammock's
// among them: fromGrok tells `hmk hook claude` it was Grok, which has its
// own.

var grokHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"Notification", "Stop", "StopFailure", "StopCancelled", "SessionEnd"}

const grokHookMarker = " hook grok"

func init() {
	hookTargets["grok"] = grokHookTarget
}

var grokHookTarget = hookTarget{
	// x.ai/cli's install script links it in ~/.grok/bin, which only your
	// shell's profile puts on PATH.
	binary: func() string { return agentBinary("grok", ".grok/bin") },
	edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
		rel := filepath.Join(".grok", "hooks", "hammock.json")
		path := filepath.Join(home, rel)
		doc, err := readConfig(path)
		if err != nil {
			return nil, nil, err
		}
		where := "~/" + rel
		if len(bytes.TrimSpace(doc)) > 0 && !bytes.Contains(doc, []byte(grokHookMarker)) {
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
		e.new = grokHooksFile(exe)
		e.changed = !bytes.Equal(normalizeJSON(doc), normalizeJSON(e.new))
		e.changes = []string{"Adds Hammock's hooks as " + where + " so Hammock knows exactly what Grok is doing"}
		return []configEdit{e}, nil, nil
	},
}

// grokHooksFile is ~/.grok/hooks/hammock.json for exe: one command per event,
// for every tool and notification (no matcher). Its timeout is 5 seconds,
// as Grok's default for most events, where Stop's and PostToolUse's would
// otherwise be 600.
func grokHooksFile(exe string) []byte {
	type handler struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type group struct {
		Hooks []handler `json:"hooks"`
	}
	hooks := map[string][]group{}
	for _, ev := range grokHookEvents {
		hooks[ev] = []group{{Hooks: []handler{{Type: "command", Command: hookCommand(exe, "grok", ev), Timeout: 5}}}}
	}
	out, _ := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	return append(out, '\n')
}

// fromGrok: this hook was run by Grok (which sets GROK_HOOK_EVENT for every
// hook it runs).
func fromGrok() bool { return os.Getenv("GROK_HOOK_EVENT") != "" }
