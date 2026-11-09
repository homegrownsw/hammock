# Benchmarks

Run with `just bench`. Numbers below: MacBook (Apple silicon), macOS 27, 2026-10-29.

| Benchmark | What it measures | Result |
|---|---|---|
| `BenchmarkInputEcho` | Keystroke round trip: client → daemon → holder → PTY → raw `cat` echo → holder → daemon → client (raw-frame transport, libghostty mirror on) | **p50 51 µs, p99 231 µs** (46/208 before holder writes moved to a queue) |
| `BenchmarkRawPTY` | OS ceiling: bare Go read loop on a PTY master, 32 MiB flood | 30.0 MB/s |
| `BenchmarkRawPTYBlocking` | Same with blocking `read(2)` (no Go poller) | 29.6 MB/s |
| `BenchmarkHolderThroughput` | PTY → pane-holder → client | 27.6 MB/s (92% of raw) |
| `BenchmarkOutputThroughput` | PTY → holder → daemon (mirror + fan-out) → client | 24–26 MB/s (~83–86% of raw) |

Notes:

- macOS PTYs hand over at most 1 KiB per read, so throughput is bounded by the
  kernel (≈30 MB/s here); any terminal reading the same PTY should hit the same
  ceiling, but that is not yet measured against Ghostty directly. The product
  budget (≥90% of Ghostty's throughput) is therefore measured against this
  ceiling; we are at ~86% end to end. Next steps if needed: avoid a per-chunk
  allocation in the holder's broadcast, batch small chunks into one frame
  when the client is behind.
- The input round trip is ~0.05 ms against an 8 ms keystroke-to-photon
  budget, so the daemon/holder hop is not a meaningful part of latency; the
  remaining budget belongs to the renderer and display.
- Keystroke-to-photon vs standalone Ghostty (screen-capture based) is still
  to build; it needs Screen Recording permission on the CI/bench machine.
