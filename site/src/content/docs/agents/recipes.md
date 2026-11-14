---
title: Agent recipes
description: Things to ask a coding agent in Hammock, and the tools it uses to do them.
sidebar:
  order: 4
---

Once Hammock's [MCP server is registered](/hammock/docs/agents/setup/#registering-the-mcp-server), you don't need to name its tools: ask in your own words and the agent picks them. Each recipe below shows a prompt and what the agent does with it. The [MCP reference](/hammock/docs/reference/mcp/) has every tool's parameters.

## Run the dev server and check the page

**You say:**

> Start the dev server and check the sign-up page renders without errors.

**What happens:** the agent starts the server in a pane of its own with `create_pane` (`pnpm dev`, beside its pane) and waits for it to be ready with `wait_for_pane`, matching a line such as `ready in`. It opens the page with `browser_open` (`localhost:5173/signup`), reads it as an outline with `browser_snapshot`, and checks `browser_console` for errors and warnings. You see the server's output and a real browser pane split beside the agent, and can use the page yourself.

## Fix a broken flow by clicking through it

**You say:**

> Logging in with a wrong password shows a blank screen instead of an error. Reproduce it in the browser and fix it.

**What happens:** in a browser pane it opened, the agent finds the fields and buttons with `browser_snapshot`, fills them in with `browser_type`, and clicks with `browser_click`, waiting for the page with `browser_wait_for`. It checks `browser_console` and `browser_network` for the failing request, edits the code, then reloads with `browser_navigate` and goes through the flow again. With **Show what agents do in browsers and simulators** on (Settings ▸ Tools), you see a cursor glide to each place it clicks.

## Test an iOS app in a simulator

**You say:**

> Build the app, run it in the simulator and check the settings screen opens from the profile tab.

**What happens:** the agent builds the app in its own shell, then opens a simulator pane with `sim_open`, installs the built `.app` with `sim_install_app` and launches it by bundle id with `sim_launch_app`. It looks with `sim_screenshot`, taps with `sim_tap` at the points the screenshot shows, and reads the app's log with `sim_logs` (filtered with a predicate such as `process == "MyApp"`) if something goes wrong. When it's done, `sim_shutdown` closes the pane and shuts the simulator down.

## Watch a log for an error

**You say:**

> Keep an eye on the server log while I try the checkout, and tell me what went wrong.

**What happens:** the agent opens a log pane with `log_open`: a file (`source: file`, with its path), the Mac's log, a simulator's, or a command's output. The log is parsed into entries you can filter yourself. When you say you've tried it, the agent reads it with `log_read`, keeping to `error` and `fault` levels, or to what's new since its last read with `after_seq`.

## Review the agent's changes

**You say:**

> Show me the diff before you open a pull request.

**What happens:** the agent opens a diff pane with `diff_open`, laid out like a pull request's Files Changed, in a new tab after its own, and ends its turn. You read it and leave comments on lines. When you send them, they reach the agent as a message: it reads them with `review_threads`, makes the changes, answers questions with `review_reply`, and marks each thread done with `review_mark_addressed` and a short note. You check each one and resolve it: the agent can't.

If you ask it to open the diff and wait for your review, it uses `review_wait` instead of ending its turn. Settings ▸ Notifications ▸ Replies to my review comments tells you when it answers.

## Point you at code

**You say:**

> Where do we decide which plan a new account gets? Show me.

**What happens:** the agent finds it, then opens the file in an editor pane beside its own with `editor_open`, with the relevant lines selected and scrolled into view. As it explains, it moves the selection to other parts of the file with `editor_select`. It works the other way too: select some lines and ask "what does this do?", and the agent reads your selection with `editor_read`.

## Schedule a recurring agent

**You say:**

> Every weekday at 8:30, review yesterday's open pull requests and give me a summary.

**What happens:** the agent saves a [scheduled task](/hammock/docs/scheduled-tasks/) with `schedule_save`: the prompt, `at: 08:30` and `days: weekdays`, with Claude Code (or another agent) to run it. Before it's saved, Hammock asks you to allow it, showing what would run and when; the agent waits for your answer. Each run then starts in a new tab in the Group, and you're told when it's ready. To show a page from a run, the prompt can end with `hmk show FILE`.

## Find something from a past session

**You say:**

> Last week we decided how to handle retries in the sync client. What did we settle on?

**What happens:** the agent searches this project's past sessions with `session_search` (`retries sync`) and reads the parts of the best match that mention them with `session_read`, rather than the whole transcript. To look in other projects' sessions too, it searches with `everywhere`, which asks you the first time.

## Run a test watcher and wait for it

**You say:**

> Run the tests in watch mode, then fix whatever fails.

**What happens:** the agent starts the watcher with `create_pane` (`pnpm test --watch`), in a pane split beside its own, and waits for the run to end with `wait_for_pane`, matching a pattern such as `Tests:.*(passed|failed)`. It reads the failures with `read_pane` (the last lines, or with `scrollback` for more), fixes the code, and waits again as the watcher reruns. The pane stays open so you can watch too, and the agent closes it with `close_pane` when it's done, or leaves it if you'd like to keep it.
