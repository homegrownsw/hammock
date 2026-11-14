---
title: Groups, tabs and panes
description: How Hammock organises your work into Groups, tabs and panes, and how to arrange, float, move and close them.
---

Everything in Hammock lives in one workspace, and the Mac app, the iPhone app, [the TUI](/hammock/docs/tui/) and the [`hmk` CLI](/hammock/docs/cli/) all show the same one.

- A **Group** is a project: a set of tabs, usually opened in one folder. Groups are listed in the sidebar.
- A **tab** holds one or more panes, split side by side or floating over each other.
- A **pane** is a terminal, an agent, or one of the [other kinds of pane](/hammock/docs/panes/): a browser, a simulator, a log, an editor or a diff.

This page uses the Mac app's default shortcuts. You can change any of them: see [Keyboard shortcuts](/hammock/docs/reference/keyboard-shortcuts/).

## Panes keep running

A background server, the daemon, owns your panes, and each pane runs in a small process of its own. The window is only a view of them. Close it, quit Hammock, update it or let it crash, and your shells, dev servers, builds and agents carry on. Open Hammock again and everything is where you left it.

The Mac app starts the daemon when it isn't running. To keep it running from login, turn on Settings ▸ General ▸ Start the server at login.

After a restart or a log-out, Hammock opens your Groups and tabs as they were. Each pane starts again in its folder, with its earlier output shown above, and agents resume the sessions they were in. A command that had already ended isn't run again.

## Groups

- **New Group** (⌘N) makes an empty Group. **New Group in Directory…** (⇧⌘N) makes one for a folder, named after it.
- **Configure Group…** (⌥⌘G) changes a Group's name, icon, picture and folder.
- Move to the next or previous Group with ⌥⌘↓ and ⌥⌘↑. Drag a Group in the sidebar to reorder it, or right-click it for Move Up and Move Down.

```sh
hmk group new api --folder ~/Developer/api
hmk group rename api "API server"
```

### A Group's folder

Each Group has a folder its new tabs open in. It follows you: when you `cd` somewhere and work there, new tabs in that Group open in the folder you last worked in. To set it yourself, use File ▸ Change Group Folder…, the folder row in Configure Group…, or:

```sh
hmk group folder api ~/Developer/api
```

### Group icons

A Group shows an icon beside its name in the sidebar. It can be:

- an **emoji**, set in Configure Group… or with `hmk group icon GROUP 🚀`;
- a **picture**, chosen in Configure Group… or with `hmk group icon GROUP --image logo.png`, which Hammock keeps on this Mac;
- the project's own **`.hammock/icon.png`** (or `.jpg`) in the Group's folder, which shows when you haven't set a picture. Commit one to a repository and everyone who opens it in Hammock sees it.

An emoji wins over a picture while it's set. To hide icons on Group headings altogether, turn off Settings ▸ Appearance ▸ Group icons (`group-icons = false`).

## Tabs

- **New Tab** (⌘T) opens a terminal in the Group you're in. The **+** button in the toolbar opens a menu of everything a tab can hold: a terminal, an agent, a browser, a simulator, a log, an editor or a diff.
- **New Agent Tab…** (⌥⌘N) starts a coding agent. See [Agents](/hammock/docs/agents/overview/).
- Switch with ⌘1 to ⌘9, or ⇧⌘] and ⇧⌘[ for the next and previous tab.
- **Rename Tab…** (⇧⌘R). Unnamed tabs are named after what's running in them.

To move a tab to another Group, drag it there in the sidebar, or right-click it and choose Move to Group.

To bring two tabs together, right-click one and choose **Merge into Tab**: its panes go beside the other tab's, or below them. To take a pane out into a tab of its own, use Pane ▸ Move Pane to New Tab. From a terminal:

```sh
hmk tab merge TAB INTO          # beside; --down to go below
hmk tab break PANE              # a pane out into its own tab
```

## The sidebar

The sidebar lists your Groups with their tabs. Show or hide it with ⌃⌘S. Settings ▸ Appearance ▸ Layout offers three layouts (`sidebar-layout`):

- **Vertical**: each Group's tabs listed under it. The default.
- **Compact**: the same, with smaller rows, so more tabs fit.
- **Horizontal**: Groups in the sidebar, and the selected Group's tabs in a tab bar along the top.

A tab running an agent shows the agent's state in its icon: working, waiting for you, finished, or stopped with an error. With Settings ▸ Appearance ▸ Agent status set to At the end of the row (`tab-status = trailing`), the state moves there and the agent's logo takes the icon's place. [Agents](/hammock/docs/agents/overview/) explains each state.

At the bottom of the sidebar, **Needs You** lists agents waiting on you: questions, permission requests and errors. Below it, **Finished** lists agents that are done and want nothing. Turn the Finished list off with Settings ▸ Appearance ▸ Show finished agents (`sidebar-finished = false`).

A collapsed Group hides its tabs. To keep tabs with an active agent showing in a collapsed Group, turn on Show active agents in collapsed Groups (`collapsed-group-agents = true`).

## Splits

- **Split Right** (⌘D) and **Split Down** (⇧⌘D) split the focused pane. Split Left and Split Up are in the Pane menu. The toolbar's split buttons open a menu, so you can split with a browser, a simulator or another kind of pane instead of a terminal.
- Move between panes with ⌘] and ⌘[.
- Drag the line between two panes to resize them.
- **Zoom Pane** (⇧⌘↩) fills the tab with the focused pane. Press it again to put it back.

```sh
hmk split PANE                  # to the right; --down, --left or --up
hmk split PANE --down -- npm run dev
```

## Floating panes

A floating pane sits over the tab's splits, like a card: handy for a dev server's output, or a quick shell you don't want in your layout.

- **New Floating Pane** (⇧⌘F) opens a terminal floating over the tab.
- **Float or Dock Pane** (⇧⌘J) lifts the focused pane out of the splits, or puts a floating one back.
- Drag a floating pane by its header to move it. Hold ⌥ while you drag to dock it into the split under the pointer.
- **Minimise Floating Pane** (⌃⌘M) puts it on the tab's **shelf**, along the bottom of the tab, still running. Click it there to bring it back.
- **Show or Hide Floating Panes** (⌘J) minimises every floating pane in the tab, or brings them all back.

```sh
hmk split PANE --float -- tail -f server.log
hmk float PANE --at 0.5,0.5,0.45,0.45   # where, in fractions of the tab
hmk dock PANE
hmk minimise PANE                        # and hmk restore PANE
hmk floats TAB                           # minimise or restore them all
```

## The command palette

**Command Palette…** (⌘K) finds any action by name: new tabs and panes, Groups, layouts, settings and more. Each action shows its shortcut, if it has one. **Find Session…** (⇧⌘O) searches agents' past sessions so you can resume one.

## Closing and reopening

⌘W closes the focused pane. On a tab's last pane it closes the tab, and on a Group's last tab it leaves the Group empty, ready for a new tab. To have it close the Group too, turn off Settings ▸ General ▸ Keep a Group when its last tab closes (`keep-empty-groups = false`).

If an agent in the pane or tab is at work, Hammock asks first (Settings ▸ Agents ▸ Warn before closing an agent that's at work, `confirm-close-busy-agent`).

**Reopen Closed Tab or Pane** (⇧⌘T) brings back the last tab or pane you closed, and its agent resumes its session. From a terminal, `hmk reopen` does the same.

## Notifications

Hammock tells you when an agent:

- is waiting for your input,
- asks for permission,
- finishes,
- hits an error,
- replies to your [review comments](/hammock/docs/panes/#diff) (off by default).

Choose which in Settings ▸ Notifications. Show notifications sets where they appear: in the Hammock window while it's in front and as macOS notifications otherwise (the default), always as macOS notifications, or always in the window (`notify-style = auto`, `system` or `in-app`).

Hammock doesn't notify you about the tab you're looking at, unless you turn on Also notify for the tab I'm looking at. The Dock icon counts the agents waiting for you (Show a count on the Dock icon). Each kind of notification has its own sound, also in Settings ▸ Notifications.

With an iPhone paired, notifications reach it when you're away from your Mac: see [Hammock on iPhone](/hammock/docs/iphone/). Every notification setting is in the [config reference](/hammock/docs/reference/config/#notifications).
