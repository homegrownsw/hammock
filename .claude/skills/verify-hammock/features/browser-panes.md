# Browser panes

A real Chromium page in a pane, beside the person's terminals: their dev server, docs, a page an agent is checking. People browse it in the Mac app. Agents drive it through MCP (open, read as an outline, click, type, run JavaScript, screenshot, read the console and network). `hmk show` puts a file or page in one too.

## Sub-features

- `browser-open`: open a page in a new tab or split (`hmk browser`, `browser_open`).
- `browser-read`: the page as an outline with clickable refs (`browser_snapshot`).
- `browser-act`: click, type, press keys (`browser_click`, `browser_type`, `browser_press_key`).
- `browser-eval`: run JavaScript and get JSON back (`browser_evaluate`).
- `browser-screenshot`: what's in view, as a PNG (`browser_screenshot`).
- `browser-console-network`: console messages and requests (`browser_console`, `browser_network`).
- `browser-nav`: load a URL, back, forward, reload (`hmk nav`, `browser_navigate`).

## How to get to it (user POV)

- CLI: `hmk browser URL [--group G] [--split PANE]`, `hmk nav PANE URL|back|forward|reload`, `hmk show FILE|URL`, `hmk devtools PANE`.
- MCP: `browser_*` tools, `show_output`.
- Mac: File ▸ New Browser Tab, the split menus' "with Browser" items, the palette ("Split Up with Browser"…).

## Driving it with verify-hammock

Preconditions:

- Baseline run (bin or β: the daemon hosts pages itself, so no app window is needed).
- A page with something to act on. A `data:` URL is enough, and needs no server: `data:text/html,<title>verify page</title><h1 id=h>verify-heading</h1><button onclick="h.textContent=42">go</button>`.

- **Open.** `$S/mcp.sh "$RUN" 01-open browser_open '{"url":"<that data: URL>"}'` prints `pane_id` and `status: Loaded … ("verify page")`. Keep the pane id in `B`.
- **Read.** `$S/mcp.sh "$RUN" 02-read browser_snapshot '{"pane_id":"'$B'"}'` lists `heading h1 "verify-heading"` and `button "go" [ref=1]`.
- **Act, then look again.** `$S/mcp.sh "$RUN" 03-act browser_click '{"pane_id":"'$B'","text":"go"}' browser_evaluate '{"pane_id":"'$B'","expression":"document.getElementById(\"h\").textContent"}' browser_screenshot '{"pane_id":"'$B'"}'` prints `Clicked button "go"`, then `42`, and saves `03-act-3-0.png`, which shows **42** above the button. Look at it.
- **Console.** `browser_console` prints `(none)` for that page. A page that logs shows its lines.
- **CLI entry.** `$S/record.sh "$RUN" 04-cli browser '<URL>' --group verify-browser` prints a tab id. `ls --json` then lists a pane with `"kind":"browser"` and the page's `<title>` as its title.

## Gotchas

- Drive pages only through `mcp.sh`. It runs the MCP calls inside the run's agent pane, because `hmk mcp` offers no tools outside a pane, and panes opened there belong to that agent. The agent may read any pane (`browser_snapshot` on one opened with `hmk browser` works), but clicking or typing in one it didn't open waits for the person's approval.
- Web content doesn't render in `snapshot.sh` PNGs: the browser pane shows its URL bar over a blank page. Use `browser_screenshot` for the page itself, and `HMK_DEBUG_CAPTURE=1` only if β has Screen Recording (it silently produced nothing in the first runs of this skill).
- Don't point it at real sites as proof: prefer `data:` URLs or a local server you start (see [simulator-panes.md](./simulator-panes.md) for one).
