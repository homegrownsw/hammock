# Migrating this Mac from Fika to Hammock: instructions for an agent

You're helping someone move this Mac from **Fika** to **Hammock**. It's the same
app (terminals, agents, browsers and simulators that keep running), renamed, with
a new bundle id, folders and command line tool (`fika` is now `hmk`). A script does
the move and keeps their Groups, tabs, running panes, schedules, settings, linked
computers and SSH key. Your job: install Hammock, run the script, then tidy up
what still mentions Fika.

Everything is served from https://hammock.jsp.scot (reachable on the tailnet
only). Fetch with `curl`. Work through the steps in order. Keep the person
informed in short, plain messages, and **ask before each step marked ASK**.

## Rules

- You must be running in a **normal terminal (Terminal.app, iTerm, Ghostty…),
  not inside Fika or Hammock**. If `FIKA_PANE_ID` or `HMK_PANE_ID` is set in your
  environment, stop and ask the person to quit you, open Terminal.app, and start
  again there.
- While the script runs, Fika's hooks and MCP server go away. If you see errors
  from hooks or an MCP server called `fika` after step 4, they're expected:
  ignore them.
- Don't touch anything about the person's projects beyond what's described here.

## 1. Check this Mac

```sh
sw_vers -productVersion
mdfind "kMDItemCFBundleIdentifier == 'com.homegrownsw.fika'"
ls -d /Applications/Hammock.app 2>/dev/null
ls -d "$HOME/Library/Application Support/Hammock" 2>/dev/null
curl -fsS -o /dev/null -w '%{http_code}\n' https://hammock.jsp.scot/migration/
```

- macOS must be 26 or later.
- Note where Fika.app is (usually /Applications/Fika.app).
- If `~/Library/Application Support/Hammock` already exists, Hammock has been
  opened before: stop and tell the person (the script won't merge into it).
- If the curl doesn't print 200, this Mac isn't on the tailnet: stop and say so.

## 2. Install Hammock (ASK before replacing an existing /Applications/Hammock.app)

```sh
cd "$(mktemp -d)"
curl -fSL https://hammock.jsp.scot/download -o Hammock.zip
ditto -x -k Hammock.zip .
/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' Hammock.app/Contents/Info.plist   # com.homegrownsw.hammock
spctl -a -vv Hammock.app 2>&1 | head -2                                                  # accepted, Notarized Developer ID
ditto Hammock.app /Applications/Hammock.app
```

**Don't open Hammock yet**: its first launch would start with empty state.

## 3. Get the migration script and preview it

```sh
curl -fsSL https://hammock.jsp.scot/migration/migrate-from-fika.sh -o ~/Downloads/migrate-from-fika.sh
chmod +x ~/Downloads/migrate-from-fika.sh
~/Downloads/migrate-from-fika.sh
```

Run without `--go`, it only prints what it would do. Summarise that for the
person in a few lines (their number of Groups and panes; what moves; that their
panes keep running; that Fika.app goes to the Trash).

## 4. Migrate (ASK first)

Tell the person that macOS will show a few prompts during this, and they
should allow them:
- **"security wants to use … Fika Key"**: Allow (it copies their SSH key across);
- **Hammock opening for the first time**: Open;
- later, as Hammock needs them: notifications, Screen Recording, Accessibility.

Then:

```sh
~/Downloads/migrate-from-fika.sh --go --trash-fika
```

If it stops with an error, show the person the error and stop. Nothing is
lost: the script says what it did, and its folders can be moved back.

When it ends, Hammock is open with their Groups and panes. Its last section lists
files that still mention Fika: keep that list for step 5.

## 5. Tidy up (show the plan, ASK, then edit)

Update everything else on this Mac that still refers to Fika so it works with
Hammock. What changed (old → new):

- The CLI `fika` → `hmk` (same subcommands: `fika ls` → `hmk ls`); the daemon
  `fikad` → `hmkd`.
- Environment variables `FIKA_*` → `HMK_*` (FIKA_PANE_ID → HMK_PANE_ID…).
- The MCP server `fika` → `hammock`, so tools `mcp__fika__*` → `mcp__hammock__*`.
- The app "Fika" → "Hammock" ("Fika Key" → "Hammock Key").
- Paths: `~/Library/Application Support/Fika` → `…/Hammock`, `~/.config/fika` →
  `~/.config/hammock`, `Fika.app` → `Hammock.app`, `Contents/Helpers/fika` →
  `Contents/Helpers/hmk`, Group icon folders `.fika/` → `.hammock/`.
- URL scheme `fika://` → `hammock://`.

Don't change "fika" meaning the Swedish coffee break, or anything unrelated to
the app.

1. Search (case-insensitively, following symlinks) for `fika`, `FIKA_` and
   `mcp__fika__` in: the files the script listed; shell startup files
   (~/.zshrc, ~/.zprofile, ~/.zshenv, ~/.bashrc, ~/.bash_profile, ~/.profile,
   ~/.config/zsh, fish config); ~/.config; ~/bin and ~/.local/bin; ~/.claude
   (CLAUDE.md, settings.json, skills, agents, commands, projects/*/memory);
   ~/.codex (AGENTS.md, config.toml, skills); ~/Library/LaunchAgents;
   `crontab -l`; and Hammock's scheduled tasks (`hmk schedule list`, then
   `hmk schedule show ID` for each). Skip caches, logs, node_modules, .git,
   backups and generated files.
2. Show the person what you found, grouped by place, with the change you'd make
   to each (old → new), and wait for their OK.
3. Make the agreed edits. In a git repository, don't commit or push unless they
   ask. Change scheduled tasks with `hmk schedule edit`, never in Hammock's files.

## 6. Check

```sh
command -v fika || echo "no fika: good"
hmk ls
claude mcp list 2>/dev/null | grep -E '^(fika|hammock)'
hmk hooks install --dry-run
```

`hmk ls` lists their Groups, `claude mcp list` shows `hammock` (not `fika`),
and the dry run has nothing left to do.

## 7. Finish

Tell the person, briefly:
- what you changed;
- **quit this terminal session and carry on in Hammock**. Panes that came across
  from Fika still have Fika's settings: agents there don't report status, and
  `ssh` and signed `git commit` may fail in them. New tabs are fine, and agents
  can be resumed there (`claude --resume`);
- on their **iPhone**: install Hammock from TestFlight, pair it with this Mac,
  then delete the old Fika app;
- anything left for them to do by hand (privacy permissions as Hammock asks).
