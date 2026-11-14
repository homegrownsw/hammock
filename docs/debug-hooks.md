# Debug hooks: checking UI without a person

Environment variables that make a development build (β, or a Debug iPhone build) show, render or record a
state on its own, so an agent can check UI without anyone clicking. [AGENTS.md](../AGENTS.md) points here. A
new hook gets a line here, in the table it belongs to.

## The Mac window

`HMK_DEBUG_SNAPSHOT=/path/prefix` renders the window (light and dark) and dumps its views, split view and
titlebar geometry. With it:

| Variable | What it does |
| --- | --- |
| `HMK_DEBUG_TAB=<name>` | Show that tab. `HMK_DEBUG_ZOOM=<n>` zooms its nth pane. |
| `HMK_DEBUG_DELAY=<s>` | Wait before rendering. Give a scratch daemon's first launch after a rebuild 10: β restarts it. |
| `HMK_DEBUG_PALETTE=1` (or `=<text>`) | The command palette open (searched for `<text>`). |
| `HMK_DEBUG_HOVER=1` | Hover states shown. |
| `HMK_DEBUG_SIDEBAR_SCROLL=<pt>` | Start with the sidebar scrolled. |
| `HMK_DEBUG_PEEK=1` | The hidden sidebar's peek panel open; pass `--args -sidebarHidden YES`. |
| `HMK_DEBUG_NOTICES=1` | Sample notification cards in the window. |
| `HMK_DEBUG_DISK_ACCESS=0` (or `1`) | Pretend Hammock hasn't (or has) Full Disk Access, for onboarding's Full Disk Access step (`HMK_ONBOARDING=1 HMK_ONBOARDING_STEP=access`) and Settings ▸ General. |
| `HMK_DEBUG_AGENT_CURSOR=1` | A made-up agent click in every browser pane and tap in every simulator pane, with its glow and badge, kept showing. |
| `HMK_DEBUG_ASLEEP=1` | Every terminal pane shown as an agent put to sleep, a made-up idle Claude where there's none: the moon in the sidebar and the pane's header, and the faded pane with its Wake Agent button; on the iPhone too, with the demo. |
| `HMK_DEBUG_SETTINGS=<pane>` | That Settings pane to `<prefix>-settings-<light\|dark>.png`: `notifications`, `keyboard`, `appearance`, `agentview`, `workspace` (onboarding's drawing of the window, both layouts), `devices`, `editor`, `tools`, `window` (the whole Settings window, sidebar included, on the pane given by `--args -settingsTab <name>`). Search it with `HMK_DEBUG_SETTINGS_QUERY=<text>`. Pick the notification style with `--args -notify-style in-app`: any config key given as a launch argument beats the file's. |
| `HMK_DEBUG_PROMPT=plain\|other\|preview\|tasks` | The agent view's question card with a made-up question, its own answer being typed, one with previews, or under a task list and PR and artifact chips, to `<prefix>-prompt-<light\|dark>.png`. |
| `HMK_DEBUG_CODEBLOCKS=1` | Code blocks with long lines, wrapped and not, and a Markdown one as source and rendered, to `<prefix>-code-<light\|dark>.png`. |
| `HMK_DEBUG_COMPOSER=<text>` | The agent view's composer with `<text>` typed (start it with `!` for a shell command), under two `!` commands as the agent view shows them, to `<prefix>-composer-<light\|dark>.png`. With `HMK_DEBUG_DICTATION=1`, the text is being dictated: the mic listening, and what's after a `\|` in `<text>` not settled yet (dimmed). |
| `HMK_DEBUG_COMPUTER_LOOK=symbols\|emoji[:search]` | The icon and colour picker for the first linked computer, else this one, to `<prefix>-look-<light\|dark>.png`. |
| `HMK_DEBUG_SCHEDULES=list\|edit` | The Scheduled Tasks sheet, or the editor on the first schedule, opened at launch and also rendered on their own to `<prefix>-schedules-<light\|dark>.png` (sheets aren't in the window's snapshot). |
| `HMK_DEBUG_START_AGENT=1` | The Start an Agent sheet on the current Group, also rendered on its own to `<prefix>-start-agent-<light\|dark>.png`. |
| `HMK_DEBUG_OPEN_DIFF=1` | The Open a Diff sheet beside the focused pane, also rendered on its own to `<prefix>-open-diff-<light\|dark>.png`, though without its repository's branches and commits, which it reads once shown. |
| `HMK_MOVE_PROMPT=1` | The launch prompt to move Hammock to Applications, which only release builds outside Applications show. Move really moves the app. |
| `HMK_CONFIG_DIR=<dir>` | A scratch config. |

Make scratch dirs, and any scratch `HMK_DIR`, with `mktemp -d`, not a fixed path under /tmp.

**Isolation.** In a worktree, its own β (`just beta`) is usually all the isolation you need. For a throwaway
state on top, start the scratch daemon first (the bundle's own `hmkd --dir <scratch>`: β shuts down one of
another version and starts its own), then `open -n --env HMK_DIR=<scratch> …` the same app, and kill only that
copy afterwards, by its PID.

**Captured as really drawn.** Live layers (CALayerHost: browser and simulator content) and materials don't
render faithfully in snapshots (the sidebar's list comes out blank). For the window as it's really drawn:

| Variable | What it does |
| --- | --- |
| `HMK_DEBUG_CAPTURE=1` | β captures its own window with ScreenCaptureKit, to `<prefix>-window-<light\|dark>.png`. The screen must be awake and unlocked, and β needs Screen Recording; when it can't, it says why on stderr and in `<prefix>-capture-error.txt`. Stop and ask the person then, rather than retrying or working blind. |
| `HMK_DEBUG_CAPTURE_ZOOM=<n>` | Then zoom and unzoom the tab's nth pane, capturing frames as it animates (`<prefix>-zoom-NN.png`). |
| `HMK_DEBUG_CAPTURE_SPLIT=<n>` | Split it (down with `HMK_DEBUG_CAPTURE_SPLIT_DOWN=1`) and close the new pane (`<prefix>-split-NN.png`; in light mode with `HMK_DEBUG_CAPTURE_LIGHT=1`). |
| `HMK_DEBUG_CAPTURE_FLOAT=<n>` | Float it and dock it again (`<prefix>-float-NN.png`). |
| `HMK_DEBUG_RECORD=<movie.mov>` | Record the window as a movie. |
| `HMK_DEBUG_DIRECTOR=<file>` | Take commands appended to a file: collapse or expand a Group, focus or zoom a pane, type into the palette (`DebugDirector.swift`). `scripts/readme-demo` (`just readme-demo`) uses it and `HMK_DEBUG_RECORD` to record the README's demo GIF on a scratch daemon with made-up Groups (`marketing/demo/`). |

Look at frames as a downscaled contact sheet (`scripts/frames`), not one full-size PNG at a time.

## The iPhone without a Mac

A Debug build launched with `SIMCTL_CHILD_HMK_DEBUG_SAMPLE=1 xcrun simctl launch <udid> com.homegrownsw.hammock`
opens straight into the demo: the pairing screen's "See a demo", for anyone without a Mac, App Review included.
A made-up Mac stands in for the daemon (`apple/iOS/Hammock/DemoMac.swift`, a `DaemonStandIn`): Groups here and
on a linked computer, agents working, waiting and done with their conversations and terminals, scheduled tasks
and their runs. Nothing is paired or sent. Allowing the waiting agent lets it finish, messages get an answer,
and what would change the Mac says to pair one: keep it working when the app learns something new.

With it:

| Variable | What it does |
| --- | --- |
| `HMK_DEBUG_COCKPIT_TAB=groups\|devices\|settings` | Open that tab. |
| `HMK_DEBUG_PATH=schedules,schedule:<id>` | Push those screens onto Sessions (tab and pane ids too). Open sheets and screens this way rather than with tap loops. |
| `HMK_DEBUG_BOTTOM=1` | Scroll Sessions to Coming Up (a schedule's page to its runs). |
| `HMK_DEBUG_MAC_THEME=<name>\|custom` | The sample Mac's theme (Tokyo Night otherwise). |
| `HMK_DEBUG_KEYBOARD=1` | Focus a terminal as it opens, keyboard up. |
| `HMK_DEBUG_TAKE=<steps>` | Play a take in the demo (`DemoDirector.swift`: wait, mark, tab, open, back, allow, type, say, and in the agent view compose, submit, dismiss, scroll), for the App Store's videos and for checking the agent view. |
| `HMK_DEBUG_LONG_CHAT=1` | The demo's polish agent (`p_polish`) has a long conversation of rows of very different heights, and answers a message as Claude does: thinking, a run of tool calls arriving one after another, then a reply. |
| `HMK_DEBUG_REPLAY=<file>` | The polish agent has a real Claude session's history instead, and a message to it plays the rest at the session's own pace, as a Mac would send it. Make the file with `go run ./scripts/agentblocks -src SESSION.jsonl -from LINE > replay.txt` (outside the repo: sessions hold private work). |
| `HMK_DEBUG_SCREENSHOT=1` | Hide the demo's bar and write `HammockScreenshot: done` to stderr once the screen has settled (`just appstore-screenshots` takes the App Store's that way, from `marketing/appstore/scenes.json`). |
| `HMK_DEBUG_THEME_PICKER=1` | With the settings tab, open Settings ▸ Theme. |
| `HMK_DEBUG_FAKE_SCAN=<hammock://pair/… link>` | Pairing for real in the simulator (a scratch daemon with `--remote-listen 127.0.0.1:PORT`): a pretend camera on the pairing screen scans that code after a moment (the simulator has no camera), for recording pairing. |

Screenshot with `xcrun simctl io <udid> screenshot`, after `xcrun simctl ui <udid> appearance light|dark`. Boot
a simulator of your own (`xcrun simctl create`) and delete it afterwards. Don't open simulator panes on a
scratch daemon against a device another session is using.
