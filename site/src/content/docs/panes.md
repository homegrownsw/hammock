---
title: Browsers, simulators, logs, editors and diffs
description: The panes that aren't terminals, how to open them from the app, from hmk and by agents, and how to turn each off.
---

Besides terminals, a pane can hold a web page, an iOS simulator, a log, a file to edit or a git diff. They sit in your splits and tabs, and keep running, like any [other pane](/hammock/docs/workspace/).

Open them from the File and Pane menus, the toolbar's **+** and split buttons, the [command palette](/hammock/docs/workspace/#the-command-palette) (⌘K), with [`hmk`](/hammock/docs/cli/) (most commands open beside the pane you run them in), or let agents open them with Hammock's [MCP tools](/hammock/docs/agents/mcp/) (tool names below link to the [MCP reference](/hammock/docs/reference/mcp/)).

To show one in place of a pane you have, click the pane's name and choose from **Show**. Hammock asks first unless nothing can be lost: a running agent, a program still running, a page, unsaved edits or the last view of a simulator.

## Turning a kind of pane off

To leave one out of Hammock's menus and agents' tools, turn it off in Settings ▸ Tools, or in the config file:

```ini
browser-enabled = false
simulator-enabled = false
editor-enabled = false
diff-enabled = false
log-enabled = false
```

Settings ▸ Tools also has Show what agents do in browsers and simulators (`agent-presence`), which shows an agent's cursor while it acts in one.

## Browser

A browser pane is a real Chromium page. All browser panes share one profile, so logins and cookies carry over and are kept when you quit.

Open one with File ▸ New Browser Tab (⇧⌘B) or Pane ▸ Split Right with Browser (⌥⌘B). In a terminal, ⇧⌘-click a link to open it in a browser pane beside it; to make that the default for a plain ⌘-click, change Settings ▸ General ▸ Links ▸ Clicked links open in (`link-open`).

```sh
hmk browser http://localhost:3000 --split "$HMK_PANE_ID"
hmk show coverage/index.html     # a file, image, PDF or URL beside this pane; again reloads it
```

Pane ▸ Show DevTools (⌥⌘I) opens Chromium's DevTools beside the page. Links a page opens in a new tab go to your default browser unless you set Settings ▸ General ▸ Links that open a new tab (`browser-new-tab = hammock`); links to other apps (`mailto:` and so on) ask first.

**Agents** open pages with [`browser_open`](/hammock/docs/reference/mcp/#browser_open), read them with [`browser_snapshot`](/hammock/docs/reference/mcp/#browser_snapshot), act on them, and use [`show_output`](/hammock/docs/reference/mcp/#show_output) to show you a report.

## Simulator

A simulator pane shows an iPhone, iPad or Apple TV simulator, or an Android emulator, in your layout. It needs Xcode (or the Android SDK) and at least one simulator (or AVD) installed. Open one from the toolbar's **+** ▸ Simulator Tab or Pane ▸ Split Right with Simulator:

```sh
hmk sim list
hmk sim "iPhone 17 Pro" --split "$HMK_PANE_ID"
```

Click to tap, drag to swipe, and type with your keyboard. The Pane menu has Home, Lock, rotation, the software keyboard and screenshots. Simulators Hammock booted shut down a minute after their last pane closes, unless you turn off Settings ▸ General ▸ Shut down simulators when their last pane closes.

An **Apple TV** has no touch screen: drive it with the Siri Remote beside it, which Pane ▸ Show Remote (⌃⌘R) hides and shows. With the pane focused, the arrow keys are the clickpad, Return is Select, Escape is Back and Space is Play/Pause, sent as the remote sends them; other keys type. Hold a button for a long press. The Pane menu also has the remote's Play/Pause, volume, Mute and Siri.

An **Android emulator** opens the same way: your Android Virtual Devices are listed under Android and Android TV (`hmk sim avd:Pixel_9`). Hammock boots the AVD with no window, or shows its emulator if one is already running with `-grpc-use-token`. One running in Android Studio can't be driven from Hammock: its pane offers Restart in Hammock, which stops it in Studio and boots it here. If the emulator stops, its pane offers Start in Hammock. Hammock finds the Android SDK where Android Studio puts it, or set `android-sdk` in the config file. The header has Back, Home and Recents, and Escape is Back. An Android TV has a remote beside it, driven like an Apple TV's.

**Agents** open one with [`sim_open`](/hammock/docs/reference/mcp/#sim_open), then look, tap, type, and install, launch and debug apps, and drive an Apple TV or Android TV with [`sim_remote`](/hammock/docs/reference/mcp/#sim_remote).

## Log

A log pane follows a file, this Mac's unified log, a simulator's log, an Android device's logcat or a command's output, and splits it into entries you can filter by text, `/regex/`, level, process or tag. Open one with File ▸ New Log Tab… (⌥⌘L), or Pane ▸ Show Simulator Log beside a simulator (an emulator's is its logcat). Pane ▸ Toggle Log View (⇧⌘A) shows the raw output.

```sh
hmk log file ./server.log
hmk log macos --process MyApp --level debug
hmk log -- pnpm dev
```

**Agents** open one with [`log_open`](/hammock/docs/reference/mcp/#log_open) and read it, filtered, with [`log_read`](/hammock/docs/reference/mcp/#log_read).

## Editor

An editor pane shows a text file, with syntax highlighting, find and go to line. Hammock keeps its buffer, so unsaved edits survive closing the app, and you can edit the same file from your Mac, your iPhone or [the TUI](/hammock/docs/tui/#editor-panes).

Open one with File ▸ Quick Open… (⌘P), File ▸ Open File… (⌘O), or Pane ▸ Split Right with Editor (⌃⌘E):

```sh
hmk edit src/main.go:42-60       # beside this pane, lines 42 to 60 selected
```

When a program changes the file on disk, the pane reloads it, or offers to if you have unsaved edits. When the file is renamed or moved, in the Finder or by git or an agent, the pane follows it; moved to the Trash, it shows as missing. File ▸ Rename… renames it from the pane, unsaved edits and all, and Reveal in Finder shows it there. Settings ▸ Editor sets wrapping, line numbers, indentation, the font and **Vim keys**; a file's `.editorconfig` wins over them. All the keys are in the [config reference](/hammock/docs/reference/config/#editor-panes).

### Markdown preview

A Markdown file can be read rendered: the buttons in the pane's header switch between its source, the source with the preview beside it, and the preview alone (View ▸ Show Preview, ⇧⌘V). The preview follows your edits, and an agent's changes to the file, as they happen, and Find (⌘F) searches it. Tick a task's checkbox to tick it in the file; a link to another file opens it beside. In a Group shared with you to view, the view you pick is yours alone. On the iPhone, Show Preview is in the pane's menu; the TUI shows the source.

```sh
hmk edit docs/plan.md --view preview
```

**Agents** use [`editor_open`](/hammock/docs/reference/mcp/#editor_open) to show you code with the lines selected, or a Markdown file rendered, and [`editor_read`](/hammock/docs/reference/mcp/#editor_read) to see what you've selected, so you can ask about "the lines I've selected".

## Diff

A diff pane shows a git diff like a pull request's Files Changed, and lets you add comments to send to an agent.

View ▸ Review Changes (⌥⌘R) shows what a pull request from this branch would, uncommitted changes included. File ▸ New Diff Tab… compares a branch, uncommitted changes, a commit or a range:

```sh
hmk diff                           # this branch against its base
hmk diff main...feature --split    # a range, side by side
```

Layout, whitespace and wrapping are in the pane's settings button, with defaults in Settings ▸ Editor.

### Reviewing an agent's work

Comment on lines as you would on a pull request. When you send them, your comments go to the agent that opened the diff, or another in the Group that you pick. The agent answers in the threads and marks each addressed; only you resolve one. Close the diff with threads open and they come back when you open it again.

**Finish Review** (q), in the bar once an agent is waiting on the diff or you've commented, is how you tell the agent you're done: pick Approve, Request Changes or Comment, add a summary if you like, and it goes to the agent with any comments not yet sent, then the diff closes. A comment on its own is only ever about its lines; only finishing speaks for the whole diff. Closing a diff with comments not yet sent asks whether to finish first.

Tick **Viewed** on a file's header (Space) to fold it away and move on to the next; the bar counts how many you've viewed. A file that changes after you've viewed it is unticked, so you look at it again.

### The keyboard

A diff pane works from the keyboard, with keys like revdiff's and GitHub's: j and k (or the arrows) move line by line, n and p from file to file, ] and [ from change to change, and Return comments on the line. Tab moves between the file list and the diff. Press ? (or the keyboard button in the pane's bar) to see them all. The Diff submenu of the View menu has the same commands, to give shortcuts of your own in Settings ▸ Keyboard.

**Agents** open a diff with [`diff_open`](/hammock/docs/reference/mcp/#diff_open) and read your comments with [`review_threads`](/hammock/docs/reference/mcp/#review_threads). An agent without the MCP tools uses `hmk review` from its shell.
