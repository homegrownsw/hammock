# Shared by the verify-hammock scripts: sourced, not run.
# A "run" is an evidence dir, dist/verify/<stamp>/ (git-ignored), holding
# run.env (what up.sh started) and everything captured. Scratch state (the
# daemon's dir, config) is elsewhere, in a mktemp -d, deleted by down.sh.

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"

# Never talk to the Hammock this shell runs in: a pane's HMK_SOCKET beats
# HMK_DIR, and a bare hmkd/hmk without HMK_CONFIG_DIR uses ~/.config/hammock.
# HMK_PERSON_KEY (Linux: the Unraid plugin's mark that you're the person)
# goes too, as just beta-env drops it: a run's caller is never vouched for.
scrub_env() {
  unset HMK_SOCKET HMK_HOOK_SOCKET HMK_PANE_ID HMK_TAB_ID HMK_GROUP_ID HMK_CONFIG_DIR HMK_DIR HMK_PERSON_KEY
}

# load_run RUN: read RUN/run.env (exports SCRATCH STATE CONFIG DAEMON_PID BUILD HMKD HMK APP).
load_run() {
  run="${1:?usage: $0 RUN (the evidence dir up.sh printed)}"
  run="$(cd "$run" && pwd)"
  [ -f "$run/run.env" ] || { echo "error: $run/run.env missing: not a verify run" >&2; exit 2; }
  # shellcheck disable=SC1091
  . "$run/run.env"
  scrub_env
  export HMK_DIR="$STATE" HMK_CONFIG_DIR="$CONFIG"
}
