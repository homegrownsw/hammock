#!/usr/bin/env bash
# Try the browser-pane designs side by side with the same page.
#   ./try.sh [b|a|cef|chrome|stop] [url]
#     b       Design B (default): Chromium in BrowserHost behind a hidden
#             window, shown in the BrowserSpike viewer via CALayerHost.
#             Quit the viewer (⌘Q) and reopen: the page keeps running.
#     a       Design A: CEF offscreen rendering with shared IOSurface
#             textures and display-driven frames (CEF's own cefclient sample).
#     cef     CEF windowed, in-process (cefclient): the best CEF can feel.
#     chrome  The same page in Google Chrome, for reference.
#     stop    Stop the spike's BrowserHost.
# The default page is testpage/index.html, which logs input and frame timing
# in its header (fps, frame p50/p99).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
mode="${1:-b}"
url="${2:-file://$here/testpage/index.html}"
cefclient() { "$here/../../scripts/fetch-cef.sh" --client; }
cache="$HOME/Library/Application Support/Hammock/browser-spike"

case "$mode" in
  b)
    "$here/build.sh" >/dev/null
    open -n "$here/out/BrowserSpike.app" --args "--url=$url"
    ;;
  a)
    open -n "$(cefclient)" --args --off-screen-rendering-enabled --shared-texture-enabled \
      --external-begin-frame-enabled "--url=$url" "--cache-path=$cache/cefclient-osr"
    ;;
  cef)
    open -n "$(cefclient)" --args "--url=$url" "--cache-path=$cache/cefclient"
    ;;
  chrome)
    open -a "Google Chrome" "$url"
    ;;
  stop)
    pkill -f "$here/out/BrowserHost.app/Contents/MacOS/BrowserHost " || true
    ;;
  *)
    sed -n '2,15p' "$0"
    exit 2
    ;;
esac
