# Agents Hammock knows

Each agent is one file in `internal/agents` ([ADR-0015](adr/0015-agent-registry.md)): how Hammock spots, starts, resumes and schedules it, where its sessions are and how they're read, its hooks and its limits. The daemon sends the list to the apps (`StateUpdate.agent_kinds`), so pickers, menus and the palette follow it.

## How Hammock knows what an agent is doing

- **Hooks**, where Hammock can install them: the agent runs `hmk hook <agent> [event]` at each point of its turn, which passes the event to the daemon over a socket of its own and never blocks or fails the agent. Events are read as Claude Code's are named: `SessionStart` (at its prompt), `UserPromptSubmit` and `PreToolUse`/`PostToolUse` (working), `PermissionRequest` and permission `Notification`s (needs input; so does Claude's question tool), `Stop` (finished a turn), `StopFailure` (errored: an API error ended the turn), `SessionEnd` (exited). Each agent's file maps its own events onto these. Hooks also say which session the pane is running, which the agent view, resume and the session finder use.
- **Its process and screen**, otherwise: the daemon looks for a known agent's process under the pane's shell, then reads the bottom of the pane's screen for the agent's working, asking and interrupted patterns (each agent's own, or generic ones such as "esc to interrupt"). Hook events always win; the screen is read only for panes that haven't sent one lately. An agent not in the registry isn't recognised.
- The states: starting, working, needs input, finished a turn (idle), errored, exited.

## The agents

| Agent | Command | Hooks | Agent view | Session finder | Resume | Start with message | Schedules | Usage limits |
|---|---|---|---|---|---|---|---|---|
| Claude Code | `claude` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Codex | `codex` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| Gemini CLI | `gemini` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| Antigravity CLI | `agy` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| OpenCode | `opencode` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| Cursor Agent | `cursor-agent` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| GitHub Copilot CLI | `copilot` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| Factory Droid | `droid` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| Qwen Code | `qwen` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – |
| Pi | `pi` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – |
| Kilo CLI | `kilo` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| Amp | `amp` | ✓ | – | – | ✓ | typed in | ✓ | asked |
| Grok CLI | `grok` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| Kimi Code | `kimi` | ✓ | ✓ | ✓ | ✓ | typed in | ✓ | asked |
| Crush | `crush` | – | ✓ | ✓ | ✓ | typed in | ✓ | – |
| Goose | `goose` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | – |
| Aider | `aider` | – | ✓ | – | ✓ | typed in | ✓ | – |
| Kiro CLI | `kiro-cli` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |
| Cline | `cline` | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | asked |

- **Hooks**: `hmk hooks install` sets up Claude Code, Codex and any other agent with hooks that it finds installed (`--harness <id>,…` picks; `--dry-run` shows the changes; `--uninstall` removes them). It backs up each file it changes, leaves your own hooks alone, and also registers Hammock's MCP server with the agents that take one. Onboarding and Settings ▸ Agents do the same. Without hooks, state comes from the agent's process and screen. Agents whose hooks never report an approval prompt (`ScreenAsks`: Cursor, Antigravity, Goose, Kiro, Cline) have their screen read for one even while hooks flow.
- **Start with message: typed in**: the agent takes no first message on its command line, so Hammock starts it bare and types the message once it's at its prompt (`TypePrompt`).
- **Usage limits**: ✓ is read from what the agent writes on disk. **asked** (and Claude Code's and Codex's too) is asked of the agent's own service, only with `usage-live = true` (Settings ▸ Agents ▸ Ask each agent's service for its usage) and only for the agents in `usage-live-agents` (Settings keeps it to those whose limits the sidebar shows; `claude-usage-live`, the old Claude-only setting, asks for Claude's alone), every 5 minutes, with the sign-in the agent's CLI keeps on this Mac (`LiveUsage`, `internal/agents/live*.go`): read for that request, sent only to that service, never kept or written back. No sign-in is refreshed (their refresh tokens rotate, so refreshing would sign the CLI out), so a short-lived one (Kimi's, Kiro's, Droid's, Gemini's) answers mostly while the agent runs; after a failed request a service's last answer is dropped once it's 15 minutes old. Antigravity's come from its own `agy -p /usage`. OpenCode's are an OpenCode Go plan's; Kilo's a Kilo Pass's; Cline's ClinePass's; Gemini's only for Workspace and paid Code Assist accounts. Qwen Code has no usage endpoint; Crush's Hyper credits are a balance with no limit; Goose, Aider and Pi have no plan of their own. The endpoints are the services' own, undocumented: from CodexBar and t3code, and the CLIs' source.
- **Continue in…** (converting a conversation to another agent) is Claude Code ⇄ Codex only: other agents have no thread codec yet.

## Not covered, and why

- **Amp** keeps threads on its servers, so there's no agent view or session finder.
- **Aider** keeps its history in each project (`.aider.chat.history.md`), with no session ids: its agent view reads the pane's project file; no session finder.
- **Crush** has only a PreToolUse hook, which can't say when it's done: screen only.
- **Cline** usually hands sessions to a shared background hub, whose hooks can't be tied to a pane: they're skipped there and the screen gives state.
- **oh-my-pi (omp)**, Hermes, Qoder, Devin, Mastra Code, Letta: not added (omp has diverged from Pi; the others are less used).

## Tried with the real CLI

Claude Code and Codex are used every day. Of the rest, as of 2026-11-10: Pi 1.5.0 (hooks, agent view, send, finder, resume, in a scratch daemon) and Crush 0.98.1 (reading its database, screen patterns) were tried with the real CLI, and Gemini CLI and Cursor were found installed and offered. Every other agent was built from its source or bundled code and docs, with test fixtures modelled on them, and hasn't been tried with the real CLI yet: each agent's release note and the commit adding it say what to check. Reports of what works and what doesn't are welcome as issues.
