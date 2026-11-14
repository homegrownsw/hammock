---
title: Controlling Hammock from agents
description: What Hammock's MCP server lets an agent do in your workspace, what it has to ask you for, and how you answer.
sidebar:
  order: 3
---

Your agents can work in Hammock as you do: run a dev server in a pane of its own, check the page in a browser, tap through your app in a simulator, show you a diff and answer your comments on it. They do it through `hmk mcp`, Hammock's MCP server, or the `hmk` command in their shell. [Setting up agents](/hammock/docs/agents/setup/#registering-the-mcp-server) explains how to register the server.

## What `hmk mcp` gives an agent

The [MCP reference](/hammock/docs/reference/mcp/) lists every tool and its parameters, in these groups: [panes](/hammock/docs/reference/mcp/#panes), [browser panes](/hammock/docs/reference/mcp/#browser-panes), [simulator panes](/hammock/docs/reference/mcp/#simulator-panes), [log panes](/hammock/docs/reference/mcp/#log-panes), [editor panes](/hammock/docs/reference/mcp/#editor-panes), [diff panes and reviews](/hammock/docs/reference/mcp/#diff-panes-and-reviews), [agent sessions](/hammock/docs/reference/mcp/#agent-sessions) and [scheduled tasks](/hammock/docs/reference/mcp/#scheduled-tasks). [Examples of controlling Hammock from agents](/hammock/docs/agents/examples/) shows them at work.

## What agents may do

Hammock decides what an agent may do from the pane it runs in, whether it uses MCP tools or `hmk` commands.

- **Read your Groups.** An agent can read every pane in your own Groups and on your [linked computers](/hammock/docs/remote/), but not Groups shared with you, unless you allow it.
- **Fully control the panes it opens**, and panes those started, without asking.
- **Ask before touching your other panes:** typing into, interrupting or closing them, driving their browsers or simulators, or acting on a linked computer's pane.
- **Ask before saving or deleting a scheduled task.** Running or pausing one asks unless the agent saved it.
- **Edit files, not your editor.** It points at lines in an editor pane, but changes files on disk, and the pane reloads them.
- **Mark review threads addressed, not resolved.** Resolving them is yours.
- **Leave the rest to you.** Pairing, sharing, linking computers and remote access aren't available to agents, and an agent can't answer a permission request.

To use a browser or simulator pane yourself, click **Take Over** on its agent badge: agents can look at it, but not act on it, until you hand it back. `hmk audit` shows what agents have done recently.

:::caution
This is a guardrail, not a sandbox. It stops an agent wandering into panes you didn't give it, but an agent can still run anything you can from its own shell. For a real boundary, use your agent's own sandbox.
:::

## Answering an agent's request

Answer wherever is handiest; the agent waits until you do.

- **In the Mac app**, in the sidebar's **Needs You** list: **Allow Once**, **Always in Group** (where offered), or **Deny**.
- **On your iPhone**, in the app or from the notification.
- **From a terminal:** `hmk ls` lists pending requests, and `hmk grant allow|always|deny ID` answers one.

## Using `hmk` from the agent's shell

Agents can also run `hmk` directly, for example to hand you a full-screen program and carry on when you quit it:

```sh
hmk tab new --name Git --wait -- lazygit
hmk split "$HMK_PANE_ID" --wait -- vim notes.md
```

See the [CLI reference](/hammock/docs/reference/cli/).

## Turning tools off

Turn off any kind of pane you don't want in **Settings ▸ Tools** (`browser-enabled = false` and so on, in the [config file](/hammock/docs/configuration/)). Its tools then disappear from the MCP server too.

## Untrusted content

Pane output, logs and web pages come from programs and websites, not from you. Hammock marks them as untrusted and tells agents not to follow instructions in them, but a page or program can still try to trick an agent.
