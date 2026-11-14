---
title: The TUI
description: hmk tui, Hammock's workspace in a terminal, with mouse support and a Ctrl-B prefix, for SSH sessions and Linux.
---

`hmk tui` shows your Hammock workspace full-screen in a terminal: the same Groups, tabs and panes the Mac app shows, live. It works with the keyboard, through a Ctrl-B prefix like tmux's, and with the mouse.

```sh
hmk tui
```

It talks to the daemon on the machine you run it on. It's useful:

- **Over SSH**, to pick up your panes and agents from another computer.
- **On Linux**, where there's no Mac app: a server running Hammock's daemon is driven with `hmk` and the TUI. See [Install on Linux](/hammock/docs/install/linux/).
- **In a terminal you prefer**, alongside or instead of the Mac app.

Quit with ⌃B q. Your panes keep running, as they do when you quit the Mac app.

## The layout

A sidebar on the left lists your Groups and their tabs, with each agent's state, and **Needs You** at the bottom for agents waiting on you. The tab you select fills the rest, with its panes split as they are in the app, floating panes included.

With the mouse you can click tabs and Groups, drag tabs to reorder them, drag the dividers between panes and the sidebar's edge, and right-click for menus. In a pane, drag to select text (it's copied) and double-click a word. Hold Shift to select in programs that use the mouse themselves. Right-click a Needs You row to allow or deny a request.

Browser and simulator panes show live pictures of the page or device in terminals that can draw them (Ghostty and Kitty), and clicks go through to them. Elsewhere they show a placeholder. Diff panes show a summary: review diffs in the Mac app.

## The palette and Settings

⌃B k opens the command palette: every action, with its key if it has one. Type to find one.

⌃B s opens the TUI's Settings, kept across runs:

- **Tabs:** vertical, in the sidebar, or horizontal, in a tab bar.
- **Animations**, **Agents in collapsed Groups**, **Program icons** (Nerd Font glyphs) and **Sidebar width**.
- **Put idle agents to sleep**, the editor's settings, and which [kinds of pane](/hammock/docs/panes/#turning-a-kind-of-pane-off) Hammock offers.

## Editor panes

The TUI draws editor panes itself, with line numbers and git marks but no syntax colours, and edits the same buffer the Mac and iPhone apps do. Its keys are in the [list below](#keys).

To edit in your own editor instead, set `editor-tui-external = true` (or Editor: open in $EDITOR in the TUI's Settings). Focusing an editor pane then opens it in `$VISUAL` or `$EDITOR` (`vi` if neither is set), as `git commit` does. Each time the editor writes the file, Hammock takes the change, and the TUI comes back when the editor exits. Edits made on your other devices meanwhile show in your editor only when it reloads the file.

## Config

The TUI reads Hammock's config file, `~/.config/hammock/config`, and picks up changes within a couple of seconds. Besides the editor keys, two are its own:

```ini
# Snap instead of animating.
tui-animations = false
# Program icons as Nerd Font glyphs (needs a Nerd Font).
tui-nerd-font = true
```

What you choose in the TUI's Settings wins over the file. Every key is in the [config reference](/hammock/docs/reference/config/#tui).

## Keys

Press ⌃B, let go, then the key. ⌃B ? shows this list in the TUI.

| Keys | Action |
|---|---|
| ⌃B k or ⌃B Space | Command palette |
| ⌃B ? | Keyboard shortcuts |
| ⌃B c or ⌃B t | New tab |
| ⌃B b | New browser tab |
| ⌃B S | New Group |
| ⌃B . | Configure Group |
| ⌃B , | Rename tab |
| ⌃B s | Settings |
| ⌃B O | Find a session |
| ⌃B @ | Scheduled tasks |
| ⌃B e | Quick Open (a file) |
| ⌃B E | New editor tab |
| ⌃B % or ⌃B \| | Split right |
| ⌃B " or ⌃B - | Split down |
| ⌃B x | Close pane |
| ⌃B T | Reopen closed tab or pane |
| ⌃B f | New floating pane |
| ⌃B F | Float or dock pane |
| ⌃B m | Minimise floating pane |
| ⌃B j | Hide or show floating panes |
| ⌃B z | Zoom floating pane |
| ⌃B n / ⌃B p | Next / previous tab |
| ⌃B 1 to 9 | Tab 1 to 9 in this Group |
| ⌃B o | Next pane |
| ⌃B ← ↑ / ⌃B → ↓ | Previous / next pane |
| ⌃B [ / ⌃B ] | Scroll back / forward a page |
| ⌃B y | Allow the newest request, once |
| ⌃B Y | Allow the newest request, always in this Group |
| ⌃B N | Deny the newest request |
| ⌃B M | Next computer (with linked computers) |
| ⌃B ⌃B | Send ⌃B to the pane |
| ⌃B q or ⌃B d | Quit |

Drag a floating pane by its header or top border, and resize it from its other edges. Hold Alt while dragging to dock it into the split under the pointer.

### In an editor pane

| Keys | Action |
|---|---|
| ⌃S | Save |
| ⌃F | Find |
| ⌃G | Go to line |
| ⌃Z / ⌃Y | Undo / redo |
| ⌃C / ⌃X | Copy / cut (paste with your terminal) |
| ⌃A / ⌃E | Start / end of the line |
| ⌥← / ⌥→ | Back / forward a word |
| Shift with a move | Select |
| Tab / ⇧Tab | Indent / outdent |

The Mac app's shortcuts are different: see [Keyboard shortcuts](/hammock/docs/reference/keyboard-shortcuts/).
