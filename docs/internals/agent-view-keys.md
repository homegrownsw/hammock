# Spike: answering Claude Code's prompts with keystrokes

2026-10-30, Claude Code 2.1.314, run in a Hammock pane on Haiku against a scratch directory.

**Result: it works for every prompt type, with a small, uniform key vocabulary.**

| Prompt | On screen | In the transcript | Keys that answer it |
|---|---|---|---|
| Workspace trust | "Accessing workspace … ❯ No, exit / Yes, I trust this folder" | nothing | ↓ then Enter (note: *No* is preselected) |
| `AskUserQuestion`, one question | numbered options, "Type something.", "Chat about this", "Enter to select" | pending `tool_use` with the full questions | the option's digit; free text: the "Type something" digit, the text, Enter |
| `AskUserQuestion`, several | a tab bar `←  ☐ Colour  ☐ Fruits  ✔ Submit  →`, one question per tab | same | single choice: digit (advances); multi-select: digits toggle, → advances; the Submit tab: `1` ("Submit answers") |
| Permission (Bash, edits…) | "Do you want to proceed?" + 3–4 numbered options that vary by tool | pending `tool_use` (no result yet) | the option's digit; the options must be read **from the screen** |
| Plan approval (`ExitPlanMode`) | "Would you like to proceed?" 1 auto / 2 manual / 3 "Tell Claude what to change" | pending `ExitPlanMode` with `plan` (markdown) | digit; feedback: `3`, the text (typed into the option), Enter |

Findings:

- **Enter is `CSI 13 u`.** Claude Code enables the kitty keyboard protocol; a bare `\r` doesn't submit. Encode keys from the pane's current keyboard flags.
- **Type, then Enter separately.** Text and Enter in one write is taken as a paste and not submitted.
- Results land in the transcript as `tool_result` text plus structured `toolUseResult` (`answers` keyed by question).
- Prompt kind = the pending tool (`AskUserQuestion` → question, `ExitPlanMode` → plan, anything else → permission); options and the selected row come from the screen (`❯ N. label` lines above an "Esc to cancel" footer).
- Shift-Tab (`ESC [ Z`) cycles manual → accept edits → plan → auto.
