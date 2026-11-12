#!/usr/bin/env bash
# Fails if the benchmarks fall outside the budgets in docs/tech-spec.md:
# keystroke round trip p99 under 8 ms, and output through the daemon at least
# half of what a bare read loop gets from a PTY on the same machine.
#
# Throughput is judged against the PTY's own ceiling (BenchmarkRawPTY), not a
# fixed MB/s: on a shared CI runner it swings from ~5 to ~14 MB/s
# from run to run, so a fixed floor failed about half the time with nothing
# wrong. Each side takes the best of a few runs, which measures what the code
# can do rather than how busy the runner's neighbours were.
set -euo pipefail
cd "$(dirname "$0")/.."
tags="${TAGS:-ghostty}"
runs="${BENCH_RUNS:-3}"
min_ratio="${BENCH_MIN_RATIO:-0.5}"

echo_out=$(go test -tags "$tags" -run xxx -bench InputEcho -benchtime 2000x ./internal/daemon/)
tput_out=$(go test -tags "$tags" -run xxx -bench OutputThroughput -benchtime 1x -count "$runs" ./internal/daemon/)
raw_out=$(go test -run xxx -bench 'RawPTY$' -benchtime 1x -count "$runs" ./internal/holder/)
echo "$echo_out" | grep Benchmark
echo "$tput_out" | grep Benchmark
echo "$raw_out" | grep Benchmark

p99=$(echo "$echo_out" | awk '/BenchmarkInputEcho/ {for (i=1;i<=NF;i++) if ($i=="p99-µs") print $(i-1)}')
best_mbs() { awk '/^Benchmark/ {for (i=1;i<=NF;i++) if ($i=="MB/s" && $(i-1) > m) m = $(i-1)} END {print m}'; }
mbs=$(echo "$tput_out" | best_mbs)
raw=$(echo "$raw_out" | best_mbs)

fail=0
awk -v v="$p99" 'BEGIN { exit !(v != "" && v < 8000) }' || { echo "FAIL: keystroke p99 ${p99}µs (budget 8000µs)"; fail=1; }
ratio=$(awk -v d="$mbs" -v r="$raw" 'BEGIN { if (d == "" || r == "" || r <= 0) print ""; else printf "%.2f", d / r }')
awk -v v="$ratio" -v m="$min_ratio" 'BEGIN { exit !(v != "" && v >= m) }' ||
  { echo "FAIL: throughput ${mbs} MB/s is ${ratio:-?} of the raw PTY's ${raw} MB/s (floor ${min_ratio})"; fail=1; }
[ $fail = 0 ] && echo "bench gate: ok (p99 ${p99}µs, ${mbs} MB/s, ${ratio} of the raw PTY's ${raw} MB/s)"
exit $fail
