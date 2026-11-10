# Answering Claude Code's prompts with keystrokes

The agent view and Needs You answer an agent's prompts by typing into its pane, as a person would. This note is what Claude Code expects. The code is [`internal/agentview/keys.go`](../../internal/agentview/keys.go) (the key scripts), [`internal/agentview/prompt.go`](../../internal/agentview/prompt.go) (reading the prompt from the screen) and `answerAgent`, `decideAgent`, `runSteps` and `sendAgent` in [`internal/daemon/agentview.go`](../../internal/daemon/agentview.go).

## Prompts and the keys that answer them

| Prompt | On screen | Keys that answer it |
|---|---|---|
| Workspace trust | "Quick safety check … ❯ No, exit / Yes, I trust this folder", footer "Enter to confirm" | Options have no numbers: ↓ or ↑ to the option, then Enter. *No* is preselected. |
| Question (`AskUserQuestion`), one question | numbered options, "Type something.", "Chat about this", footer "Enter to select" | The option's digit. Free text: the "Type something" digit, the text, then Enter. |
| Question, several | a tab bar (`←  ☐ Colour  ☐ Fruits  ✔ Submit  →`), one question per tab | Single choice: the digit, which moves to the next tab. Multi-select: digits toggle, → moves on. The Submit tab has no footer; its `1` submits. |
| Question with previews | options on the left, each one's preview boxed beside them, a "Notes:" line, footer "n to add notes" | A digit only moves the selection; Enter picks. Notes: `n`, the text, Esc (keeps the text), then Enter. |
| Permission (Bash, edits…) | "Do you want to proceed?" and three or four numbered options, which vary by tool | The option's digit. The options must be read from the screen. |
| Plan approval (`ExitPlanMode`) | "Would you like to proceed?" with numbered options, the last "Tell Claude what to change", footer "ctrl+g to edit" | The digit. Feedback: the last option's digit, the text, Enter. |

Deny, and stopping the agent, is Esc. Needs You's Allow is the first option: Enter if it's already selected, else its digit; in a menu without numbers, the option starting "Yes".

## Reading the prompt

- The screen is the source. A menu is the `❯ N. label` lines (`›` in Codex) above a footer of key hints in the last ten lines: "Esc to cancel", "ctrl+g to edit", "Enter to select" and Codex's equivalents. `❯` marks the selected option.
- The question tool doesn't write its call to the session file until it's answered, so the transcript can't be relied on for the options. When a pending call is there, its kind decides the prompt's kind (`ExitPlanMode` is a plan, `AskUserQuestion` a question, anything else a permission), and the question's full text, descriptions and previews come from it, since the screen cuts long lines to the terminal's width.
- Answers land in the transcript as `tool_result` text plus a structured `toolUseResult` (`answers` keyed by question).

## Constraints on typing

- **Enter is `CSI 13 u`** (`\x1b[13u`). Claude Code turns on the kitty keyboard protocol, and a bare `\r` doesn't submit. `Enter(kitty)` encodes it from the pane's current keyboard flags, read from the daemon's VT mirror.
- **Text and Enter go in separate writes.** Written together they're taken as a paste and not submitted. `runSteps` leaves at least 80 ms between an answer's keystrokes, since keys that arrive together are read as one burst ("1n" is neither a number nor `n`). `sendAgent` leaves 150 ms between its pastes and Enter.
- **Every step is checked against the screen first.** An answer carries the options the client saw. Each step waits (up to 1.5 s) until the screen still shows those options, and the selection where the previous step left it, so a changed or vanished prompt never gets stray keys.
- **Messages go in as pastes**, bracketed when the program turned on mode 2004 (Claude Code does). Newlines then stay in one message, and an attached image's path becomes `[Image #n]`. Whatever was already typed in the input box is cleared first.
