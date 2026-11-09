# Agents Hammock knows

Each agent is one file in `internal/agents` ([ADR-0015](adr/0015-agent-registry.md)): how Hammock spots, starts, resumes and schedules it, where its sessions are and how they're read, its hooks and its limits. The daemon sends the list to the apps (`StateUpdate.agent_kinds`), so pickers, menus and the palette follow it. Claude Code and Codex behave as they always have.

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

- **Hooks**: `hmk hooks install --harness <id>` (Settings ▸ Agents and onboarding set up Claude Code, Codex and any other agent found installed). Without hooks, state comes from the agent's process and screen. Agents whose hooks never report an approval prompt (`ScreenAsks`: Cursor, Antigravity, Goose, Kiro, Cline) have their screen read for one even while hooks flow.
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

Pi 1.5.0 (hooks, agent view, send, finder, resume, in a scratch daemon) and Crush 0.98.1 (reading its database, screen patterns). Gemini CLI, Cursor and Crush are found installed on the development Mac (the palette offers them). Every other agent was built from its source or bundled code and docs, with fixtures modelled on them, and needs trying by hand: each agent's release note and the commit adding it say what to check.
