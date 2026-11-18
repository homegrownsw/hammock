#!/usr/bin/env bash
# A scratch daemon for the Android app to pair with and use while you work on
# it, from this checkout's bin/ (just build), that can't touch your own
# Hammock or config. It's started with --dev-pairing (development builds
# only), so this script (and an agent running it) can make its pairing codes.
#
#   android/scripts/dev-daemon.sh up [PORT]   start it; prints its state dir, and the pairing link on stderr
#   android/scripts/dev-daemon.sh pair DIR [--emulator]   a fresh pairing link (single use, 10 minutes);
#                                             --emulator: at 10.0.2.2, for video over QUIC (UDP) from an emulator
#   android/scripts/dev-daemon.sh hmk DIR …   hmk against it
#   android/scripts/dev-daemon.sh down DIR    stop it and delete its state
#
# It listens for phones on 127.0.0.1:PORT (7499 by default) and, with an
# emulator or a phone attached over adb, forwards the phone's own
# 127.0.0.1:PORT to it (adb reverse), so the link's address works there as is.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
hmkd="$root/bin/hmkd" hmk="$root/bin/hmk"

# A pane's own daemon beats HMK_DIR: never let this reach yours.
scrub() { unset HMK_SOCKET HMK_PANE_ID HMK_HOOK_SOCKET HMK_TAB_ID HMK_GROUP_ID; }

use() {
  [ -f "${1:-}/pid" ] || { echo "error: give the dir 'up' printed" >&2; exit 2; }
  scrub
  export HMK_DIR="$1" HMK_CONFIG_DIR="$1/config"
}

link() { "$hmk" pair --json | jq -r .link; }

# The link with 127.0.0.1 changed to 10.0.2.2, the emulator's name for this
# Mac's loopback: UDP gets there (video over QUIC), where adb reverse is TCP
# only. Needs protoc.
emulator_link() {
  local b64 pad
  b64="$(link | sed 's|^hammock://pair/||' | tr '_-' '/+')"
  pad=$(( (4 - ${#b64} % 4) % 4 ))
  b64="$b64$(printf '%*s' "$pad" '' | tr ' ' '=')"
  printf 'hammock://pair/%s\n' "$(printf '%s' "$b64" | base64 -d \
    | protoc -I "$root/proto" --decode=hammock.v1.Pairing hammock/v1/hammock.proto \
    | sed 's/"127\.0\.0\.1:/"10.0.2.2:/' \
    | protoc -I "$root/proto" --encode=hammock.v1.Pairing hammock/v1/hammock.proto \
    | base64 | tr -d '=\n' | tr '/+' '_-')"
}

case "${1:-}" in
up)
  port="${2:-7499}"
  [ -x "$hmkd" ] && [ -x "$hmk" ] || { echo "error: bin/hmkd or bin/hmk missing: just build" >&2; exit 1; }
  dir="$(mktemp -d /tmp/hmkdev.XXXXXX)" # short: socket paths have a 104-byte limit
  mkdir -p "$dir/config"
  printf 'notify-style = in-app\n' >"$dir/config/config"
  use_dir="$dir"
  scrub
  export HMK_DIR="$dir" HMK_CONFIG_DIR="$dir/config"
  nohup "$hmkd" --dir "$dir" --remote --remote-listen "127.0.0.1:$port" --dev-pairing >"$dir/hmkd.out" 2>&1 &
  echo $! >"$dir/pid"
  for _ in $(seq 100); do
    "$hmk" ls --json >/dev/null 2>&1 && break
    kill -0 "$(cat "$dir/pid")" 2>/dev/null || { cat "$dir/hmkd.out" >&2; exit 1; }
    sleep 0.1
  done
  if command -v adb >/dev/null && adb get-state >/dev/null 2>&1; then
    adb reverse "tcp:$port" "tcp:$port" >/dev/null && echo "adb reverse tcp:$port" >&2
  fi
  echo "pairing link: $(link)" >&2
  echo "$use_dir"
  ;;
pair)
  use "${2:-}"
  if [ "${3:-}" = --emulator ]; then emulator_link; else link; fi
  ;;
hmk)
  use "${2:-}"
  shift 2
  exec "$hmk" "$@"
  ;;
down)
  use "${2:-}"
  kill "$(cat "$2/pid")" 2>/dev/null || true
  sleep 0.5
  pkill -f "pane-holder --socket $2/" 2>/dev/null || true
  rm -rf "$2"
  ;;
*)
  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
  ;;
esac
