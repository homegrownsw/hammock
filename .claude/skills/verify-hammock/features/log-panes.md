# Log panes

A log followed in a pane and parsed into entries (time, level, message, process): a file, the Mac's unified log, a simulator's log, Android's logcat, or a command's output. People filter it in the app. Agents read it filtered by text, level, process or tag, and follow new entries.

## Sub-features

- `log-open`: follow a file, `macos`, `sim`, `logcat` or a command (`hmk log …`, `log_open`).
- `log-read`: entries, filtered (`hmk log read PANE [TEXT] [--level L] [--json]`, `log_read`).
- `log-follow`: new lines appended later become entries.
- `log-clear`: forget the entries (`hmk log clear`).

## How to get to it (user POV)

- CLI: `hmk log file PATH | macos | sim [DEVICE] | logcat | -- CMD [--group G | --split PANE]`, `hmk log read|clear PANE`.
- MCP: `log_open`, `log_read`.
- Mac: the split menus' "with Log…" items, the palette ("Split Right with Log…").

## Driving it with verify-hammock

Preconditions:

- Baseline run. A log file in the scratch dir: `eval "$(sed 's/^/export /' "$RUN/run.env")"; L=$SCRATCH/app.log; printf 'INFO start\n' >"$L"`. Group `verify-log`.

- **Open.** `$S/record.sh "$RUN" 01-open log file "$L" --group verify-log` prints the pane id (`G`).
- **Follow.** Append `printf 'ERROR verify-boom 42\nINFO later\n' >>"$L"`, then wait about two seconds.
- **Read, parsed.** `$S/record.sh "$RUN" 02-read log read "$G" --json` prints three entries: `{"level":"info","message":"start","seq":1,…}`, `{"level":"error","message":"verify-boom 42","seq":2,…}` and `{"level":"info","message":"later","seq":3,…}`.
- **Filter.** `$S/record.sh "$RUN" 03-errors log read "$G" --level error` prints only `… ERROR   verify-boom 42`.
- **Clear.** `04-clear log clear "$G"`, then `05-read log read "$G" --json` is empty until more lines arrive.

## Gotchas

- `ls --json` reports a log pane as `"kind":"terminal"`, titled after the file (`app.log`), so you can't find it by kind. Use the id `hmk log` printed.
- Levels are parsed from the text (`ERROR`, `INFO`…). A line without a recognisable level gets the default level, so assert on `message` too.
- `hmk log macos` and `log sim` follow the person's real logs. Fine to read, but don't copy their entries into anything public.
