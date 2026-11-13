#!/usr/bin/env bash
# Render the Mac app's window, as a throwaway β copy on a verify run's scratch
# daemon sees it, to RUN/NAME-{light,dark}.png and RUN/NAME-views.txt (the
# view tree, with the last lines of each terminal pane's text).
#   snapshot.sh RUN NAME [KEY=VALUE ...]
# KEY=VALUE are extra env for the app: AGENTS.md's HMK_DEBUG_* hooks, e.g.
#   snapshot.sh "$RUN" palette HMK_DEBUG_PALETTE=split
#   snapshot.sh "$RUN" settings HMK_DEBUG_SETTINGS=keyboard
#   snapshot.sh "$RUN" tab HMK_DEBUG_TAB=shell HMK_DEBUG_DELAY=8
# Needs a run started with up.sh --beta (the app swaps a daemon of another
# build for its own). A window shows on screen for a few seconds: someone may
# be at this Mac. The app stays open until this exits (or down.sh).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
. "$here/lib.sh"
load_run "${1:-}"
name="${2:?usage: snapshot.sh RUN NAME [KEY=VALUE ...]}"
shift 2
[ "$BUILD" = beta ] || { echo "error: this run's daemon is from bin/; start one with up.sh --beta" >&2; exit 1; }

prefix="$run/$name"
rm -f "$prefix-views.txt"
delay=4
for kv in "$@"; do case "$kv" in HMK_DEBUG_DELAY=*) delay="${kv#*=}";; esac; done

# Run the binary directly (not open -b / open -n): we get its pid, and every
# β shares one bundle id, so nothing else's copy is touched.
env "$@" HMK_DEBUG_SNAPSHOT="$prefix" "$APP/Contents/MacOS/Hammock" >"$run/$name-app.log" 2>&1 &
apid=$!
echo "$apid" >"$run/app.pid"

# The views file is written last, after both PNGs (and any capture).
deadline=$(( $(date +%s) + delay + 40 ))
while [ ! -s "$prefix-views.txt" ]; do
  kill -0 "$apid" 2>/dev/null || { echo "error: app exited; see $run/$name-app.log" >&2; exit 1; }
  [ "$(date +%s)" -lt "$deadline" ] || { echo "error: no $prefix-views.txt after $((delay + 40)) s" >&2; break; }
  sleep 0.5
done
sleep 1 # extra renders (settings, palette sheets) land beside it
kill -TERM "$apid" 2>/dev/null || true
for _ in $(seq 50); do kill -0 "$apid" 2>/dev/null || break; sleep 0.1; done
kill -KILL "$apid" 2>/dev/null || true
wait "$apid" 2>/dev/null || true # reap it quietly (no "Terminated" notice)
rm -f "$run/app.pid"

# The app must not have replaced our daemon.
lock="$(cat "$STATE/run/daemon.lock" 2>/dev/null || true)"
[ "$lock" = "$DAEMON_PID" ] || echo "warn: the daemon lock is now '$lock', not $DAEMON_PID: the app started its own; down.sh still cleans it up (it's under the scratch dir)" >&2
ls -1 "$prefix"* 2>/dev/null
[ -s "$prefix-views.txt" ]
