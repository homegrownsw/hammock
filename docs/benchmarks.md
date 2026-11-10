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
