#!/usr/bin/env bash
# Tear down a verify run: the app copy it opened, its daemon, its panes and
# their helpers, and its scratch dir. Keeps the evidence (the run dir).
#   down.sh RUN
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
. "$here/lib.sh"
load_run "${1:-}"

wait_gone() { for _ in $(seq 50); do kill -0 "$@" 2>/dev/null || return 0; sleep 0.1; done; return 1; }

# The app first: it restarts a daemon that stops under it.
if [ -f "$run/app.pid" ]; then
  apid="$(cat "$run/app.pid")"
  kill -TERM "$apid" 2>/dev/null && { wait_gone "$apid" || kill -KILL "$apid" 2>/dev/null; }
fi
# The daemon and its panes (panes outlive a daemon stopped without --panes).
"$HMK" stop --panes >/dev/null 2>&1 || true
kill -TERM "$DAEMON_PID" 2>/dev/null && { wait_gone "$DAEMON_PID" || kill -KILL "$DAEMON_PID" 2>/dev/null; }
# Anything left that this run started: pane holders and helper hosts are
# given sockets under its scratch dir, a path no other process has.
left="$(ps -axww -o pid=,command= | while read -r pid cmd; do case "${cmd:-}" in (*"$SCRATCH/"*) echo "$pid";; esac; done)"
if [ -n "$left" ]; then
  # shellcheck disable=SC2086
  kill -TERM $left 2>/dev/null; sleep 0.5
  # shellcheck disable=SC2086
  kill -KILL $left 2>/dev/null || true
fi
case "$SCRATCH" in
  */tmp.*|*/T/*) rm -rf "$SCRATCH";;
  *) echo "refusing to delete unexpected scratch path: $SCRATCH" >&2;;
esac
echo "stopped run; evidence kept in $run"
