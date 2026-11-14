---
title: Agents in Hammock
description: How Hammock recognises coding agents, what their states mean, and how to start, resume, move and rest them.
sidebar:
  order: 1
---

Run a coding agent in any Hammock pane and Hammock notices it. You see at a glance which agent is working, which needs you and which has finished, across every Group, on the Mac and on your iPhone. The [supported agents](/hammock/docs/reference/agents/) table says what Hammock can do with each.

## How Hammock recognises an agent

- **Hooks.** With Hammock's hooks installed, the agent tells Hammock each step of its turn, and which session the pane is running. See [Setting up agents](/hammock/docs/agents/setup/).
- **Its process and screen.** Without hooks, Hammock looks for a known agent's process in the pane and reads its screen for signs of working or asking.

## States

| State | What it means |
|---|---|
| Starting | Hammock has started the agent, and it isn't at its prompt yet. |
| Working | It's working on your prompt: thinking, running tools. |
| Needs input | It's waiting for you: a permission prompt, or a question. |
| Finished | Its turn is done, and it's waiting for your next message. |
| Errored | An error, such as a dropped connection or a rate limit, ended its turn. |
| Exited | The agent quit. |
| Asleep | Hammock ended its process to free memory. See [Sleep and wake](#sleep-and-wake). |

You see them on each tab's icon in the sidebar, in the sidebar's **Needs You** and **Finished** lists, as notifications, as a count on the Dock icon, and on your [paired iPhone](/hammock/docs/iphone/). Each can be turned off in Settings.

## The agent view

For most agents Hammock can show the conversation as a native, readable view instead of the terminal: your messages, the agent's replies and its tool calls. You can send messages from it, attach pictures and dictate.

Switch a pane between the two with Pane ▸ Toggle Agent View (⇧⌘A). Settings ▸ Agent View ▸ Agent panes open in (`agent-default-view`) picks which one agent panes start in.

## Starting an agent

- **File ▸ New Agent Tab…** (⌥⌘N): pick the agent, the Group and the folder, and optionally a first message.
- **From the command line:**

  ```sh
  hmk agent claude --dir ~/Developer/app "Fix the failing login test"
  ```

  `--group` picks a Group, and `--on` a [linked computer](/hammock/docs/remote/).

## Resuming sessions

Agents keep running when you quit Hammock, update it or it crashes. After your Mac restarts, Hammock resumes each agent's session, if hooks told it which session it was.

To pick up an older session:

- **File ▸ Find Session…** (⇧⌘O) searches past sessions on this Mac and resumes the one you pick in a new tab. **Resume a Session from This Group…** (⌥⌘O) looks only in the Group's folders.
- **`hmk sessions [QUERY]`** lists matching sessions, and **`hmk resume ID`** resumes one.

**File ▸ Reopen Closed Tab or Pane** (⇧⌘T) brings back the last tab or pane you closed, and its agent resumes its session.

## Continue in another agent

Right-click a Claude Code pane and choose **Continue in Codex** (or a Codex one, **Continue in Claude**): Hammock converts the conversation and starts the other agent with it, in the same pane. From the command line, `hmk switch PANE codex`.

## Sleep and wake

Hammock can end an idle agent's process to free its memory, and keep its pane ready to pick up where it left off.

- **By itself:** Settings ▸ Agents ▸ Put idle agents to sleep (`agent-sleep`), after 3 to 48 hours idle. It's off unless you turn it on.
- **By hand:** Pane ▸ Put Agent to Sleep, or `hmk sleep [PANE]`.

Only an agent that has finished its turn sleeps, never one working or waiting for you. To wake it, click **Wake**, type in the pane, or run `hmk wake [PANE]`: its session resumes in a new process.

## Usage limits

Hammock can show your agents' usage limits (Claude Code's five-hour and weekly ones, for example) in the sidebar: turn them on in Settings ▸ Agents ▸ Usage Limits in the Sidebar. With **Ask each agent's service for its usage** (`usage-live`), Hammock asks the agent's service directly, using the sign-in its CLI keeps on your Mac. The sign-in is sent only to that service, and never kept or changed.

## Related

- [Setting up agents](/hammock/docs/agents/setup/): hooks, the MCP server and agent settings.
- [Controlling Hammock from agents](/hammock/docs/agents/mcp/): what agents can do with your workspace.
- [Examples of controlling Hammock from agents](/hammock/docs/agents/examples/): things to ask your agent.
- [Scheduled tasks](/hammock/docs/scheduled-tasks/): agents that run on a schedule.
