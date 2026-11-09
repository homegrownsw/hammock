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

Wherever notes are listed, NEW comes before FIXED, and FIXED before INTERNAL where INTERNAL is shown.

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

The version is semver, `x.y.z`, and **one for both apps**: `MARKETING_VERSION` in
`apple/iOS/Config/Version.xcconfig`, which every target includes and the Mac's own build reads.
`just version` shows it; `just version 0.3.0` sets it.

| App | Version shown | Build number |
| --- | --- | --- |
| Mac (Sparkle) | `CFBundleShortVersionString`, from Version.xcconfig | `CFBundleVersion`: the release commit's number on `main` (`git rev-list --count`), which Sparkle compares |
| iOS | `CFBundleShortVersionString`, from Version.xcconfig | `CFBundleVersion`: Xcode Cloud's `CI_BUILD_NUMBER`, written in by `ci_scripts/ci_pre_xcodebuild.sh` |

A release is tagged `Mac-v<x.y.z>-r<n>` or `iOS-v<x.y.z>-r<n>`, where **n is the release commit's number on
`main`**: so `Mac-v0.2.0-r615` is Mac build 615, and an iOS release cut alongside it is `iOS-v0.2.0-r615`.
Every cut makes a commit, so no two releases share a number. App Store Connect insists on Xcode Cloud's
own build number for iOS, so that one doesn't line up; the tag's `r<n>` says which commit (and Mac build)
it goes with. The committed `CURRENT_PROJECT_VERSION` is **0**, meaning "not a counted build".

### What it does

1. **Checks** that the tree is clean and exactly level with `origin/main` (the commit and tags are made
   before the push). A detached HEAD on `main` is fine; another branch isn't, unless
   `HMK_RELEASE_BRANCH=<branch>` names it (a worktree level with `main`).
2. **Freezes the notes**: this release's are the pending NEW and FIXED notes, with the version's earlier
   releases' after them.
   - Mac: as HTML for Sparkle's window (`This release`, then `Earlier in <version>`);
   - iOS: `apple/iOS/TestFlight/WhatToTest.{en-GB,en-US}.txt`, which Xcode Cloud reads from the tagged
     commit, cut at TestFlight's 4000 characters.
3. **Files** the pending notes under `release-notes/<app>/v<version>/`, **commits**
   `release(mac): v<version> (r<n>)` (or `release(ios): …`, or for both `release: v<version> (r<n>, Mac and iOS)`)
   and **tags** it.
4. **Builds the Mac update** from the tagged commit (`scripts/mac-update build`): `UPDATES=1 just share`
   (Developer ID, notarized; 1Password asks first), then `generate_appcast` signs it with the EdDSA key
   (keychain, account "fika"; backed up in 1Password), makes deltas from earlier releases, and embeds the
   notes. If anything here fails, the release commit and tags are taken back and nothing is published.
5. **Bumps, if asked**, in a second commit after the tag (`release(mac): bump to v<next>`): sets the next
   version, and writes each app's `release-notes/<app>/v<version>.md`.
6. **Pushes** `main` and the tags in one atomic push. If it's rejected, everything is taken back as in 4.
   An `iOS-v*` tag starts Xcode Cloud.
7. **Uploads the Mac update** (`scripts/mac-update upload <n>`): the zip and deltas, then the appcast, to
   the update server. Installed copies are offered it at their next daily check. A failed upload doesn't
   undo the release; it says how to retry.

`bump` means "this is the **last** release of this version": it's tagged under the current version and the
version goes up afterwards, for both apps. To start a new version before its first release instead, set it
in its own commit (`just version 0.3.0`) and cut with `no-bump`.

```sh
just release-mac                          # prompts (gum)
just release-mac no-bump                  # a Mac update, keeping the version
just release-ios no-bump                  # a TestFlight build
just release-apple no-bump                # both, in one release commit
just release-apple bump 0.3.0             # both apps' last 0.2.0 releases, then set 0.3.0
just release-mac no-bump allow-empty      # a release with no notes for people to read
HMK_RELEASE_DRYRUN=1 just release-apple no-bump   # print the plan, change nothing
```

With arguments every confirmation is a yes, and the edge cases fail instead: a release with nothing for
people to read needs `allow-empty`, and a bump must go up.

### The update server

For now a static nginx in a container on the update server (`UPDATE_HOST`, `UPDATE_DIR` and `UPDATE_CONTAINER`
in `.release.env`), behind `https://hammock.jsp.scot`; moving it to public GitHub Releases is planned. The
feed is `https://hammock.jsp.scot/appcast.xml`; each release's zip is `https://hammock.jsp.scot/Hammock-<n>.zip`,
and `https://hammock.jsp.scot/download` redirects to the latest one: the link for someone's first install.
Every upload rewrites the one-line `latest.conf` the server's nginx includes, then reloads nginx. Every released zip is also kept in `dist/releases/` (synced back from
the server when missing) so deltas can be made from them.

### Xcode Cloud (the owner, once, in App Store Connect)

- [ ] In the iOS release workflow's start conditions, start on a **tag** matching `iOS-v*`, with the
      Archive action and TestFlight deployment.
- [ ] Check that nothing else archives on every push to `main`.

`ci_pre_xcodebuild.sh` writes `CI_BUILD_NUMBER` into Version.xcconfig and, for an `iOS-v*` tag, fails a
build whose tag names another version.

## Tests

`just test-release` runs `scripts/tests/release`: the real scripts against throwaway repos with a bare
remote (they commit, tag and push for real), with a stand-in for `scripts/mac-update` so nothing is built
or uploaded. It covers `just note` and `note-check`, the dry run, iOS, Mac and joint cuts, a failed build,
a failed upload, a rejected push, a bump, the preflight, `ci_pre_xcodebuild.sh`, and that every committed
note is well formed.
