---
title: Scheduled tasks
description: Run an agent's prompt or a command on a schedule, each run a new tab in its Group, from the app, hmk or an agent.
---

A scheduled task runs an agent with a prompt, or runs a command, at set times: every weekday morning, every night, every two hours. Hammock's daemon starts each run in a new tab in the task's Group, so you find the results waiting, and with an agent you can carry on the conversation where the run left off.

Runs are started by the daemon, so they happen whether or not the app is open. If the computer was asleep or off when a run was due, Hammock catches it up once when it wakes, the same day (or within one interval, for a cron schedule). Runs it couldn't catch up are noted as missed.

## Which agents can be scheduled

Claude Code is the default, and Codex and every other agent Hammock knows can be scheduled too: see the Schedules column in [Supported agents](/hammock/docs/reference/agents/). Or schedule any command, run by your login shell in the task's folder.

## Making one in the app

Choose File ▸ New Scheduled Task… (⌥⇧⌘N). To schedule what a tab is already doing, right-click it in the sidebar and choose **Schedule This…**: the folder, Group and agent are filled in, and for an agent, its session's first prompt too.

In the sheet:

- **Name**, and **Run**: an agent, or a command.
- **Prompt** (the agent's first message) or **Command**.
- **Folder**, where it runs, and **Group**, where each run's tab opens. If the Group has been closed, the next run brings it back.
- **When:** every day, weekdays, weekends, custom days or a cron expression, and the time. Cron takes five fields (minute, hour, day of month, month, weekday), in the computer's own time.
- **If still running:** what happens when a run is due and the last one hasn't finished. By default the new one is skipped; it can also stop the last one and start afresh, or run both.
- **Give up after** (agents): a run waiting on you this long is stopped. It only counts while the agent waits on you.
- **Keep the last** few runs' tabs. Older runs' tabs close as new ones open, except ones you've typed in.
- **Notify me:** when a run is ready, only about problems, or never. By default, an agent's run tells you when it's ready, and a command's only when something goes wrong.
- **Close the tab when it succeeds** (commands).
- For Claude Code, **Remembered tools**: tools allowed in every run. When a run asks permission for a tool, **Always Allow for This Schedule** in Needs You adds it here.
- For Codex, its **Sandbox** and **Approval** policy, or your own Codex settings.

**Run Now** saves the task and runs it straight away.

A run that asks you something goes to Needs You in the sidebar, like any agent.

## Making one with hmk

```sh
hmk schedule add [NAME] (--at HH:MM [--days D] | --cron EXPR) [OPTIONS] -- PROMPT|COMMAND
```

It runs in the current folder and Group unless you say otherwise. The main options:

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

`hmk schedule help` lists them all, and the [CLI reference](/hammock/docs/reference/cli/#hmk-schedule) has the rest.

A command given as one word is run as the line you wrote, so quote it: `-- 'make && make test'`. Given as several words, they're the command's arguments.

## Examples

A summary every weekday at 08:30, from Claude Code, in a Group called Briefings (`--group` names a Group you already have):

```sh
hmk schedule add "Morning summary" --at 08:30 --days weekdays \
  --dir ~/Developer/app --group Briefings \
  -- "Summarise yesterday's commits and open pull requests in this repository, then write it to summary.md and run hmk show summary.md."
```

`hmk show` puts the summary in a browser pane beside the run, ready when you sit down.

The tests every night at 01:00, with the tab closed if they pass and a notification only if they fail:

```sh
hmk schedule add "Nightly tests" --command --at 01:00 \
  --dir ~/Developer/app --close-on-success --notify problems \
  -- 'make test'
```

A Codex check every two hours on weekdays, working hours only:

```sh
hmk schedule add "Triage" --codex --cron '0 9-17/2 * * 1-5' --sandbox read-only \
  -- "Look at new issues in this repository and suggest labels."
```

## Managing them

View ▸ Scheduled Tasks (⇧⌘S) lists your tasks. Select one to see its settings and recent runs, and to **Pause** or **Resume** it, **Edit…** it, **Run Now**, **Skip Next Run** or **Delete…** it. Each run shows how it went; **Open Tab** goes to its tab, or once that's closed, **Resume Session** carries on with its agent's session in a new tab. Right-click a task for Run Now, Pause or Resume, Edit… and Delete…. Half an hour before a run is due, the sidebar's footer shows what's next; click it to open the list.

Deleting a task stops future runs. Tabs from its runs stay open.

From a terminal, `SCHEDULE` is a task's id or name:

```sh
hmk schedule                       # list them
hmk schedule show "Nightly tests"  # its settings and runs
hmk schedule edit "Nightly tests" --at 02:00
hmk schedule run "Nightly tests"   # now, in a new tab
hmk schedule pause "Nightly tests" # and hmk schedule resume
hmk schedule skip "Nightly tests"  # skip the next run; --undo to take it back
hmk schedule rm "Nightly tests"
```

In [the TUI](/hammock/docs/tui/), ⌃B @ opens the list.

## Agents and scheduled tasks

Agents can make and manage scheduled tasks with the MCP tools [`schedule_save`](/hammock/docs/reference/mcp/#schedule_save), [`schedule_list`](/hammock/docs/reference/mcp/#schedule_list), `schedule_run`, `schedule_pause` and `schedule_delete`, or with `hmk schedule` from their shell.

You stay in charge:

- When an agent adds or changes a task, Hammock asks you first, showing what it would run and when.
- Deleting a task always asks you.
- An agent may run, pause or resume a task it made itself (with your leave) without asking again. Doing that to any other task asks you first.
