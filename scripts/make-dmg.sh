#!/usr/bin/env bash
# Makes Hammock.dmg, the first-install download: the app and an Applications shortcut in a window drawn to
# drag one onto the other (marketing/dmg-background.html, rendered to apple/Resources/dmg-background*.png).
#
#   scripts/make-dmg.sh <Hammock.app> <out.dmg> [signing identity]
#
# With an identity the DMG itself is signed too (scripts/notarize-mac-app.sh signs, notarizes and staples
# it for a release, after the app inside is stapled). Without one it's unsigned: fine to try the window,
# not to ship. `just dmg` makes one from dist/share/Hammock.app.
#
# Sparkle's updates stay the zip: the DMG is only for people downloading Hammock. Make it outside
# dist/releases, where generate_appcast would take it for another update.
#
# dmgbuild writes Finder's layout (.DS_Store) itself, so this needs no Finder or logged-in desktop. It runs
# from uv, pinned, with nothing installed.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
app="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
out="$2"
identity="${3:-}"
[ -d "$app" ] || { echo "No app at $app" >&2; exit 1; }
command -v uvx >/dev/null || { echo "make-dmg needs uv (brew install uv)" >&2; exit 1; }
case "$out" in */dist/releases/*) echo "Not in dist/releases: generate_appcast would offer it as an update." >&2; exit 1 ;; esac

mkdir -p "$(dirname "$out")"
rm -f "$out"
uvx --quiet --from 'dmgbuild==1.6.5' dmgbuild \
  -s "$root/scripts/dmg-settings.py" \
  -D app="$app" -D background="$root/apple/Resources/dmg-background.png" \
  Hammock "$out"

if [ -n "$identity" ]; then
  codesign --force --timestamp --sign "$identity" "$out"
  codesign --verify --strict "$out"
fi
echo "$out"
