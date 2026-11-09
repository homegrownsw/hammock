package main

// Cursor's CLI agent reads hooks from ~/.cursor/hooks.json (the Cursor app
// shares it): {"version": 1, "hooks": {"<event>": [{"command": …}]}}, each
// run with the event's JSON (which names it, as hook_event_name) on stdin.
// A config naming an event Cursor doesn't know is rejected whole, so these
// are the events every Cursor with hooks has had (the newer preToolUse and
// postToolUse would cover more tools, but break older ones). Its interactive
// CLI only runs beforeSubmitPrompt and stop hooks from its own hooks.json,
// not Claude Code's settings it also reads.
func init() {
	hookTargets["cursor"] = flatHooks{
		harness: "cursor",
		file:    ".cursor/hooks.json",
		events: []string{"sessionStart", "sessionEnd", "beforeSubmitPrompt", "beforeShellExecution", "afterShellExecution",
			"beforeMCPExecution", "afterMCPExecution", "afterFileEdit", "stop"},
		cmdKey: "command",
		entry:  map[string]any{"timeout": 5},
		top:    map[string]any{"version": 1},
		why:    "so Hammock knows exactly what Cursor Agent is doing",
	}.target()
}
