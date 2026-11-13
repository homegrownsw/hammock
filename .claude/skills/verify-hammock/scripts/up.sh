#!/usr/bin/env bash
# Start a scratch Hammock daemon for verification and print its run dir.
#   up.sh            daemon and CLI from bin/ (build first: just build)
#   up.sh --beta     daemon and CLI from this checkout's β bundle (just mac-dev),
#                    required for snapshot.sh: the app replaces a daemon of
#                    another build with its own
# Prints the run dir (dist/verify/<stamp>) on stdout's last line; pass it to
# doctor.sh, hmk, snapshot.sh and down.sh.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
. "$here/lib.sh"
scrub_env

build=bin
[ "${1:-}" = "--beta" ] && build=beta

if [ "$build" = beta ]; then
  eval "$("$repo/scripts/beta-identity.sh")"
  app="$beta_app"
  [ -d "$app" ] || { echo "error: $app not built: just mac-dev" >&2; exit 1; }
  hmkd="$app/Contents/MacOS/hmkd" hmk="$app/Contents/Helpers/hmk"
else
  app=""
  hmkd="$repo/bin/hmkd" hmk="$repo/bin/hmk"
  [ -x "$hmkd" ] && [ -x "$hmk" ] || { echo "error: bin/hmkd or bin/hmk missing: just build" >&2; exit 1; }
fi

run="$repo/dist/verify/$(date +%Y%m%d-%H%M%S)-$$"
mkdir -p "$run"
# mktemp -d, short: pane socket paths have a 104-byte limit.
scratch="$(mktemp -d)"
mkdir -p "$scratch/state" "$scratch/config"
# Defaults the app and daemon would otherwise take from your real config.
printf 'notify-style = in-app\n' >"$scratch/config/config"

export HMK_DIR="$scratch/state" HMK_CONFIG_DIR="$scratch/config"
"$hmkd" --dir "$scratch/state" >"$run/hmkd.log" 2>&1 &
pid=$!

cat >"$run/run.env" <<EOF
SCRATCH=$(printf %q "$scratch")
STATE=$(printf %q "$scratch/state")
CONFIG=$(printf %q "$scratch/config")
DAEMON_PID=$pid
BUILD=$build
HMKD=$(printf %q "$hmkd")
HMK=$(printf %q "$hmk")
APP=$(printf %q "$app")
EOF

# Ready = the daemon answers on its socket and lists an (empty) workspace.
for _ in $(seq 100); do
  if "$hmk" status --json >/dev/null 2>&1 && "$hmk" ls --json >/dev/null 2>&1; then
    "$hmkd" -version >"$run/version.txt"
    echo "ready: hmkd pid $pid ($build), state $scratch/state" >&2
    echo "$run"
    exit 0
  fi
  kill -0 "$pid" 2>/dev/null || { echo "error: hmkd exited; see $run/hmkd.log" >&2; cat "$run/hmkd.log" >&2; exit 1; }
  sleep 0.1
done
echo "error: hmkd (pid $pid) didn't answer in 10 s; see $run/hmkd.log. Run down.sh $run" >&2
exit 1
