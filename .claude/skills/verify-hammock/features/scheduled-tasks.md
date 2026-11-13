# Scheduled tasks

A person schedules an agent's prompt or a shell command to run at a time (`--at 08:30 --days weekdays`) or on a cron expression. Each run opens a new tab in the schedule's Group, and `hmk schedule show` lists past runs. Adding or editing a schedule from an agent asks the person first.

## Sub-features

- `schedule-add`: add a command or agent schedule.
- `schedule-list-show`: `hmk schedule` lists it, and `show --json` gives its settings and runs.
- `schedule-run-now`: `hmk schedule run` opens a run tab in its Group straight away.
- `schedule-pause-skip`: pause, resume, skip the next run.
- `schedule-rm`: delete it.

## How to get to it (user POV)

- CLI: `hmk schedule add|edit|show|run|pause|resume|skip|rm` (`hmk schedule help`).
- Mac: the Scheduled Tasks sheet (command palette: "Scheduled Tasks"); `HMK_DEBUG_SCHEDULES=list|edit` renders it.
- iPhone: Sessions ▸ Coming Up.
- MCP: `schedule_save`, `schedule_run`, `schedule_list`, `schedule_pause`, `schedule_delete`.

## Driving it with verify-hammock

Preconditions:

- Baseline run. Group `verify-sched`: `$S/hmk "$RUN" group new verify-sched`.
- **The person is available to approve.** The daemon sees you as an agent, and an agent's `schedule add` waits for their OK (see Gotchas). They approve from a terminal outside Hammock (Terminal.app, say), pointed at this run: `HMK_DIR=<STATE from $RUN/run.env> HMK_CONFIG_DIR=<CONFIG> <HMK> grant allow <id>`. Give them that line with the values filled in.

- **Add, in the background.** Run it with `run_in_background`: `$S/record.sh "$RUN" 01-add schedule add verify-nightly --at 03:00 --command --keep-open --dir /tmp --group verify-sched -- sh -c 'echo verify-ran-$((6*7))'`.
- **Find the request.** `$S/hmk "$RUN" ls --json | jq '.grants'` lists the pending request with its `id`. Hand the person the `grant allow` line. `01-add.exit` becomes 0 once they allow it.
- **Listed.** `$S/record.sh "$RUN" 02-list schedule` shows `verify-nightly` at 03:00, daily.
- **Run now.** `$S/record.sh "$RUN" 03-run schedule run verify-nightly` (it may ask the same way). Then `04-ls ls --json`: `verify-sched` has a new tab. `hmk read` on its pane shows `verify-ran-42`.
- **Runs recorded.** `05-show schedule show verify-nightly --json` lists that run.
- **Remove.** `06-rm schedule rm verify-nightly`, then `07-list schedule` says there are no scheduled tasks.

## Gotchas

- Without the person this journey fails at step one. After about two minutes, `schedule add` exits 1 with `hammock: the person said no (or didn't answer)` and nothing is added. Report that as unverified-needs-approval. Never run `hmk grant allow` yourself: the daemon refuses an agent answering grants, and the guardrail is the point.
- `--command` runs through the login shell: one argument is a shell line, several are argv (`-- sh -c '…'`). The person's rc files run, so assert on the command's output, not the prompt.
- Without `--keep-open`, a clean exit closes the run's tab before you can read it.
- `--group` and `--dir` default to "here", meaningless from outside a pane, so always pass both.
