---
name: verify-hammock
description: Drive a real, isolated Hammock (the hmkd daemon, the hmk CLI, the TUI, and the Mac app's window rendered headless) to prove a change works the way a user would see it. Starts a scratch daemon that can't touch the installed Hammock, β or your config, drives Groups, tabs, panes, the TUI and the Mac window, captures evidence to dist/verify/, and tears it down. Use when verifying a Hammock change end to end, reproducing a bug against a live daemon, or capturing proof (transcripts, window PNGs) for a commit or PR.
---

# Verify Hammock

Hammock is a daemon (`hmkd`) that owns terminals, agents, browsers and simulators, grouped into **Groups ▸ tabs ▸ panes**. People see it through the Mac app (SwiftUI), the iPhone app, the TUI (`hmk tui`) and the `hmk` CLI, and they all show the same workspace. This skill gives you one **scratch daemon per run**. You drive it with the CLI or TUI, or render the Mac window against it, and capture what you see.

Every helper takes `RUN`, the evidence dir that `up.sh` prints (`dist/verify/<stamp>-<pid>/`, git-ignored). Helpers live in `.claude/skills/verify-hammock/scripts/`. Run them from the repo root (or a worktree's root): they find the checkout from their own path.

## Before anything: isolation rules

- **You are probably inside a Hammock pane.** Its `HMK_SOCKET` points at the person's own Hammock and beats `HMK_DIR`, so a bare `hmk ls` from your shell reads (and `hmk close` would change) their real workspace. Always go through `scripts/hmk RUN …` or `scripts/record.sh`, which clear it.
- **A bare `hmkd`/`hmk` uses `~/.config/hammock/config`.** The helpers set `HMK_CONFIG_DIR` to a scratch dir. Never start a daemon by hand without it.
- **Never touch the installed `Hammock.app`, its daemon, or another checkout's β.** All β copies share one bundle id, so never `open -b`, `quit app id …` or `pkill Hammock`. Helpers kill only PIDs they started, or processes whose command line holds this run's unique scratch path.
- Runs are independent: two can run side by side (each has its own `mktemp -d` state dir and socket).
- **The scratch daemon sees you as an agent too.** It walks up your process tree, finds Claude Code, and applies the agent guardrails. You fully control Groups and panes you open there, but some actions wait for the person to approve: adding or editing a schedule, typing into panes you didn't make. A pending request shows in `hmk ls --json` as `.grants`, and the command returns `the person said no (or didn't answer)` after about two minutes. Never answer a grant yourself (`hmk grant` is refused to agents anyway). The recipe either gets the person to approve, or it reports the step as unverified.

## Launch

The surface you'll verify decides which build to run against.

| Surface | Build first | Start |
|---|---|---|
| CLI, TUI, daemon behaviour | `just build` (Go → `bin/`) | `RUN=$(.claude/skills/verify-hammock/scripts/up.sh \| tail -1)` |
| Mac window (needs the app) | `just mac-dev` (this checkout's β; it also runs `just build`) | `RUN=$(.claude/skills/verify-hammock/scripts/up.sh --beta \| tail -1)` |

`up.sh` makes the scratch state and config dirs, starts `hmkd --dir <scratch>/state` in the background, and writes `RUN/run.env` (scratch paths, `DAEMON_PID`, which binaries). **Ready** means `hmk status --json` and `hmk ls --json` both answer. It waits up to 10 s, then prints `ready: hmkd pid N (bin|beta), …` on stderr and `RUN` on stdout. A new daemon has no Groups: `ls --json` is `{"groups":[],"attention":[],"grants":[]}`. Everything a journey needs, it creates.

The Mac window needs `--beta` because the app replaces a daemon from another build with its own. `just mac-dev` in a worktree builds `dist/Hammock β <branch>.app`, and `up.sh --beta` uses whichever β this checkout builds (`scripts/beta-identity.sh`).

## Doctor

Run `.claude/skills/verify-hammock/scripts/doctor.sh "$RUN"` first, and again whenever something looks off. It's read-only and checks four things:

1. `DAEMON_PID` is alive and runs the binary this run chose.
2. The state dir's `run/daemon.lock` holds that PID (nothing else took the dir over).
3. `hmk status --json` reports this run's state dir, not the person's.
4. The build's commit matches `git describe --dirty` of the checkout. It only warns here: rebuild if you changed code since.

It exits non-zero on any `FAIL`. Run `doctor.sh` with no argument to list runs whose daemons are still alive (left over from runs that weren't torn down).

## Drive

**CLI** (`hmk help` lists every command; IDs take any unique prefix, and Groups and tabs also take names):

```sh
S=.claude/skills/verify-hammock/scripts
$S/hmk "$RUN" group new verify-demo                 # prints gp_…
$S/hmk "$RUN" tab new verify-demo --name shell -- /bin/sh   # prints tb_…
$S/hmk "$RUN" ls --json | jq -r '.groups[].tabs[].panes[].id'   # pn_…
$S/hmk "$RUN" send pn_xxxx 'echo verify-$((6*7))' --enter
$S/hmk "$RUN" read pn_xxxx                          # the screen as text: shows verify-42
```

Run panes with an explicit command (`-- /bin/sh`, `-- /bin/zsh -f`). Without one, a pane runs the person's login shell and rc files, so its screen shows their prompt, paths and whatever their rc prints. That isn't reproducible, and it's private.

**TUI**: run it in a tmux session of your own, with the run's env:

```sh
eval "$(sed 's/^/export /' "$RUN/run.env")"
tmux new-session -d -s "hmk-verify-$$" -x 160 -y 45 \
  "env -u HMK_SOCKET -u HMK_PANE_ID -u HMK_HOOK_SOCKET HMK_DIR='$STATE' HMK_CONFIG_DIR='$CONFIG' '$HMK' tui"
tmux send-keys -t "hmk-verify-$$" C-b c          # Ctrl-B prefix: c new tab, % / " split, o next pane, q quit
tmux capture-pane -p -t "hmk-verify-$$" >"$RUN/tui.txt"
tmux kill-session -t "hmk-verify-$$"
```

**Mac window**: `snapshot.sh RUN NAME [KEY=VALUE …]` runs a throwaway copy of this checkout's β on the run's daemon, with `HMK_DEBUG_SNAPSHOT`. After `HMK_DEBUG_DELAY` seconds (4 by default) it renders the 1280×800 window to `RUN/NAME-light.png` and `RUN/NAME-dark.png`, and dumps the view tree to `RUN/NAME-views.txt`, including the last lines of every terminal pane's text. Then it quits that copy. KEY=VALUE pairs are AGENTS.md's other debug hooks, such as `HMK_DEBUG_TAB=<tab name>`, `HMK_DEBUG_PALETTE=<text>` and `HMK_DEBUG_SETTINGS=<pane>`. The window shows on screen for a few seconds, so tell the person first if they're at this Mac. The first launch after a rebuild is slower: pass `HMK_DEBUG_DELAY=10`.

**Not covered here:** the iPhone app (use AGENTS.md's simulator demo, `SIMCTL_CHILD_HMK_DEBUG_SAMPLE=1`), real clicks and drags in the Mac app (ask the person to try it in β), and browser and simulator panes' live content (snapshots don't render CALayerHost; AGENTS.md's `HMK_DEBUG_CAPTURE=1` does, with Screen Recording permission).

## Evidence

Everything goes in `RUN` (`dist/verify/<stamp>-<pid>/`, git-ignored), and teardown leaves it there.

- **CLI steps:** run them through `scripts/record.sh RUN NAME HMK-ARGS…`. It keeps `NAME.cmd`, `.out`, `.err` and `.exit`, prints stdout, and exits with hmk's status. Number the names (`01-…`, `02-…`) so the run reads in order.
- **Daemon log:** `RUN/hmkd.log`. **Build:** `RUN/version.txt`. **App log:** `RUN/NAME-app.log`.
- **Window:** `NAME-{light,dark}.png` and `NAME-views.txt` from `snapshot.sh`. Look at the PNG with Read. Use `views.txt` for exact assertions: pane geometry, `hidden=`, and the terminal text.

Proof standards:

- Drive the user's path (`hmk` commands, TUI keys, the app's own launch hooks), never the daemon's protobuf socket directly or a test-only setter.
- Capture the action and the state after it: the command's output, then a second, read-only look (`ls --json`, `read`, a snapshot) that shows the change took.
- Check side effects too. A pane's process should exist (`ps` with the scratch path), and a closed pane's process should be gone.
- `hmk status --json` lists this Mac's network addresses, and pane text can show the person's paths. Evidence stays local. Before attaching any of it to an issue or PR (the repo is public, per AGENTS.md), use made-up data and check what it shows.

## Cleanup

```sh
.claude/skills/verify-hammock/scripts/down.sh "$RUN"
```

It quits the app copy `snapshot.sh` opened (if one is still running), runs `hmk stop --panes`, then stops `DAEMON_PID`. It also stops any process whose command line holds the run's scratch path (pane holders, helper hosts) and deletes the scratch dir. `RUN` and its evidence stay. Kill your own tmux session separately. Run `down.sh` after every run, failed ones included, and `doctor.sh` with no argument to find any you missed. Delete old `dist/verify/*` dirs yourself when they're no longer needed.

## Feature map

`features/README.md` indexes recipes for each user-facing feature: how a person reaches it, the exact commands, and the end state that proves it. Use the matching file. If a change touches a feature with several entry points (CLI, TUI, Mac menu, palette), the proof covers each one the map lists, or says which were skipped and why.
