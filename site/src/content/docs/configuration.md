---
title: Configuration
description: Hammock's config file, themes, keyboard shortcuts, tools you can turn off, and the Settings window.
---

## The config file

Hammock's settings live in one plain text file, `~/.config/hammock/config`, in
[Ghostty's syntax](https://ghostty.org/docs/config): one `key = value` per line, and `#` for comments. A key you
leave out takes its default.

- **Settings writes the same file.** Change something in Settings and the key appears in the file; edit the file and
  Settings follows. Edits made elsewhere apply within a couple of seconds, with no restart.
- **Dotfiles work.** If `~/.config/hammock/config` is a symlink (stow, chezmoi and the like), it stays one: Hammock
  writes to the file it points to.
- **Open it** from Settings ▸ Appearance ▸ **Edit Config File**. A new file starts with a commented header listing
  every key.

[The config file reference](/hammock/docs/reference/config/) lists every key, with its default and values.

### Falling back to Ghostty

If you use Ghostty, Hammock reads your Ghostty config too (`~/.config/ghostty/config`, or Ghostty's own folder in
`~/Library/Application Support`). These keys fall back to Ghostty's value when Hammock's file doesn't set them:

- `font-family`, `font-size`, `font-thicken`, `adjust-cell-height`
- `cursor-style`, `cursor-style-blink`
- `clipboard-paste-protection`, `clipboard-paste-bracketed-safe`

Set one in Hammock's file and it wins. `copy-on-select` doesn't fall back: it's off unless Hammock's file turns it on.

## Themes

`app-theme` picks one of Hammock's themes, which colour the terminal, the window, the agent view and the active pane
border together: Hammock (the default), Native, Graphite, Ocean, Lagoon, Rose, Lavender, Sand, Apricot, Cocoa,
Catppuccin, Tokyo Night, Gruvbox, Nord, Rosé Pine and Solarized.

```sh
hmk theme              # list them; * marks the one in use
hmk theme nord         # use one (any case)
```

**Custom** sets each part on its own: the terminal's colours (`theme`, from Ghostty theme files, with a light and a
dark one if you like), the window's (`window-theme`), the agent view's (`agent-color`), the active pane border
(`active-pane-border-color`), and the sidebar and toolbar colours.

```ini
app-theme = custom
theme = dark:Catppuccin Mocha,light:Catppuccin Latte
window-theme = Graphite
```

Terminal themes are Ghostty theme files, found in:

- `~/.config/hammock/themes` (Settings ▸ Appearance ▸ **Open Themes Folder**)
- `~/.config/ghostty/themes`
- the themes bundled with Ghostty.app, if it's in Applications

## Keyboard shortcuts

Change any shortcut in Settings ▸ Keyboard, which you can search by name or by keys. In the config file, each
change is a `keybind` line, `chord=action`, using the action's name:

```ini
# Give an action a shortcut
keybind = ctrl+cmd+b=new_browser_tab
# Free a shortcut
keybind = cmd+j=unbind
# Take an action's shortcut away
keybind = none=new_tab
# digits stands for 1…9
keybind = ctrl+cmd+digits=select_tab
```

`keybind = clear` drops every default. [Keyboard shortcuts](/hammock/docs/reference/keyboard-shortcuts/) lists every
action's name and default. The TUI has keys of its own: see [The TUI](/hammock/docs/tui/#keys).

## Turning tools off

Hammock can open browsers, simulators, editors, diffs and logs beside your terminals. If you already do one of these
your own way, turn it off in Settings ▸ Tools, or with its key:

```ini
browser-enabled = false
simulator-enabled = false
editor-enabled = false
diff-enabled = false
log-enabled = false
```

A tool that's off isn't offered anywhere: not in Hammock's menus, the command palette or shortcuts, and not to
agents, whose MCP tools for it are left out. Panes of that kind already open stay open.

## Keeping the config elsewhere

`HMK_CONFIG_DIR` points Hammock at a different folder for its config, read as `$HMK_CONFIG_DIR/config`. A symlink is
usually simpler for dotfiles, since the app, the daemon and every `hmk` would all need the variable.

## The Settings window

Open Settings with ⌘, or from the command palette. Its search finds single settings and jumps to them.

| Pane | What's in it |
|---|---|
| General | Starting the server at login, the menu bar item, keeping empty Groups, the `hmk` command, Full Disk Access, browser links, simulators, and the server's status and state folder. |
| Appearance | Light and dark, your theme, the sidebar, tabs, panes and cursor, and the config file and themes folder. |
| Terminal | The terminal's font, copying and pasting, and images programs show. |
| Tools | Turning browser, simulator, editor, diff and log panes on or off, and showing what agents do in browsers and simulators. |
| Editor | Editor and diff panes: wrapping, line numbers, indents, font, Vim keys and how diffs are laid out. |
| Keyboard | Every shortcut, searchable by name or keys. |
| Agents | Warning before closing an agent at work, putting idle agents to sleep, usage limits in the sidebar, and installing Hammock's hooks and tools. |
| Agent View | Which view agent panes open in, and the agent view's colour, fonts, pictures and dictation. |
| Notifications | Which agent events notify you, how, with what sounds, and a test notification. |
| Sharing | Your name and picture, and the Groups you share with other people. |
| Devices | Your other computers, paired iPhones, remote access and screen sharing. |
| SSH | Your Hammock Key, and using the SSH keys of the computer you're at. |
| About | The version, updates, quitting with the server, uninstalling and acknowledgements (Hammock ▸ About Hammock). |

## An example

```ini
# Look
app-theme = Nord
sidebar-layout = horizontal
pane-spacing = compact
dim-inactive-panes = true

# Terminal
font-family = JetBrains Mono
font-size = 14
adjust-cell-height = 10%
cursor-style = bar
copy-on-select = true

# Agents
agent-sleep = 12h
notify-finished = false
notify-style = system

# Tools I don't use
simulator-enabled = false

# Shortcuts
keybind = ctrl+cmd+b=new_browser_tab
keybind = cmd+j=unbind

# Remote access: Tailscale and the home network
remote-network = tailscale
remote-network = 192.168.1.0/24
```

Every key, its values and its default are in [the config file reference](/hammock/docs/reference/config/).
