---
title: Examples of controlling Hammock from agents
description: Things to ask a coding agent in Hammock, and the tools it uses to do them.
sidebar:
  order: 4
---

Once Hammock's [MCP server is registered](/hammock/docs/agents/setup/#registering-the-mcp-server), ask in your own words and the agent picks the tools. The [MCP reference](/hammock/docs/reference/mcp/) has every tool's parameters.

## Run the dev server and check the page

**You say:**

> Start the dev server and check the sign-up page renders without errors.

**What happens:** the agent starts the server in a pane of its own with `create_pane` and waits for it with `wait_for_pane`. It opens the page with `browser_open`, reads it with `browser_snapshot` and checks `browser_console` for errors. The browser pane is beside the agent, for you to use too.

## Fix a broken flow by clicking through it

**You say:**

> Logging in with a wrong password shows a blank screen instead of an error. Reproduce it in the browser and fix it.

**What happens:** the agent finds the fields with `browser_snapshot`, fills them in with `browser_type` and clicks with `browser_click`. It checks `browser_console` and `browser_network` for the failing request, fixes the code, then reloads with `browser_navigate` and tries again. You see a cursor where it clicks.

## Test an iOS app in a simulator

**You say:**

> Build the app, run it in the simulator and check the settings screen opens from the profile tab.

**What happens:** the agent builds the app, opens a simulator pane with `sim_open`, and installs and launches it with `sim_install_app` and `sim_launch_app`. It looks with `sim_screenshot`, taps with `sim_tap`, and reads the app's log with `sim_logs` if something goes wrong. `sim_shutdown` closes it when it's done.

## Watch a log for an error

**You say:**

> Keep an eye on the server log while I try the checkout, and tell me what went wrong.

**What happens:** the agent opens a log pane on the file with `log_open`. When you say you've tried it, it reads the errors with `log_read`.

## Review the agent's changes

**You say:**

> Show me the diff before you open a pull request.

**What happens:** the agent opens a diff pane with `diff_open`, laid out like a pull request, and ends its turn. Your comments reach it as a message: it reads them with `review_threads`, makes the changes, answers with `review_reply` and marks each thread with `review_mark_addressed`. Resolving them is yours.

## Point you at code

**You say:**

> Where do we decide which plan a new account gets? Show me.

**What happens:** the agent opens the file in an editor pane with `editor_open`, with the lines selected, and moves the selection as it explains with `editor_select`. The other way round, select some lines and ask "what does this do?": it reads your selection with `editor_read`.

## Schedule a recurring agent

**You say:**

> Every weekday at 8:30, review yesterday's open pull requests and give me a summary.

**What happens:** the agent saves a [scheduled task](/hammock/docs/scheduled-tasks/) with `schedule_save`, and Hammock asks you to allow it first. Each run starts in a new tab in the Group.

## Find something from a past session

**You say:**

> Last week we decided how to handle retries in the sync client. What did we settle on?

**What happens:** the agent searches this project's past sessions with `session_search` and reads the relevant parts of the best match with `session_read`. Searching other projects' sessions asks you the first time.

## Run a test watcher and wait for it

**You say:**

> Run the tests in watch mode, then fix whatever fails.

**What happens:** the agent starts the watcher in a pane beside its own with `create_pane`, and waits for each run with `wait_for_pane`. It reads the failures with `read_pane`, fixes the code and waits again as the watcher reruns, then closes the pane with `close_pane`.
