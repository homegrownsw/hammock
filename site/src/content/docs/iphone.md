---
title: Using the iPhone app
description: Follow your agents, answer them, and type in your terminals from your iPhone.
---

The iPhone app shows the same Groups, tabs and panes as your Mac, live. Everything still runs on the Mac, so you can follow an agent, answer it, or type in a terminal when you're away. To set it up, see [Installing on iPhone](/hammock/docs/install/iphone/).

## When the Mac can't be reached

While the iPhone is connecting to your Mac, or has lost it, a bar under each screen's title says so, and after a few seconds that it can't reach it, with how long ago what's shown was last updated. It keeps trying; **Try Again** tries now, and so does pulling down on Sessions or Groups. **Allow** and **Deny** wait until it's back.

## Sessions

Sessions lists your agents, most pressing first:

- **Needs You**: agents waiting for an answer or stopped by an error, permission requests, and people asking to join a Group you share.
- **Ready for You**: automations' runs that finished while you were away.
- **Working** and **Recent**: agents at work, then those that finished in the last 48 hours.
- **Coming Up**: your next automations, with **Run** (**Run Early** when one is due within 12 hours, in place of that run).
- **Usage Limits**, if you've chosen any in Settings.

Tap a session to open it, or swipe left to close it. Touch and hold one to see its pane, live, with **Allow** and **Deny** when it's asking permission and the pane's actions, such as **Interrupt**; tap the preview to open it. The **+** button starts an agent, and the clock button finds a past session to resume.

## Groups

Groups shows each Group with its tabs, as on your Mac. Each Group's header has buttons for a new tab (**Terminal** or **Agent…**) and a menu to edit, delete or leave it. The button at the top right makes a new Group.

Tap a tab to open it, swipe left to close or rename it, or touch and hold it to see its pane, live, with the rest of its actions. **Edit** at the top reorders tabs.

A tab with several panes opens on the one you last had open there. A strip along the bottom has a pill for each pane, its floating ones after a divider: tap one to switch, or swipe sideways across the pane (beside a simulator's screen or a browser's page, not on it). A pill turns orange when its agent is waiting on you, and shows a dot when one has finished. Touch and hold a pill for more, such as **Interrupt**, **Put Agent to Sleep** or **Move to New Tab**.

The button at the left of the strip opens Exposé: the tab laid out as on the Mac, each pane live. Tap one to go to it.

If you've [linked computers](/hammock/docs/remote/#linked-computers), Groups shows every computer's Groups, with chips at the top to pick one.

## Terminals

Tap a terminal to bring up the keyboard, with a row above it of the keys a phone lacks: **esc**, **tab**, **ctrl** and **⌥** (which apply to the next key), the arrows and delete. Swipe the row for symbols such as `|` and `~`, Home and End. Drag to scroll back. Touch and hold to select a word, keep dragging or move its handles to select more, then **Copy**; **Select All** selects the screen, and **Paste** pastes. Touch and hold a terminal's pill for **Copy Screen**. In programs that use the mouse, a tap is a click. A hardware keyboard works too, and ⌥ with a letter sends Esc and the letter, as in a shell's ⌥B and ⌥F. While the iPhone can't reach your Mac, keys do nothing and **Offline** shows above the keyboard: tap it to try again. When a terminal's program fails, a bar along its bottom has **Run Again** (or **Resume**, for an agent) and **Close**.

Panes keep the size your Mac gave them, shrunk to fit if they're wider than the screen. A smaller font fits more columns: pinch a terminal to change the size, for every terminal, or set it in Settings.

## The agent view

For [agents that support it](/hammock/docs/reference/agents/), a pane can show the conversation as a native view instead of the terminal: messages, the tools it ran and the changes it made. Choose **Conversation** or **Terminal** in the … menu, and Settings ▸ Agent View chooses which agent tabs open in.

Tap a file the agent mentions to see it, or one of its running tasks or subagents to watch it. **Latest** takes you back to the end.

**Replying.** Type in the box at the bottom and tap the arrow to send. While the agent works and the box is empty, the same button stops it. In Claude Code, a message starting with `!` runs as a shell command. Type `/` for the agent's commands, or `@` for a file in the Group's folder, and tap one to put it in. With a hardware keyboard, ↑ in the empty box brings back what you sent before. Tap the context gauge beside the model to compact or clear the conversation.

**Attachments.** Tap **+** to attach pictures or videos. Touch and hold one to take it out again.

**Dictation.** Tap the microphone to dictate, and again to stop.

**Questions.** An agent's question appears as a card with its options to tap. **Answer in Terminal** switches to the terminal for anything the card can't answer. With a hardware keyboard, press an option's number, ← and → to move between questions, and Esc for the terminal.

## Permission requests and notifications

An agent waiting for permission appears at the top of Sessions with what it's asking to do, and **Allow**, **Deny** and **Open**. When that's more than fits, **Review** opens the pane in place of **Allow**. For a scheduled Claude Code run, touch and hold **Allow** for **Always Allow for This Schedule**. Hammock's own requests, such as an agent asking to type in a pane it didn't open, appear there too.

Notifications are sent while you're away from your Mac. Choose which in Settings ▸ Notify Me When: someone asks to join, or an agent needs you, finishes or hits an error. You can answer Hammock's requests, join requests and an agent's permission prompt (**Allow** or **Deny**) from the notification itself, once your iPhone is unlocked. If the agent's prompt has changed, or can only be answered in the agent view, nothing is pressed and the notification says so: tap it to open the pane. Hammock's icon and the Sessions tab count what's waiting on you: agents that need your input, and permission, join and Hammock Key requests. Notifications are encrypted on your Mac, and only your iPhone can read them. See [Permissions](/hammock/docs/permissions/#on-your-iphone) for allowing them.

## Live Activities and the Needs You widget

Turn on **Live Activities** in Settings to see your agents on the Lock Screen and in the Dynamic Island, in your theme's colours, while you're away from your Mac: the agent that needs you most (else the one most recently at work), its Group and tab, what it's doing, for how long, and how many others are working. It starts while an agent is working once you've used Hammock on your iPhone since you last used your Mac (not just for your Mac being locked), and ends when you're back at your Mac, or soon after the last agent stops. When the agent is waiting on a permission prompt, **Allow** and **Deny** answer it once your iPhone is unlocked. Apple shows a Live Activity's content as it's sent, so it can't be encrypted: with them on, agent names, Groups and what they're doing pass through Hammock's push relay unencrypted.

The **Needs You** widget, on the Home Screen or the Lock Screen, shows how many things are waiting on you and the first of them; tap one to open its pane. It doesn't need Live Activities on.

## Approving the Hammock Key

If you've let your iPhone approve your Mac's [Hammock Key](/hammock/docs/remote/#the-hammock-key), a request to sign arrives as a notification. **Approve…** asks for Face ID; **Don't Allow** refuses.

## Browser, simulator, editor, diff and files panes

- **Browser panes** show the page live from the Mac, sized for your iPhone until someone uses it on the Mac; tap it to make it fit again. Tap to click, drag to scroll, pinch to zoom. The bar at the bottom has **Back**, **Forward**, **Reload**, **Type** and **Open in Safari**.
- **Pin Notes**, in a browser or simulator pane's top bar or its pill's menu, shows an agent what you mean: touch and hold where something should change, write a note, and **Send to** sends every pin with a screenshot (see [Design notes](/hammock/docs/panes/#design-notes)).
- **Simulator panes** show the Mac's iOS simulator or Android emulator, to tap, swipe and type into. Touch and hold **Type** to send your iPhone's clipboard to the device, or copy the device's to your iPhone. An Android phone's Back, Home and Recents are in the bar at the bottom. On an Apple TV or Android TV, tap for Select, touch and hold for a long Select, and swipe to move around; **Back**, **TV**, **Play/Pause**, **Volume** and **Type** are in the bar at the bottom (touch and hold **Volume** for Volume Down, Mute and Siri).
- **Editor panes** open the file for editing. **Save**, **Find** and **Go to Line** are in the menu, and for a Markdown file, **Show Preview**.
- **Diff panes** show the diff in one column; on an iPad with room, side by side too. Comments and replies work as on the Mac.
- **Files panes** show the folder's tree: tap a file to open it in a pane beside it, and touch and hold for Rename, Move to…, Duplicate, Move to Trash and more. **New Files Tab** is in a tab's menu.
- **Log panes** show their entries, with a switch to the raw terminal.

## Devices

Devices lists the computers this iPhone is paired with: tap one to switch to it, or swipe left to forget it. **Add Computer** pairs another. A Mac with screen sharing on appears under **Show Screen**: see [Screen sharing](/hammock/docs/screen-sharing/).

## Settings

The iPhone's settings are its own, not your Mac's: the theme, fonts and sizes for the agent view, terminal and editors, attachments and dictation, which usage limits and notifications you see, and **Privacy**. **Require Face ID** there locks Hammock when you come back to it.
