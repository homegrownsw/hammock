---
title: The TUI
description: hmk tui, Hammock's workspace in a terminal, with mouse support and a Ctrl-B prefix, for SSH sessions and Linux.
---

`hmk tui` shows your Hammock workspace full-screen in a terminal: the same Groups, tabs and panes as the Mac app, live, driven with the mouse or a Ctrl-B prefix like tmux's. Use it over SSH, on [Linux](/hammock/docs/install/linux/), where there's no app, or in a terminal you prefer.

```sh
hmk tui
```

Quit with ⌃B q; your panes keep running.

## The layout

The sidebar lists your Groups, tabs and agents' states, with **Needs You** at the bottom; the selected tab fills the rest. Click, drag and right-click as you would in the app. Drag in a pane to copy text, holding Shift in programs that use the mouse themselves.

Browser and simulator panes show live pictures in terminals that can draw them (Ghostty and Kitty). Diff panes show a summary: review diffs in the Mac app.

## The palette and Settings

⌃B k opens the command palette. ⌃B s opens the TUI's Settings: tab layout, animations, Nerd Font icons, sidebar width, the editor, and which [kinds of pane](/hammock/docs/panes/#turning-a-kind-of-pane-off) Hammock offers. They win over the config file; every key is in the [config reference](/hammock/docs/reference/config/#tui).

## Editor panes

The TUI draws editor panes itself, without syntax colours, and edits the same buffer as the Mac and iPhone apps ([keys below](#keys)). To use `$VISUAL` or `$EDITOR` instead, set `editor-tui-external = true`: focusing an editor pane opens the file there, and Hammock takes each save.

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
