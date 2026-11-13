#!/usr/bin/env bash
# Builds what the Mac app embeds besides its Swift code, into bin/, as
# `just build apple-deps browser simulator` does (for where there's no
# just: the Xcode project's HammockMac target):
#   - libghostty-vt (third_party/ghostty/out) and its xcframework
#     (apple/Frameworks), from the shared build (ghostty-deps.sh);
#   - hmkd, hmk and pane-holder (Go);
#   - Hammock Browser.app (CEF), Hammock Simulator.app, Hammock Screen.app and
#     Hammock Key.app.
# Needs zig, go, pkg-config and cmake; xcodegen for the simulator runner.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

out="$root/third_party/ghostty/out"
./scripts/ghostty-deps.sh

export PKG_CONFIG_PATH="$out/share/pkgconfig"
version="${HMK_VERSION:-$(git describe --always --dirty 2>/dev/null || echo dev)+$(date +%Y%m%d%H%M%S)}"
go build -tags ghostty -ldflags "-X github.com/homegrownsw/hammock/internal/daemon.Version=$version" -o bin/ ./cmd/...
echo "hmkd $version"

./helpers/browser/build.sh
./helpers/simulator/build.sh
./helpers/screen/build.sh
./helpers/key/build.sh
./scripts/build-menu-app.sh
