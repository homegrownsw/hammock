#!/usr/bin/env bash
# Builds libhmkquic.so (cmd/hmkquic: the Android app's QUIC video transport,
# internal/mediaview on quic-go) for arm64 phones and x86_64 emulators, into
# OUT/<abi>/ (a jniLibs directory; android/media's Gradle build runs this).
# Needs Go and the Android NDK (ANDROID_NDK_HOME, or the newest under the SDK).
#
#   scripts/android-quic.sh OUT
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="${1:?usage: android-quic.sh OUT}"
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
  ANDROID_NDK_HOME="$(ls -d "$sdk"/ndk/* 2>/dev/null | sort -V | tail -1)"
  [ -n "$ANDROID_NDK_HOME" ] || { echo "error: no Android NDK (install one with sdkmanager 'ndk;27.1.12297006')" >&2; exit 1; }
fi
command -v go >/dev/null || { echo "error: Go isn't on PATH (the Android app's QUIC video is built with it)" >&2; exit 1; }
bin="$(ls -d "$ANDROID_NDK_HOME"/toolchains/llvm/prebuilt/*/bin | head -1)"
api=33 # the app's minSdk

for pair in arm64-v8a:arm64:aarch64 x86_64:amd64:x86_64; do
  IFS=: read -r abi goarch clang <<<"$pair"
  mkdir -p "$out/$abi"
  (cd "$root" && CGO_ENABLED=1 GOOS=android GOARCH="$goarch" CC="$bin/$clang-linux-android$api-clang" \
    go build -buildmode=c-shared -trimpath -ldflags "-s -w" -o "$out/$abi/libhmkquic.so" ./cmd/hmkquic)
  rm -f "$out/$abi/libhmkquic.h"
done
