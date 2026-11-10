#!/usr/bin/env bash
# Builds Hammock for Linux: hmk, hmkd and pane-holder for each architecture
# asked (amd64 and arm64 by default), cross-compiled with zig from a Mac (or
# built on Linux), and a tarball of each:
#   dist/linux/<arch>/{hmk,hmkd,pane-holder}
#   dist/linux/hammock-<version>-linux-<arch>.tar.gz
#
# libghostty-vt is built once per pinned Ghostty commit and architecture, in
# the cache ghostty-deps.sh shares between checkouts (<cache>/<commit>/
# linux-<arch>). The binaries link it statically and need only glibc 2.28 or
# later (Debian 10, Ubuntu 18.10, RHEL 8, Unraid 6.9 onward).
#
#   scripts/build-linux.sh [amd64|arm64 ...]
#   HMK_VERSION=0.3.0-r700 scripts/build-linux.sh amd64
#
# Needs zig, go, pkg-config and jq.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

archs=("$@")
[ ${#archs[@]} -gt 0 ] || archs=(amd64 arm64)
glibc=2.28
rev="$(grep -v '^#' third_party/ghostty/VERSION | head -1)"
if [ "$(uname)" = Darwin ]; then
  cache_root="${HMK_GHOSTTY_CACHE:-$HOME/Library/Caches/hammock/ghostty}"
else
  cache_root="${HMK_GHOSTTY_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/hammock/ghostty}"
fi
version="${HMK_VERSION:-$(git describe --always --dirty 2>/dev/null || echo dev)+$(date +%Y%m%d%H%M%S)}"
out="$root/dist/linux"
mkdir -p "$out"
# The third-party software in the binaries and its licences (`hmk
# acknowledgements` shows the same), as text, from the file `just
# acknowledgements` makes: into each tarball, and the Unraid package.
notices="$out/THIRD_PARTY_NOTICES.txt"
{
  printf 'Hammock for Linux includes this third-party software. Each one\x27s licence follows.\n\n'
  jq -r '[.components[] | select(.apps | index("linux"))] | map("  \(.name)\(if .version then " " + .version else "" end) (\(.license))") | join("\n")' internal/acknowledgements/Acknowledgements.json
  jq -r '.components[] | select(.apps | index("linux")) | "\n" + ("=" * 72) + "\n\n" + .name + (if .version then " " + .version else "" end) + "\nLicence: " + .license + "\n" + (if .url then .url + "\n" else "" end) + (if (.packages // [] | length) > 1 then "Packages: " + (.packages | map(.name + "@" + .version) | join(" ")) + "\n" else "" end) + (if .note then .note + "\n" else "" end) + "\n" + .text' internal/acknowledgements/Acknowledgements.json
} > "$notices"

for arch in "${archs[@]}"; do
  case "$arch" in
    amd64) zt="x86_64-linux-gnu" ;;
    arm64) zt="aarch64-linux-gnu" ;;
    *) echo "unknown architecture $arch (amd64 or arm64)" >&2; exit 2 ;;
  esac
  lib="$cache_root/$rev/linux-$arch"
  if [ ! -f "$lib/.ok" ]; then
    echo "libghostty-vt for linux-$arch (once per Ghostty commit)…"
    rm -rf "$lib"
    GHOSTTY_SRC="$cache_root/$rev/src" GHOSTTY_OUT="$lib" \
      "$root/scripts/build-libghostty.sh" "-Dtarget=$zt.$glibc" >/dev/null
    touch "$lib/.ok"
  fi
  # Its pkg-config files name where it was built: here, now.
  for pc in "$lib"/share/pkgconfig/*.pc; do
    perl -pi -e "s|^prefix=.*|prefix=$lib|" "$pc"
  done

  bin="$out/$arch"
  rm -rf "$bin"
  mkdir -p "$bin"
  # (libghostty's Go package links the static library unless built with
  # the dynamic tag.)
  PKG_CONFIG_PATH="$lib/share/pkgconfig" CGO_ENABLED=1 GOOS=linux GOARCH="$arch" \
    CC="zig cc -target $zt.$glibc" CXX="zig c++ -target $zt.$glibc" \
    go build -tags ghostty -trimpath \
      -ldflags "-s -w -X github.com/homegrownsw/hammock/internal/daemon.Version=$version" \
      -o "$bin/" ./cmd/...

  name="hammock-$version-linux-$arch"
  stage="$(mktemp -d)"
  mkdir "$stage/$name"
  cp "$bin"/{hmk,hmkd,pane-holder} "$stage/$name/"
  cp "$notices" "$root/LICENSE" "$stage/$name/"
  cat > "$stage/$name/README.txt" <<EOF
Hammock $version for Linux ($arch)

hmk (the CLI and TUI), hmkd (the daemon) and pane-holder (one per pane,
keeping it running across daemon restarts). Keep the three together.

Install, for your own user:

  mkdir -p ~/.local/bin && cp hmk hmkd pane-holder ~/.local/bin/
  hmk service install     # run hmkd from boot with systemd (lingering)
  hmk hooks install       # let the agents it finds report to Hammock

Without systemd, start it with: hmk start (or hmkd --dir DIR from your init).

Reach it from your Mac and iPhone (remote access listens only on the
networks you choose; Tailscale is the easy one):

  hmk remote enable tailscale && hmk remote on
  hmk link --accept       # then Link a Computer… on the Mac, and check the
                          # code: hmk link confirm CODE (or answer here)
  hmk pair                # a QR code for Hammock on your iPhone

hmk status shows how it's doing; hmk --help lists everything.

Hammock is under the MIT licence (LICENSE); the third-party software in it
and its licences are in THIRD_PARTY_NOTICES.txt (or: hmk acknowledgements).
EOF
  # Plain ustar, root-owned, without macOS's extended attributes (which
  # GNU tar warns about).
  if tar --version 2>/dev/null | grep -q bsdtar; then
    COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata --uid 0 --gid 0 --uname root --gname root \
      -C "$stage" -czf "$out/$name.tar.gz" "$name"
  else
    tar --owner=0 --group=0 -C "$stage" -czf "$out/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
  echo "$out/$name.tar.gz"
done
