#!/usr/bin/env bash
# Builds libghostty-vt (static lib, pkg-config files and, on macOS, the
# xcframework) from the pinned Ghostty commit into third_party/ghostty/out.
# Requires zig (see build.zig.zon minimum_zig_version at the pinned commit).
# GHOSTTY_SRC and GHOSTTY_OUT put the source and build elsewhere (ghostty-deps.sh
# builds into a cache shared by every checkout).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dir="$root/third_party/ghostty"
rev="$(grep -v '^#' "$dir/VERSION" | head -1)"
src="${GHOSTTY_SRC:-$dir/src}"
out="${GHOSTTY_OUT:-$dir/out}"

if [ ! -d "$src/.git" ]; then
  git init -q "$src"
  git -C "$src" remote add origin https://github.com/ghostty-org/ghostty.git
fi
if [ "$(git -C "$src" rev-parse HEAD 2>/dev/null || true)" != "$rev" ]; then
  git -C "$src" fetch -q --depth 1 origin "$rev"
  git -C "$src" checkout -q --detach FETCH_HEAD
fi

cd "$src"
zig build -Demit-lib-vt -Doptimize=ReleaseFast --prefix "$out" "$@"
echo "libghostty-vt built at $rev -> $out"
echo "export PKG_CONFIG_PATH=$out/share/pkgconfig"
