# Hammock dev tasks. Recipes that build fetch libghostty-vt themselves (`deps`):
# built once per pinned Ghostty commit and shared by every checkout and
# worktree. `just worktree NAME` makes a worktree ready to build.

# Release settings naming your own machines and vaults live in .release.env
# (git-ignored, KEY=value lines): UPDATE_HOST,
# UPDATE_DIR and UPDATE_CONTAINER for scripts/mac-update, HMK_SHARE_OP_ITEM and
# HMK_SHARE_OP_ACCOUNT for `just share`.
set dotenv-filename := ".release.env"
set dotenv-load

export PKG_CONFIG_PATH := justfile_directory() / "third_party/ghostty/out/share/pkgconfig"
tags := "ghostty"

default: build

# Build libghostty-vt from the pinned Ghostty commit (needs zig).
ghostty:
    ./scripts/build-libghostty.sh

# libghostty-vt where this checkout's builds want it: linked from the shared
# build in ~/Library/Caches/hammock/ghostty (made the first time for a commit).
deps:
    @git config core.hooksPath .githooks
    ./scripts/ghostty-deps.sh

# A worktree in .claude/worktrees/NAME on a new branch NAME (from main),
# ready to build. It takes the signing identity and AGENTS.local.md (both gitignored), or its β
# would be signed ad hoc and lose macOS privacy grants on every rebuild.
worktree name:
    git worktree add .claude/worktrees/{{name}} -b {{name}} main
    git config core.hooksPath .githooks
    [ ! -f .signing-identity ] || cp .signing-identity .claude/worktrees/{{name}}/
    [ ! -f AGENTS.local.md ] || cp AGENTS.local.md .claude/worktrees/{{name}}/
    cd .claude/worktrees/{{name}} && ./scripts/ghostty-deps.sh

# Build all Go binaries into bin/.
build: deps
    go build -tags {{tags}} -ldflags "-X github.com/homegrownsw/hammock/internal/daemon.Version=$(git describe --always --dirty)+$(date +%Y%m%d%H%M%S) -X github.com/homegrownsw/hammock/internal/daemon.ProductVersion=$(scripts/mac-version)" -o bin/ ./cmd/...

# Go tests, everything or what's named: `just test -run TestX ./internal/agents/` runs that package only
# (flags alone, `just test -run TestX`, still mean every package). The full suite takes minutes, most of it
# internal/daemon: iterate on one package or `test-quick`, and run the whole once before committing.
[positional-arguments]
test *args: deps
    #!/usr/bin/env bash
    set -euo pipefail
    pkgs=(./...)
    for a in "$@"; do case "$a" in ./*|../*|github.com/*) pkgs=() ;; esac; done
    go test -tags {{tags}} "$@" ${pkgs[@]+"${pkgs[@]}"}

# Every Go package but internal/daemon (the slow one): `just test-quick -run TestX`.
[positional-arguments]
test-quick *args: deps
    go test -tags {{tags}} "$@" $(go list ./... | grep -v '/internal/daemon$')

[positional-arguments]
race *args: deps
    #!/usr/bin/env bash
    set -euo pipefail
    pkgs=(./...)
    for a in "$@"; do case "$a" in ./*|../*|github.com/*) pkgs=() ;; esac; done
    go test -tags {{tags}} -race "$@" ${pkgs[@]+"${pkgs[@]}"}

# go vet, and gofmt: a file gofmt would change fails it (`gofmt -w cmd internal` fixes them).
vet: deps
    go vet -tags {{tags}} ./...
    @files="$(gofmt -l cmd internal)"; [ -z "$files" ] || { echo "gofmt would change:"; echo "$files"; exit 1; } >&2

proto:
    PATH="$PATH:$(go env GOPATH)/bin" protoc -I proto --go_out=. --go_opt=module=github.com/homegrownsw/hammock proto/hammock/v1/hammock.proto

# Run the daemon in the foreground against a scratch state dir.
dev-daemon dir="/tmp/hmk-dev": deps
    go build -tags {{tags}} -o bin/ ./cmd/... && ./bin/hmkd --dir {{dir}}

# Regenerate Swift protobuf code for the Apple clients.
proto-swift:
    protoc -I proto --swift_out=apple/Sources/HammockKit/Proto --swift_opt=Visibility=Public proto/hammock/v1/hammock.proto

# Rebuild editor and diff panes' pages (apple/Resources/editor.html and diff.html, committed) from
# apple/Editor: CodeMirror and Hammock's glue, bundled with esbuild.
editor-js:
    ./apple/Editor/build.sh

# Regenerate the apps' Acknowledgements (internal/acknowledgements/Acknowledgements.json): the
# third-party software each app ships and its licences, from go list, Package.resolved, the editor's
# bundle, THIRD_PARTY_NOTICES.md and third_party/acknowledgements.json. `just test` fails when it's stale.
acknowledgements: deps
    ./scripts/fetch-cef.sh >/dev/null
    go run ./internal/acknowledgements/gen

# Regenerate the emoji picker's list (a linked computer's icon) from Unicode's emoji-test.txt.
emoji-data version="16.0":
    scripts/emoji-data.sh {{version}}

# Regenerate the program icon tables from scripts/program-icons/icons.json:
# HammockKit's paths and SF Symbols, and the TUI's Nerd Font glyphs.
program-icons:
    cd scripts/program-icons && npm ci --silent
    test -f scripts/program-icons/node_modules/.glyphnames-3.3.0.json || curl -fsSL https://raw.githubusercontent.com/ryanoasis/nerd-fonts/v3.3.0/glyphnames.json -o scripts/program-icons/node_modules/.glyphnames-3.3.0.json
    node scripts/program-icons/generate.mjs scripts/program-icons/node_modules/.glyphnames-3.3.0.json
    gofmt -w internal/tui/programicons_gen.go

# The libghostty-vt xcframework in the Swift package (now part of `deps`).
apple-deps: deps

# Build Hammock for Linux into dist/linux: hmk, hmkd and pane-holder, and a
# tarball, for each architecture (amd64 and arm64 unless named), with zig.
linux *archs:
    ./scripts/build-linux.sh {{archs}}

# Build the Unraid plugin's package (.txz) and .plg into dist/unraid.
unraid: (linux "amd64")
    ./unraid/build.sh

# Build the browser host (CEF; downloads the pinned build on first use).
browser:
    ./helpers/browser/build.sh

# Build the simulator panes' host into bin/Hammock Simulator.app.
simulator:
    ./helpers/simulator/build.sh

# Build the screen-sharing host into bin/Hammock Screen.app (ADR-0014).
screen:
    ./helpers/screen/build.sh

# Build the Hammock Key helper into bin/Hammock Key.app.
key:
    ./helpers/key/build.sh

# Build Hammock.app (with the Go daemon binaries bundled) into dist/.
mac-app: build apple-deps browser simulator screen key
    ./scripts/build-mac-app.sh

# Update the Hammock you're running to this checkout's build: build it
# (mac-app, swapped in beside the running one), quit the running app and
# its daemon (panes keep running), and open the new one. The main checkout
# only (a worktree's app is its β: just beta).
prod: mac-app
    ./scripts/update-prod.sh

# share's first step: a locked screen, or an agent's sandbox hiding the keychain, fails before the build
# (scripts/preflight).
[private]
share-preflight:
    scripts/preflight screen notary

# A notarized Hammock.app to send to someone: built into dist/share, signed
# with Developer ID, notarized and stapled, zipped as dist/share/Hammock.zip,
# and in dist/share/Hammock.dmg, the first-install download (also notarized).
# Notarization's credentials (team Homegrown Software) come from the
# notarytool keychain profile NOTARY_PROFILE (default fika-notary), which
# needs nobody at the Mac. Make it once with `xcrun notarytool
# store-credentials fika-notary --team-id 2G974K78BH` and an Apple ID and
# app-specific password, or an App Store Connect API key (--key, --key-id,
# --issuer). Without it, the Apple ID and password are read from 1Password,
# which asks on screen; OP_BIOMETRIC_UNLOCK_ENABLED makes op find the
# 1Password app even when it doesn't detect the CLI integration by itself.
# Name the item (fields username and password) and account in .release.env:
# HMK_SHARE_OP_ITEM=op://<vault>/<item>, HMK_SHARE_OP_ACCOUNT=<you>.1password.com.
share_op_item := env("HMK_SHARE_OP_ITEM", "")
share_op_account := env("HMK_SHARE_OP_ACCOUNT", "")
share: share-preflight build apple-deps browser simulator screen key
    #!/usr/bin/env bash
    set -euo pipefail
    OUT="{{justfile_directory()}}/dist/share" ./scripts/build-mac-app.sh
    export NOTARY_PROFILE="${NOTARY_PROFILE:-fika-notary}"
    if [ -n "${APPLE_ID:-}" ] && [ -n "${APP_PASSWORD:-}" ]; then
        # Read already (scripts/release asks for them up front).
        unset NOTARY_PROFILE
    elif ! xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1; then
        echo "no notarytool profile $NOTARY_PROFILE; reading the credentials from 1Password" >&2
        if [ -z "{{share_op_item}}" ] || [ -z "{{share_op_account}}" ]; then
            echo "set HMK_SHARE_OP_ITEM and HMK_SHARE_OP_ACCOUNT (in .release.env, see the justfile), or make the notarytool profile" >&2
            exit 1
        fi
        unset NOTARY_PROFILE
        export OP_BIOMETRIC_UNLOCK_ENABLED=true
        APPLE_ID="$(op read '{{share_op_item}}/username' --account {{share_op_account}})"
        APP_PASSWORD="$(op read '{{share_op_item}}/password' --account {{share_op_account}})"
        export APPLE_ID APP_PASSWORD
    fi
    ./scripts/notarize-mac-app.sh "{{justfile_directory()}}/dist/share/Hammock.app"

# An unsigned Hammock.dmg, to look at its window (scripts/make-dmg.sh): from
# dist/share/Hammock.app unless given another app, into dist/dmg-preview.
# Open it on each new major macOS before releasing: Finder draws the icons and
# their names over the background, and has moved them before.
dmg app=(justfile_directory() / "dist/share/Hammock.app"):
    ./scripts/make-dmg.sh "{{app}}" "{{justfile_directory()}}/dist/dmg-preview/Hammock.dmg"
    open "{{justfile_directory()}}/dist/dmg-preview/Hammock.dmg"

# ---- Releases and their notes (release-notes/README.md)

# Add a release note: `just note mac fixed "Pane headers no longer flicker."` (mac, ios or both).
[positional-arguments]
note *args:
    scripts/note "$@"

# Check that a change to app code adds a release note for each app it reaches.
[positional-arguments]
note-check *args:
    scripts/note-check "$@"

# Show each app's version, or set them: `just version 2026.2.0` (both), `just version mac 2026.1.4` (one).
[positional-arguments]
version *args:
    scripts/version "$@"

# Cut a Mac release (a Sparkle update, published as a GitHub Release): `just release-mac next` (the next patch,
# 2026.1.0 → 2026.1.1) or a version (+ allow-empty). Builds, notarizes (the fika-notary keychain profile; see share)
# and uploads it. Versions: release-notes/README.md.
[positional-arguments]
release-mac *args:
    scripts/release mac "$@"

# Cut a TestFlight build, built here and uploaded (scripts/ios-testflight): `just release-ios build` (another build of the
# iPhone's version), `next` (a hotfix's new patch, which App Review sees again) or a version (+ allow-empty).
[positional-arguments]
release-ios *args:
    scripts/release ios "$@"

# Cut both apps' joint release in one release commit: `just release-apple next` (2026.1.x → 2026.2.0) or a version.
[positional-arguments]
release-apple *args:
    scripts/release apple "$@"

# The iPhone app's App Store listing and TestFlight test information come from marketing/appstore/ (its README).
# Install Fastlane and Playwright (for the marketing images) into marketing/, once per machine (Ruby 3.3, Node 24).
appstore-install:
    cd marketing && bundle config --local path vendor/bundle && bundle install
    cd marketing && pnpm install && pnpm exec playwright install chromium

# Offline: limits, fields, keywords, URLs, categories, age rating, review notes, TestFlight's text.
appstore-check:
    node --disable-warning=ExperimentalWarning marketing/scripts/check-metadata.ts

# A read-only look before appstore-push: the key, the app, its versions, What's New, TestFlight, URLs, precheck.
appstore-preview: appstore-check
    marketing/scripts/appstore-fastlane ios preview

# Saves in App Store Connect; never submits, never a binary.
# Upload the listing text, screenshots, age rating, review details, What's New and TestFlight's test information.
appstore-push: appstore-check
    marketing/scripts/appstore-fastlane ios push

# Upload only TestFlight's test information.
appstore-push-testflight: appstore-check
    marketing/scripts/appstore-fastlane ios push_testflight

# Overwrite marketing/appstore/metadata/ with what App Store Connect holds, then review it with git diff.
appstore-pull:
    marketing/scripts/appstore-pull

# Set the repo's About on GitHub (description, website, topics) from marketing/github/.
github-about:
    marketing/scripts/github-about push

# Overwrite marketing/github/ with the About GitHub has, then review it with git diff.
github-about-pull:
    marketing/scripts/github-about pull

# Take the App Store screenshots from the demo in the Simulator: [SCENE] [--light|--dark] [--device iphone|ipad|duo].
appstore-screenshots *args:
    marketing/scripts/appstore-screenshots {{args}}

# Compose the marketing images from the screenshots: [--only PANEL] [--device iphone|ipad|duo].
appstore-marketing *args:
    node --disable-warning=ExperimentalWarning marketing/scripts/appstore-marketing.ts {{args}}

# Record the App Store's videos from the demo and cut them (the app preview and the search results video).
appstore-preview-video *args:
    marketing/scripts/appstore-preview-video {{args}}

# Record the README's demo GIF (marketing/demo.gif) from this checkout's β, on a scratch daemon with made-up Groups.
readme-demo: mac-dev
    scripts/readme-demo

# What's New for an iPhone version, from its release notes (--pending: with the notes not cut yet).
appstore-whats-new version *args:
    marketing/scripts/appstore-whats-new {{version}} {{args}}

# Copy the website (marketing/) into the homegrownsw.com site repo, as site/hammock/ (HMK_SITE_REPO, default
# ~/Developer/homegrownsw-site). Commit it there and `just deploy` it from there: this publishes nothing.
# The page's comments are for the repo, so the copy drops them: each one starts its own line, `//` or `/* … */`.
site:
    #!/usr/bin/env bash
    set -euo pipefail
    repo="${HMK_SITE_REPO:-$HOME/Developer/homegrownsw-site}"
    [ -d "$repo/site" ] || { echo "No site repo at $repo (set HMK_SITE_REPO)." >&2; exit 1; }
    out="$repo/site/hammock"
    mkdir -p "$out"
    strip() { perl -0pe 's{^[ \t]*/\*.*?\*/[ \t]*\n}{}gms; s{^[ \t]*//.*\n}{}gm' "$1"; }
    strip marketing/index.html > "$out/index.html"
    strip marketing/page.css > "$out/page.css"
    for p in privacy support; do mkdir -p "$out/$p"; strip "marketing/$p.html" > "$out/$p/index.html"; done
    cp marketing/banner.png "$out/"
    sips -Z 512 apple/Resources/AppIcon-1024.png --out "$out/icon-512.png" >/dev/null
    sips -Z 256 apple/Resources/AppIcon-1024.png --out "$out/icon-256.png" >/dev/null
    find "$out" -type f -exec chmod 644 {} +
    git -C "$repo" status --short -- site/hammock

# The release scripts' tests: notes, the check, and cutting releases against a throwaway repo.
test-release:
    scripts/tests/release

# Dev build "Hammock β" with its own daemon and state, for working on
# Hammock without touching the prod app you use day to day. In a worktree it's
# that worktree's own, "Hammock β <branch>", with its own daemon and state too
# (scripts/beta-identity.sh; BETA_SHARED=1 builds the shared one).
mac-dev: build apple-deps browser simulator screen key
    VARIANT=beta ./scripts/build-mac-app.sh

# Build this checkout's β and (re)start it: only this copy, by its path.
beta: mac-dev
    ./scripts/beta.sh restart

# This checkout's β: its app, state dir, socket, and whether it's running.
beta-info:
    @./scripts/beta.sh info

# Point hmk at this checkout's β daemon: eval "$(just beta-env)".
beta-env:
    @./scripts/beta.sh env

# Remove a worktree's own β: its processes, state dir and app. Run it
# before removing the worktree (just worktree-remove does).
beta-clean:
    ./scripts/beta.sh clean

# Remove β state dirs left by worktrees removed without beta-clean.
beta-prune:
    ./scripts/beta.sh prune

# Land .claude/worktrees/NAME's branch on main (rebased, fast-forwarded, pushed) and tear the worktree down,
# from any checkout; stops while a release runs (scripts/land). Run the tests first.
land name:
    scripts/land {{name}}

# Tear down .claude/worktrees/NAME: its own β (beta-clean), then the
# worktree, then its branch if it's merged. Stops if anything still runs
# from the worktree (the shared β's daemon started from its build, say).
worktree-remove name:
    #!/usr/bin/env bash
    set -euo pipefail
    wt="{{justfile_directory()}}/.claude/worktrees/{{name}}"
    [ -d "$wt" ] || { echo "error: no worktree at $wt" >&2; exit 1; }
    BETA_ROOT="$wt" ./scripts/beta.sh clean
    running="$(ps -axww -o pid=,command= | while read -r pid cmd; do case "${cmd:-}" in (*"$wt/"*) echo "$pid $cmd" ;; esac; done)"
    if [ -n "$running" ]; then
        printf 'error: these still run from %s; stop them (or restart them from another build) first:\n%s\n' "$wt" "$running" >&2
        exit 1
    fi
    branch="$(git -C "$wt" symbolic-ref --short -q HEAD || true)"
    # Only a link to the shared Ghostty build; untracked in worktrees made
    # before .gitignore matched it.
    [ ! -L "$wt/third_party/ghostty/out" ] || rm "$wt/third_party/ghostty/out"
    git worktree remove "$wt"
    [ -z "$branch" ] || git branch -d "$branch" || echo "kept branch $branch: it isn't merged (git branch -D to drop it)"

# Fails when benchmarks leave their budgets (run in CI).
bench-gate:
    ./scripts/bench-gate.sh

# Latency and throughput benchmarks (see docs/benchmarks.md).
bench:
    go test -tags {{tags}} -run xxx -bench InputEcho -benchtime 2000x ./internal/daemon/
    go test -tags {{tags}} -run xxx -bench OutputThroughput -benchtime 1x ./internal/daemon/
    go test -run xxx -bench . -benchtime 3x ./internal/holder/

# The Mac terminal renderer's draw time on fixed screens, offscreen, in this
# checkout's β (RenderBench.swift, docs/benchmarks.md). Writes OUT (a JSON
# file; a temporary one if empty) and a PNG of each screen beside it.
render-bench out="": mac-dev
    #!/usr/bin/env bash
    set -euo pipefail
    eval "$(./scripts/beta-identity.sh)"
    out="{{out}}"
    [ -n "$out" ] || out="$(mktemp -d)/render.json"
    # Light, whatever the Mac's appearance, so runs (and their PNGs) compare.
    HMK_DEBUG_RENDER_BENCH="$out" "$beta_app/Contents/MacOS/Hammock" -appearance light
    echo "results: $out"

# Generate the iOS Xcode project and build it for the simulator.
ios: deps
    xcodebuild -project apple/iOS/HammockIOS.xcodeproj -scheme Hammock -destination 'platform=iOS Simulator,name=iPhone 18 Pro' -derivedDataPath /tmp/hmk-ios-build CODE_SIGNING_ALLOWED=NO build

# Build the iOS app for an iPhone and install it (Release, signed for the team, HMK_TEAM, by default ours;
# with DevLog's timings: HMK_DEV). The iPhone: `just ios-device <udid>`, else HMK_IOS_DEVICE, else the one
# real iPhone paired with this Mac (`xcrun devicectl list devices`). HMK_PUSH_RELAY is the push relay's URL
# (relay/push); it defaults to ours.
ios-device device="": deps
    #!/usr/bin/env bash
    set -euo pipefail
    device="{{device}}"
    device="${device:-${HMK_IOS_DEVICE:-}}"
    if [ -z "$device" ]; then
        list="$(mktemp)"; trap 'rm -f "$list"' EXIT
        xcrun devicectl list devices --json-output "$list" >/dev/null
        phones="$(jq -r '.result.devices[] | select(.hardwareProperties.reality == "physical" and .hardwareProperties.deviceType == "iPhone") | "\(.hardwareProperties.udid)\t\(.deviceProperties.name)"' "$list")"
        if [ -z "$phones" ]; then echo "No iPhone is paired with this Mac." >&2; exit 1; fi
        if [ "$(wc -l <<<"$phones")" -gt 1 ]; then
            printf 'More than one iPhone; name one (just ios-device <udid>, or HMK_IOS_DEVICE):\n%s\n' "$phones" >&2; exit 1
        fi
        device="${phones%%$'\t'*}"
        echo "Installing on ${phones#*$'\t'} ($device)"
    fi
    xcodebuild -project apple/iOS/HammockIOS.xcodeproj -scheme Hammock -configuration Release -destination "id=$device" -derivedDataPath /tmp/hmk-ios-device DEVELOPMENT_TEAM="${HMK_TEAM:-2G974K78BH}" SWIFT_ACTIVE_COMPILATION_CONDITIONS='$(inherited) HMK_DEV' HMK_PUSH_RELAY="${HMK_PUSH_RELAY:-https://hmk-push.homegrownsw.workers.dev}" -allowProvisioningUpdates build
    xcrun devicectl device install app --device "$device" /tmp/hmk-ios-device/Build/Products/Release-iphoneos/Hammock.app
