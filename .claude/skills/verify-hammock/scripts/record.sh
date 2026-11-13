#!/usr/bin/env bash
# Run hmk against a verify run's daemon and keep the proof: RUN/NAME.cmd,
# .out, .err and .exit. Prints stdout and exits with hmk's status.
#   record.sh RUN NAME HMK-ARGS...
#   e.g. record.sh "$RUN" 01-group-new group new verify-demo
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
. "$here/lib.sh"
load_run "${1:-}"
name="${2:?usage: record.sh RUN NAME HMK-ARGS...}"
shift 2
printf 'hmk' >"$run/$name.cmd"; printf ' %q' "$@" >>"$run/$name.cmd"; echo >>"$run/$name.cmd"
"$HMK" "$@" >"$run/$name.out" 2>"$run/$name.err"
code=$?
echo "$code" >"$run/$name.exit"
cat "$run/$name.out"
[ -s "$run/$name.err" ] && cat "$run/$name.err" >&2
exit "$code"
