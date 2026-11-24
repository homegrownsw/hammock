---
title: Automations
description: Run an agent's prompt or a command on a schedule, once, or when something happens, each run a new tab in its Group, from the app, hmk or an agent.
---

An automation runs an agent with a prompt, or a command, at set times: every weekday morning, every night, every two hours, or once at a date and time. It can also run when something happens: CI failing on your pull request, an agent finishing, a calendar event coming up (see [Triggers](#triggers)). Each run opens a new tab in the automation's Group, so the results are waiting for you, and you can carry on an agent's conversation where the run left off.

Runs happen whether or not the app is open. A run missed while the computer was asleep or off is caught up once when it wakes.

## Which agents can run

Claude Code by default, or any agent with a tick in the Schedules column of [Supported agents](/hammock/docs/reference/agents/), or any command, run by your login shell in the automation's folder.

## Making one in the app

Choose File ▸ New Automation… (⌥⇧⌘N), or right-click a tab in the sidebar and choose **Automate This…** to start from what it's doing. Besides the prompt or command and when to run it, the sheet sets:

- **Folder**, where it runs, and **Group**, where its tabs open.
- **Runs** on a schedule, once (at a date and time), or only on its triggers, and its **Triggers**.
- **If still running:** skip the new run (the default), stop the last one, or run both.
- **Give up after** (agents): stop a run that's waited on you this long.
- **Keep the last** few runs' tabs; older ones close, unless you've typed in them.
- **Notify me:** when a run is ready, only about problems, or never.
- For Claude Code, **Remembered tools**, allowed in every run: **Always Allow for This Automation** in Needs You adds to them (a command by its first word or two, but never one that runs whatever follows it, like `bash -c`, `python3 -c`, `sudo`, `find` or `awk`: allow those once). For Codex, its sandbox and approval policy.

A run that asks you something goes to Needs You, like any agent.

## Making one with hmk

```sh
hmk automation add [NAME] (--at HH:MM [--days D] | --cron EXPR) [OPTIONS] -- PROMPT|COMMAND
```

It runs in the current folder and Group unless you give `--dir` or `--group`. Quote a command with shell syntax as one word: `-- 'make && make test'`.

| Option | What it does |
|---|---|
| `--at HH:MM` | The time of day. |
| `--days D` | With `--at`: `daily` (the default), `weekdays`, `weekends`, or days such as `mon,wed,fri`. |
| `--cron EXPR` | A five-field cron expression instead of `--at`. |
| `--once 'YYYY-MM-DD HH:MM'` | Run once, then, instead of on a schedule. |
| `--trigger T` | Run when something happens ([Triggers](#triggers)). Repeat it for more. Without `--at`, `--cron` or `--once`, it runs only on its triggers. |
| `--events-only`, `--no-triggers` | Run only on its triggers; drop its triggers. |
| `--cooldown SECONDS` | The least time between runs its triggers start (60 by default; 0 for none). |
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
hmk automation add "Morning summary" --at 08:30 --days weekdays \
  --dir ~/Developer/app --group Briefings \
  -- "Summarise yesterday's commits and open pull requests in this repository, then write it to summary.md and run hmk show summary.md."
```

The tests every night at 01:00, closing the tab if they pass and notifying you only if they fail:

```sh
hmk automation add "Nightly tests" --command --at 01:00 \
  --dir ~/Developer/app --close-on-success --notify problems \
  -- 'make test'
```

A Codex check every two hours in working hours on weekdays:

```sh
hmk automation add "Triage" --codex --cron '0 9-17/2 * * 1-5' --sandbox read-only \
  -- "Look at new issues in this repository and suggest labels."
```

## Triggers

A trigger starts a run when something happens, as well as at the automation's times, or instead of them (**Only on triggers**). In the app, add them under **Triggers** in the automation's sheet. With `hmk` or an agent, write each one as below.

| Trigger | `--trigger` | Runs when |
|---|---|---|
| GitHub | `github:ci-failed` | Checks fail on your open pull request, or on the folder's branch. |
| | `github:review-requested` | Your review is requested. |
| | `github:reviewed` | Someone reviews or comments on your pull request. |
| | `github:label=NAME` | An open issue or pull request has the label. |
| | `github:assigned` | An issue or pull request is assigned to you. |
| | `github:merged` | Your pull request is merged. |
| Agent | `agent:finished`, `agent:needs-you`, `agent:finished,needs-you` | An agent finishes, or needs you. Add `@GROUP` or `@PANE` for one Group or pane. |
| Command | `exit`, `exit@GROUP`, `exit@PANE` | A pane's command exits with an error. |
| Log | `log:REGEX@PANE` | A line in a [log pane](/hammock/docs/panes/) matches. It ignores case unless the pattern has a capital. |
| Files | `files`, `files:GLOB`, `files:GLOB@DIR` | Files change in the automation's folder (or `DIR`), once they've settled. `*.go` matches names anywhere; `src/**/*.ts` matches paths. In a git repository, what `.gitignore` leaves out isn't watched; elsewhere, `node_modules` and build folders aren't. |
| Calendar | `calendar:MINUTES[,title=TEXT][,calendar=NAME][,attendees]` | Minutes before an event whose title contains the text, in that calendar, with other people. Mac only. |
| Webhook | `webhook` | Its [webhook](#webhooks) is called. |

GitHub triggers watch the folder's GitHub repository (`gh repo set-default`'s, else the `upstream` remote's, else `origin`'s) with the [GitHub CLI](https://cli.github.com), every couple of minutes, so install `gh` and run `gh auth login` first. Calendar triggers read your calendars in the Hammock app, which asks for access the first time; runs still start while the app is closed, for events it has already read. Nothing that's already so when you add a trigger starts a run. What happens in an automation's run, or in panes a run opened, never triggers an automation, so automations can't set each other off in a loop.

**What the run gets.** An agent's prompt is followed by what happened: the pull request, the failing checks, the log line, the files, the event, the webhook's body. Put `{{event}}` in the prompt to place it there instead. A command gets it in environment variables: `HMK_EVENT` (the kind), `HMK_EVENT_SUMMARY`, `HMK_EVENT_BODY`, and `HMK_EVENT_<DETAIL>` for each detail, such as `HMK_EVENT_NUMBER` or `HMK_EVENT_URL`. The run's record says what started it, such as **CI failed on #42**.

:::caution
What a trigger passes on is written by other people: a pull request's title, a comment, a log line, a webhook's body. Hammock gives it to the agent marked as data, not instructions, but write the prompt so the automation does nothing harmful whatever that text says, and keep the tools it may use to what it needs.
:::

**Bursts.** Runs started by triggers are at least a minute apart, or the automation's **At most every** (which can be no limit). Events in between wait, and start one run together, which gets all of them. When the automation skips runs while one is going, they wait for it to end instead.

### Webhooks

A webhook trigger gives the automation a secret URL on [remote access](/hammock/docs/remote/)'s listener, so only the networks you chose in Settings ▸ Devices reach it. Choose **Copy Webhook URL** on the automation (or `hmk automation webhook NAME`), and `POST` to it. The body, up to 64 KB, is what happened:

```sh
curl -k -X POST --data @payload.json https://100.64.0.2:7433/hook/SECRET
```

`-k` is needed because the certificate is Hammock's own. **New URL…** in the automation's sheet (or `hmk automation webhook NAME --new`) makes a new secret, and the old URL stops working.

To call it from the internet, such as from a GitHub repository's webhooks, put [Tailscale Funnel](https://tailscale.com/kb/1223/funnel) in front of it with your Mac's Tailscale address. The URL is then `https://` your Mac's Tailscale name, then `/hook/SECRET`:

```sh
tailscale funnel --bg --set-path /hook https+insecure://100.64.0.2:7433/hook
```

### From a script

`hmk trigger` runs an automation as if a trigger had fired, with what happened as its data: text, `-` for standard input, or `@FILE`.

```sh
hmk trigger "Deploy check" --data "$(git log -1 --format=%s)"
make test 2>&1 | tail -50 | hmk trigger "Fix the tests" --data -
```

## Examples of triggers

Fix CI when it fails on your pull request:

```sh
hmk automation add "Fix CI" --trigger github:ci-failed --dir ~/Developer/app \
  -- "CI failed on a pull request of mine. Find out why from the failing checks, fix it on that branch and push."
```

Prepare for one-to-ones ten minutes before they start:

```sh
hmk automation add "1:1 prep" --trigger 'calendar:10,title=1:1,attendees' --dir ~/Notes \
  -- "Write prep notes for this meeting: {{event}}"
```

## Managing them

View ▸ Automations (⇧⌘S) lists your automations, and ⌃B @ in [the TUI](/hammock/docs/tui/). Select one to pause, edit, run, skip or delete it, and to see its recent runs: **Open Tab** goes to a run's tab, or **Resume Session** carries on its agent's session once the tab's closed. In the list, type to search, ⌘R runs the selected automation and ⌫ deletes it. Deleting an automation leaves its runs' tabs open.

To run an automation now instead of at its next time, choose **Run Now Instead of 09:00** (in the TUI, R): its next run moves on to the one after. **Run Now** runs it as well, and the next run still happens. When the next run is within 12 hours, the Mac's run button brings it forward, and its menu has Run Now.

```sh
hmk automation                       # list them
hmk automation run "Nightly tests"   # now, in a new tab
hmk automation run "Nightly tests" --early  # now, instead of at its next time
hmk automation pause "Nightly tests" # and hmk automation resume
```

## Agents and automations

Agents can make and manage automations with MCP tools such as [`automation_save`](/hammock/docs/reference/mcp/#automation_save) and [`automation_list`](/hammock/docs/reference/mcp/#automation_list), or `hmk automation`. Hammock asks you before an agent adds, changes or deletes one, or runs or pauses one it didn't make.

## Turning them off

If you automate things some other way, turn Automations off in Settings ▸ Tools (`schedules-enabled = false`). Hammock then leaves them out of its menus, and agents can't make or run them. Your automations are kept but don't run until you turn it back on, and none count as missed.
