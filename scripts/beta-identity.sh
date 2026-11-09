#!/usr/bin/env bash
# Which Hammock β this checkout builds, as shell assignments to eval:
#   beta_slug beta_name beta_dir beta_label beta_port beta_menu beta_app
#
# The main checkout builds the shared β: "Hammock β", state in
# ~/Library/Application Support/Hammock Beta, login item …daemon.beta.
# A worktree builds its own, named after its branch ("Hammock β floating-panes"):
# its own state dir, daemon, remote port and app (dist/Hammock β <branch>.app),
# so worktrees never fight over one β or its daemon. It keeps β's bundle id,
# so macOS privacy grants, notifications and settings (UserDefaults) are
# shared; it has no menu bar item (that's a login item of its own, one per
# bundle id), and its daemon is started by the app, not as a login item.
#
#   BETA_NAME=x    name it x instead of the branch
#   BETA_SHARED=1  build the shared β from a worktree
#   BETA_MENU=1    include the menu bar item (it replaces the shared β's)
#   BETA_ROOT=dir  the checkout to answer for (default: this script's)
set -euo pipefail
root="${BETA_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
export LC_ALL=C

slug="${BETA_NAME:-}"
if [ -z "$slug" ] && [ -z "${BETA_SHARED:-}" ]; then
  gitdir="$(git -C "$root" rev-parse --path-format=absolute --git-dir)"
  common="$(git -C "$root" rev-parse --path-format=absolute --git-common-dir)"
  if [ "$gitdir" != "$common" ]; then
    branch="$(git -C "$root" symbolic-ref --short -q HEAD || basename "$root")"
    slug="${branch##*/}"
  fi
fi
# Short and plain: it's in socket paths (104 bytes at most) and a launchd label.
slug="$(printf %s "$slug" | tr 'A-Z' 'a-z' | tr -cs 'a-z0-9.-' '-' | sed 's/^[-.]*//' | cut -c1-20 | sed 's/[-.]*$//')"

if [ -z "$slug" ]; then
  name="Hammock β" dir="Hammock Beta" label="com.homegrownsw.hammock.daemon.beta" port=7434 menu=1
else
  name="Hammock β $slug" dir="Hammock Beta $slug" label="com.homegrownsw.hammock.daemon.beta.$slug"
  # β's remote port is 7434; a worktree's is one of 7500–7999.
  port=$((7500 + $(printf %s "$slug" | cksum | cut -d' ' -f1) % 500))
  menu="${BETA_MENU:-0}"
fi

# A pane's socket is the longest path in the state dir.
sock="$HOME/Library/Application Support/$dir/run/panes/pn_xxxxxxxx.sock"
if [ "${#sock}" -gt 103 ]; then
  echo "error: β's socket paths would be too long ($sock); pick a shorter BETA_NAME" >&2
  exit 1
fi

printf 'beta_slug=%q\nbeta_name=%q\nbeta_dir=%q\nbeta_label=%q\nbeta_port=%q\nbeta_menu=%q\nbeta_app=%q\n' \
  "$slug" "$name" "$dir" "$label" "$port" "$menu" "$root/dist/$name.app"
