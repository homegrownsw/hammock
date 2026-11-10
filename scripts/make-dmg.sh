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
# from uv with nothing installed, it and its dependencies pinned: this runs beside the signing identity.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
usage="usage: scripts/make-dmg.sh <Hammock.app> <out.dmg> [signing identity]"
[ -d "${1:?$usage}" ] || { echo "No app at $1" >&2; exit 1; }
app="$(cd "$(dirname "$1")" && pwd -P)/$(basename "$1")"
mkdir -p "$(dirname "${2:?$usage}")"
out="$(cd "$(dirname "$2")" && pwd -P)/$(basename "$2")"
identity="${3:-}"
command -v uvx >/dev/null || { echo "make-dmg needs uv (brew install uv)" >&2; exit 1; }
case "$out" in "$(cd "$root" && pwd -P)"/dist/releases/*)
  echo "Not in dist/releases: generate_appcast would offer it as an update." >&2; exit 1 ;;
esac

rm -f "$out"
uvx --quiet --from 'dmgbuild==1.6.5' --with 'ds_store==1.3.3' --with 'mac_alias==2.2.3' dmgbuild \
  -s "$root/scripts/dmg-settings.py" \
  -D app="$app" -D background="$root/apple/Resources/dmg-background.png" \
  Hammock "$out"

# dmgbuild doesn't check its copy of the app: look inside before anything is signed or shipped.
mnt="$(mktemp -d)"
hdiutil attach -quiet -readonly -nobrowse -mountpoint "$mnt" "$out"
trap 'hdiutil detach -quiet "$mnt" 2>/dev/null; rmdir "$mnt" 2>/dev/null' EXIT
inside="$mnt/$(basename "$app")"
[ -d "$inside" ] && [ "$(readlink "$mnt/Applications")" = /Applications ] ||
  { echo "$out is missing the app or the Applications shortcut." >&2; exit 1; }
# The app as it was given: signed if it was (any build here is, ad hoc at least).
codesign --verify --strict "$inside"
hdiutil detach -quiet "$mnt" && trap - EXIT && rmdir "$mnt"

if [ -n "$identity" ]; then
  codesign --force --timestamp --sign "$identity" "$out"
  codesign --verify --strict "$out"
fi
echo "$out"
