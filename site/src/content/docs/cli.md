---
title: The CLI
description: A guide to hmk, Hammock's command line, by what you want to do.
---

`hmk` is Hammock's command line. It sees and controls the same Groups, tabs and panes as the Mac app, so you can script your workspace, check on agents from a shell, or attach to a pane from another terminal. This page is a tour; every command and option is in the [CLI reference](/hammock/docs/reference/cli/), or `hmk help`.

## Where it is

On a Mac, setup puts `hmk` on your `PATH`; if you skipped that, use **Install** under Settings ▸ General ▸ Command-line tool. On Linux it comes with the daemon: see [Installing on Linux](/hammock/docs/install/linux/).

## IDs and names

Groups, tabs and panes have IDs starting `gr_`, `tb_` and `pn_`. Any unique prefix will do (`pn_k3` for `pn_k3x9q2ma`), and Groups and tabs can be named by their titles. Inside a pane, `HMK_PANE_ID` holds that pane's ID. Commands that make something print its ID:

```sh
pane=$(hmk split "$HMK_PANE_ID" --down -- npm run dev)
```

For scripts, `hmk ls`, `hmk status`, `hmk sessions` and others take `--json`.

## Look at what's running

`hmk ls` lists every Group, tab and pane, with each agent's state. `hmk status` says whether the daemon is running, and exits with status 3 if it isn't. `hmk focus PANE` shows a pane in the Mac app.

## Open things

```sh
hmk group new app --folder ~/Developer/app   # a Group whose tabs open in that folder
hmk tab new app --name server -- npm run dev # a tab in it, running a command
hmk split pn_k3 --down -- npm test           # a pane below another
```

Without a command, a tab or split runs your shell. `--float` floats a split over the tab.

### Waiting for a command

`--wait` makes `hmk tab new` or `hmk split` wait for the command to end and exit with its status, so a script can hand someone a program (a reviewer, an editor) and carry on when they're done. On success the pane closes; on failure it stays open. Status **125** means the pane couldn't start, so the command never ran.

```sh
hmk tab new --name review --wait -- tig
```

## Talk to panes

```sh
hmk send pn_k3 "npm test" --enter     # type a command and press Enter
hmk send pn_k3 --key ctrl-c           # press Ctrl-C
hmk read pn_k3 --scrollback           # the pane's text
hmk attach pn_k3                      # use it from this terminal; Ctrl-] detaches
```

`hmk close` closes a pane, tab or Group, and `hmk reopen` brings back the last one you closed.

:::note
Run by an agent, `hmk` can read your Groups and control the panes it opened; typing into, closing or scripting other panes asks you first. See [Agents and MCP](/hammock/docs/agents/mcp/).
:::

## Agents

```sh
hmk agent claude --dir ~/Developer/app "Fix the failing test in auth"
```

starts an agent in a new tab in that folder's Group, with a first message. Use its short name: `claude`, `codex`, `gemini` and [the others Hammock knows](/hammock/docs/reference/agents/). `--on` starts it on a [linked computer](/hammock/docs/remote/#linked-computers).

```sh
hmk sessions auth                  # earlier sessions mentioning "auth"
hmk resume ID --in codex           # resume one, here in another agent
```

`hmk sleep PANE` ends an idle agent's process to free its memory, and `hmk wake PANE` (or typing in its pane) starts it again; `agent-sleep` in the [config reference](/hammock/docs/reference/config/) does it automatically. `hmk grant` answers a permission request, once you allow it in the Mac app or on your iPhone.

Hooks, scheduled tasks and MCP have their own pages: [Agent setup](/hammock/docs/agents/setup/), [Scheduled tasks](/hammock/docs/scheduled-tasks/) and [MCP](/hammock/docs/agents/mcp/).

## Browser, simulator, log, editor and diff panes

`hmk browser`, `hmk show`, `hmk sim`, `hmk log`, `hmk edit` and `hmk diff` open the [other kinds of pane](/hammock/docs/panes/).

## Scripting

A few lines of shell and [jq](https://jqlang.org) go a long way. This lists the agents waiting for you:

```sh
hmk ls --json | jq -r '
  .groups[].tabs[].panes[]
  | select(.agent.state == "needs_input")
  | "\(.id)  \(.title)"'
```

## The daemon

Your panes run in Hammock's daemon, `hmkd`, which keeps going when you quit the app.

```sh
hmk stop             # stop the daemon; panes keep running
hmk stop --panes     # stop it and end every pane
hmk restart
```

An open Hammock app starts it again by itself. On Linux, `hmk service install` runs it as a systemd user service.
