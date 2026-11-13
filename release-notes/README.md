# Release notes

One small file per change, for each app it reaches. When a release is cut, an app's pending notes are
frozen into it: on the Mac they become the notes in Sparkle's update window (embedded in the appcast on
the update server), on iOS TestFlight's "What to Test". This follows the release
machinery of the owner's other apps; the TUI will get notes of its own later.

```
release-notes/mac/        and the same for release-notes/ios/
  pending/        one file per change not yet in a release
  v<x.y.z>/       the notes frozen into releases of that version
  v<x.y.z>.md     the version's summary, written when the version is bumped
```

The Mac app ships the daemon, the `hmk` CLI, the TUI and the helpers, so a change to any of them is a Mac
change. HammockKit and the protocol reach both apps.

## A note

`release-notes/<app>/pending/<issue>-<slug>.md`, or `<slug>.md` without an issue:

```
tag: FIXED
date: 2026-11-05
Pane headers no longer flicker while you type.
```

- **The first line** is `tag: NEW`, `tag: FIXED` or `tag: INTERNAL`.
- **The second** is the date it was written, which `just note` adds.
- **The rest** is the note, one or two sentences, or a short paragraph for a feature several changes built.
  NEW and FIXED are for the people using Hammock (friends testing it today), so write them for them: what they
  can do now, or what no longer goes wrong. One feature gets one note, however many commits built it: merge
  them before the release. INTERNAL can be terse; it never leaves this repo.

Wherever notes are listed, they're in sections with a heading (New, Fixed, and Internal where INTERNAL is
shown), in that order, rather than a tag on each note.

## Adding one: `just note`

```sh
just note mac fixed "Pane headers no longer flicker while you type."
just note ios new 44 "Long-press a pane to close it."      # with its GitHub issue (44 or #44)
just note both internal none "Protocol tidy-up."           # both apps; `none`: no issue
```

The app comes first: `mac`, `ios` or `both`. `tag` is `new`, `fixed` or `internal`, in any case. A note for
both apps has the same name in each. The issue is optional; left out, it comes from a branch named
`<name>/<n>-<slug>`. With no tag or text, `just note` prompts ([gum](https://github.com/charmbracelet/gum)).

## The check: `just note-check`

It fails a change to app code that adds no note under the `pending/` of an app it reaches, comparing the
working tree (committed or not) with where this checkout left `origin/main`. Run it before you push.

- **The Mac app:** `apple/Sources/{HammockMac,HammockMenu,HammockUpdates}/`, `apple/iOS/HammockMac/`,
  `apple/Resources/`, `cmd/`, `internal/` and `helpers/`.
- **The iOS app:** `apple/iOS/{Hammock,HammockNotify,Shared}/`.
- **Both:** `apple/Sources/HammockKit/` and `proto/`.
- **Not counted:** tests (`*_test.go`, `testdata/`, `apple/Tests/`), release output
  (`apple/iOS/TestFlight/`), scripts, docs, the relay and build settings.
- **A change nobody using Hammock will notice** (a refactor, a test-only change) can skip it:
  `NO_RELEASE_NOTE=1 just note-check`. When in doubt, add an `internal` note.

## Cutting releases: `just release-mac`, `release-ios` and `release-apple`

All three run `scripts/release`: for the Mac (a Sparkle update), for iOS (a TestFlight build), or both in
one step. A release pushes straight to `main`, so it's the owner's to run, or an agent's when asked.

### Versions and numbers

Each app has its own version, `YYYY.N.P`, in `apple/iOS/Config/Version.xcconfig` (`HAMMOCK_MAC_VERSION` and
`HAMMOCK_IOS_VERSION`, which the targets make their `MARKETING_VERSION`):

- **YYYY** is the year the release ships, and **N** counts the year's joint releases: both apps, cut together
  with `release-apple`, at `YYYY.N.0`. When the first two numbers match, the apps line up.
- **P** is a patch. Mac releases cost nothing, so between joint releases the Mac ships `2026.1.1`, `2026.1.2`…,
  every Mac release a new version. A new iPhone version costs an App Review (TestFlight's too), so the iPhone
  gets more TestFlight builds of its `YYYY.N.0` instead, and a patch only for a hotfix (it takes the iPhone's
  own next patch, whatever the Mac's of that number held).
- A new year starts again at `.1.0`: `2027.1.0` follows `2026.5.x`.

The release commit itself sets the version, so the file holds each app's latest release (or the next one, if
it was set ahead). `just version` shows both; `just version 2026.2.0` sets both, `just version mac 2026.1.4`
one, only to release at a version `next` wouldn't choose.

| App | Version shown | Build number |
| --- | --- | --- |
| Mac (Sparkle) | `CFBundleShortVersionString`, `HAMMOCK_MAC_VERSION` | `CFBundleVersion`: the release commit's number on `main` (`git rev-list --count`), which Sparkle compares |
| iOS | `CFBundleShortVersionString`, `HAMMOCK_IOS_VERSION` | `CFBundleVersion`: the same, the release commit's number, which App Store Connect asks only to go up |

A release is tagged `Mac-v<x.y.z>-r<n>` or `iOS-v<x.y.z>-r<n>`, where **n is the release commit's number on
`main`**: so `Mac-v2026.1.0-r1040` is Mac build 1040, and an iOS release cut alongside it is
`iOS-v2026.1.0-r1040`, iPhone build 1040. Every cut makes a commit, so no two releases share a number.
(Earlier TestFlight builds came from Xcode Cloud, with its own, lower, build numbers.) The committed `CURRENT_PROJECT_VERSION` is **0**, meaning "not a counted build".

Before 2026.1.0 the version was `0.x.y`, one for both apps, and a version had several releases of each.

### What it does

1. **Checks** that the tree is clean and exactly level with `origin/main` (the commit and tags are made
   before the push). A detached HEAD on `main` is fine; another branch isn't, unless
   `HMK_RELEASE_BRANCH=<branch>` names it (a worktree level with `main`).
2. **Chooses the version** for each app (`next`, `build` or the one given, below) and writes it into
   Version.xcconfig.
3. **Freezes the notes**: this release's are the pending NEW and FIXED notes, with the earlier releases' of
   its family (`2026.1.*`) after them, since Sparkle only shows the newest release's notes.
   - Mac: as HTML for Sparkle's window and the GitHub Release (this release's New and Fixed, then
     `Earlier in 2026.1`'s);
   - iOS: `apple/iOS/TestFlight/WhatToTest.{en-GB,en-US}.txt`, which the upload sends from the tagged
     commit, cut at TestFlight's 4000 characters.
4. **Files** the pending notes under `release-notes/<app>/v<version>/`; an app moving on from a version gets
   that version's summary, `release-notes/<app>/v<old>.md` (INTERNAL included). It **commits**
   `release(mac): v<version> (r<n>)` (or `release(ios): …`, or for both `release: v<version> (r<n>, Mac and iOS)`)
   and **tags** it.
5. **Builds the Mac update** from the tagged commit (`scripts/mac-update build`): `UPDATES=1 just share`
   (Developer ID, notarized; 1Password asks first), then `generate_appcast` signs it with the EdDSA key
   (keychain, account "fika"; backed up in 1Password), makes deltas from earlier releases, and embeds the
   notes. **Builds the iPhone's** (`scripts/ios-testflight build`): archives it with the release's build
   number and exports it signed for App Store Connect, to `dist/testflight/<n>/Hammock.ipa`, checking its
   version, build number and push relay. If anything here fails, the release commit and tags are taken back
   and nothing is published.
6. **Pushes** `main` and the tags in one atomic push. If it's rejected, everything is taken back as in 5.
7. **Uploads the Mac update** (`scripts/mac-update upload <n>`): the zip and deltas, then the appcast, to
   the update server. Installed copies are offered it at their next daily check. A failed upload doesn't
   undo the release; it says how to retry.
8. **Uploads the iPhone's build to TestFlight** (`scripts/ios-testflight upload <n>`), with What to Test from
   the tag, and waits for App Store Connect to process it. A failed upload doesn't undo the release either.

`next` is the next patch for the Mac or iOS (`2026.1.0` → `2026.1.1`) and the next joint release for both
(→ `2026.2.0`, or `2027.1.0` in a new year). An app whose version in the file hasn't been released yet is
released at that version instead. `build` (iOS only) is another TestFlight build of the iPhone's version.

```sh
just release-mac                          # prompts (gum)
just release-mac next                     # a Mac update: 2026.1.0 → 2026.1.1
just release-ios build                    # another TestFlight build of the iPhone's 2026.1.0
just release-ios next                     # an iPhone hotfix, 2026.1.1 (App Review again)
just release-apple next                   # both apps' next joint release, 2026.2.0
just release-apple 2026.3.0               # …or a version of your choosing
just release-mac next allow-empty         # a release with no notes for people to read
HMK_RELEASE_DRYRUN=1 just release-apple next   # print the plan, change nothing
```

With arguments every confirmation is a yes, and the edge cases fail instead: a release with nothing for
people to read needs `allow-empty`, and a version must go up (the Mac's must be new).

### Where releases go

Each Mac release is a GitHub Release on `homegrownsw/hammock` (`scripts/mac-update upload`), marked Latest:
the zip (also as `Hammock.zip`) and its deltas, the DMG for first installs (as `Hammock.dmg`, built by
`just share`), the appcast, and the Linux tarballs and Unraid plugin. The app and the website use
`homegrownsw.com/hammock`, whose nginx (`nginx/hammock.conf` in `homegrownsw/homegrownsw-site`)
redirects to the latest release: the feed is `https://homegrownsw.com/hammock/appcast.xml`, and
`https://homegrownsw.com/hammock/download` is the link for someone's first install. Moving releases elsewhere
means changing those redirects, not the app. Every released zip is also kept in `dist/releases/` (the last
few fetched from GitHub when missing) so deltas can be made from them. Uploading needs `gh`, signed in with
access to the repo.

### TestFlight builds

`scripts/ios-testflight` builds the iPhone app on this Mac: `xcodebuild archive` of the `Hammock` scheme
(Release, signed for development with automatic signing), then `-exportArchive` for App Store Connect, which
re-signs it with Xcode's cloud-managed distribution certificate, so no Apple Distribution certificate is needed
here. Both, and the upload (Fastlane's `upload_to_testflight`, the `upload_build` lane), authenticate with the
App Store Connect API key in `marketing/.env.appstore` (marketing/appstore/README.md). A release asks
1Password for everything it needs at the start, before building anything: this key for the iPhone, and
notarization's Apple ID and password for the Mac (unless there's a notarytool profile), then holds them until
it ends, so nothing is left to time out an hour in. Run releases outside an agent's sandbox. The key needs the Admin role, or
App Manager with access to cloud-managed distribution certificates. `HMK_TESTFLIGHT_GROUPS=Group,Other` also
sends the build to those external testers' groups; internal groups get it as App Store Connect is set up to.

The push relay (`relay/push`, a Cloudflare Worker) is at https://hmk-push.homegrownsw.workers.dev, built into
the app (`HMK_PUSH_RELAY` overrides it). Phones built from Xcode use APNs' sandbox and TestFlight builds its
production environment, so the relay's APNs key must allow both (Apple turns a key down for the wrong one with
`BadEnvironmentKeyInToken`, which the daemon log shows).

The Xcode project's `Hammock Mac` scheme (the `HammockMacApp` target: the same bundle as `just mac-app`, its
helpers embedded and signed by a build phase from `bin/`, built by `scripts/build-mac-payloads.sh`) is for
archiving the Mac app in Xcode; releases are built with `just share`.

## Tests

`just test-release` runs `scripts/tests/release`: the real scripts against throwaway repos with a bare
remote (they commit, tag and push for real), with stand-ins for `scripts/mac-update` and
`scripts/ios-testflight` so nothing is built or uploaded. It covers `just note` and `note-check`, the dry run,
choosing versions, iOS, Mac and joint cuts, a failed build and a failed upload of each app, a rejected push,
the preflight, and that every committed note is well formed.
