# ADR-0006: Own neutral thread store for agent switching

Status: Accepted · 2026-10-29

## Context
Switching a session between Claude Code and Codex requires translating transcripts. Atuin Threads already does this (neutral record store, rewriting native transcripts on resume, optional E2EE sync). Options were integrating Atuin or building our own.

## Decision
Build an in-house, vendor-neutral, append-only thread store with per-harness codecs, modelled on Atuin's approach. Local-only first, sync-ready schema. Fall back to a generated handoff brief when conversion fails validation.

## Consequences
- Full control over UX and data model; no external dependency.
- We own ongoing maintenance as harness transcript formats drift (golden-file tests per version).
- Documented losses: reasoning, images, oversized tool output.

## As built (2026-11-10)
Built as decided (`internal/thread`), local only. Codecs exist for Claude Code and Codex; the other agents Hammock knows ([ADR-0015](0015-agent-registry.md)) can't be switched to or from yet. Sync between hosts hasn't been started.
