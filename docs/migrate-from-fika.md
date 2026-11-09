# Moving a Mac from Fika to Hammock

For other people's Macs, https://hammock.jsp.scot/migration has the same steps, simpler: open Terminal,
start Claude and paste one message ([migration-page/](migration-page/)).

Hammock is Fika renamed, with new bundle ids, folders and command (`hmk`), so
nothing carries over by itself. `scripts/migrate-from-fika.sh` moves one Mac
across, keeping its Groups, tabs, **running panes**, schedules, settings,
linked computers and SSH key. An agent then tidies up what only judgement can:
your dotfiles, skills, memories and scheduled tasks that mention Fika.

## When

- **After** the rename is on `main` and there's a Hammock build to install
  (`just mac-app` on a dev Mac, or the shared, notarized zip from `just share`).
- **All your Macs in one sitting.** Linked computers only talk to the same
  version: a Hammock Mac and a Fika Mac can't reach each other.
- **The iPhone afterwards:** the iPhone app is a new app (new bundle id), so
  install Hammock from TestFlight and pair it again once the Macs are done.

## Where

- **In Terminal.app, not in a Fika pane.** The script quits Fika and stops its
  daemon. Panes do carry on (their holders outlive the daemon, and Hammock's
  daemon picks them up), so running it in a pane works with `--from-pane`, but
  if anything goes wrong mid-way Terminal is still there.
- **Not from inside an agent running in Fika** for the same reason: run the
  script yourself, then start the agent (below) in Hammock.
- The script is self-contained: from a checkout at or after the rename, or
  copied to the Mac on its own.

## Steps, on each Mac

1. **Install Hammock.app** (in /Applications, or anywhere: pass `--hammock
   PATH`). **Don't open it yet**: its first launch would start with empty
   state, and the script refuses to merge into that.
2. **Dry run**, to see what it will do; it changes nothing:

   ```sh
   scripts/migrate-from-fika.sh
   ```

3. **Do it:**

   ```sh
   scripts/migrate-from-fika.sh --go            # add --trash-fika, --reset-old-permissions to tidy up
   ```

   It:
   1. reads Fika's Groups (to find their `.fika` icon folders);
   2. removes Fika's hooks and MCP server from Claude Code and Codex (`fika hooks install --uninstall`);
   3. quits Fika and its menu bar item, and stops its daemon, login item and browser, screen and simulator hosts, leaving panes running;
   4. moves `~/Library/Application Support/Fika` to `…/Hammock` (pointing the running panes at it), `~/.config/fika` to `~/.config/hammock` and `~/Library/Caches/fika` to `…/hammock`;
   5. copies the app's settings (`defaults`: sidebar, windows, notification style…);
   6. copies the Fika Key to the Hammock Key: the same Secure Enclave key, so nothing to re-add to GitHub or servers;
   7. updates the block Fika added to `~/.ssh/config` (a backup is left at `config.before-hammock`);
   8. renames Group icon folders `.fika` to `.hammock`;
   9. removes the `fika` link and links `hmk` in its place;
   10. opens Hammock, checks its panes came across and runs `hmk hooks install`;
   11. lists files that still mention Fika.

4. **Answer macOS as it asks:**
   - *"security wants to use the Fika Key"*: **Allow** (step 6 reads it once);
   - Login Items: allow **Hammock** in System Settings ▸ General ▸ Login Items if it asks, and remove Fika's if it's still listed;
   - privacy permissions (notifications, Screen Recording, Accessibility…): Hammock asks again, as each is first needed.
5. **Run the agent tidy-up** (below) in a new Hammock pane.
6. **Move work out of panes that carried over** when convenient: their shells
   still have Fika's environment (`FIKA_PANE_ID`, `SSH_AUTH_SOCK`, git's signing
   program…), so agents there don't report status to Hammock, and `ssh` and
   signed `git commit` fail (they point at Fika's old folder). New panes are
   fine: resume an agent there (`claude --resume`, or from Hammock's sessions)
   and it picks up where it was.

### Dev Macs

- Fika β isn't migrated. Once you've no use for it, delete its state
  (`~/Library/Application Support/Fika Beta*`) and quit any β still running;
  worktrees from before the rename keep building Fika β until rebased.
- `~/Library/Caches/fika` (libghostty-vt's cache among it) moves with step 4,
  so the next `just build` finds it under `…/hammock`.

### If it goes wrong

Nothing is deleted. Up to step 4, just open Fika again. After it: quit Hammock
and `hmk stop`, move `…/Application Support/Hammock` back to `…/Fika` (and
`~/.config/hammock`, `~/Library/Caches/hammock`), restore
`~/.ssh/config.before-hammock`, open Fika and run `fika hooks install`.

## The agent tidy-up

Start Claude Code (or Codex) in a new **Hammock** pane, in your home folder,
and give it this prompt:

````text
This Mac has just moved from Fika to Hammock: the same terminal-and-agents app,
renamed. The app's own state has been migrated already (by
scripts/migrate-from-fika.sh). Your job is to update everything *else* on this
Mac that still refers to Fika, so it works with Hammock.

What changed (old → new):
- The CLI `fika` → `hmk` (subcommands are unchanged: `fika ls` → `hmk ls`);
  the daemon `fikad` → `hmkd`.
- Environment variables `FIKA_*` → `HMK_*` (FIKA_PANE_ID → HMK_PANE_ID, FIKA_DIR → HMK_DIR…).
- The MCP server `fika` → `hammock`, so tools `mcp__fika__*` → `mcp__hammock__*`.
- The app "Fika" → "Hammock" ("Fika β" → "Hammock β", "Fika Key" → "Hammock Key").
- Paths: `~/Library/Application Support/Fika` → `…/Hammock`, `~/.config/fika` →
  `~/.config/hammock`, `Fika.app` → `Hammock.app`, `Contents/Helpers/fika` →
  `Contents/Helpers/hmk`, Group icon folders `.fika/` → `.hammock/`.
- URL scheme `fika://` → `hammock://`; bundle ids `com.homegrownsw.fika…` → `com.homegrownsw.hammock…`.

Do NOT change:
- "fika" meaning the Swedish coffee break, or anything unrelated to the app.
- The source checkout's path (`~/Developer/Fika`): it keeps its name. (The GitHub
  repo is now `homegrownsw/hammock`; GitHub redirects the old name.) Claude's per-project folders
  under ~/.claude/projects are named after paths: leave their names alone.
- Anything inside the Hammock source repo itself (already renamed), release
  notes, or the keychain names `fika` (Sparkle signing key) and `fika-notary`.

Steps:
1. Find references. Search (case-insensitively, following symlinks) for
   `fika`, `FIKA_` and `mcp__fika__` in: shell startup files (~/.zshrc,
   ~/.zprofile, ~/.zshenv, ~/.bashrc, ~/.bash_profile, ~/.profile, fish config);
   ~/.config; ~/bin and ~/.local/bin scripts; ~/.claude (CLAUDE.md,
   settings.json, skills, agents, commands, plugins you own, and
   projects/*/memory); ~/.codex (AGENTS.md, config.toml, skills); ~/Library/LaunchAgents;
   `crontab -l`; and Hammock's scheduled tasks (`hmk schedule list`, and each
   task's command or prompt). Skip caches, logs, node_modules, .git and
   downloaded or generated files.
2. Show me what you found, grouped by place, with the change you'd make to
   each (old line → new line), and anything you're unsure about. Wait for my OK.
3. Make the agreed edits. Where a file is in a git repository, edit it but
   don't commit or push unless I ask. For memories, update the facts (CLI
   names, paths, tool names) without rewriting their history.
   Scheduled tasks: change them with `hmk schedule` (or the hammock MCP
   tools), not by editing Hammock's database.
4. Check it works:
   - `which fika` finds nothing and `hmk ls` lists my Groups;
   - `claude mcp list` shows `hammock` and no `fika`;
     `hmk hooks install --dry-run` has nothing left to do;
   - in a new Hammock pane, `ssh-add -l` lists the Hammock Key;
   - `grep` again: nothing app-related says Fika any more.
5. Tell me what you changed, and what's left for me to do by hand (privacy
   permissions, re-pairing the iPhone, other Macs).
````
