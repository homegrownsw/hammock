# Docs site

Hammock's documentation, published at https://homegrownsw.com/hammock/docs/: [Astro](https://astro.build) with
[Starlight](https://starlight.astro.build), and search by Pagefind (built in, static).

- `just docs-dev` previews it as you edit, at http://localhost:4321/hammock/docs/.
- `just docs` builds it into `site/dist`.
- `just site` copies it, with the website, into the homegrownsw.com site repo as `site/hammock/docs/`. It publishes
  nothing.

## Pages

- **Hand-written:** Markdown in `src/content/docs/`. The sidebar is in `astro.config.mjs`, so a new page goes there
  too. Link pages with their full path (`/hammock/docs/agents/mcp/`).
- **Generated:** `src/content/docs/reference/` (git-ignored) is made by `just docs-gen` (`scripts/docsgen`) from
  Hammock's source:

  | Page | Made from |
  |---|---|
  | Config file reference | `internal/hammockconfig/keys.go` |
  | Supported agents | the agent registry, `internal/agents` |
  | CLI reference | `hmk help` |
  | MCP reference | `hmk mcp --list-tools` |
  | Keyboard shortcuts | `Keybinds.actions` (`apple/Sources/HammockKit/Keybinds.swift`, dumped by `gen/keybinds`) |

  To change one of them, change its source.

## Style

Write for people using Hammock, like the README: plain British English, short sentences, real commands, menu paths
as Settings ▸ Agents. Check every claim against the code. Everything here is public (AGENTS.md): use placeholders
in examples and screenshots.
