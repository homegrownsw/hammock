# The TUI

`hmk tui` is Hammock in a terminal: a sidebar of Groups and tabs, the focused tab's panes split beside it, and mouse support. Ctrl-B is the prefix (`c` new tab, `%`/`"` split, `o` next pane, `n`/`p` tab, `x` close, `q` quit, `?` keys). It shows the same workspace as the Mac app.

## Sub-features

- `tui-sidebar`: Groups and tabs made elsewhere (CLI) show in its sidebar.
- `tui-new-tab`: Ctrl-B `c` opens a tab the daemon then lists.
- `tui-split`: Ctrl-B `%` splits the focused pane.
- `tui-type`: typing reaches the focused pane's process.
- `tui-quit`: Ctrl-B `q` leaves the TUI, and panes keep running.

## How to get to it (user POV)

- Run `hmk tui` in any terminal (on the Mac, over SSH, or on Linux).

## Driving it with verify-hammock

Preconditions:

- Baseline run. A Group `verify-tui` with one tab running `/bin/sh`, made with the CLI.
- The tmux session from SKILL.md's Drive section, named `hmk-verify-$$` (here `T`), 160×45, running `$HMK tui` with the run's `HMK_DIR`/`HMK_CONFIG_DIR` and the pane env cleared.

- **Sidebar.** Wait a second, then `tmux capture-pane -p -t "$T" >"$RUN/01-tui.txt"`. It contains `verify-tui`.
- **Type.** `tmux send-keys -t "$T" 'echo verify-$((6*7))' Enter`, then capture to `02-tui.txt`. It contains a line with `verify-42`. Cross-check with `$S/hmk "$RUN" read <pane>`, which shows the same.
- **New tab.** `tmux send-keys -t "$T" C-b c`, then `$S/record.sh "$RUN" 03-ls ls --json`. `verify-tui` now has two tabs.
- **Split.** `tmux send-keys -t "$T" C-b %`, then `04-ls ls --json`. The focused tab has two panes. The capture `05-tui.txt` shows a divider between them.
- **Quit.** `tmux send-keys -t "$T" C-b q`. `tmux capture-pane` shows the plain shell. `06-ls ls --json` still lists every pane.
- `tmux kill-session -t "$T"` when done, then `down.sh`.

## Gotchas

- `tmux send-keys C-b …` delivers Ctrl-B straight to the TUI (send-keys bypasses tmux's own prefix), so no tmux config is needed.
- New tabs from the TUI run the default shell (the person's, with rc files). Don't assert on their prompt.
