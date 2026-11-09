#!/usr/bin/env bash
# This checkout's Hammock β (scripts/beta-identity.sh says which): the shared
# one in the main checkout, a worktree's own elsewhere.
#   scripts/beta.sh info     its name, app, state dir, socket and whether it runs
#   scripts/beta.sh restart  quit this β (only this copy, by path) and open it
#   scripts/beta.sh env      exports for the CLI to talk to its daemon
#   scripts/beta.sh clean    a worktree's β: quit it, its daemon, panes and
#                            helpers, and delete its state dir and app
#   scripts/beta.sh prune    the same for state dirs ("Hammock Beta <name>") that
#                            no worktree of this repo builds any more
# BETA_ROOT=dir answers for another checkout (just worktree-remove).
set -euo pipefail
scripts="$(cd "$(dirname "$0")" && pwd)"
eval "$("$scripts/beta-identity.sh")"
app="$beta_app"
state="$HOME/Library/Application Support/$beta_dir"
sock="$state/run/hmkd.sock"

# PIDs of processes running from this app bundle: $1 is a path inside it
# ("" for all of them: the app, hmkd, pane holders and helper hosts).
pids() {
  local prefix="$app/${1:-}"
  ps -axww -o pid=,command= | while read -r pid cmd; do
    case "${cmd:-}" in "$prefix"*) echo "$pid" ;; esac
  done
}

# The daemon holding this state dir's lock (it writes its pid there), or "".
daemon_pid() {
  local pid
  pid="$(cat "$state/run/daemon.lock" 2>/dev/null || true)"
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && echo "$pid" || true
}

# Processes using a state dir: its daemon (it writes its pid in the lock),
# and pane holders and helper hosts (given sockets in it), from any build.
state_pids() {
  local dir="$1" pid
  pid="$(cat "$dir/run/daemon.lock" 2>/dev/null || true)"
  [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null || echo "$pid"
  ps -axww -o pid=,command= | while read -r pid cmd; do
    case "${cmd:-}" in *"$dir/run/"*) echo "$pid" ;; esac
  done
}

# SIGTERM the given PIDs and wait (up to 5 s) for them to go.
stop() {
  [ $# -gt 0 ] || return 0
  kill -TERM "$@" 2>/dev/null || true
  for _ in $(seq 50); do
    kill -0 "$@" 2>/dev/null || return 0
    sleep 0.1
  done
  echo "still running after 5 s: $*" >&2
  return 1
}

case "${1:-info}" in
  info)
    echo "name:   $beta_name${beta_slug:+ (this worktree only)}"
    echo "app:    $app$([ -d "$app" ] || echo ' (not built: just mac-dev)')"
    echo "state:  $state"
    echo "socket: $sock"
    echo "label:  $beta_label"
    echo "port:   $beta_port"
    app_pid="$(pids Contents/MacOS/Hammock | head -1)"
    if [ -n "$app_pid" ]; then echo "app:    running, pid $app_pid"; else echo "app:    not running"; fi
    # Whichever app started it (the shared β's may be another checkout's).
    daemon_pid="$(daemon_pid)"
    # Its executable (launchd starts it as plain "hmkd").
    daemon_exe="$([ -z "$daemon_pid" ] || lsof -a -p "$daemon_pid" -d txt -Fn 2>/dev/null | sed -n 's/^n//p' | head -1)"
    case "$daemon_exe" in
      "") if [ -n "$daemon_pid" ]; then echo "daemon: running, pid $daemon_pid"; else echo "daemon: not running"; fi ;;
      "$app/"*) echo "daemon: running, pid $daemon_pid" ;;
      *) echo "daemon: running, pid $daemon_pid, from another build: $daemon_exe" ;;
    esac
    ;;
  restart)
    [ -d "$app" ] || { echo "error: $app isn't built (just mac-dev)" >&2; exit 1; }
    # Only the app; it replaces a daemon of another version itself.
    # shellcheck disable=SC2046
    stop $(pids Contents/MacOS/Hammock)
    # -n: another worktree's β has the same bundle id, and open would
    # bring that one forward instead.
    open -n "$app"
    echo "opened $app"
    ;;
  env)
    # The pane you run in sets HMK_SOCKET (its own daemon, often prod),
    # which beats HMK_DIR: eval "$(just beta-env)" points hmk at this β.
    # β's config is in its state dir too (hammockconfig.Dir).
    printf 'unset HMK_SOCKET HMK_HOOK_SOCKET HMK_PANE_ID\nexport HMK_DIR=%q HMK_CONFIG_DIR=%q\nalias hmk=%q\n' "$state" "$state" "$app/Contents/Helpers/hmk"
    ;;
  clean)
    if [ -z "$beta_slug" ]; then
      echo "error: this is the shared β ($beta_name); clean only removes a worktree's own" >&2
      exit 1
    fi
    # The app first, so it doesn't start the daemon again.
    # shellcheck disable=SC2046
    stop $(pids Contents/MacOS/Hammock)
    # shellcheck disable=SC2046
    stop $(state_pids "$state") $(pids)
    launchctl bootout "gui/$(id -u)/$beta_label" 2>/dev/null || true
    rm -rf "$state" "$app"
    echo "removed $beta_name: its processes, $state and $app"
    ;;
  prune)
    # Each worktree's state dir name, from its own checkout's branch.
    live="$(git -C "$scripts/.." worktree list --porcelain | sed -n 's/^worktree //p' | while read -r wt; do
      (eval "$(env -u BETA_NAME -u BETA_SHARED BETA_ROOT="$wt" "$scripts/beta-identity.sh" 2>/dev/null)" && echo "$beta_dir") || true
    done)"
    found=0
    for dir in "$HOME/Library/Application Support/Hammock Beta "*; do
      [ -d "$dir" ] || continue
      grep -qxF "$(basename "$dir")" <<<"$live" && continue
      # Kept while its daemon runs from a build that's still there (a
      # worktree's β built with BETA_NAME, which isn't its branch).
      pid="$(cat "$dir/run/daemon.lock" 2>/dev/null || true)"
      exe="$([ -z "$pid" ] || lsof -a -p "$pid" -d txt -Fn 2>/dev/null | sed -n 's/^n//p' | head -1)"
      if [ -n "$exe" ] && [ -e "$exe" ]; then
        echo "kept $dir: its daemon (pid $pid) runs from $exe"
        continue
      fi
      found=1
      # shellcheck disable=SC2046
      stop $(state_pids "$dir") || true
      name="$(basename "$dir")"
      launchctl bootout "gui/$(id -u)/com.homegrownsw.hammock.daemon.beta.${name#Hammock Beta }" 2>/dev/null || true
      rm -rf "$dir"
      echo "removed $dir (no worktree builds it)"
    done
    [ "$found" = 1 ] || echo "no β state left over from removed worktrees"
    ;;
  *)
    echo "usage: scripts/beta.sh [info|restart|env|clean|prune]" >&2
    exit 2
    ;;
esac
