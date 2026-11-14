---
title: The CLI
description: A guide to hmk, Hammock's command line, by what you want to do.
---

`hmk` is Hammock's command line. It sees and controls the same Groups, tabs and panes as the Mac app, so you can script your workspace, check on agents from a shell, or attach to a pane from another terminal. This page is a guided tour. For every command and option, see the [CLI reference](/hammock/docs/reference/cli/), or run `hmk help`.

## Where it is

On a Mac, `hmk` is inside the app, at `Hammock.app/Contents/Helpers/hmk`. Setup links it into a folder on your `PATH` (`/opt/homebrew/bin`, `/usr/local/bin` or `~/.local/bin`, the first it can write to). If you skipped that, link it from Hammock ▸ Settings ▸ General ▸ Command-line tool ▸ **Install**, or **Install the hmk Command** in the command palette.

On Linux, `hmk` comes with the daemon: see [Installing on Linux](/hammock/docs/install/linux/).

In a Hammock pane, `hmk` talks to the daemon that runs that pane (through `HMK_SOCKET`, which Hammock sets there), even with `HMK_DIR` set.

## IDs and names

Everything has an ID with a prefix that says what it is:

| Prefix | What |
|---|---|
| `gr_` | a Group |
| `tb_` | a tab |
| `pn_` | a pane |

You can shorten any ID to a prefix that only one thing has (`pn_k3` for `pn_k3x9q2ma`), and name Groups and tabs by their titles instead. If what you type matches more than one thing, `hmk` lists them and does nothing.

Inside a pane, `HMK_PANE_ID` holds that pane's ID, so a script can act on the pane it runs in:

```sh
hmk split "$HMK_PANE_ID" --down -- htop
```

Commands that make something print its ID, so you can capture it:

```sh
pane=$(hmk split "$HMK_PANE_ID" -- npm run dev)
```

`hmk tab new` prints the new tab's ID; `hmk split` prints the new pane's.

For scripts, `hmk ls`, `hmk status`, `hmk sessions` and several others take `--json`.

## Look at what's running

```sh
hmk ls
```

lists every Group, its tabs and their panes, with each agent's state. `hmk ls --json` is the same for scripts: each pane's kind, title, folder and size, and for an agent its state (`starting`, `working`, `needs_input`, `idle`, `errored` or `exited`), its summary and whether you've seen it since. It also lists what's in Needs You, and permission requests waiting for an answer.

```sh
hmk status
```

says whether the daemon is running, with its Groups, remote access and linked computers. It exits with status 3 when the daemon isn't running, so a script can check:

```sh
hmk status >/dev/null || echo "Hammock isn't running"
```

`hmk focus PANE` shows a pane in the Mac app.

## Open things

```sh
hmk group new app --folder ~/Developer/app   # a Group whose tabs open in that folder
hmk tab new app --name server -- npm run dev # a tab in it, running a command
hmk split pn_k3 --down -- npm test           # a pane below another
```

Without a Group, `hmk tab new` opens the tab in the Group of the pane it's run in. Without a command, a tab or split runs your shell. `--cwd DIR` sets the folder. `hmk split` goes to the right by default; `--down`, `--left` and `--up` put it elsewhere, and `--float` floats it over the tab.

Commands run through your login shell, so they're found on the same `PATH` as in a terminal.

### Waiting for a command

`--wait` makes `hmk tab new` or `hmk split` wait until the command ends, then exit with its exit status. It's for handing someone a program in a pane and carrying on once they're done: a script can open a reviewer or an editor and pick up when it's closed.

- If the command succeeds, its pane closes.
- If it fails, its pane stays open so you can read what happened, and `hmk` exits with the command's status.
- If the pane's terminal couldn't start at all, `hmk` exits with status **125** and says why. The command never ran.

```sh
hmk tab new --name review --wait -- tig
echo "tig exited with $?"
```

`--wait` needs a command after `--`.

## Talk to panes

```sh
hmk send pn_k3 "npm test" --enter     # type a command and press Enter
hmk send pn_k3 --key ctrl-c           # press Ctrl-C
hmk send pn_k3 --key up --enter       # the previous command, again
```

`hmk send` types its text first, then each `--key` in order (`escape`, `tab`, `up`, `ctrl-c`, `f2` and so on), then Enter if you give `--enter`.

```sh
hmk read pn_k3                # the pane's screen, as text
hmk read pn_k3 --scrollback   # with its scrollback
```

```sh
hmk attach pn_k3
```

attaches this terminal to the pane, so you can use it from an SSH session or another terminal app. Press Ctrl-] to detach. The pane keeps running.

`hmk close` closes a pane, tab or Group, and `hmk reopen` brings back the last one you closed.

:::note
Run by an agent, `hmk` acts within the agent's scope: it can read your Groups and control the panes it opened, and typing into, closing or scripting other panes asks you first. See [Agents and MCP](/hammock/docs/agents/mcp/).
:::

## Agents

```sh
hmk agent claude --dir ~/Developer/app "Fix the failing test in auth"
```

starts an agent in a new tab, in that folder's Group (making one if there isn't one), with a first message. Name the agent by its short name: `claude`, `codex`, `gemini`, `opencode` and [the others Hammock knows](/hammock/docs/reference/agents/). `--group` puts it in a Group of your choosing, and `--on` on a [linked computer](/hammock/docs/remote/#linked-computers).

To find and pick up an earlier session:

```sh
hmk sessions auth                  # sessions mentioning "auth"
hmk sessions read ID refresh       # that session's messages about "refresh"
hmk resume ID                      # resume it in a new tab
hmk resume ID --in codex           # or continue it in another agent
```

`hmk switch PANE AGENT` moves a running pane's session to another agent.

An idle agent still holds its memory. `hmk sleep PANE` ends its process until it's needed, and `hmk wake PANE` starts it again. So does typing in its pane, or the apps' **Wake Agent** button. Both act on the pane they're run in when you leave out PANE. To do it automatically, see `agent-sleep` in the [config reference](/hammock/docs/reference/config/).

`hmk grant allow|always|deny ID` answers a permission request (`hmk ls --json` lists them under `grants`). Answering from a terminal is one of the things only you may do, so it waits for you to allow it in the Mac app or on your iPhone.

Hooks, scheduled tasks and MCP have their own pages: [Agent setup](/hammock/docs/agents/setup/), [Scheduled tasks](/hammock/docs/scheduled-tasks/) and [MCP](/hammock/docs/agents/mcp/).

## Browser, simulator, log, editor and diff panes

- `hmk browser URL` opens a web page in a browser pane; `hmk show FILE` shows a file (HTML, Markdown, an image, a PDF) in one beside this pane.
- `hmk sim` opens an iOS simulator in a pane; `hmk sim list` lists the devices.
- `hmk log file PATH` follows a log file in a log pane (or `macos`, `sim`, `logcat`, or `-- CMD`), and `hmk log read PANE` prints its entries, filtered.
- `hmk edit FILE:LINE` opens a file in an editor pane beside this one, with that line selected.
- `hmk diff` opens this branch's changes in a diff pane to review, like a pull request.

## Scripting

A few lines of shell and [jq](https://jqlang.org) go a long way. This lists the agents waiting for you, with their pane IDs:

```sh
hmk ls --json | jq -r '
  .groups[].tabs[].panes[]
  | select(.agent.state == "needs_input")
  | "\(.id)  \(.title)"'
```

And this runs the tests in a tab of the `app` Group, and says how they went once they finish:

```sh
#!/bin/sh
if hmk tab new app --name tests --wait -- go test ./... >/dev/null; then
  echo "Tests passed"
else
  status=$?
  if [ "$status" -eq 125 ]; then
    echo "The tests' tab couldn't start" >&2
  else
    echo "Tests failed ($status): their tab is still open" >&2
  fi
  exit "$status"
fi
```

(`>/dev/null` drops the tab ID `hmk tab new` prints.)

## The daemon

Your panes run in Hammock's daemon, `hmkd`, which keeps going when you quit the app.

```sh
hmk stop             # stop the daemon; panes keep running
hmk stop --panes     # stop it and end every pane
hmk start
hmk restart
```

An open Hammock app starts the daemon again by itself. On Linux, `hmk service install` runs it as a systemd user service, from boot.

The daemon keeps its state (its socket, database and logs) in a state directory: on a Mac `~/Library/Application Support/Hammock`, and on Linux `~/.local/state/hammock` (or `$XDG_STATE_HOME/hammock`). Set `HMK_DIR` to use another, and run a separate daemon there:

```sh
HMK_DIR=/tmp/hammock-scratch hmk start
HMK_DIR=/tmp/hammock-scratch hmk ls
```

In a pane, `hmk` goes to the pane's own daemon even with `HMK_DIR` set: unset `HMK_SOCKET` first to reach another one.
