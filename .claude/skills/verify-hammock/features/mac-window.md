# The Mac window

The Mac app shows the daemon's workspace: a sidebar of Groups and their tabs, the selected tab's panes drawn by libghostty, a toolbar, the command palette and Settings. Without real clicks it can be verified as rendered: build the state with the CLI, then render the window with the app's debug hooks.

## Sub-features

- `window-tab`: a tab's panes, with their terminal text, laid out as `hmk ls` says.
- `window-sidebar`: Groups and tabs in the sidebar (the view tree; materials render blank in PNGs).
- `palette`: the command palette open, searched for some text.
- `settings-pane`: one Settings pane rendered (`notifications`, `keyboard`, `ssh`, `appearance`, `agentview`, `workspace`, `devices`, `editor`, `tools`, `window`, `about`).
- `light-dark`: each renders in both appearances.

## How to get to it (user POV)

- Open Hammock. Groups are in the sidebar. Click a tab. File ▸ Command Palette… (⌘K by default) opens the palette. Hammock ▸ Settings (⌘,).

## Driving it with verify-hammock

Preconditions:

- `just mac-dev` built this checkout's β. The run was started with `up.sh --beta`.
- The person knows a β window will flash up for a few seconds.
- A Group `verify-mac` with a tab `shell` running `/bin/sh` that has printed `verify-42` (see [pane-io.md](./pane-io.md)).

- **Render the tab.** `$S/snapshot.sh "$RUN" 01-tab HMK_DEBUG_TAB=shell HMK_DEBUG_DELAY=8`. It lists `01-tab-light.png`, `01-tab-dark.png` and `01-tab-views.txt`, and exits 0.
- **Assert.** `01-tab-views.txt` has a `TerminalNSView … hidden=false renderer=…` line followed by terminal text containing `verify-42`. Look at both PNGs: the pane shows `verify-42`.
- **Palette.** `$S/snapshot.sh "$RUN" 02-palette HMK_DEBUG_PALETTE=split`. The PNGs show the palette listing split commands.
- **Settings.** `$S/snapshot.sh "$RUN" 03-settings HMK_DEBUG_SETTINGS=keyboard`. That also writes `03-settings-settings-light.png` and `-dark.png`.
- **Daemon unchanged.** After each snapshot, `$S/doctor.sh "$RUN"` still passes: the app used the run's daemon rather than replacing it.

## Gotchas

- The sidebar's list and other materials come out blank in snapshots (AGENTS.md). Assert on `views.txt` for the sidebar, or use `HMK_DEBUG_CAPTURE=1` (needs Screen Recording for β and an awake screen) for a faithful `NAME-window-*.png`.
- From `up.sh` (bin build) the app would swap the daemon for its own. `snapshot.sh` refuses such a run.
- The first launch after `just mac-dev` is slow (helpers re-sign and register). Use `HMK_DEBUG_DELAY=10` for it.
- β copies share a bundle id and its UserDefaults, so window frames and collapsed Groups may carry over from the person's own β. Don't assert on those.
- `HMK_DEBUG_TAB` matches the tab's title. An unnamed tab is named after its command, so name it (`--name`).
