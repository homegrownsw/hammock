#!/usr/bin/env bash
# Puts libghostty-vt where a checkout's builds look for it, building it only
# once per pinned Ghostty commit for the whole Mac (as fetch-cef.sh does for
# CEF), so a new worktree doesn't build it again:
#   - ~/Library/Caches/hammock/ghostty/<commit>/out: the build, shared;
#   - third_party/ghostty/out: a link to it (Go's cgo, via pkg-config);
#   - apple/Frameworks/ghostty-vt.xcframework: an APFS clone of its
#     xcframework (the Swift package's binary target).
# Fast when everything's there; run by the justfile's recipes before they
# build. The first time for a commit it takes the main checkout's own build
# if that's at the same commit, else builds it (zig).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dir="$root/third_party/ghostty"
rev="$(grep -v '^#' "$dir/VERSION" | head -1)"
cache="${HMK_GHOSTTY_CACHE:-$HOME/Library/Caches/hammock/ghostty}/$rev"

# The checkout's own build, from before the cache, at the pinned commit:
# kept as it is.
own() { [ -d "$1/out/lib/ghostty-vt.xcframework" ] && [ ! -L "$1/out" ] &&
  [ "$(git -C "$1/src" rev-parse HEAD 2>/dev/null || true)" = "$rev" ]; }

if [ ! -f "$cache/.ok" ]; then
  mkdir -p "$cache"
  main="$(cd "$(git -C "$root" rev-parse --path-format=absolute --git-common-dir)/.." && pwd)"
  if own "$dir"; then
    seed="$dir/out"
  elif own "$main/third_party/ghostty"; then
    seed="$main/third_party/ghostty/out"
  else
    seed=""
  fi
  rm -rf "$cache/out"
  if [ -n "$seed" ]; then
    echo "libghostty-vt: from $seed"
    cp -cR "$seed" "$cache/out"
  else
    GHOSTTY_SRC="$cache/src" GHOSTTY_OUT="$cache/out" "$root/scripts/build-libghostty.sh"
  fi
  # Its pkg-config files name where it was built: here, now.
  for pc in "$cache"/out/share/pkgconfig/*.pc; do
    sed -i '' "s|^prefix=.*|prefix=$cache/out|" "$pc"
  done
  touch "$cache/.ok"
fi

# A checkout with its own build at this commit keeps it; otherwise (none, or
# an older commit's) it gets the shared one.
if ! own "$dir"; then
  if [ "$(readlink "$dir/out" 2>/dev/null || true)" != "$cache/out" ]; then
    rm -rf "$dir/out"
    ln -s "$cache/out" "$dir/out"
  fi
fi

# The Swift package's copy: replaced when it isn't this commit's.
fw="$root/apple/Frameworks/ghostty-vt.xcframework"
stamp="$root/apple/Frameworks/.ghostty-rev"
if [ ! -d "$fw" ] || [ "$(cat "$stamp" 2>/dev/null || true)" != "$rev" ]; then
  mkdir -p "$root/apple/Frameworks"
  rm -rf "$fw"
  cp -cR "$dir/out/lib/ghostty-vt.xcframework" "$fw"
  echo "$rev" > "$stamp"
fi
