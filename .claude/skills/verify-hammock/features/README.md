# Hammock verification map

The maintained recipes for proving Hammock's user-facing behaviour against a scratch daemon. Read this index, then follow the feature's file.

## Baseline preconditions

- A run started with `.claude/skills/verify-hammock/scripts/up.sh` (`--beta` for anything in the Mac window), its dir in `$RUN`, and `S=.claude/skills/verify-hammock/scripts`.
- `$S/doctor.sh "$RUN"` passes with no `FAIL`.
- The daemon starts empty (no Groups). Each recipe creates what it needs under Group names starting `verify-`.
- Never drive a daemon this run didn't start: no bare `hmk`, no `HMK_SOCKET` from the pane you run in.

## Driving conventions

- Every `hmk` call goes through `$S/hmk "$RUN" …`, or `$S/record.sh "$RUN" NN-name …` when it's proof.
- Address things by the IDs commands print (`gp_…`, `tb_…`, `pn_…`) or by `verify-` names, never by position in `hmk ls`.
- Panes run an explicit, rc-free command (`-- /bin/sh`, `-- /bin/zsh -f`) so their screens are reproducible and private.
- Wait for output by polling `hmk read` for an expected string (up to a few seconds), not with a fixed sleep.
- `hmk` exit codes matter: non-zero with a message on stderr is a failure, even if stdout looks plausible.

## Proof and skip reporting

- Each proof step pairs the action (`NN-*.cmd/.out/.exit`) with a read-only look at the result (`ls --json`, `read`, a window snapshot).
- Mac-window proof is the `-light.png`/`-dark.png` pair (look at it) plus an assertion on `-views.txt`.
- If an entry point can't be driven here (a real click, the iPhone), say so and name it. It's unverified, not verified some other way.
- A precondition the scratch daemon can't provide is a failure, with the reason added to that feature's Gotchas. Don't weaken the journey to make it pass.

## Feature entry contract

Each file has an H1, one paragraph on what a user sees, then exactly these H2s in order: `Sub-features`, `How to get to it (user POV)`, `Driving it with verify-hammock`, `Gotchas`.

## Features

- [Groups, tabs and panes](./groups-tabs-panes.md): create, rename, split, float, close and reopen the workspace's structure.
- [Pane input and output](./pane-io.md): type into a pane and read its screen and scrollback.
- [The TUI](./tui.md): `hmk tui` in a terminal, its sidebar and Ctrl-B keys.
- [The Mac window](./mac-window.md): the sidebar, tabs and terminal panes as the app draws them, plus its command palette and Settings.
- [Scheduled tasks](./scheduled-tasks.md): a command on a schedule, run now, landing as a tab in its Group.
