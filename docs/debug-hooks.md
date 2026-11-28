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
| `HMK_DEBUG_WINDOW_WIDTH=<pt>` | Render the window this wide (1280 by default), to see panes narrower. |
| `HMK_DEBUG_DELAY=<s>` | Wait before rendering. Give a scratch daemon's first launch after a rebuild 10: β restarts it. |
| `HMK_DEBUG_PALETTE=1` (or `=<text>`) | The command palette open (searched for `<text>`). |
| `HMK_DEBUG_HOVER=1` | Hover states shown. |
| `HMK_DEBUG_FOLDER_DROP=group:<n>` (or `line:<n>`) | A folder dragged from the Finder over the sidebar: the nth Group lit to take it as tabs, or the line before the nth Group (one past the last: after it) where new Groups would go. |
| `HMK_DEBUG_SIDEBAR_SCROLL=<pt>` | Start with the sidebar scrolled. |
| `HMK_DEBUG_PEEK=1` | The hidden sidebar's peek panel open; pass `--args -sidebarHidden YES`. |
| `HMK_DEBUG_TAB_PREVIEW=<n>` (or `=<text>`) | The nth tab's hover preview (in sidebar order, from 1; or the first whose title contains `<text>`) shown and kept. It floats in a window of its own, so capture the screen, not the window. |
| `HMK_DEBUG_NOTICES=1` | Sample notification cards in the window. |
| `HMK_DEBUG_FOOTER=1` | The sidebar's footer (Groups \| Sessions and the computers) with 0, 1, 3 and 5 made-up linked computers, at the sidebar's narrowest (220), ideal (272) and widest (380), with all shown and with one chosen, to `<prefix>-footer-<light\|dark>.png`. |
| `HMK_DEBUG_NEEDS_YOU=<n>` | The sidebar's Needs You and Finished on their own, with `<n>` made-up agents waiting and 8 finished, at the sidebar's width (more than fit shows "+N more"; Finished needs `--args -sidebar-finished true` if the config hides it), to `<prefix>-needs-you-<light\|dark>.png`. |
| `HMK_DEBUG_DISK_ACCESS=0` (or `1`) | Pretend Hammock hasn't (or has) Full Disk Access, for onboarding's Full Disk Access step (`HMK_ONBOARDING=1 HMK_ONBOARDING_STEP=access`) and Settings ▸ General. |
| `HERDR_HOME=<dir>` | Read herdr's session from `<dir>/session.json` (made-up workspaces, with splits) for onboarding's herdr step (`HMK_ONBOARDING=1 HMK_ONBOARDING_STEP=herdr`, shown even without herdr) and `hmk import herdr`. A made-up Claude session resumes when `CLAUDE_CONFIG_DIR` points at a folder with `projects/<x>/<id>.jsonl`. |
| `HMK_DEBUG_AGENT_CURSOR=1` | A made-up agent click in every browser pane and tap in every simulator pane, with its glow and badge, kept showing. |
| `HMK_DEBUG_PIN_MODE=1` | Pin Notes on in every browser and simulator pane (the iPhone's too): the ring and the Send bar, with two made-up pins on a pane that has none (they can't be sent). |
| `HMK_DEBUG_ASLEEP=1` | Every terminal pane shown as an agent put to sleep, a made-up idle Claude where there's none: the moon in the sidebar and the pane's header, and the faded pane with its Wake Agent button; on the iPhone too, with the demo. |
| `HMK_DEBUG_SETTINGS=<pane>` | That Settings pane to `<prefix>-settings-<light\|dark>.png`: `notifications`, `keyboard`, `appearance`, `agentview`, `workspace` (onboarding's drawing of the window, both layouts), `devices`, `editor`, `tools`, `window` (the whole Settings window, sidebar included, on the pane given by `--args -settingsTab <name>`). Search it with `HMK_DEBUG_SETTINGS_QUERY=<text>`. Pick the notification style with `--args -notify-style in-app`: any config key given as a launch argument beats the file's. |
| `HMK_DEBUG_PROMPT=plain\|other\|preview\|tasks\|review\|grant` | The agent view's question card with a made-up question, its own answer being typed, one with previews, under a task list and PR and artifact chips, or the Submit step's review of the answers; or (`grant`) Hammock's permission card for a made-up automation, to `<prefix>-prompt-<light\|dark>.png`. |
| `HMK_DEBUG_COMPOSER=<text>` | The agent view's composer with `<text>` typed (start it with `!` for a shell command, or `/` to see commands offered), to `<prefix>-composer-<light\|dark>.png`. With `HMK_DEBUG_DICTATION=1`, the text is being dictated: the mic listening, and what's after a `\|` in `<text>` not settled yet (dimmed). |
| `HMK_DEBUG_COMPUTER_LOOK=symbols\|emoji[:search]` | The icon and colour picker for the first linked computer, else this one, to `<prefix>-look-<light\|dark>.png`. |
| `HMK_DEBUG_SCHEDULES=list\|edit\|new` | The Automations window opened at launch (editing the first automation, or a new one), and with `HMK_DEBUG_SNAPSHOT` captured as drawn to `<prefix>-schedules-<light\|dark>.png` (with β's Screen Recording and the screen unlocked; else a snapshot of its content, with the sidebar's list blank), and its list's rows on their own to `<prefix>-schedules-list-<light\|dark>.png`. |
| `HMK_DEBUG_SCHEDULES_SAMPLE=1` | With `HMK_DEBUG_SCHEDULES`: made-up automations (one watching nine repositories, GitHub, calendar, files, This Mac, a command check, a webhook with a made-up URL, Only If, a paused one, Slack triggers with a made-up workspace connected: `sc_sample_slack`) and runs they started, shown as if the daemon had sent them, the first selected (and the one edited). `HMK_DEBUG_SCHEDULES_SELECT=<id>` selects another (`sc_sample_tests`, `sc_sample_ci`); `HMK_DEBUG_SCHEDULES_HEIGHT=<pt>` makes the window taller, to see all of it. |
| `HMK_DEBUG_SLACK=connect\|connected` | With `HMK_DEBUG_SCHEDULES`: the Connect Slack sheet over the Automations window (`connected`: as it shows a made-up workspace once the tokens are checked). Captured with the window by `HMK_DEBUG_SNAPSHOT`. |
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
| `HMK_DEBUG_RECORD=<movie.mov>` | Record the window as a movie, drawn as the active window throughout, whatever app has the focus. |
| `HMK_DEBUG_DIRECTOR=<file>` | Take commands appended to a file: collapse or expand a Group, focus or zoom a pane, type into the palette (`DebugDirector.swift`); drive Pin Notes (`pins on|off|point|add|send`, with a drawn pointer); show a pane's agent view, pick its renderer (`agent renderer native\|web`), bring a row to the top, open or close it, or list its rows (`agent show\|toggle\|list`, for comparing the two renderers); size the window; capture it (`capture PATH`), or a burst of frames while another command runs (`burst PATH SECONDS COMMAND`, with β's Screen Recording); or `drag SECONDS`: resize the window every frame, as dragging its corner does, and report to stderr how it kept up (`DebugResizeDrag.swift`). `scripts/readme-demo` (`just readme-demo`) uses it and `HMK_DEBUG_RECORD` to record the README's demo GIF on a scratch daemon with made-up Groups (`marketing/demo/`). |

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
| `HMK_DEBUG_SELECT=<col>,<row>` | Select the word at that cell of a terminal as it opens, handles and edit menu showing, as a touch and hold does (`HMK_DEBUG_PATH=p_shell`: the demo's shell). |
| `HMK_DEBUG_EXPOSE=1` | Open Exposé over a tab of several panes as it opens (`HMK_DEBUG_PATH=t_site`: the demo's tab of several panes). |
| `HMK_DEBUG_HOLD_PREVIEW=<pane id>` | Show that pane's touch-and-hold preview over Sessions, as a row's menu shows it, without its menu (`p_shell`: a terminal; `p_polish`: an agent, as its conversation). |
| `HMK_DEBUG_TAKE=<steps>` | Play a take in the demo (`DemoDirector.swift`: wait, mark, tab, open, back, allow, type, say, and in the agent view compose, submit, dismiss, scroll), for the App Store's videos and for checking the agent view. |
| `HMK_DEBUG_LONG_CHAT=1` | The demo's polish agent (`p_polish`) has a long conversation of rows of very different heights, and answers a message as Claude does: thinking, a run of tool calls arriving one after another, then a reply. "medium" in the message makes the reply shorter, and "right after" sends a note just before it. |
| `HMK_DEBUG_NEW_CHAT=1` | The polish agent's conversation starts empty, and a first message to it reaches the transcript a moment after it's sent, as from a Mac. |
| `HMK_DEBUG_REPLAY=<file>` | The polish agent has a real Claude session's history instead, and a message to it plays the rest at the session's own pace, as a Mac would send it. Make the file with `go run ./scripts/agentblocks -src SESSION.jsonl -from LINE > replay.txt` (outside the repo: sessions hold private work). |
| `HMK_DEBUG_SCREENSHOT=1` | Hide the demo's bar and write `HammockScreenshot: done` to stderr once the screen has settled (`just appstore-screenshots` takes the App Store's that way, from `marketing/appstore/scenes.json`). |
| `HMK_DEBUG_LINK=down\|drop[:<s>]` | The demo's Mac can't be reached: `down`, never (the first load, then the connection bar's "Can't reach"); `drop`, from a moment after the app has its state (reconnecting, then can't reach), for `<s>` seconds if given. |
| `HMK_DEBUG_THEME_PICKER=1` | With the settings tab, open Settings ▸ Theme. |
| `HMK_DEBUG_FAKE_SCAN=<hammock://pair/… link>` | Pairing for real in the simulator (a scratch daemon with `--remote-listen 127.0.0.1:PORT`): a pretend camera on the pairing screen scans that code after a moment (the simulator has no camera), for recording pairing. |
| `HMK_DEBUG_SCREEN_BAR=1\|<name>` | A made-up Mac's screen (Studio, or `<name>`) minimised: its bar with a drawn thumbnail above the tab bar, and at the top of a pane (`HMK_DEBUG_PATH=p_polish`). No stream; tapping it does nothing. |
| `HMK_DEBUG_LIVE_ACTIVITY=working\|needs_you\|finished` | Start a Live Activity with made-up content (an agent at work, one waiting on a permission with Allow and Deny, or one finished) and fill the Needs You widget, to look at on the Lock Screen and in the Dynamic Island (`LiveActivities.showSample`). Without the demo too. On iOS 27.2 a screenshot leaves the Dynamic Island out unless you pass `--mask=black`. |

Screenshot with `xcrun simctl io <udid> screenshot`, after `xcrun simctl ui <udid> appearance light|dark`. Boot
a simulator of your own (`xcrun simctl create`) and delete it afterwards. Don't open simulator panes on a
scratch daemon against a device another session is using.

## Android without a computer

The Android app has the same demo (`android/app/src/main/kotlin/com/homegrownsw/hammock/demo/DemoDaemon.kt`, a
`DaemonStandIn`, with its made-up content in `DemoContent.kt`). Its hooks are intent extras, read by a debug build
(`com.homegrownsw.hammock.dev`) only:

```sh
adb shell am start -n com.homegrownsw.hammock.dev/com.homegrownsw.hammock.MainActivity \
  --ez sample true --ez longChat true
```

| Extra | What it does |
| --- | --- |
| `--ez sample true` | Open straight into the demo, as the pairing screen's "See a demo". The others need it. |
| `--ez longChat true` | As `HMK_DEBUG_LONG_CHAT=1`: the polish agent's long conversation, with a page before it, and Claude-like replies. |
| `--ez newChat true` | As `HMK_DEBUG_NEW_CHAT=1`: the polish agent's conversation starts empty. |
| `--es pane <id>` | Open that pane, as tapping its notification does: `--es pane p_polish` for the demo's polish agent. |
| `--es macTheme <name>\|custom` | As `HMK_DEBUG_MAC_THEME`: the sample Mac's theme. |
| `--es hammockKey 1\|agent` | As `HMK_DEBUG_HMK_KEY`: the Mac's Hammock Key, this phone an approver, and a request waiting (`agent`: an agent's ssh login). |
| `--es link down\|drop[:<s>]` | As `HMK_DEBUG_LINK`: the demo's computer can't be reached, never or from a moment after the app has its state. |
| `--es pair '<hammock://pair/… link>'` | Pair with that link at once, without the confirmation, whether the app is starting or already open (`android/scripts/dev-daemon.sh` prints one). |
| `--es updateFeed <url>` | Check that feed for updates now, as a release build checks homegrownsw.com/hammock/android.json once a day (`UpdateCheck.kt`): a higher build in it is offered. Debug builds allow plain HTTP to this Mac, so serve one at `http://10.0.2.2:<port>/` (the emulator) or over `adb reverse`. |

`DemoDaemon(pace = …)` scales its delays; its JVM tests (`./gradlew :app:testDebugUnitTest --tests '*Demo*'`) run it
fast. Not ported yet: `HMK_DEBUG_REPLAY`, `HMK_DEBUG_TAKE` and `HMK_DEBUG_SCREENSHOT`.

### Browser and simulator panes from Android

`android/scripts/dev-daemon.sh` runs `bin/hmkd`, with no app to host browser or simulator panes. For those, run a
copy of this checkout's β signed ad hoc, whose daemon then takes `--dev-pairing` (a team-signed one refuses it, and
its hosts refuse an ad hoc daemon):

```sh
just mac-dev
dir=$(mktemp -d /tmp/hmkb.XXXXXX) app="$dir/Hammock β adhoc.app"   # short: socket paths
cp -R "dist/Hammock β <branch>.app" "$app" && codesign --force --deep -s - "$app"
HMK_DIR=$dir "$app/Contents/MacOS/hmkd" --dir "$dir" --remote --remote-listen 127.0.0.1:7498 --dev-pairing &
open -n --env HMK_DIR="$dir" "$app"          # after the daemon, so it uses that one
adb reverse tcp:7498 tcp:7498
HMK_DIR=$dir "$app/Contents/Helpers/hmk" pair --json | jq -r .link   # then --es pair, above
```

From an emulator, pair with `android/scripts/dev-daemon.sh pair "$dir" --emulator` instead (no `adb reverse`): its
link names `10.0.2.2`, the emulator's way to this Mac's loopback, which UDP reaches too, so video comes over QUIC.
Over `adb reverse` (TCP only) QUIC can't connect, and the video falls back to in band after a few seconds.

Unset a pane's `HMK_SOCKET` first. Against that `HMK_DIR`, `hmk group new NAME`, then `hmk browser URL` or
`hmk sim DEVICE`, opens a pane to watch from the phone. The stream logs to logcat under `HammockVideo`.
