---
title: Using the iPhone app
description: Follow your agents, answer them, and type in your terminals from your iPhone.
---

The iPhone app shows the same Groups, tabs and panes as your Mac, live. Everything still runs on the Mac, so you can follow an agent, answer it, or type in a terminal when you're away. To set it up, see [Installing on iPhone](/hammock/docs/install/iphone/).

## Sessions

Sessions lists your agents, most pressing first:

- **Needs You**: agents waiting for an answer or stopped by an error, permission requests, and people asking to join a Group you share.
- **Ready for You**: scheduled task runs that finished while you were away.
- **Working** and **Recent**: agents at work, then those that finished in the last 48 hours.
- **Coming Up**: your next scheduled tasks, with **Run Now**.
- **Usage Limits**, if you've chosen any in Settings.

Tap a session to open it, or swipe left to close it. The **+** button starts an agent, and the clock button finds a past session to resume.

## Groups

Groups shows each Group with its tabs, as on your Mac. Each Group's header has buttons for a new tab (**Terminal** or **Agent…**) and a menu to edit, delete or leave it. The button at the top right makes a new Group.

Tap a tab to open it, swipe left to close or rename it, or touch and hold it for the rest of its actions. **Edit** at the top reorders tabs.

A tab with several panes opens as an overview, laid out as on the Mac. Tap a pane to open it, or touch and hold it for more, such as **Interrupt**, **Put Agent to Sleep** or **Move to New Tab**.

If you've [linked computers](/hammock/docs/remote/#linked-computers), Groups shows every computer's Groups, with chips at the top to pick one.

## Terminals

Tap a terminal to bring up the keyboard, with a row above it of the keys a phone lacks: **esc**, **tab**, **ctrl**, the arrows and delete. Drag to scroll back, and touch and hold to paste. In programs that use the mouse, a tap is a click. A hardware keyboard works too.

Panes keep the size your Mac gave them, shrunk to fit if they're wider than the screen. A smaller font in Settings fits more columns.

## The agent view

For [agents that support it](/hammock/docs/reference/agents/), a pane can show the conversation as a native view instead of the terminal: messages, the tools it ran and the changes it made. The switch at the top of the screen goes between the two, and Settings ▸ Agent View chooses which agent tabs open in.

Tap a file the agent mentions to see it, or one of its running tasks or subagents to watch it. **Latest** takes you back to the end.

**Replying.** Type in the box at the bottom and tap the arrow to send. While the agent works and the box is empty, the same button stops it. In Claude Code, a message starting with `!` runs as a shell command.

**Attachments.** Tap **+** to attach pictures or videos. Touch and hold one to take it out again.

**Dictation.** Tap the microphone to dictate, and again to stop.

**Questions.** An agent's question appears as a card with its options to tap. **Answer in Terminal** switches to the terminal for anything the card can't answer.

## Permission requests and notifications

An agent waiting for permission appears at the top of Sessions with **Allow**, **Deny** and **Open**. For a scheduled Claude Code run, touch and hold **Allow** for **Always Allow for This Schedule**. Hammock's own requests, such as an agent asking to type in a pane it didn't open, appear there too.

Notifications are sent while you're away from your Mac. Choose which in Settings ▸ Notify Me When: someone asks to join, or an agent needs you, finishes or hits an error. You can answer Hammock's requests and join requests from the notification itself. Notifications are encrypted on your Mac, and only your iPhone can read them. See [Permissions](/hammock/docs/permissions/#on-your-iphone) for allowing them.

## Approving the Hammock Key

If you've let your iPhone approve your Mac's [Hammock Key](/hammock/docs/remote/#the-hammock-key), a request to sign arrives as a notification. **Approve…** asks for Face ID; **Don't Allow** refuses.

## Browser, simulator, editor and diff panes

- **Browser panes** show the page live from the Mac. Tap to click, drag to scroll, pinch to zoom. The bar at the bottom has **Back**, **Forward**, **Reload**, **Type** and **Open in Safari**.
- **Simulator panes** show the Mac's iOS simulator, to tap, swipe and type into.
- **Editor panes** open the file for editing. **Save**, **Find** and **Go to Line** are in the menu.
- **Diff panes** show the diff in one column. Comments and replies work as on the Mac.
- **Log panes** show their entries, with a switch to the raw terminal.

## Devices

Devices lists the computers this iPhone is paired with: tap one to switch to it, or swipe left to forget it. **Add Computer** pairs another. A Mac with screen sharing on appears under **Show Screen**: see [Screen sharing](/hammock/docs/screen-sharing/).

## Settings

The iPhone's settings are its own, not your Mac's: the theme, fonts and sizes for the agent view, terminal and editors, attachments and dictation, which usage limits and notifications you see, and **Privacy**. **Require Face ID** there locks Hammock when you come back to it.
