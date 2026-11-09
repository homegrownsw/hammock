package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Antigravity CLI reads hooks from ~/.gemini/config/hooks.json (agy 1.5.3's
// bundled docs; the Antigravity app shares the file): a JSON object of
// named hooks, each its events' handlers, {"hammock": {"SessionStart":
// [{"type": "command", "command": …, "timeout": 5}], "PostToolUse":
// [{"matcher": "*", "hooks": [{…}]}], …}}. Hammock's are all under "hammock",
// so installing sets that one entry and uninstalling deletes it, leaving
// every other hook (herdr's, say) alone.
//
// Its payloads don't name their event, so each command does. Every hook
// must print a JSON object, which `hmk hook antigravity` does ({}: no
// steps injected, nothing decided); `|| echo '{}'` does it too if this
// hmk has gone. PreToolUse isn't hooked: its answer is a permission
// decision, and Hammock has none to make. Hooks run in the agent's loop,
// one at a time, so they must be quick: hmk's is.

const antigravityHookName = "hammock"

// antigravityHookEvents: SessionStart; PreInvocation, before each call to
// the model (a turn starting, and each step of it); PostToolUse, after each
// tool; Stop, the turn over.
var antigravityHookEvents = []string{"SessionStart", "PreInvocation", "PostToolUse", "Stop"}

func init() { hookTargets["antigravity"] = antigravityHookTarget }

var antigravityHookTarget = hookTarget{
	edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
		rel := filepath.Join(".gemini", "config", "hooks.json")
		path := filepath.Join(home, rel)
		where := "~/" + rel
		doc, err := readConfig(path)
		if err != nil {
			return nil, nil, err
		}
		settings := map[string]any{}
		if len(bytes.TrimSpace(doc)) > 0 {
			if err := json.Unmarshal(doc, &settings); err != nil {
				return nil, nil, fmt.Errorf("parse %s: %w", where, err)
			}
		}
		e := configEdit{path: path, old: doc, new: doc, diffFrom: normalizeJSON(doc)}
		cur, has := settings[antigravityHookName]
		if has && !antigravityIsHammocks(cur) {
			return nil, []string{where + ` has a hook named "hammock" that isn't Hammock's: left as it is`}, nil
		}
		if uninstall {
			if has {
				delete(settings, antigravityHookName)
				e.changed = true
			}
			e.changes = []string{"Removes Hammock's hooks from " + where + ", keeping your own"}
		} else {
			want := antigravityHooks(exe)
			if !has || !antigravityJSONEqual(cur, want) {
				settings[antigravityHookName] = want
				e.changed = true
			}
			e.changes = []string{"Adds Hammock's hooks to " + where + " so Hammock knows exactly what Antigravity is doing"}
		}
		if e.changed {
			out, err := json.MarshalIndent(settings, "", "  ")
			if err != nil {
				return nil, nil, err
			}
			e.new = append(out, '\n')
		}
		return []configEdit{e}, nil, nil
	},
}

// antigravityHooks is Hammock's entry in hooks.json for exe.
func antigravityHooks(exe string) map[string]any {
	spec := map[string]any{}
	for _, ev := range antigravityHookEvents {
		h := map[string]any{"type": "command", "command": antigravityHookCommand(exe, ev), "timeout": 5}
		if ev == "PreToolUse" || ev == "PostToolUse" {
			// Its tool events take handlers in groups, by tool.
			spec[ev] = []any{map[string]any{"matcher": "*", "hooks": []any{h}}}
		} else {
			spec[ev] = []any{h}
		}
	}
	return spec
}

func antigravityHookCommand(exe, event string) string {
	return hookCommand(exe, "antigravity", event) + " || echo '{}'"
}

// antigravityIsHammocks: a hooks.json entry is the one Hammock wrote.
func antigravityIsHammocks(v any) bool {
	b, _ := json.Marshal(v)
	return strings.Contains(string(b), " hook antigravity ")
}

// antigravityJSONEqual: a and b are the same JSON.
func antigravityJSONEqual(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}
