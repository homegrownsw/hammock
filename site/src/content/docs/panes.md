---
title: Browsers, simulators, logs, editors and diffs
description: The panes that aren't terminals, how to open them from the app, from hmk and by agents, and how to turn each off.
---

Besides terminals, a pane can hold a web page, an iOS simulator, a log, a file to edit or a git diff to review. They sit in your splits and tabs like any terminal, and keep running the same way: see [Groups, tabs and panes](/hammock/docs/workspace/).

You can open each kind of pane in three ways:

- **In the app:** from the File and Pane menus, the toolbar's **+** (new tab) and split buttons, a pane's right-click menu, or the [command palette](/hammock/docs/workspace/#the-command-palette) (⌘K).
- **From a terminal**, with [`hmk`](/hammock/docs/cli/). Run in a pane, most commands open beside it.
- **By agents**, through Hammock's [MCP tools](/hammock/docs/agents/mcp/). The tool names below link to the [MCP reference](/hammock/docs/reference/mcp/).

## Turning a kind of pane off

If you already do one of these your own way, turn it off in Settings ▸ Tools: Browser, Simulator, Editor, Diff Viewer or Log Viewer. Hammock then leaves it out of its menus and palette, and agents don't get its tools. Panes already open stay open. In the config file:

```ini
browser-enabled = false
simulator-enabled = false
editor-enabled = false
diff-enabled = false
log-enabled = false
```

Settings ▸ Tools also has Show what agents do in browsers and simulators (`agent-presence`): while an agent acts in one of those panes, Hammock shows its cursor, a glow and a badge.

## Browser

A browser pane is a real Chromium page, not a picture of one. Scrolling, pinching, text input and pop-up menus work as they do in a browser. Every browser pane shares one profile, so your logins and cookies carry over between panes and are kept when you quit.

**Open one:**

- File ▸ New Browser Tab (⇧⌘B).
- Pane ▸ Split Right with Browser (⌥⌘B) or Split Down with Browser (⌥⇧⌘B).
- In a terminal, ⌘-click a link to open it in your default browser, or ⇧⌘-click to open it in a browser pane beside the terminal. To have links open in Hammock by default, choose a Hammock pane or tab in Settings ▸ General ▸ Links ▸ Clicked links open in (`link-open`); ⇧⌘-click then opens your default browser instead.

```sh
hmk browser http://localhost:3000            # a new browser tab
hmk browser http://localhost:3000 --split "$HMK_PANE_ID"
hmk nav PANE reload                          # or back, forward, stop, or a URL
```

`hmk show` puts a file or page in a browser pane beside the one you run it in: an HTML or Markdown file, an image, a PDF, or a URL. Run it again and it reloads that pane rather than opening another.

```sh
hmk show report.html
hmk show coverage/index.html
```

**DevTools:** Pane ▸ Show DevTools (⌥⌘I), or `hmk devtools PANE`, opens Chromium's DevTools for the page in a pane beside it.

**Links:** a link a page opens in a new tab goes to your default browser, or to a Hammock browser tab if you choose that in Settings ▸ General ▸ Links that open a new tab (`browser-new-tab = hammock`). ⌘-click, middle-click and sign-in windows always open in Hammock. A link to another app (`mailto:`, `zoommtg:` and so on) asks you first; the schemes you allow are listed in Settings ▸ General ▸ Links to other apps (`browser-open-schemes`).

**Agents** open pages with [`browser_open`](/hammock/docs/reference/mcp/#browser_open), read them with [`browser_snapshot`](/hammock/docs/reference/mcp/#browser_snapshot), and act with `browser_click`, `browser_type` and `browser_press_key`. They can check `browser_console` and `browser_network` when debugging, take a `browser_screenshot`, and use [`show_output`](/hammock/docs/reference/mcp/#show_output) to show you a report they've made.

## Simulator

A simulator pane shows an iOS simulator right in your layout, with no Simulator app window. It needs Xcode and at least one simulator installed.

**Open one:**

- The toolbar's **+** ▸ Simulator Tab, then pick a device.
- Pane ▸ Split Right with Simulator.

```sh
hmk sim list                     # the simulators on this Mac
hmk sim                          # a booted one, else the newest iPhone
hmk sim "iPhone 17 Pro" --split "$HMK_PANE_ID"
```

A device that isn't running is booted for you. Click to tap, drag to swipe, and type with your keyboard. While a simulator pane is focused, the Pane menu has its controls: Home (⇧⌘H), Lock (⌘L), Rotate Left and Rotate Right (⌘← and ⌘→), Toggle Software Keyboard (⇧⌘K), Screenshot to File (⌘S) and Screenshot to Clipboard (⌃⌘C).

The pane draws the device around the screen. For the screen alone, set `simulator-frame = false`.

**Shutting down:** Pane ▸ Shut Down Simulator, or `hmk sim shutdown PANE`. Simulators Hammock booted also shut down a minute after their last pane closes. To keep them running, turn off Settings ▸ General ▸ Shut down simulators when their last pane closes (`simulator-shutdown = false`).

**Agents** open one with [`sim_open`](/hammock/docs/reference/mcp/#sim_open), look with `sim_screenshot`, act with `sim_tap`, `sim_swipe`, `sim_type`, `sim_pinch`, `sim_rotate` and `sim_press_button`, and install, launch and debug apps with `sim_install_app`, `sim_launch_app` and `sim_logs`. They call `sim_shutdown` when they're done.

## Log

A log pane follows a log and splits each line into entries (time, level, process, tag, message) that you can filter. It can follow:

- a **file**;
- **this Mac**'s unified log;
- a **simulator**'s log;
- an **Android** device's logcat (through `adb`);
- a **command**'s output, such as a dev server.

**Open one:** File ▸ New Log Tab… (⌥⌘L), or Pane ▸ Split Right with Log…, then choose the source. For the Mac's or a simulator's log you can narrow it to a process, a predicate and a level. With a simulator pane focused, Pane ▸ Show Simulator Log opens its log beside it straight away.

**Filtering:** type in the filter bar to match text, or `/a regex/`. The level buttons show only the levels you pick, and right-clicking an entry offers Show Only and Hide for its process or tag. Pane ▸ Toggle Log View (⇧⌘A) switches between the entries and the raw terminal output.

```sh
hmk log file ./server.log
hmk log macos --process MyApp --level debug
hmk log sim --predicate 'subsystem == "com.example.app"'
hmk log logcat
hmk log -- pnpm dev
hmk log read PANE error --level error,fault --limit 50
hmk log clear PANE
```

**Agents** open one with [`log_open`](/hammock/docs/reference/mcp/#log_open) and read it, filtered, with [`log_read`](/hammock/docs/reference/mcp/#log_read).

## Editor

An editor pane shows a text file, with syntax highlighting, find, go to line and undo. It's built on CodeMirror. The file's buffer is kept by Hammock, not the window, so unsaved edits survive closing the app, and the same file can be edited from your Mac, your iPhone or [the TUI](/hammock/docs/tui/#editor-panes).

**Open one:**

- File ▸ Quick Open… (⌘P) finds a file in the Group's folder. File ▸ Open File… (⌘O) picks one.
- File ▸ New Editor Tab (⌥⌘E) opens an empty one.
- Pane ▸ Split Right with Editor (⌃⌘E) or Split Down with Editor (⌃⇧⌘E).

```sh
hmk edit src/main.go:42          # beside this pane, line 42 selected
hmk edit src/main.go:42-60 --tab # lines 42 to 60, in a new tab
hmk edit --read PANE --json      # its text, unsaved edits included
```

While an editor pane is focused: Save (⌘S), Save As… (⌥⇧⌘S), Go to Line… (⌘L), and Find (⌘F). File ▸ Compare with File on Disk shows how your unsaved edits differ from the file, and Reload from Disk throws them away. When a program changes the file on disk, the pane reloads it; if you have unsaved edits, it offers to. Click the language name in the pane's header to choose how the file is highlighted.

Settings ▸ Editor sets wrapping (also View ▸ Wrap Lines in Editors, ⌥⌘Z), line numbers, tab width, spaces or tabs, the font, and **Vim keys** (`editor-vim = true`). A file's `.editorconfig` comes before your tab width and indent settings. All the keys are in the [config reference](/hammock/docs/reference/config/#editor-panes).

**Agents** use [`editor_open`](/hammock/docs/reference/mcp/#editor_open) to show you code they're talking about, with the lines selected, and [`editor_read`](/hammock/docs/reference/mcp/#editor_read) to see what you have open and selected, so you can ask about "the lines I've selected". They change files on disk as usual, and the pane picks up the change.

## Diff

A diff pane shows a git diff the way a pull request's Files Changed does, for you to review, and sends your comments to an agent.

**Open one:**

- View ▸ Review Changes (⌥⌘R) opens, in a new tab after the current one, what a pull request from this branch would show, uncommitted changes included. On the default branch, it shows what's uncommitted.
- File ▸ New Diff Tab… and Pane ▸ Split Right with Diff… let you choose what to compare: This Branch, Uncommitted, A Commit or A Range.

```sh
hmk diff                           # this branch against its base
hmk diff --uncommitted
hmk diff HEAD~1                    # one commit
hmk diff main...feature --split    # a range, side by side
```

Choose a unified or split layout, ignore whitespace and wrap long lines from the pane's settings button, or set the defaults in Settings ▸ Editor (`diff-style`, `diff-whitespace`, `diff-wrap`).

### Reviewing an agent's work

Leave comments on lines, as you would on a pull request. When you send them, they go to an agent as a message: the one that opened the diff, or another agent in the Group that you pick. If the agent is waiting on you, for a permission or an answer, your comments wait until it isn't.

The agent answers in the threads and marks each one addressed when it has dealt with it. Only you resolve a thread. To hear when an agent replies, turn on Settings ▸ Notifications ▸ Replies to my review comments.

Close a diff with threads still open and the review is kept: open the same diff again and they come back.

**Agents** open a diff with [`diff_open`](/hammock/docs/reference/mcp/#diff_open) when you ask to see their changes, read your comments with [`review_threads`](/hammock/docs/reference/mcp/#review_threads), answer with `review_reply`, mark threads with `review_mark_addressed`, and start threads of their own with `review_comment`. An agent without the MCP tools can do the same from its shell:

```sh
hmk review                         # the open threads on your diffs
hmk review reply THREAD "Done: renamed it."
hmk review address THREAD "Moved the check into the parser."
```
