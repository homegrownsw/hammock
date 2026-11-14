---
title: Configuration
description: Hammock's config file, themes, keyboard shortcuts, tools you can turn off, and the Settings window.
---

## The config file

Hammock's settings live in one plain text file, `~/.config/hammock/config`, in
[Ghostty's syntax](https://ghostty.org/docs/config): one `key = value` per line, and `#` for comments. Settings
writes the same file, and edits to it apply within a couple of seconds. Open it from Settings ▸ Appearance ▸ **Edit
Config File**. If it's a symlink into your dotfiles, it stays one.

[The config file reference](/hammock/docs/reference/config/) lists every key, with its default and values. For example:

```ini
app-theme = Nord
sidebar-layout = horizontal
font-family = JetBrains Mono
font-size = 14
copy-on-select = true
agent-sleep = 12h
notify-style = system
simulator-enabled = false
keybind = ctrl+cmd+b=new_browser_tab
```

### Falling back to Ghostty

If you use Ghostty, its font, cursor and paste-protection settings apply in Hammock too, unless Hammock's file sets
them.

## Themes

`app-theme` picks one of Hammock's themes, which colour the terminal, the window and the agent view together.

```sh
hmk theme              # list them
hmk theme nord         # use one
```

**Custom** sets each part on its own, such as the terminal's colours from any Ghostty theme file, with a light and a
dark one if you like:

```ini
app-theme = custom
theme = dark:Catppuccin Mocha,light:Catppuccin Latte
window-theme = Graphite
```

Add your own theme files to `~/.config/hammock/themes` (Settings ▸ Appearance ▸ **Open Themes Folder**).

## Keyboard shortcuts

Change any shortcut in Settings ▸ Keyboard. In the config file, each change is a `keybind` line, `chord=action`:

```ini
# Give an action a shortcut
keybind = ctrl+cmd+b=new_browser_tab
# Free a shortcut
keybind = cmd+j=unbind
# Take an action's shortcut away
keybind = none=new_tab
```

[Keyboard shortcuts](/hammock/docs/reference/keyboard-shortcuts/) lists every action's name and default. The TUI has
keys of its own: see [The TUI](/hammock/docs/tui/#keys).

## Turning tools off

Turn off browser, simulator, editor, diff or log panes you don't use in Settings ▸ Tools: see
[Turning a kind of pane off](/hammock/docs/panes/#turning-a-kind-of-pane-off).

## The Settings window

Open Settings with ⌘,. Its search finds any setting and jumps to it.
