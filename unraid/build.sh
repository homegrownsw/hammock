#!/usr/bin/env bash
# Builds the Unraid plugin from the linux-amd64 binaries (just linux amd64):
#   dist/unraid/hammock-<version>-x86_64-1.txz   the Slackware package
#   dist/unraid/hammock.plg                      the plugin, pointing at it
# Upload both to BASE_URL (default https://hammock.jsp.scot/linux), where
# Unraid's plugin manager looks for updates (the .plg's pluginURL).
#
#   VERSION     the plugin's version (default YYYY.MM.DD.NNNNN: today and the
#               commit count, so it sorts as Unraid compares them)
#   BASE_URL    where the .txz and .plg are served from
#   CHANGES     the .plg's CHANGES text (default: the version and commit)
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/.." && pwd)"
bin="$root/dist/linux/amd64"
out="$root/dist/unraid"

for f in hmk hmkd pane-holder; do
  [ -x "$bin/$f" ] || { echo "no $bin/$f: run just linux amd64 first" >&2; exit 1; }
done
count="$("$root/scripts/commit-number" "$root")"
version="${VERSION:-$(date +%Y.%m.%d).$(printf %05d "$count")}"
base_url="${BASE_URL:-https://hammock.jsp.scot/linux}"
package="hammock-$version-x86_64-1.txz"
changes="${CHANGES:-### $version
Hammock $(git -C "$root" describe --always --dirty) for Unraid.}"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
cp -R "$here/package/." "$stage/"
cp "$bin"/{hmk,hmkd,pane-holder} "$stage/usr/local/hammock/bin/"
# The licences of what's in them (build-linux.sh writes it), and Hammock's.
cp "$root/dist/linux/THIRD_PARTY_NOTICES.txt" "$root/LICENSE" "$stage/usr/local/hammock/"
find "$stage" -name .DS_Store -delete
find "$stage" -type d -exec chmod 755 {} +
find "$stage" -type f -exec chmod 644 {} +
chmod 755 "$stage"/usr/local/hammock/bin/* "$stage/usr/local/bin/hmk" \
  "$stage"/usr/local/emhttp/plugins/hammock/scripts/* "$stage"/usr/local/emhttp/plugins/hammock/event/*

mkdir -p "$out"
rm -f "$out"/hammock-*.txz
# A Slackware package is a tar.xz (installpkg takes it as it is), root-owned.
if tar --version 2>/dev/null | grep -q bsdtar; then
  COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata --uid 0 --gid 0 --uname root --gname root \
    -C "$stage" -cJf "$out/$package" install usr
else
  tar --owner=0 --group=0 -C "$stage" -cJf "$out/$package" install usr
fi
sha="$(shasum -a 256 "$out/$package" | cut -d' ' -f1)"

esc() { printf '%s' "$1" | sed -e 's/[\/&|]/\\&/g'; }
changes_file="$stage/changes"
printf '%s\n' "$changes" > "$changes_file"
sed -e "s|@VERSION@|$(esc "$version")|g" -e "s|@BASE_URL@|$(esc "$base_url")|g" \
    -e "s|@PACKAGE@|$(esc "$package")|g" -e "s|@SHA256@|$sha|g" \
    -e "/@CHANGES@/{r $changes_file" -e 'd;}' \
    "$here/hammock.plg" > "$out/hammock.plg"
echo "$out/$package"
echo "$out/hammock.plg ($version, $base_url)"
