# Benchmarks

Run with `just bench` (it takes under a minute; it needs libghostty-vt, which `just deps` builds once). Numbers below: Apple M4 Pro, macOS 27, 2026-11-10, three runs (the range), on a machine doing other work.

| Benchmark | What it measures | Result |
|---|---|---|
| `BenchmarkInputEcho` | Keystroke round trip: client → daemon → holder → PTY → raw `cat` echo → holder → daemon → client (raw-frame transport, libghostty mirror on) | **p50 53–57 µs, p99 156–214 µs** |
| `BenchmarkRawPTY` | OS ceiling: bare Go read loop on a PTY master, 32 MiB flood | 27.9–28.3 MB/s |
| `BenchmarkRawPTYBlocking` | Same with blocking `read(2)` (no Go poller) | 27.7–28.3 MB/s |
| `BenchmarkHolderThroughput` | PTY → pane-holder → client | 26.8–27.4 MB/s (~96% of raw) |
| `BenchmarkOutputThroughput` | PTY → holder → daemon (mirror + fan-out) → client | 24.5–27.6 MB/s (~88–98% of raw) |

`InputEcho` and `OutputThroughput` are in `internal/daemon/bench_test.go`, the others in `internal/holder/bench_test.go`.

`just bench-gate` fails if the keystroke round trip's p99 goes over 8 ms or output throughput under 10 MB/s (a floor that leaves room for slow CI machines). The CI workflow (`.github/workflows/ci.yml`) runs it on every push to `main` and every pull request once GitHub Actions are switched on for the repository.

Notes:

- macOS PTYs hand over at most 1 KiB per read, so throughput is bounded by the
  kernel (≈28 MB/s here); any terminal reading the same PTY should hit the same
  ceiling, but that isn't yet measured against Ghostty directly. The product
  budget (≥90% of Ghostty's throughput) is therefore measured against this
  ceiling for now.
- The input round trip is ~0.05 ms against an 8 ms keystroke-to-photon
  budget, so the daemon and holder hop is not a meaningful part of latency;
  the rest of the budget belongs to the renderer and display.
- Keystroke-to-photon against standalone Ghostty (screen-capture based) isn't
  built yet ([#19](https://github.com/homegrownsw/hammock/issues/19)); it
  needs Screen Recording permission on the machine running it.

## The Mac terminal renderer

`just render-bench` builds this checkout's β and draws fixed screens offscreen, at 2x, through the pane's real drawing code (`RenderBench.swift`), then prints a table and writes JSON (and a PNG of each screen, to compare renderers by eye). Each draw is split into `frame` (copying the screen out of libghostty-vt into Swift cells) and `draw` (drawing them). Between draws the screen changes as it would under output: a line scrolls in, a TUI redraws, a key is typed. Each screen is drawn twice: whole every time, and only the rows that changed, as the pane does, and the two last frames must match pixel for pixel. It leaves out Core Animation's upload of the layer and the display itself: that's keystroke-to-photon ([#19](https://github.com/homegrownsw/hammock/issues/19)).

CoreText baseline, every draw of the whole pane: Apple M4 Pro, macOS 27, Hack Nerd Font 14 pt, 2026-11-11, three runs (p50 ranged within 2%; one run shown). Milliseconds per draw, p50 / p99:

| Screen | Grid | `frame` | `draw` | Total | Draws/s at p50 |
|---|---|---|---|---|---|
| plain | 80×24 | 0.04 / 0.05 | 0.90 / 1.04 | 0.94 / 1.08 | 1063 |
| plain | 200×60 | 0.28 / 0.31 | 5.31 / 5.63 | 5.59 / 5.94 | 179 |
| plain | 400×100 | 0.91 / 1.02 | 17.10 / 19.75 | 18.02 / 20.67 | 55 |
| colour (256-colour words, some backgrounds, bold, italic) | 200×60 | 0.40 / 0.43 | 15.73 / 16.34 | 16.13 / 16.74 | 62 |
| colour | 400×100 | 1.36 / 1.56 | 52.09 / 53.87 | 53.45 / 55.24 | 19 |
| tui (status bar, box drawing, row backgrounds, block bars) | 200×60 | 0.34 / 0.36 | 7.75 / 8.13 | 8.09 / 8.46 | 124 |
| unicode (CJK, emoji, Nerd Font icons, powerline, combining marks) | 200×60 | 0.42 / 0.47 | 30.82 / 31.99 | 31.22 / 32.43 | 32 |
| typing (a short shell history, one key per draw) | 200×60 | 0.15 / 0.16 | 0.86 / 1.08 | 1.01 / 1.23 | 989 |

What it says:

- Drawing is the cost, not getting the screen out of libghostty: `frame` is 3–8% of each draw.
- The cost follows the text and its style runs, not the pane's size: a sparse 200×60 shell draws in 1 ms, the same pane full of coloured words in 16 ms, and Unicode with fallback fonts in 31 ms. A full pane is drawn on every change, a single typed key included.
- A 120 Hz display gives 8.3 ms a frame. Colourful or Unicode-heavy screens at 200×60 miss it, and a full-screen pane on a large display (400×100 is about a 6K display at 14 pt) misses even 60 Hz: 18 ms plain, 53 ms coloured.

### Drawing only the rows that changed

Since 2026-11-11, a pane redraws only the rows output changed (libghostty-vt's dirty tracking) and the rows the cursor left and reached. Same machine, p50 / p99 per draw, whole pane against changed rows:

| Screen | Grid | Whole pane | Changed rows | Rows drawn |
|---|---|---|---|---|
| typing (sparse shell) | 200×60 | 1.05 / 1.24 | 0.23 / 0.30 | 1 |
| typing at a full screen of colour (a coding agent, an editor) | 200×60 | 16.04 / 16.81 | 0.44 / 0.46 | 1 |
| scrolling output (plain, colour, Unicode), a TUI redrawing everything | any | unchanged | unchanged | all |

A keystroke now costs a row, whatever else is on screen; in β, typing into a 117×37 pane of colour drew in 0.2 ms. Output that scrolls changes every row, so floods and big panes still cost what the first table says: a Metal renderer (#18) is what would bring those down.

`HMK_DEBUG_RENDER_STATS=<file>` times the real thing instead. Every draw, and every write of a pane's output into its engine, is timed, and a JSON line is appended every two seconds. Flooding a 117×37 pane with 256-colour text (`cat` of a 115 MB file) gave 120 draws/s, the display's rate, at 3.3 ms each. That was 39% of the main thread drawing and 24% parsing about 52 MB/s of output. The daemon still skipped output and sent a fresh screen 4–5 times a second, as it does for a client that has fallen behind. At that size, delivery rather than drawing is what a flood runs into.
