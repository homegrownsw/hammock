package main

// Gemini CLI's hooks (docs/hooks/reference.md, 0.69) and Qwen Code's
// (docs/users/features/hooks.md, 0.26) both live under "hooks" in the
// user's settings.json, in Claude Code's nested shape; each payload names
// its event (hook_event_name). Gemini's timeouts are in milliseconds,
// Qwen's in seconds. Neither asks you to trust hooks in your own settings
// (only a project's).

func init() {
	hookTargets["gemini"] = nestedHooks{
		harness: "gemini",
		file:    ".gemini/settings.json",
		// Not BeforeModel, AfterModel or BeforeToolSelection: they fire for
		// every request and streamed chunk, and say nothing about state.
		events: []string{"SessionStart", "BeforeAgent", "BeforeTool", "AfterTool", "Notification", "AfterAgent", "SessionEnd"},
		millis: true,
		why:    "so Hammock knows exactly what Gemini is doing",
	}.target()
	hookTargets["qwen"] = nestedHooks{
		harness: "qwen",
		file:    ".qwen/settings.json",
		events: []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
			"PermissionRequest", "Notification", "Stop", "StopFailure", "SessionEnd"},
		why: "so Hammock knows exactly what Qwen is doing",
	}.target()
}
