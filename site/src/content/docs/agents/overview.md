---
title: Agents in Hammock
description: How Hammock recognises coding agents, what their states mean, and how to start, resume, move and rest them.
sidebar:
  order: 1
---

Run a coding agent in any Hammock pane and Hammock notices it. You see at a glance which agent is working, which needs you and which has finished, across every Group, on the Mac and on your iPhone. Hammock knows Claude Code, Codex and many others: the [supported agents](/hammock/docs/reference/agents/) table says what it can do with each.

## How Hammock recognises an agent

Hammock has two ways of knowing what an agent is doing.

- **Hooks.** With Hammock's hooks installed, the agent tells Hammock at each point of its turn: when it starts, when you send it a prompt, when it uses a tool, when it asks for permission, when it finishes and when it quits. Hooks also say which session the pane is running, which the agent view and resuming use. [Setting up agents](/hammock/docs/agents/setup/) explains how to install them.
- **Its process and screen.** Without hooks, Hammock looks for a known agent's process in the pane, then reads the bottom of the pane's screen for the agent's own signs of working or asking (such as "esc to interrupt").

Hooks always win: the screen is only read for panes that haven't sent a hook event lately. An agent Hammock doesn't know isn't recognised.

## States

| State | What it means |
|---|---|
| Starting | Hammock has started the agent, and it isn't at its prompt yet. |
| Working | It's working on your prompt: thinking, running tools. |
| Needs input | It's waiting for you: a permission prompt, or a question. |
| Finished | It's at its prompt, waiting for your next message: its turn is done. |
| Errored | An error, such as a dropped connection or a rate limit, ended its turn. |
| Exited | The agent quit. |
| Asleep | Hammock ended its process to free memory. See [Sleep and wake](#sleep-and-wake). |

You see them:

- **In the sidebar**, on each tab's icon (or at the end of its row, with `tab-status = trailing`).
- **In the sidebar's Needs You list**, at the bottom: agents waiting for you, permission requests from agents, and errors. Below it, **Finished** lists agents that have finished and that you haven't looked at yet. Turn that list off in Settings ▸ Appearance ▸ Show finished agents.
- **As notifications**, when an agent needs your input, asks permission, finishes or hits an error. Each can be turned off in Settings ▸ Notifications.
- **On the Dock icon**, a count of the agents waiting for you (Settings ▸ Notifications ▸ Show a count on the Dock icon).
- **On your iPhone**, in the app and as notifications, when it's [paired](/hammock/docs/iphone/).

## The agent view

For most agents Hammock can show the conversation as a native, readable view instead of the terminal: your messages, the agent's replies and its tool calls. You can send messages from it, attach pictures and dictate.

Switch a pane between the two with Pane ▸ Toggle Agent View (⇧⌘A), or Show Agent View and Show Terminal in the command palette. The agent keeps running in its terminal either way.

Which one agent panes open in is Settings ▸ Agent View ▸ Agent panes open in (`agent-default-view`). Unless you change it, they open in the terminal on the Mac and in the agent view on the iPhone.

## Starting an agent

- **File ▸ New Agent Tab…** (⌥⌘N) opens Start an Agent: pick the agent, the Group and the folder, and optionally a first message. It starts in a new tab, in the folder's login shell, as if you'd typed the agent's command yourself. It's also on a Group's right-click menu and in the command palette.
- **From the command line:**

  ```sh
  hmk agent claude --dir ~/Developer/app "Fix the failing login test"
  ```

  This starts Claude Code in a new tab in that folder, in the folder's Group (or a new one), with that first message. `--group` picks a Group, and `--on` a [linked computer](/hammock/docs/remote/).

An agent that takes no first message on its command line is started bare, and Hammock types the message once it's at its prompt (*typed in* in the [supported agents](/hammock/docs/reference/agents/) table).

## Resuming sessions

Agents keep running when you quit Hammock, update it or it crashes. After your Mac restarts, Hammock starts each agent pane again and resumes its session, with the options it was started with, provided it knows the session (hooks tell it) and the agent hadn't quit.

To pick up an older session:

- **File ▸ Find Session…** (⇧⌘O) searches past sessions on this Mac by title, folder and what was said, and resumes the one you pick in a new tab. **Resume a Session from This Group…** (⌥⌘O) looks only in the Group's folders.
- **`hmk sessions [QUERY]`** lists matching sessions, and `hmk sessions read ID` shows a session's messages.
- **`hmk resume ID`** resumes one in a new tab in its folder's Group.

**File ▸ Reopen Closed Tab or Pane** (⇧⌘T), or `hmk reopen`, brings back the last tab or pane you closed, and its agent resumes its session.

## Continue in another agent

A Claude Code conversation can carry on in Codex, and a Codex one in Claude Code. Right-click the pane and choose **Continue in Codex** (or **Continue in Claude**): Hammock stops the agent, converts its conversation and starts the other agent with it, in the same pane. From the command line, `hmk switch PANE codex`, or `hmk resume ID --in codex` for a past session.

Only Claude Code and Codex can convert to each other so far.

## Sleep and wake

An idle agent still holds its memory. Hammock can end its process and keep its pane as it was, ready to pick up where it left off.

- **By itself:** Settings ▸ Agents ▸ Put idle agents to sleep (`agent-sleep`) sleeps agents idle for 3, 6, 12, 24 or 48 hours. It's off unless you turn it on.
- **By hand:** Pane ▸ Put Agent to Sleep, the pane's right-click menu, or `hmk sleep [PANE]`.

Only an agent at rest sleeps: one that finished its turn (or ended it with an error), with nothing still running in its pane, and whose session can be resumed. One working or waiting for you never does, nor a scheduled task's run.

A sleeping pane is dimmed under a **Wake** button. Clicking it, typing in the pane, sending its agent a message, or `hmk wake [PANE]` resumes the session in a new process, in the same folder with the same options. What you'd typed and not sent is typed in again. Just looking at the pane doesn't wake it. A sleeping pane stays asleep when your Mac restarts.

## Usage limits

Hammock can show your agents' usage limits (Claude Code's five-hour and weekly ones, for example) in the sidebar. Turn them on per agent in Settings ▸ Agents ▸ Usage Limits in the Sidebar.

Claude Code's and Codex's are read from what the agents keep on disk. With **Ask each agent's service for its usage** (`usage-live`), Hammock also asks each agent's own service every 5 minutes, using the sign-in the agent's CLI keeps on your Mac. The sign-in is used only for that request, sent only to that service, and never kept or changed. The [supported agents](/hammock/docs/reference/agents/) table says which agents' limits can be shown.

## Related

- [Setting up agents](/hammock/docs/agents/setup/): hooks, the MCP server and agent settings.
- [Controlling Hammock from agents](/hammock/docs/agents/mcp/): what agents can do with your workspace.
- [Agent recipes](/hammock/docs/agents/recipes/): things to ask your agent.
- [Scheduled tasks](/hammock/docs/scheduled-tasks/): agents that run on a schedule.
