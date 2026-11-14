# Terminal panes

The everyday pane: a shell or command in a terminal the daemon owns. A person (or an agent, through `hmk` or MCP) types into it and reads its screen, including scrollback, without attaching. Output keeps arriving while no client watches, and the Mac draws it with libghostty.

## Sub-features

- `send-text`: type text, optionally followed by Enter.
- `send-keys`: named keys (`escape`, `ctrl-c`, `up`, `f2`…), each pressed separately.
- `read-screen`: the visible screen as text. `read-scrollback`: everything kept.
- `background-output`: output written while nobody is attached is there to read later.
- `mac-render`: the Mac window draws the same text (a snapshot's `views.txt` and PNGs).

## How to get to it (user POV)

- CLI: `hmk send PANE [TEXT] [--key NAME]… [--enter]`, `hmk read PANE [--scrollback]`, `hmk attach PANE` (interactive; Ctrl-] detaches).
- MCP: `send_to_pane`, `read_pane`, `wait_for_pane` (`hmk mcp`).
- Mac, TUI and iPhone: typing into a focused pane.

## Driving it with verify-hammock

Preconditions:

- Baseline run. A Group `verify-io` with a tab running `/bin/sh`: `$S/hmk "$RUN" group new verify-io` and `$S/hmk "$RUN" tab new verify-io --name io -- /bin/sh`. Its pane ID is in `P` (from `ls --json`).

- **Type a command.** `$S/record.sh "$RUN" 01-send send "$P" 'echo verify-$((6*7))' --enter` exits 0.
- **Read it back.** Poll `$S/record.sh "$RUN" 02-read read "$P"` until its output has a line that is exactly `verify-42`. Matching the arithmetic result, not the typed text, proves the shell ran it.
- **Keys.** Send `sleep 30` with `--enter`, then `03-ctrl-c send "$P" --key ctrl-c`. Within a second, a later `read` shows a fresh `sh-…$` prompt after `^C`.
- **Background output.** `04-send send "$P" 'for i in 1 2 3 4 5 6 7 8 9 10; do seq 1 10; done; echo verify-done' --enter`, wait for `verify-done`, then `05-scroll read "$P" --scrollback`. That holds more lines than the pane's rows (34 by default).
- **In the window** (β run). `$S/snapshot.sh "$RUN" 06-tab HMK_DEBUG_TAB=io`. `06-tab-views.txt` has `TerminalNSView … hidden=false` followed by the pane's last lines (`verify-done`), and the PNGs show them. Terminal content renders in snapshots; web-drawn panes don't.

## Gotchas

- `send` returns before the shell has run anything. Poll `read`; don't read straight after `send`.
- `read` returns the screen only, so long output scrolls away. Use `--scrollback` to assert on it.
- The typed command echoes, so grep for output the command produces (`verify-42`), not text you typed (`verify-$((6*7))`).
- Panes are 120×34 until a client sizes them. A snapshot or TUI resizes them.
