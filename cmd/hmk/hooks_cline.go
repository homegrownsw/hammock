package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Cline's CLI (3.x, read at 3.0.74: sdk/packages/core/src/hooks) runs hook
// scripts it finds by name: a file named after its event (TaskStart,
// UserPromptSubmit, PreToolUse, PostToolUse, TaskComplete, TaskError,
// TaskCancel…, with no extension or one of .sh, .bash, .zsh, .js, .ts, .py,
// .ps1…) in ~/.cline/hooks, among other folders, every such file running,
// with the event's JSON on stdin. Hammock writes one small script per event
// there, named <Event>.bash so it sits beside any of the person's own
// (which are usually <Event>.sh or <Event>), that forwards it to `hmk hook
// cline <Event>`.
//
// Cline awaits PreToolUse and PostToolUse and reads their stdout as JSON
// ({} changes nothing; anything else unreadable is logged and ignored), so
// the script prints {} whatever happens. It runs the other events detached.
//
// Cline's sessions are run by its shared background hub when one is up
// (usual: the first cline starts it), and the hub runs their hooks with its
// own environment, the one of whichever cline started it, so HMK_PANE_ID
// there names that cline's pane, not the session's. The hub's environment
// has CLINE_NO_INTERACTIVE set (sdk/packages/core/src/hub/daemon), so the
// script skips those, and only sessions cline runs in its own process are
// reported; the screen says what the others are doing.

const clineHooksDir = ".cline/hooks"

// clineHookEvents are the events Hammock's scripts forward, by Cline's names.
var clineHookEvents = []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "TaskComplete", "TaskError", "TaskCancel"}

func init() {
	why := "so Hammock knows what Cline is doing"
	var files []hookTarget
	for _, ev := range clineHookEvents {
		files = append(files, pluginFile{file: clineHookFile(ev), content: clineHookScript(ev), why: why}.target())
	}
	hookTargets["cline"] = hookTarget{
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
			// One set of scripts: say so once.
			for i := range edits {
				switch {
				case i > 0:
					edits[i].changes = nil
				case uninstall:
					edits[i].changes = []string{"Removes Hammock's hook scripts from ~/" + clineHooksDir + ", keeping your own"}
				default:
					edits[i].changes = []string{"Adds Hammock's hook scripts to ~/" + clineHooksDir + " " + why}
				}
			}
			return edits, notes, nil
		},
		// Uninstalled, the folder goes too if nothing else is in it.
		apply: func(_ string, uninstall, dry bool) error {
			if home, err := os.UserHomeDir(); err == nil && uninstall && !dry {
				_ = os.Remove(filepath.Join(home, clineHooksDir))
			}
			return nil
		},
	}
}

// clineHookFile is Hammock's script for ev, relative to the home folder.
func clineHookFile(ev string) string { return filepath.Join(clineHooksDir, ev+".bash") }

// clineHookScript is Hammock's script for ev, for this copy of hmk.
func clineHookScript(ev string) func(exe string) []byte {
	return func(exe string) []byte {
		return []byte(strings.Join([]string{
			"#!/bin/sh",
			"# " + pluginMarker + ": tells Hammock what Cline is doing in its panes.",
			"# Not when Cline's background hub runs the session (CLINE_NO_INTERACTIVE):",
			"# the hub has the environment of the terminal that started it, not this one's.",
			`if [ -n "$HMK_PANE_ID" ] && [ -z "$CLINE_NO_INTERACTIVE" ]; then`,
			"\t" + hookCommand(exe, "cline", ev) + " >/dev/null 2>&1 || true",
			"else",
			"\tcat >/dev/null",
			"fi",
			"# Cline reads PreToolUse's and PostToolUse's output: {} changes nothing.",
			"echo '{}'",
			"",
		}, "\n"))
	}
}
