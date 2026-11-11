#!/usr/bin/env bash
# just prod: the Hammock you're running, updated to this checkout's build.
# `just prod` builds it first (mac-app, which swaps the new app in beside the
# running one); this then quits the running app and its daemon, and opens
# the new app, which starts the new daemon. Panes keep running throughout
# (their holders outlive the daemon), agents included.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"

# Prod is the main checkout's build: a worktree's dist/Hammock.app isn't the
# one you run (its β is: just beta).
if [ "$(git -C "$root" rev-parse --path-format=absolute --git-dir)" != "$(git -C "$root" rev-parse --path-format=absolute --git-common-dir)" ]; then
  echo "just prod: run it in the main checkout; this is a worktree (its own app is β: just beta)" >&2
  exit 1
fi

app="$root/dist/Hammock.app"
hmk="$app/Contents/Helpers/hmk"
[ -x "$hmk" ] || { echo "just prod: no $app to open (just mac-app builds it)" >&2; exit 1; }
# Prod's own hmk, on prod's daemon: not a pane's (HMK_SOCKET and HMK_DIR beat
# the bundle's), nor β's.
prodhmk() { env -u HMK_SOCKET -u HMK_DIR -u HMK_CONFIG_DIR "$hmk" "$@"; }

# waitgone PID SECONDS: until the process has exited (false if it hasn't).
waitgone() {
  local i
  for ((i = 0; i < $2 * 4; i++)); do
    kill -0 "$1" 2>/dev/null || return 0
    sleep 0.25
  done
  return 1
}

# The app first: an open app starts the daemon again. By its path (β runs
# as "Hammock" too, from another).
for pid in $(pgrep -f "^$app/Contents/MacOS/Hammock\$" || true); do
  kill -TERM "$pid"
  if ! waitgone "$pid" 15; then
    echo "just prod: Hammock (pid $pid) didn't quit: is it asking something on screen?" >&2
    exit 1
  fi
done

# Then the daemon, asked to stop: panes keep running.
prodhmk stop >/dev/null 2>&1 || true

open "$app"
# Up once it answers (the app starts the daemon).
for ((i = 0; i < 60; i++)); do
  if prodhmk status >/dev/null 2>&1; then
    echo "Hammock updated: $(prodhmk status 2>/dev/null | head -1)"
    exit 0
  fi
  sleep 0.5
done
echo "just prod: Hammock is open, but its daemon hasn't answered yet (hmk status)" >&2
exit 1
