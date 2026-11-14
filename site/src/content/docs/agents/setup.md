---
title: Setting up agents
description: Install Hammock's hooks and MCP server for your coding agents, and the settings that change how Hammock treats them.
sidebar:
  order: 2
---

Hammock recognises agents without any setup, by their process and screen. Installing its hooks makes that exact, and gives your agents Hammock's tools.

## Installing hooks

Hammock's setup offers to install hooks the first time you open it. You can do it again at any time from **Settings ▸ Agents ▸ Install Hooks & Tools**, or from a terminal:

```sh
hmk hooks install
```

This sets up Claude Code and Codex, and any other agent with hooks whose CLI it finds on your Mac. For each, it adds Hammock's hooks to the agent's own config files and registers Hammock's MCP server with the agents that take one.

- It **backs up** each file it changes first, beside the original (`settings.json.hammock-backup-…`).
- It **keeps your own hooks**: Hammock's are added alongside them, and only Hammock's are ever removed.
- Running it again is safe: files that are already up to date are left alone, and hooks pointing at an old copy of Hammock are updated.

Its options:

| Option | What it does |
|---|---|
| `--harness claude,codex,…` | Set up only these agents, by id: `claude`, `codex`, `gemini`, `cursor`, `copilot`, `droid` and so on. |
| `--dry-run` | Show what would change, without writing anything. Settings ▸ Agents ▸ Preview Changes does the same. |
| `--uninstall` | Take Hammock's hooks and MCP server out again. Settings ▸ Agents ▸ Remove does the same. |

The [supported agents](/hammock/docs/reference/agents/) table shows which agents have hooks.

### What hooks give you

- **Exact states.** The agent reports each step of its turn, so Hammock knows the moment it starts working, asks for permission, finishes or errors, rather than reading its screen.
- **Its session.** Hooks tell Hammock which session the pane is running, which the agent view and resuming after a restart rely on.

Without hooks, Hammock still shows an agent's state from its process and screen, just less precisely.

### Doing it by hand

If you keep your agents' config by hand, `hmk hooks print` prints the hook configuration for Claude Code and Codex, to merge in yourself. For Claude Code it goes in `~/.claude/settings.json`; for Codex in `~/.codex/hooks.json`, with `hooks = true` under `[features]` in `~/.codex/config.toml`. `hmk hooks install` covers the other agents.

## Registering the MCP server

`hmk mcp` is Hammock's MCP server: it gives an agent tools to open and drive panes, browsers, simulators and more. `hmk hooks install` registers it for Claude Code, Codex, Factory Droid, Goose, Kimi Code and Pi. [Controlling Hammock from agents](/hammock/docs/agents/mcp/) explains what it offers.

To add it yourself, point your agent at the `hmk` inside the app, with the argument `mcp`. For Claude Code:

```sh
claude mcp add --scope user hammock -- /Applications/Hammock.app/Contents/Helpers/hmk mcp
```

For Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.hammock]
command = "/Applications/Hammock.app/Contents/Helpers/hmk"
args = ["mcp"]
```

Agents that take a JSON file of MCP servers use the same shape:

```json
{
  "mcpServers": {
    "hammock": {
      "command": "/Applications/Hammock.app/Contents/Helpers/hmk",
      "args": ["mcp"]
    }
  }
}
```

It's safe to register it for every project: outside a Hammock pane, the server offers no tools.

## Agent settings

These live in Settings and in the [config file](/hammock/docs/configuration/). The [config reference](/hammock/docs/reference/config/) has every key.

**Settings ▸ Agents**

| Setting | Key | Default |
|---|---|---|
| Warn before closing an agent that's at work | `confirm-close-busy-agent` | on |
| Put idle agents to sleep | `agent-sleep` (`off`, `3h`, `6h`, `12h`, `24h`, `48h`) | off |
| Usage Limits in the Sidebar | `usage-limits-claude`, `usage-limits-codex`, and so on | off |
| Ask each agent's service for its usage | `usage-live` | off |

**Settings ▸ Agent View**

| Setting | Key |
|---|---|
| Agent panes open in | `agent-default-view` (`terminal` or `agent`) |
| Agent view colour | `agent-color` |
| Agent view font, size and line spacing | `agent-font-family`, `agent-font-size`, `agent-line-spacing` |
| Agent view code font, and wrapping | `agent-code-font-family`, `agent-code-font-size`, `agent-code-wrap` |
| Picture size, here and on other computers | `agent-image-size`, `agent-image-size-remote` |
| Dictation button | `agent-dictation` |

**Settings ▸ Notifications**

| Setting | Key | Default |
|---|---|---|
| Notify me when an agent is waiting for my input | `notify-needs-input` | on |
| … asks for permission | `notify-permission` | on |
| … finishes | `notify-finished` | on |
| … hits an error | `notify-error` | on |
| … replies to my review comments | `notify-review-replies` | off |
| Show a count on the Dock icon | `dock-badge` | on |

Each kind of notification has its own sound, too (`notify-sound-needs-input` and so on).

**Settings ▸ Tools**

| Setting | Key | Default |
|---|---|---|
| Show what agents do in browsers and simulators | `agent-presence` | on |
| Browser, Simulator, Editor, Diff Viewer, Log Viewer | `browser-enabled`, `simulator-enabled`, `editor-enabled`, `diff-enabled`, `log-enabled` | on |

With `agent-presence` on, an agent acting in a browser or simulator pane shows a cursor where it acts, a glow and a badge. The badge's **Take Over** stops agents acting on that pane until you hand it back.

## Not covered

- **Amp** keeps its threads on its own servers, so there's no agent view or session finder for it.
- **Aider** keeps its history in each project, with no session ids: its agent view reads the project's history file, and there's no session finder.
- **Crush** has no hook that says when it's done, so its state comes from its screen.
- **Cline** usually hands sessions to a shared background process, whose hooks can't be tied to a pane: there, its state comes from its screen.
- Agents not in the [supported agents](/hammock/docs/reference/agents/) table aren't recognised. If you'd like one added, [open an issue](/hammock/docs/reporting-issues/).
