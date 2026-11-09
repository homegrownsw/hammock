#!/bin/bash
# Xcode Cloud, just before xcodebuild: put Xcode Cloud's build number into the project, and check the
# version against the release tag (release-notes/README.md).
#
# Xcode Cloud owns the build number: App Store Connect records every build under its CI_BUILD_NUMBER,
# whatever the project says. Without this the archive would carry the committed CURRENT_PROJECT_VERSION (0,
# the local placeholder) as CFBundleVersion while TestFlight showed another number. Both live in
# Config/Version.xcconfig, which every target includes. This is the pre-xcodebuild script rather than
# post-clone because only this one is sure to run for the archive action itself.
set -euo pipefail

repo="${CI_PRIMARY_REPOSITORY_PATH:-$(cd "$(dirname "$0")/../../.." && pwd)}"
xcconfig="$repo/apple/iOS/Config/Version.xcconfig"
[ -f "$xcconfig" ] || { echo "✗ ci_pre_xcodebuild: no $xcconfig" >&2; exit 1; }

version=$(sed -nE 's/^MARKETING_VERSION = ([^ ]+).*$/\1/p' "$xcconfig")
build="${CI_BUILD_NUMBER:-}"
if [ -z "$build" ]; then
  echo "▸ ci_pre_xcodebuild: CI_BUILD_NUMBER unset (not an Xcode Cloud build), leaving the build number alone"
else
  case "$build" in
    *[!0-9]*) echo "✗ ci_pre_xcodebuild: CI_BUILD_NUMBER '$build' isn't a whole number" >&2; exit 1 ;;
  esac
  sed -i.bak -E "s/^CURRENT_PROJECT_VERSION = .*/CURRENT_PROJECT_VERSION = ${build}/" "$xcconfig"
  rm -f "$xcconfig.bak"
  # Read it back: a sed that matched nothing still succeeds.
  stamped=$(sed -nE 's/^CURRENT_PROJECT_VERSION = ([^ ]+).*$/\1/p' "$xcconfig")
  [ "$stamped" = "$build" ] ||
    { echo "✗ ci_pre_xcodebuild: tried to set CURRENT_PROJECT_VERSION to $build, Version.xcconfig reads '$stamped'" >&2; exit 1; }
  echo "✓ ci_pre_xcodebuild: version $version, build $build"
fi

# TestFlight builds start from an iOS-v<version>-r<n> tag that `just release-ios` pushes. A tag naming a
# version the project doesn't set would upload under the wrong version, silently, and TestFlight can't take
# it back. Branch builds have no CI_TAG, so there's nothing to check.
tag="${CI_TAG:-}"
case "$tag" in
  "") exit 0 ;;
  iOS-v*) ;;
  *) echo "▸ ci_pre_xcodebuild: tag '$tag' isn't an iOS release tag, so there's no version to check"; exit 0 ;;
esac
tag_version=$(printf '%s' "$tag" | sed -nE 's/^iOS-v([0-9]+\.[0-9]+\.[0-9]+)-r[0-9]+$/\1/p')
if [ -z "$tag_version" ]; then
  echo "✗ ci_pre_xcodebuild: tag '$tag' isn't iOS-v<x.y.z>-r<n>; release tags come from just release-ios" >&2
  exit 1
fi
if [ "$tag_version" != "$version" ]; then
  echo "✗ ci_pre_xcodebuild: tag $tag says version $tag_version, but MARKETING_VERSION is '$version'" >&2
  exit 1
fi
echo "✓ ci_pre_xcodebuild: $tag matches the project's version"
