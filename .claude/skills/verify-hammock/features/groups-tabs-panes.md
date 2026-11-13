# Groups, tabs and panes

A person organises their terminals into Groups (the sidebar's sections), each holding tabs, each split into panes. Panes can float over their tab and dock back. Closing a tab and reopening it brings it back. Every client (Mac, iPhone, TUI, CLI) sees the same structure.

## Sub-features

- `group-new`: create a Group, optionally from a folder.
- `group-rename`: rename it. `group-rm`: remove it.
- `tab-new`: open a tab running a command in a Group.
- `split`: split a pane right or down.
- `float-dock`: float a pane over its tab, then dock it back.
- `close-reopen`: close a tab, then `hmk reopen` restores it.

## How to get to it (user POV)

- CLI: `hmk group new|rename|rm`, `hmk tab new`, `hmk split`, `hmk float`/`hmk dock`, `hmk close`, `hmk reopen`.
- TUI: Ctrl-B `c` (tab), `%`/`"` (split), `x` (close). See [tui.md](./tui.md).
- Mac: the sidebar's + and right-click menus, File and View menus, the command palette (File ▸ Command Palette…, ⌘K by default). Real clicks need the person; the window's resulting state can be snapshotted ([mac-window.md](./mac-window.md)).

## Driving it with verify-hammock

Preconditions:

- Baseline run, any build. Empty daemon.

- **Create a Group.** `$S/record.sh "$RUN" 01-group-new group new verify-demo` exits 0 and prints a `gp_…` ID.
- **Open a tab.** `$S/record.sh "$RUN" 02-tab-new tab new verify-demo --name shell -- /bin/sh` prints a `tb_…` ID.
- **Split it.** Take the pane: `P=$($S/hmk "$RUN" ls --json | jq -r '.groups[]|select(.title=="verify-demo").tabs[0].panes[0].id')`. Then `$S/record.sh "$RUN" 03-split split "$P" --down -- /bin/sh` prints a new `pn_…`.
- **Float and dock.** `$S/record.sh "$RUN" 04-float float <new pane>`, then `05-dock dock <new pane>`. Both exit 0.
- **Rename.** `$S/record.sh "$RUN" 06-rename group rename verify-demo verify-renamed`.
- **Proof of structure.** `$S/record.sh "$RUN" 07-ls ls --json`. Group `verify-renamed` has tab `shell` with two panes, both `"kind":"terminal"`. Plain `hmk ls` shows the same as a tree (`gp_…  verify-renamed`, `tb_…  shell`, `pn_…` rows with size and cwd).
- **Close and reopen.** `08-close close <tb_…>`. `09-ls ls --json` no longer has it. `10-reopen reopen`. `11-ls ls --json` has a tab named `shell` again.
- **Side effect.** While the panes run, `ps -axww -o pid=,command= | grep "$SCRATCH"` shows a pane-holder per pane (load `SCRATCH` from `$RUN/run.env`).

## Gotchas

- In `ls --json` a Group's name is `.title`, not `.name` (`.name` is null). A tab has `.title` and `.named`.
- `hmk tab new` with no GROUP means "the Group this pane runs in", which is meaningless against a scratch daemon. Always pass the Group.
- Without `-- CMD` a pane runs the person's login shell, so its rc files run and its screen shows their prompt.
- `float`/`dock` change layout only. The pane list in `ls --json` looks the same, so prove them with a Mac snapshot (`views.txt` geometry) if position matters.
