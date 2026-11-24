---
title: Agents in Hammock
description: How Hammock recognises coding agents, what their states mean, and how to start, resume, move and rest them.
sidebar:
  order: 1
---

Run a coding agent in any Hammock pane and Hammock notices it. You see at a glance which agent is working, which needs you and which has finished, across every Group, on the Mac and on your phone. The [supported agents](/hammock/docs/reference/agents/) table says what Hammock can do with each.

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

You see them on each tab's icon in the sidebar, in the sidebar's **Needs You** and **Finished** lists, as notifications, as a count on the Dock icon, as an orange count beside Hammock's menu bar icon (whose menu lists them, opening each agent's tab when you choose it), and on your paired [iPhone](/hammock/docs/iphone/) or [Android phone](/hammock/docs/android/). Each can be turned off in Settings.

## The agent view

For most agents Hammock can show the conversation as a native, readable view instead of the terminal: your messages, the agent's replies and its tool calls. You can send messages from it, attach pictures and dictate. What you type in the message box shows on your other devices too, so you can start a message on the Mac and finish it on your phone (attachments stay on the device you added them on). As in a shell, ↑ in an empty message box brings back what you sent before, and ↓ returns to what you were typing.

Type `/` for the agent's commands, matched by any part of their name, or `@` (in Claude Code and Codex) for a file in the Group's folder. Tab takes the highlighted one; ↑ and ↓ move the highlight, after which Return takes it too. Esc puts the list away.

The gauge beside the model shows how full the agent's context is. Click it to see how many tokens are left, or to compact or clear the conversation (`/compact`, `/clear`; New Chat in Codex). A message sent while Claude Code works can wait until its turn ends: right-click it (touch and hold on a phone) and choose **Send Now** to give it to Claude straight away, as Ctrl+Enter does in its terminal.

When the agent asks for permission or asks a question, its options appear as a card: click one, or press its number (1–9). ← and → move between questions, and Esc switches to the terminal.

Switch a pane between the two with Pane ▸ Toggle Agent View (⇧⌘A). Settings ▸ Agent View ▸ Agent panes open in (`agent-default-view`) picks which one agent panes start in.

## Starting an agent

- **File ▸ New Agent Tab…** (⌥⌘N): pick the agent, the Group and the folder, and optionally a first message.
- **A default agent:** set Settings ▸ Agents ▸ Default agent (`default-agent`), and **New Claude Code Tab** (or whichever you picked) joins New Agent Tab… wherever it's offered. It starts the agent straight away, in the Group's folder, and takes ⌥⌘N; New Agent Tab… moves to ⌃⌥⌘N. Each computer has its own default, set on that computer, and a Group uses its computer's.
- **Beside a pane:** the toolbar's split buttons and Pane ▸ Split Right (and the other Split menus) offer **Agent…** and your default agent, which start it in a new pane in that pane's folder.
- **From the command line:**

  ```sh
  hmk agent claude --dir ~/Developer/app "Fix the failing login test"
  ```

  `--group` picks a Group, and `--on` a [linked computer](/hammock/docs/remote/). With no agent named (`hmk agent --dir ~/Developer/app`), it starts that computer's default agent.

### In a new worktree

When the folder is in a git repository, turn on **In a New Worktree** in New Agent Tab… and name it, or run `hmk agent --worktree fix-login`. The agent starts in a new worktree of the repository, in the repository's Group; the sheet shows the command that will run first. Hammock makes it the first of these ways the repository has:

1. A `.hammock/config` file in the repository, with `worktree-create` (a command; `{name}` is the name and `{path}` where it goes, each quoted for the shell) and `worktree-dir` (the folder new worktrees go in).
2. A recipe it already has: a `just` recipe, Makefile target (given `NAME=…`) or package.json script called `worktree`, `worktree-add` or `worktree-new`.
3. The agent's own option: Claude Code's `--worktree`, which makes it in `.claude/worktrees/` and names its branch itself.
4. `git worktree add`, on a branch of that name (a new one, or the branch if it exists), in `.claude/worktrees/`, with the git-ignored files a `.worktreeinclude` lists copied in.

A worktree of that name that's there already is used again. Recipes and commands run in your login shell in the repository, outside any agent's sandbox.

A tab shows its branch's pull request (open, merged or closed, when [`gh`](https://cli.github.com) can see it) or, in a worktree, a branch. A minute after the branch is merged or its pull request closed, a worktree's tab offers **Remove Worktree…**: in its row, its right-click menu, File, the command palette, or `hmk worktree remove`. It always asks first, showing the command that will run: the repository's `worktree-remove` command or recipe (`worktree-rm` too, given the worktree's name), else `git worktree remove`, which also deletes the branch if it's merged. Then the tabs in it close. Hammock never removes one by itself. With uncommitted changes, `git worktree remove` can drop them once you confirm; a repository's own command can't, so commit or discard them first.

```ini
# .hammock/config
worktree-create = scripts/new-worktree {name}
worktree-remove = scripts/remove-worktree {path}
```

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

## Keep your Mac awake

Turn on Settings ▸ Agents ▸ **Keep this Mac awake while agents work** (`agent-keep-awake`) and your Mac won't go to sleep while any agent is working, so a long task left running finishes. Once none is (they've finished, or are waiting for you), it sleeps as usual. The display still sleeps, and closing a laptop's lid still puts it to sleep.

## Usage limits

Hammock can show your agents' usage limits (Claude Code's five-hour and weekly ones, for example) in the sidebar: turn them on in Settings ▸ Agents ▸ Usage Limits in the Sidebar. With **Ask each agent's service for its usage** (`usage-live`), Hammock asks the agent's service directly, using the sign-in its CLI keeps on your Mac. The sign-in is sent only to that service, and never kept or changed.

## Related

- [Setting up agents](/hammock/docs/agents/setup/): hooks, the MCP server and agent settings.
- [Controlling Hammock from agents](/hammock/docs/agents/mcp/): what agents can do with your workspace.
- [Examples of controlling Hammock from agents](/hammock/docs/agents/examples/): things to ask your agent.
- [Scheduled tasks](/hammock/docs/scheduled-tasks/): agents that run on a schedule.
