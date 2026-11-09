#!/usr/bin/env bash
# Fails if the benchmarks fall outside the budgets in docs/tech-spec.md:
# keystroke round trip p99 under 8 ms, output throughput at least 10 MB/s
# (the macOS PTY tops out near 30 MB/s; the floor leaves room for slow CI).
set -euo pipefail
cd "$(dirname "$0")/.."
tags="${TAGS:-ghostty}"
echo_out=$(go test -tags "$tags" -run xxx -bench InputEcho -benchtime 2000x ./internal/daemon/)
tput_out=$(go test -tags "$tags" -run xxx -bench OutputThroughput -benchtime 1x ./internal/daemon/)
echo "$echo_out" | grep Benchmark
echo "$tput_out" | grep Benchmark
p99=$(echo "$echo_out" | awk '/BenchmarkInputEcho/ {for (i=1;i<=NF;i++) if ($i=="p99-µs") print $(i-1)}')
mbs=$(echo "$tput_out" | awk '/BenchmarkOutputThroughput/ {for (i=1;i<=NF;i++) if ($i=="MB/s") print $(i-1)}')
fail=0
awk -v v="$p99" 'BEGIN { exit !(v != "" && v < 8000) }' || { echo "FAIL: keystroke p99 ${p99}µs (budget 8000µs)"; fail=1; }
awk -v v="$mbs" 'BEGIN { exit !(v != "" && v >= 10) }' || { echo "FAIL: throughput ${mbs} MB/s (floor 10 MB/s)"; fail=1; }
[ $fail = 0 ] && echo "bench gate: ok (p99 ${p99}µs, ${mbs} MB/s)"
exit $fail
