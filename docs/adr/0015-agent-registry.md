# ADR-0015: One registry of agents, each agent in its own files

Status: Accepted · 2026-11-07

## Context
Hammock grew up around Claude Code, with Codex added beside it. Every agent-specific decision is a `switch` on `"claude"` / `"codex"`: about 70 places in 25 Go files (heuristics, resume, start, schedules, session finder, agent view, switching, usage, tab names, hook install, the TUI) and as many in 25 Swift files (names, logos, pickers, "Continue in…", usage toggles). Adding a third agent would mean touching all of them, and missing one fails quietly.

We want first-class support for many more agents (Gemini CLI, OpenCode, Cursor, Copilot, Droid, Amp, Pi, Qwen, Crush, Grok, Kiro, Aider, Goose…), without changing anything for people using Claude Code.

See [agents.md](../agents.md) for the agents and what each supports.

## Decision

### Go: `internal/agents`
One `Agent` value per agent, in its own file (`internal/agents/claude.go`, `codex.go`, `gemini.go`, …), registered in `agents.All`. It says everything the rest of Hammock asks about an agent:

- **Identity**: `ID` (the harness string, as before: `claude`, `codex`), `Name` ("Claude Code"), `Short` ("Claude"), the process/executable names that run it (`Procs`), the command on PATH (`Command`), how to install it (`Install`).
- **Running it**: `Start(prompt)` (the command typed into a new tab, interactive, with an optional first message), `Resume(id)`, `ResumeID(argv)` (the session a command line resumes), `Schedule(s)` (a scheduled run's command, with that agent's own options), `Screen` (regexps the heuristics use: working, needs input, interrupted).
- **Its sessions**: `Sessions` (`Locate(id)`, `Newest(cwd)`, `List()` for the session finder, and `Index(...)` reading one file into the finder's records), `NewParser(cwd)` (the agent view's parser, from `internal/agentview`), `Codec` (from `internal/thread`, for switching to or from it), `SlashCommands`.
- **Its limits**: `Usage` (read from what the CLI keeps on disk).

Everything else asks the registry (`agents.Get(id)`, `agents.All`) instead of switching. An unknown harness (a process Hammock doesn't know) gets nil and the generic behaviour it gets today.

Parsers stay in `internal/agentview/<id>.go` and codecs in `internal/thread/<id>.go`, beside the helpers they share; the registry wires them up. `internal/sessions` takes a list of sources from the registry instead of knowing Claude's and Codex's folders. Hook installers become one type per agent in `cmd/hmk/hooks_<id>.go` behind an interface, so `hmk hooks install --harness X` and the onboarding plan cover any agent that has one.

### Clients: the daemon sends the catalogue
`StateUpdate.agent_kinds` (on a full snapshot): each agent's id, names, whether it's installed here, and what it can do (start, resume, agent view, switch to, schedule, usage, hooks). The Mac and iPhone build their pickers, "Continue in…" menus and usage toggles from it (the TUI, built with the daemon, uses the registry itself), so a newer daemon's agents appear on an older phone with a generic mark. A daemon too old to send it is taken to know Claude Code and Codex, as before.

Logos stay in the apps (`AgentMark`), keyed by id, with a monogram for agents without one.

### What doesn't change
Harness ids, the proto's `harness` strings, config keys (`sidebarUsage.claude`), hook commands, schedule kinds (`claude`, `codex`, `command`), and every Claude Code and Codex behaviour. The refactor lands first, on its own, with the existing tests passing; agents are added after it, one at a time.

## As built (2026-11-07)
- Landed as decided: the refactor first, then agents one at a time. Nineteen agents are registered ([agents.md](../agents.md)).
- `Agent` describes its sessions in fields of its own (`Roots`, `Locate`, `Newest`, `Sessions`, `Index`) rather than a `Sessions` value, and finds its codec through `internal/thread` (`Agent.Codec()`): only Claude Code and Codex have one, so only they can be switched between.
