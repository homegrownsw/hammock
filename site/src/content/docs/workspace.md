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

The Hammock Server, running in the background, owns your panes; the window is only a view of them. Quit Hammock, update it, or even crash it, and your shells, servers and agents carry on. After a restart or log-out, your Groups and tabs come back as they were: each pane starts again in its folder with its earlier output, and agents resume their sessions.

## Groups

**New Group** (⌘N), or **New Group in Directory…** (⇧⌘N) for one named after a folder. **Configure Group…** (⌥⌘G) changes its name, icon and folder. ⌥⌘↓ and ⌥⌘↑ move between Groups; drag one in the sidebar to reorder it.

```sh
hmk group new api --folder ~/Developer/api
```

### A Group's folder

New tabs open in the Group's folder, which follows you: it's the folder you last worked in. Set it yourself with File ▸ Change Group Folder… or `hmk group folder`.

### Group icons

A Group's icon is an emoji or picture set in Configure Group… (or `hmk group icon`), or, if you haven't set one, the project's own `.hammock/icon.png` (or `.jpg`): commit one and everyone who opens the repository in Hammock sees it. Settings ▸ Appearance ▸ Group icons hides them.

## Tabs

- **New Tab** (⌘T) opens a terminal; the toolbar's **+** opens any other kind of tab.
- **New Agent Tab…** (⌥⌘N) starts a coding agent. See [Agents](/hammock/docs/agents/overview/).
- Switch with ⌘1 to ⌘9, or ⇧⌘] and ⇧⌘[.
- **Rename Tab…** (⇧⌘R). Unnamed tabs are named after what's running in them.

Drag a tab to another Group in the sidebar to move it. Right-click a tab and choose **Merge into Tab** to put its panes beside another tab's; Pane ▸ Move Pane to New Tab does the reverse.

## The sidebar

The sidebar (⌃⌘S) lists your Groups with their tabs, vertically, compactly, or with the tabs along the top (Settings ▸ Appearance ▸ Layout). A tab running an agent shows its state in its icon: [Agents](/hammock/docs/agents/overview/) explains each one.

At the bottom, **Needs You** lists agents waiting on you, and **Finished** those that are done.

## Splits

- **Split Right** (⌘D) and **Split Down** (⇧⌘D). The toolbar's split buttons split with other kinds of pane.
- Move between panes with ⌘] and ⌘[, and drag the line between them to resize.
- **Zoom Pane** (⇧⌘↩) fills the tab with the focused pane, and puts it back.

```sh
hmk split PANE --down -- npm run dev
```

## Floating panes

A floating pane sits over the tab's splits, handy for a dev server's output or a quick shell.

- **New Floating Pane** (⇧⌘F), or **Float or Dock Pane** (⇧⌘J) to lift the focused pane out or put it back.
- Drag one by its header to move it; hold ⌥ to dock it into the split under the pointer.
- **Minimise Floating Pane** (⌃⌘M) puts it, still running, on the tab's **shelf** along the bottom. **Show or Hide Floating Panes** (⌘J) does it for them all.

```sh
hmk split PANE --float -- tail -f server.log
```

## Windows of their own

Right-click a Group, tab or pane and choose **Open in New Window** (or use the Window menu) to show it in a window of its own, for instance to share just that on a call. It's the same Group, tab or pane, not a copy: what you do there happens in your workspace, and closing the window closes nothing. A tab or pane fills its window; a Group's window has only that Group's tabs, with its own selected tab.

A pane showing in two windows takes the size of the one you were in last. A browser page shows in one window at a time: click it in the other to bring it there.

```sh
hmk window open tab TAB
```

## The command palette

**Command Palette…** (⌘K) finds any action by name, with its shortcut. **Find Session…** (⇧⌘O) searches agents' past sessions so you can resume one.

## Closing and reopening

⌘W closes the focused pane, and with it an emptied tab. Hammock asks first if an agent there is at work. **Reopen Closed Tab or Pane** (⇧⌘T, or `hmk reopen`) brings it back, and its agent resumes its session.

## Notifications

Hammock tells you when an agent is waiting for you, asks for permission, finishes, hits an error, or replies to your [review comments](/hammock/docs/panes/#diff). Choose which, where they appear and their sounds in Settings ▸ Notifications. With an iPhone paired, they reach it when you're away: see [Hammock on iPhone](/hammock/docs/iphone/). Every setting is in the [config reference](/hammock/docs/reference/config/#notifications).
