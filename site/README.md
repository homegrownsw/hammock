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

## Review comments

In `just docs-dev`, select any text in a page and click **Comment** to leave a note on it. The **Comments** button
at the bottom right lists the page's comments, highlights what they're about, and shows replies. They're kept in
`site/.comments.jsonl` (git-ignored), one JSON object per line: the page, its `source` file (or, for a generated
page, what it's made from), the heading before it (`context`), the passage (`quote`) and the `comment`.

To work through them, `just docs-comments` lists the open ones. Fix the page, then set the comment's `"status"` to
`"resolved"` and add a `"reply"` saying what changed; the open page shows it within a few seconds. None of this
is in the built site (`dev/comments.mjs` runs in the dev server only).

## Style

Write for people using Hammock, like the README: plain British English, short sentences, real commands, menu paths
as Settings ▸ Agents. Check every claim against the code. Everything here is public (AGENTS.md): use placeholders
in examples and screenshots.
