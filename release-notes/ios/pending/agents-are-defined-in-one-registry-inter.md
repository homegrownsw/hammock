tag: INTERNAL
date: 2026-11-07
Agents are defined in one registry (internal/agents, ADR-0015) instead of switches on claude/codex across the daemon, CLI and TUI; the daemon sends clients its agent catalogue (StateUpdate.agent_kinds).
