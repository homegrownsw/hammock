---
title: Using the iPhone app
description: Follow your agents, answer them, and type in your terminals from your iPhone.
---

The iPhone app shows the same Groups, tabs and panes as your Mac, live. Everything still runs on the Mac: the phone is another window onto it, so you can follow an agent, answer it, or type in a terminal when you're away. To set it up, see [Installing on iPhone](/hammock/docs/install/iphone/).

The app has four tabs along the bottom: **Sessions**, **Groups**, **Devices** and **Settings**. The title at the top is the name of the computer you're looking at.

## Sessions

Sessions lists your agents by what's happening, most pressing first:

- **Needs You**: agents waiting for an answer or stopped by an error, permission requests, and people asking to join a Group you share. The Sessions tab's badge counts them.
- **Ready for You**: scheduled task runs that finished while you were away.
- **Working** and **Recent**: agents at work, then those that have finished. Recent shows the last 48 hours; **Show More…** shows the rest.
- **Coming Up**: your next scheduled tasks, with **Run Now** and **See All**.
- **Usage Limits**, if you've chosen any in Settings.

Tap a session to open it. Swipe left on one to close it; if that would stop something, it asks first.

The **+** button starts an agent, in a Group you choose. The clock button finds a past session to resume.

## Groups

Groups shows everything as it's organised on your Mac: each Group with its tabs. Tap a Group's name to collapse or expand it (the phone remembers this itself, apart from the Mac). Each Group's header has buttons to open a new tab (**Terminal** or **Agent…**) and a menu with **Edit Group…** and **Delete Group**, or **Leave Group** for one someone shared with you.

On a tab:

- Tap it to open it.
- Swipe left for **Close** and **Rename**.
- Touch and hold it for the rest of its actions.
- Tap **Edit** at the top to drag tabs into a new order.

The button at the top right makes a new Group.

A tab with several panes opens as an overview of its panes, laid out as they are on the Mac. Tap one to open it. Touch and hold one for more: **Interrupt** an agent at work, continue its session in another agent, **Put Agent to Sleep** or **Wake Agent**, **Copy Session Link**, **Move to New Tab** or **Close Pane**.

If you've [linked computers](/hammock/docs/remote/#linked-computers), Groups shows every computer's Groups, with a row of chips at the top to show all of them or just one.

## Terminals

A terminal pane fills the screen. Tap it to bring up the keyboard, and tap again to put it away. Above the keyboard is a row of keys a phone keyboard lacks: **esc**, **tab**, **ctrl**, the four arrows, delete, and a button to hide the keyboard. The arrows and delete repeat while you hold them.

- Drag up and down to scroll back.
- Touch and hold to paste.
- In programs that use the mouse, a tap is a click and a drag scrolls them.
- A hardware keyboard works too.

Panes keep the size your Mac gave them. One wider than your screen shrinks to fit; a smaller font in Settings fits more columns.

## The agent view

For an agent that supports it, a pane can show its conversation as a native view instead of its terminal: messages, replies, the tools it ran and the changes it made. The switch at the top of the screen goes between the agent view and the terminal. Settings ▸ Agent View chooses which one agent tabs open in, and each session remembers your choice once you switch it. [Supported agents](/hammock/docs/reference/agents/) lists which agents have one.

Scroll up to read earlier messages. When you're scrolled up, **Latest** takes you back to the end. Tap a file the agent mentions to see it, and tap one of its running tasks or subagents to watch it.

**Replying.** Type in the box at the bottom and tap the arrow to send. While the agent works and the box is empty, the same button is a stop button, which interrupts it. In Claude Code, a message starting with `!` runs as a shell command, and the box says so.

**Attachments.** Tap **+** to attach pictures or videos, with **Photos…** or **Choose File…**. They appear in your message as chips; touch and hold one to take it out again. Settings ▸ Attachments ▸ Picture size chooses how big pictures are sent.

**Dictation.** Tap the microphone to dictate, and tap it again to stop. It uses the iPhone's on-device speech recognition, and the first time it may have to download a speech model. Turn the button off with Settings ▸ Dictation ▸ Dictation Button.

**Questions.** When an agent asks you a question, it appears as a card at the bottom of the agent view, with its options to tap. **Answer in Terminal** switches to the terminal for anything the card can't answer.

## Permission requests and notifications

When an agent is waiting for permission, it appears at the top of Sessions with **Allow**, **Deny** and **Open**, so you can answer without opening it. For a scheduled Claude Code run, touch and hold **Allow** for **Always Allow for This Schedule**.

Hammock's own requests (an agent asking to type in a pane it didn't open, say, or something only you may do, such as pairing or linking) appear there too, with **Allow**, **Deny** and sometimes **Always**.

Notifications are sent while you're away from your Mac. Choose which ones you get in Settings ▸ Notify Me When:

- Someone asks to join
- An agent needs you
- An agent finishes
- An agent hits an error

Tap a notification to open its session. Hammock's own requests can be answered from the notification itself (**Allow** or **Deny**), and so can join requests (**Let View**, **Decline**, or **Let Drive…**, which opens the app). Notifications are encrypted on your Mac, and only your iPhone can read them. iOS asks whether Hammock may send them: see [Permissions](/hammock/docs/permissions/#on-your-iphone).

## Approving the Hammock Key

If you've let your iPhone approve your Mac's [Hammock Key](/hammock/docs/remote/#the-hammock-key), a request to sign reaches it as a notification. **Approve…** opens the app and asks for Face ID; **Don't Allow** refuses. In the app you can also approve for a few minutes in that pane. Each approval is signed by a key in your iPhone's own Secure Enclave, so only you, holding the phone, can approve.

## Browser, simulator, editor and diff panes

- **Browser panes** show the page as live video from the Mac. Tap to click, drag to scroll, pinch to zoom, and touch and hold to open the page's own menu. The bar at the bottom has **Back**, **Forward**, **Reload**, **Type** (the keyboard, to type into the page) and **Open in Safari**.
- **Simulator panes** show the Mac's iOS simulator. Tap, swipe and type into it, with its Home and Lock buttons.
- **Editor panes** open the file for editing, with a bar above the keyboard for the keys a phone lacks. **Save**, **Find** and **Go to Line** are in the menu.
- **Diff panes** show the diff in one column, with the files in a sheet. Comments and replies, and sending them to the agent, work as they do on the Mac.
- **Log panes** show their entries, with a switch to the raw terminal.

## Devices

Devices lists the computers this iPhone is paired with. It shows one at a time: tap one to switch to it. Swipe left on one to forget it. **Add Computer** pairs another: scan the code your Mac shows, or paste its link.

If a Mac has screen sharing turned on, it appears under **Show Screen**. Tap it to see and control that Mac's whole screen. See [Screen sharing](/hammock/docs/screen-sharing/).

**This iPhone ▸ Name** is what your computers call this phone.

## Settings

The iPhone's settings are its own, not your Mac's:

- **Appearance**: the theme (by default, the same as your Mac's), and light or dark.
- **Agent View**: whether agent tabs open in the agent view or the terminal.
- **Text**: the agent view's font, size, line spacing, code size, and when code wraps, with a preview.
- **Attachments** and **Dictation**, as above.
- **Terminal**: the terminal's font, size and line spacing.
- **Editors and Diffs**: their font.
- **Privacy**: **Require Face ID** when you come back to Hammock after the time in **Lock After**, and hide it in the app switcher. With it on, answering from a notification opens the app and asks first.
- **Usage Limits**: which agents' limits show at the bottom of Sessions.
- **Notify Me When**: the notifications above.
