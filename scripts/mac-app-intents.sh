#!/usr/bin/env bash
# Makes the Mac app's App Intents metadata (Metadata.appintents: Shortcuts'
# Run Hammock Automation) into RESOURCES, as Xcode does for its targets and
# SwiftPM doesn't. The action is in one file that uses only AppIntents and
# Foundation (apple/Sources/HammockMac/RunAutomationIntent.swift), so it's
# compiled on its own, as the HammockMac module, for its constant values.
#   mac-app-intents.sh RESOURCES BINARY BUNDLE_ID
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
resources="$1" binary="$2" bundle_id="$3"
src="$root/apple/Sources/HammockMac/RunAutomationIntent.swift"
target="arm64-apple-macos26.0"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
sdk="$(xcrun --sdk macosx --show-sdk-path)"
toolchain="$(cd "$(dirname "$(xcrun --find swiftc)")/../.." && pwd)"
xcode="$(xcodebuild -version | awk '/Build version/ {print $3}')"
# The protocols whose conformances' constant values the metadata reads (Xcode's list).
echo '["AnyResolverProviding","AppEntity","AppEnum","AppExtension","AppIntent","AppIntentsPackage","AppShortcutProviding","AppShortcutsProvider","AppUnionValue","AppUnionValueCasesProviding","DynamicMigration_v1","DynamicOptionsProvider","EntityQuery","ExtensionPointDefining","IntentValueQuery","Resolver","StaticMigration_v1","TransientEntity","_AssistantIntentsProvider","_GenerativeFunctionExtractable","_IntentValueRepresentable"]' >"$tmp/protocols.json"
(cd "$tmp" && xcrun swiftc -module-name HammockMac -parse-as-library -target "$target" -sdk "$sdk" \
  -emit-const-values -Xfrontend -const-gather-protocols-file -Xfrontend "$tmp/protocols.json" \
  -c "$src" -o "$tmp/intent.o")
echo "$src" >"$tmp/sources"
echo "$tmp/intent.swiftconstvalues" >"$tmp/consts"
[ -f "$tmp/intent.swiftconstvalues" ] || mv "$tmp"/*.swiftconstvalues "$tmp/intent.swiftconstvalues"
xcrun appintentsmetadataprocessor --toolchain-dir "$toolchain" --module-name HammockMac --sdk-root "$sdk" \
  --xcode-version "$xcode" --platform-family macOS --deployment-target 26.0 --bundle-identifier "$bundle_id" \
  --output "$tmp/out" --target-triple "$target" --binary-file "$binary" --source-file-list "$tmp/sources" \
  --swift-const-vals-list "$tmp/consts" --force --compile-time-extraction --deployment-aware-processing \
  --no-app-shortcuts-localization >/dev/null 2>&1
[ -d "$tmp/out/Metadata.appintents" ] || { echo "no App Intents metadata was made" >&2; exit 1; }
rm -rf "$resources/Metadata.appintents"
cp -R "$tmp/out/Metadata.appintents" "$resources/"
