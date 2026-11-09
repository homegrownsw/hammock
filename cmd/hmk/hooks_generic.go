package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Builders for the usual kinds of hook configuration, so an agent's
// hookTarget (hooks_agents.go) is mostly a description of its own.

// hookCommand is the command an agent runs for Hammock's hooks: `hmk hook
// HARNESS`, and the event's name when the agent's payload doesn't say it.
func hookCommand(exe, harness, event string) string {
	cmd := shellQuote(exe) + " hook " + harness
	if event != "" {
		cmd += " " + event
	}
	return cmd
}

// nestedHooks describes hooks in Claude Code's shape, which many agents
// copied: {"hooks": {"Event": [{"matcher": …, "hooks": [{"type":
// "command", "command": …, "timeout": …}]}]}}.
type nestedHooks struct {
	harness string
	// file is the settings file, relative to the home folder.
	file   string
	events []string
	// named: the payload doesn't name its event, so the command does.
	named bool
	// matcherFor is the matcher for an event's entry ("" for none).
	matcherFor func(ev string) string
	// timeout is in seconds, or with millis, milliseconds.
	timeout int
	millis  bool
	// what Hammock's hooks are for, in the onboarding's words ("so Hammock knows
	// exactly what Droid is doing").
	why string
}

func (n nestedHooks) target() hookTarget {
	marker := " hook " + n.harness
	cmdFor := func(exe string) func(string) string {
		return func(ev string) string {
			if n.named {
				return hookCommand(exe, n.harness, ev)
			}
			return hookCommand(exe, n.harness, "")
		}
	}
	matcherFor := n.matcherFor
	if matcherFor == nil {
		matcherFor = func(string) string { return "" }
	}
	timeout := n.timeout
	if timeout == 0 {
		timeout = 5
	}
	if n.millis && n.timeout == 0 {
		timeout = 5000
	}
	return hookTarget{
		edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
			path := filepath.Join(home, n.file)
			doc, err := readConfig(path)
			if err != nil {
				return nil, nil, err
			}
			e := configEdit{path: path, old: doc, new: doc, diffFrom: normalizeJSON(doc)}
			where := "~/" + n.file
			if uninstall {
				if len(doc) > 0 {
					e.new, e.changed, err = uninstallHooksJSON(doc, marker)
				}
				e.changes = []string{"Removes Hammock's hooks from " + where + ", keeping your own"}
			} else {
				e.new, e.changed, err = installHooksJSONEach(doc, cmdFor(exe), marker, n.events, matcherFor, func(string) int { return timeout })
				e.changes = []string{fmt.Sprintf("Adds Hammock's hooks to %s %s", where, n.why)}
			}
			if err != nil {
				return nil, nil, err
			}
			return []configEdit{e}, nil, nil
		},
	}
}

// flatHooks describes hooks as a flat list per event, as Cursor's and
// Copilot's are: {"hooks": {"event": [{<cmdKey>: …, …}]}}, with extra keys
// at the top (Cursor's "version": 1).
type flatHooks struct {
	harness string
	file    string
	events  []string
	named   bool
	cmdKey  string         // "command" (Cursor), "bash" (Copilot)
	entry   map[string]any // other keys in each entry
	top     map[string]any // keys the file must have at its top
	why     string
}

func (f flatHooks) target() hookTarget {
	marker := " hook " + f.harness
	return hookTarget{
		edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
			path := filepath.Join(home, f.file)
			doc, err := readConfig(path)
			if err != nil {
				return nil, nil, err
			}
			e := configEdit{path: path, old: doc, new: doc, diffFrom: normalizeJSON(doc)}
			where := "~/" + f.file
			if uninstall {
				if len(doc) > 0 {
					e.new, e.changed, err = editFlatHooks(doc, f, marker, "")
				}
				e.changes = []string{"Removes Hammock's hooks from " + where + ", keeping your own"}
			} else {
				e.new, e.changed, err = editFlatHooks(doc, f, marker, exe)
				e.changes = []string{fmt.Sprintf("Adds Hammock's hooks to %s %s", where, f.why)}
			}
			if err != nil {
				return nil, nil, err
			}
			return []configEdit{e}, nil, nil
		},
	}
}

// editFlatHooks puts Hammock's entry under each of f's events (exe "": takes
// Hammock's entries out), keeping everything else.
func editFlatHooks(doc []byte, f flatHooks, marker, exe string) ([]byte, bool, error) {
	settings := map[string]any{}
	if len(bytes.TrimSpace(doc)) > 0 {
		if err := json.Unmarshal(doc, &settings); err != nil {
			return nil, false, fmt.Errorf("parse %s: %w", f.file, err)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	if exe == "" {
		for ev, v := range hooks {
			var kept []any
			for _, e := range asSlice(v) {
				if m, ok := e.(map[string]any); ok && isHookFor(m[f.cmdKey], marker) {
					changed = true
					continue
				}
				kept = append(kept, e)
			}
			if len(kept) == 0 {
				delete(hooks, ev)
			} else {
				hooks[ev] = kept
			}
		}
	} else {
		for k, v := range f.top {
			if _, ok := settings[k]; !ok {
				settings[k] = v
				changed = true
			}
		}
		for _, ev := range f.events {
			cmd := hookCommand(exe, f.harness, "")
			if f.named {
				cmd = hookCommand(exe, f.harness, ev)
			}
			entries := asSlice(hooks[ev])
			present := false
			for _, e := range entries {
				if m, ok := e.(map[string]any); ok && isHookFor(m[f.cmdKey], marker) {
					present = true
					if m[f.cmdKey] != cmd {
						m[f.cmdKey] = cmd
						changed = true
					}
				}
			}
			if present {
				continue
			}
			entry := map[string]any{f.cmdKey: cmd}
			for k, v := range f.entry {
				entry[k] = v
			}
			hooks[ev] = append(entries, entry)
			changed = true
		}
	}
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	return append(out, '\n'), changed, err
}

// pluginFile describes a plugin Hammock writes into an agent's plugin folder
// (OpenCode's, Pi's), which forwards its events to `hmk hook`. The file
// carries Hammock's path; reinstalling rewrites it.
type pluginFile struct {
	file    string                  // relative to the home folder
	content func(exe string) []byte // the plugin, for this copy of hmk
	why     string
}

// pluginMarker is in every plugin file Hammock writes, so uninstalling removes
// only Hammock's own.
const pluginMarker = "Written by Hammock (hmk hooks install)"

func (pf pluginFile) target() hookTarget {
	return hookTarget{
		edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
			path := filepath.Join(home, pf.file)
			doc, err := readConfig(path)
			if err != nil {
				return nil, nil, err
			}
			where := "~/" + pf.file
			e := configEdit{path: path, old: doc}
			if len(doc) > 0 && !bytes.Contains(doc, []byte(pluginMarker)) {
				return nil, []string{where + " isn't Hammock's: left as it is"}, nil
			}
			if uninstall {
				if len(doc) > 0 {
					e.remove, e.changed = true, true
					e.changes = []string{"Removes Hammock's plugin " + where}
				}
				return []configEdit{e}, nil, nil
			}
			e.new = pf.content(exe)
			e.changed = !bytes.Equal(doc, e.new)
			e.changes = []string{fmt.Sprintf("Adds Hammock's plugin %s %s", where, pf.why)}
			return []configEdit{e}, nil, nil
		},
	}
}

// removeConfig deletes a file Hammock wrote.
func removeConfig(path string, dry bool) error {
	if dry {
		fmt.Printf("--- %s (would remove)\n", path)
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("%s: removed\n", strings.TrimSpace(path))
	return nil
}
