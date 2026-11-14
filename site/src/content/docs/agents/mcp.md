---
title: Controlling Hammock from agents
description: What Hammock's MCP server lets an agent do in your workspace, what it has to ask you for, and how you answer.
sidebar:
  order: 3
---

Your agents can work in Hammock as you do: run a dev server in a pane of its own, check the page in a browser, tap through your app in a simulator, show you a diff and answer your comments on it. They do it through `hmk mcp`, Hammock's MCP server, or the `hmk` command in their shell. [Setting up agents](/hammock/docs/agents/setup/#registering-the-mcp-server) explains how to register the server.

## What `hmk mcp` gives an agent

The [MCP reference](/hammock/docs/reference/mcp/) lists every tool and its parameters. In short:

- **[Panes](/hammock/docs/reference/mcp/#panes):** list your panes, read what's on their screens, start commands in new panes, type into them, wait for output, arrange and close them.
- **[Browser panes](/hammock/docs/reference/mcp/#browser-panes):** open a real Chromium page you can see too, read it as an outline, click, type, run JavaScript, and read its console and network requests. `show_output` shows you a file or page the agent made.
- **[Simulator panes](/hammock/docs/reference/mcp/#simulator-panes):** open an iOS simulator, install and launch apps, tap, swipe, type, take screenshots and read its log.
- **[Log panes](/hammock/docs/reference/mcp/#log-panes):** follow a file, the Mac's or a simulator's log, Android's logcat or a command's output, and read it filtered.
- **[Editor panes](/hammock/docs/reference/mcp/#editor-panes):** point you at code with lines selected, and read what you have open and selected.
- **[Diff panes and reviews](/hammock/docs/reference/mcp/#diff-panes-and-reviews):** show you a diff to review, like a pull request, and reply to your comments on it.
- **[Agent sessions](/hammock/docs/reference/mcp/#agent-sessions):** search and read past agent sessions on your Mac.
- **[Scheduled tasks](/hammock/docs/reference/mcp/#scheduled-tasks):** list, save, run and pause [scheduled tasks](/hammock/docs/scheduled-tasks/).

The server works only for agents running in a Hammock pane. Outside one, it offers no tools.

## What agents may do

Hammock decides what an agent may do from the pane it runs in, not from what the agent says about itself. The same rules apply whether it uses MCP tools or `hmk` commands.

- **Read your Groups.** An agent can list and read every pane in your own Groups, and on your [linked computers](/hammock/docs/remote/). Groups other people have shared with you are left out, unless you allow it.
- **Fully control the panes it opens.** It can type into, interrupt, script, rearrange and close panes it started (and panes those started), without asking.
- **Ask before touching your other panes.** Typing into, interrupting or closing a pane it didn't open, driving a browser or simulator pane it didn't open, or acting on a linked computer's pane, asks you first.
- **Ask before saving a scheduled task.** Saving or deleting a schedule always asks you. Running or pausing one asks unless the agent saved it.
- **Edit files, not your editor.** An agent can point at lines in an editor pane, but never types into it or saves it: it changes files on disk, and the pane reloads them.
- **Mark review threads addressed, not resolved.** It can reply to your comments and mark threads addressed. Resolving them is yours.
- **Leave the rest to you.** Pairing devices, sharing Groups, linking computers, remote access and the like aren't available to agents at all, and an agent can't answer a permission request, its own or another's.

When you're using a browser or simulator pane yourself, click **Take Over** on its agent badge (or Take Over from Agents on its right-click menu): agents can still look at it, but can't act on it until you hand it back.

What agents do is audited: `hmk audit` shows the most recent actions.

:::caution
This is a guardrail, not a sandbox. It stops an agent wandering into panes you didn't give it, but an agent can still run anything you can from its own shell. For a real boundary, use your agent's own sandbox.
:::

## Answering an agent's request

When an agent asks, the request says what it wants to do and to which pane, with the agent's own reason, marked as the agent's words. Answer it wherever is handiest:

- **In the Mac app**, in the sidebar's **Needs You** list: **Allow Once**, **Always in Group** (where offered: allow this agent to do the same in this Group from now on), or **Deny**.
- **On your iPhone**, in the app or straight from the notification: **Allow** or **Deny**.
- **From a terminal:** `hmk ls` lists pending requests with their ids, and `hmk grant allow ID`, `hmk grant always ID` or `hmk grant deny ID` answers one.

The agent waits for your answer. A request nobody answers is eventually denied.

## Using `hmk` from the agent's shell

Agents can also run `hmk` directly. Hammock's MCP server suggests this to agents for one thing in particular: handing you a full-screen program, such as a diff reviewer or an editor, and carrying on when you quit it. The agent runs it as a background shell command with `--wait`:

```sh
hmk tab new --name Git --wait -- lazygit
hmk split "$HMK_PANE_ID" --wait -- vim notes.md
```

The program opens in a new tab, or beside the agent's pane. When it exits cleanly the pane closes and the command returns with its exit status, so the agent is woken without a time limit and without blocking. If the pane's terminal couldn't start, the command exits with 125 and says why: the program never ran, so you never saw it.

`hmk help` lists every command, and the [CLI reference](/hammock/docs/reference/cli/) explains them.

## Turning tools off

If you already do one of these your own way, turn it off in **Settings ▸ Tools**: Browser, Simulator, Editor, Diff Viewer or Log Viewer (`browser-enabled = false` and so on, in the [config file](/hammock/docs/configuration/)). Hammock then stops offering that kind of pane, in its menus and to agents: its tools disappear from the MCP server, and the server's instructions stop mentioning it.

## Untrusted content

What's on a pane's screen, in a log, in a page's text or console, or in a simulator's log, comes from programs and websites, not from you. Hammock's tools mark it as untrusted, and the server tells agents never to follow instructions found in it. A page or program can still try to trick an agent, which is one more reason the permission rules above exist.
