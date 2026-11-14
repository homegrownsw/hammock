---
title: Scheduled tasks
description: Run an agent's prompt or a command on a schedule, each run a new tab in its Group, from the app, hmk or an agent.
---

A scheduled task runs an agent with a prompt, or a command, at set times: every weekday morning, every night, every two hours. Each run opens a new tab in the task's Group, so the results are waiting for you, and you can carry on an agent's conversation where the run left off.

Runs happen whether or not the app is open. A run missed while the computer was asleep or off is caught up once when it wakes.

## Which agents can be scheduled

Claude Code by default, or any agent with a tick in the Schedules column of [Supported agents](/hammock/docs/reference/agents/), or any command, run by your login shell in the task's folder.

## Making one in the app

Choose File ▸ New Scheduled Task… (⌥⇧⌘N), or right-click a tab in the sidebar and choose **Schedule This…** to start from what it's doing. Besides the prompt or command and when to run it, the sheet sets:

- **Folder**, where it runs, and **Group**, where its tabs open.
- **If still running:** skip the new run (the default), stop the last one, or run both.
- **Give up after** (agents): stop a run that's waited on you this long.
- **Keep the last** few runs' tabs; older ones close, unless you've typed in them.
- **Notify me:** when a run is ready, only about problems, or never.
- For Claude Code, **Remembered tools**, allowed in every run: **Always Allow for This Schedule** in Needs You adds to them. For Codex, its sandbox and approval policy.

A run that asks you something goes to Needs You, like any agent.

## Making one with hmk

```sh
hmk schedule add [NAME] (--at HH:MM [--days D] | --cron EXPR) [OPTIONS] -- PROMPT|COMMAND
```

It runs in the current folder and Group unless you give `--dir` or `--group`. Quote a command with shell syntax as one word: `-- 'make && make test'`.

| Option | What it does |
|---|---|
| `--at HH:MM` | The time of day. |
| `--days D` | With `--at`: `daily` (the default), `weekdays`, `weekends`, or days such as `mon,wed,fri`. |
| `--cron EXPR` | A five-field cron expression instead of `--at`. |
| `--claude`, `--codex`, `--AGENT` | Which agent runs it (Claude Code by default). `AGENT` is the agent's id, such as `--gemini` or `--cursor`. |
| `--command` | Run a command instead of an agent. |
| `--dir DIR`, `--group GROUP`, `--name NAME` | Where it runs, and where its tabs open. |
| `--notify ready\|problems\|never\|default` | When to tell you. |
| `--overlap skip\|stop\|both` | If the last run is still going. |
| `--give-up MINUTES`, `--keep N` | Stop a run that waits on you this long; how many runs' tabs to keep. |
| `--close-on-success`, `--keep-open` | Commands: close the tab after a clean exit, or don't. |
| `--allow RULE`, `--no-allow` | Claude Code: tools allowed in every run, as `--allowedTools` rules. |
| `--sandbox MODE`, `--approval POLICY` | Codex: its `--sandbox` and `--ask-for-approval`. |
| `--on COMPUTER` | Add it on a [linked computer](/hammock/docs/remote/). |

## Examples

A summary every weekday at 08:30, from Claude Code, shown in a browser pane beside the run:

```sh
hmk schedule add "Morning summary" --at 08:30 --days weekdays \
  --dir ~/Developer/app --group Briefings \
  -- "Summarise yesterday's commits and open pull requests in this repository, then write it to summary.md and run hmk show summary.md."
```

The tests every night at 01:00, closing the tab if they pass and notifying you only if they fail:

```sh
hmk schedule add "Nightly tests" --command --at 01:00 \
  --dir ~/Developer/app --close-on-success --notify problems \
  -- 'make test'
```

A Codex check every two hours in working hours on weekdays:

```sh
hmk schedule add "Triage" --codex --cron '0 9-17/2 * * 1-5' --sandbox read-only \
  -- "Look at new issues in this repository and suggest labels."
```

## Managing them

View ▸ Scheduled Tasks (⇧⌘S) lists your tasks, and ⌃B @ in [the TUI](/hammock/docs/tui/). Select one to pause, edit, run, skip or delete it, and to see its recent runs: **Open Tab** goes to a run's tab, or **Resume Session** carries on its agent's session once the tab's closed. Deleting a task leaves its runs' tabs open.

```sh
hmk schedule                       # list them
hmk schedule run "Nightly tests"   # now, in a new tab
hmk schedule pause "Nightly tests" # and hmk schedule resume
```

## Agents and scheduled tasks

Agents can make and manage tasks with MCP tools such as [`schedule_save`](/hammock/docs/reference/mcp/#schedule_save) and [`schedule_list`](/hammock/docs/reference/mcp/#schedule_list), or `hmk schedule`. Hammock asks you before an agent adds, changes or deletes a task, or runs or pauses one it didn't make.
