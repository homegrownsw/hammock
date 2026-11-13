#!/usr/bin/env bash
# Read-only: is this verify run's daemon worth driving? Exits non-zero, saying
# why, if not. With no RUN, lists runs whose daemons are still alive.
#   doctor.sh RUN
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
. "$here/lib.sh"

if [ -z "${1:-}" ]; then
  for f in "$repo"/dist/verify/*/run.env; do
    [ -f "$f" ] || continue
    pid="$(sed -n 's/^DAEMON_PID=//p' "$f")"
    if kill -0 "$pid" 2>/dev/null; then echo "alive: $(dirname "$f") (hmkd pid $pid)"; fi
  done
  exit 0
fi

load_run "$1"
fail=0
ok() { echo "ok:   $*"; }
bad() { echo "FAIL: $*"; fail=1; }

# 1. Our process, still up, and the one built from what we think.
if kill -0 "$DAEMON_PID" 2>/dev/null; then
  exe="$(lsof -a -p "$DAEMON_PID" -d txt -Fn 2>/dev/null | sed -n 's/^n//p' | head -1)"
  [ "$exe" = "$HMKD" ] && ok "hmkd pid $DAEMON_PID runs $HMKD" || bad "pid $DAEMON_PID runs '$exe', not $HMKD"
else
  bad "hmkd pid $DAEMON_PID isn't running (see $run/hmkd.log)"
fi

# 2. It holds this state dir (its lock has its pid), so nothing else does.
lock="$(cat "$STATE/run/daemon.lock" 2>/dev/null || true)"
[ "$lock" = "$DAEMON_PID" ] && ok "state dir lock held by $DAEMON_PID" || bad "state lock has '$lock', not $DAEMON_PID: another daemon took it (did β replace it?)"

# 3. The CLI reaches it, not the Hammock this shell runs in.
status="$("$HMK" status --json 2>&1)" || bad "hmk status failed: $status"
dir="$(jq -r '.dir // .stateDir // empty' <<<"$status" 2>/dev/null)"
case "$dir" in
  "") ok "hmk status answers (no dir field to compare)";;
  "$STATE"|"$(cd "$STATE" && pwd -P)") ok "hmk talks to $dir";;
  *) bad "hmk talks to $dir, not $STATE";;
esac

# 4. The build is the checkout's current one (rebuilt since the last commit?).
built="$(cut -d+ -f1 "$run/version.txt" 2>/dev/null)"
head="$(git -C "$repo" describe --always --dirty)"
[ "$built" = "$head" ] && ok "build $built matches the checkout" || echo "warn: build is $built, checkout is $head: rebuild (just build / just mac-dev) if you changed code"

# 5. If an app copy was launched against it, it's alive.
if [ -f "$run/app.pid" ]; then
  apid="$(cat "$run/app.pid")"
  kill -0 "$apid" 2>/dev/null && ok "app pid $apid running" || echo "info: app pid $apid has exited"
fi
exit $fail
