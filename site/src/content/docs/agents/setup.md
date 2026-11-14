---
title: Setting up agents
description: Install Hammock's hooks and MCP server for your coding agents, and the settings that change how Hammock treats them.
sidebar:
  order: 2
---

Hammock recognises agents without any setup, by their process and screen. Its hooks make an agent's state exact and tell Hammock which session a pane is running, which the agent view and resuming rely on. Its MCP server gives your agents Hammock's tools.

## Installing hooks

Hammock's setup offers to install hooks the first time you open it. Run it again from **Settings ▸ Agents ▸ Install Hooks & Tools**, or:

```sh
hmk hooks install
```

This adds Hammock's hooks to the config of every agent it finds on your Mac, and registers Hammock's MCP server with those that take one. It backs up each file it changes, and leaves your own hooks alone.

| Option | What it does |
|---|---|
| `--harness claude,codex,…` | Set up only these agents, by id: `claude`, `codex`, `gemini`, `cursor`, `copilot`, `droid` and so on. |
| `--dry-run` | Show what would change, without writing anything. Settings ▸ Agents ▸ Preview Changes does the same. |
| `--uninstall` | Take Hammock's hooks and MCP server out again. Settings ▸ Agents ▸ Remove does the same. |

The [supported agents](/hammock/docs/reference/agents/) table shows which agents have hooks.

### Doing it by hand

`hmk hooks print` prints the hook configuration for Claude Code and Codex, to merge in yourself: into `~/.claude/settings.json` for Claude Code, or `~/.codex/hooks.json` for Codex, with `hooks = true` under `[features]` in `~/.codex/config.toml`.

## Registering the MCP server

`hmk mcp` is Hammock's MCP server ([what it offers](/hammock/docs/agents/mcp/)). `hmk hooks install` registers it for you. To add it yourself, point your agent at the `hmk` inside the app, with the argument `mcp`. For Claude Code:

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

Outside a Hammock pane the server offers no tools, so it's safe to register for every project.

## Agent settings

Settings ▸ Agents, Agent View, Notifications and Tools hold the settings for how Hammock treats agents: sleeping idle ones, usage limits, the agent view's look, which notifications you get, and which kinds of pane agents can use. The [config reference](/hammock/docs/reference/config/) has every key.

## Not covered

Agents not in the [supported agents](/hammock/docs/reference/agents/) table aren't recognised, and that table says which features each one lacks. If you'd like one added, [open an issue](/hammock/docs/reporting-issues/).
