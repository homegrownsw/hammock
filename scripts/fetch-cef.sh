#!/usr/bin/env bash
# Downloads the pinned CEF binary distribution (checksum-verified) and builds
# its C++ wrapper library. Prints the install dir:
#   ~/Library/Caches/hammock/cef/<version>
# With --client, fetches the prebuilt cefclient sample instead and prints the
# path to cefclient.app, to compare Hammock's browser panes against CEF's own sample.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
pinned() { grep -v '^#' "$root/third_party/cef/VERSION" | sed -n "$1p"; }
ver="$(pinned 1)"
cache="${HMK_CEF_CACHE:-$HOME/Library/Caches/hammock/cef}"

fetch() { # dist-suffix sha dest
  local name="cef_binary_${ver}_macosarm64$1" sha="$2" dest="$3"
  [ -f "$dest/.ok" ] && return
  mkdir -p "$cache"
  local tmp
  tmp="$(mktemp -d "$cache/dl.XXXXXX")"
  trap 'rm -rf "$tmp"' EXIT
  curl -fSL --progress-bar -o "$tmp/cef.tar.bz2" \
    "https://cef-builds.spotifycdn.com/$(printf %s "$name" | sed 's/+/%2B/g').tar.bz2" >&2
  echo "$sha  $tmp/cef.tar.bz2" | shasum -a 1 -c - >&2
  tar -xjf "$tmp/cef.tar.bz2" -C "$tmp"
  rm -rf "$dest"
  mv "$tmp/$name" "$dest"
  touch "$dest/.ok"
  rm -rf "$tmp"
  trap - EXIT
}

if [ "${1:-}" = "--client" ]; then
  dest="$cache/$ver-client"
  fetch _client "$(pinned 3)" "$dest"
  find "$dest" -maxdepth 3 -name cefclient.app -print -quit
  exit
fi

dest="$cache/$ver"
fetch _minimal "$(pinned 2)" "$dest"
if [ ! -f "$dest/build/libcef_dll_wrapper/libcef_dll_wrapper.a" ]; then
  cmake -S "$dest" -B "$dest/build" -DPROJECT_ARCH=arm64 -DCMAKE_BUILD_TYPE=Release >/dev/null
  cmake --build "$dest/build" --target libcef_dll_wrapper -j "$(sysctl -n hw.ncpu)" >/dev/null
fi
echo "$dest"
