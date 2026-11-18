#!/usr/bin/env bash
# Builds libghostty-vt for Android (arm64 phones, x86_64 emulators on Intel
# Macs) from the pinned Ghostty commit, into the cache ghostty-deps.sh shares
# between checkouts: <cache>/<commit>/android-<abi>/{lib,include}. Once per
# commit and ABI; android/terminal's CMake links it statically.
# Needs zig and the Android NDK (ANDROID_NDK_HOME, or the newest under
# ~/Library/Android/sdk/ndk).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
rev="$(grep -v '^#' "$root/third_party/ghostty/VERSION" | head -1)"
cache="${HMK_GHOSTTY_CACHE:-$HOME/Library/Caches/hammock/ghostty}/$rev"
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  sdk="${ANDROID_HOME:-$HOME/Library/Android/sdk}"
  ANDROID_NDK_HOME="$(ls -d "$sdk"/ndk/* 2>/dev/null | sort -V | tail -1)"
  [ -n "$ANDROID_NDK_HOME" ] || { echo "error: no Android NDK (install one with sdkmanager 'ndk;27.1.12297006')" >&2; exit 1; }
fi
export ANDROID_NDK_HOME

for pair in arm64-v8a:aarch64-linux-android x86_64:x86_64-linux-android; do
  abi="${pair%%:*}" target="${pair#*:}"
  out="$cache/android-$abi"
  [ -f "$out/.ok" ] && continue
  echo "libghostty-vt for Android $abi (once per Ghostty commit)…" >&2
  rm -rf "$out"
  GHOSTTY_SRC="$cache/src" GHOSTTY_OUT="$out" "$root/scripts/build-libghostty.sh" "-Dtarget=$target" >/dev/null
  touch "$out/.ok"
done
