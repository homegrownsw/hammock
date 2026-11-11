# The App Store listing

The iPhone app's App Store listing, and its TestFlight test information, live here and are pushed to App Store
Connect with Fastlane, so a change to them is a reviewable diff rather than something typed into a web form and
forgotten.

```
marketing/
  appstore/
    metadata/                 the listing text, as deliver reads it
      en-GB/  en-US/          one directory per locale: name, subtitle, promotional text, description, keywords,
                              URLs. The same words for now (nothing in them is spelt differently): change both
      review_information/     notes.txt, what App Review is told. The contact isn't here (see "The API key")
      copyright.txt  primary_category.txt  secondary_category.txt  …
    testflight/<locale>/      TestFlight's Beta App Description and URLs (its feedback email is the review contact's)
    age_rating.json           the age rating questionnaire (deliver's app_rating_config_path)
    app-privacy.md            the App Privacy label, entered by hand (deliver can't upload it), and why
    whats-new/                GENERATED, gitignored
  fastlane/  Appfile Fastfile how it's pushed
  scripts/                    appstore-fastlane (the key), appstore-pull, appstore-whats-new, check-metadata.ts
  Gemfile  Gemfile.lock       Fastlane, pinned (bundler, into marketing/vendor/)
  package.json                for the Node scripts
```

**Nothing here builds or uploads the app.** Builds come from Xcode Cloud (`just release-ios`,
release-notes/README.md). Fastlane is here for `deliver` and App Store Connect's API, nothing else: no
`build_app`, no `match`, no signing.

## The commands

```bash
just appstore-install          # Fastlane, once per machine (Ruby 3.3)
just appstore-check            # offline: limits, fields, keywords, URLs, categories, age rating, review notes
just appstore-preview          # read-only against App Store Connect: the key, the app, its versions, What's New,
                               # what a push would change in TestFlight, the URLs, precheck
just appstore-push             # upload the listing text, age rating, review details, What's New and TestFlight's
                               # test information. Saves; never submits
just appstore-push-testflight  # only TestFlight's test information
just appstore-pull             # overwrite metadata/ with what App Store Connect holds, then `git diff`
just appstore-whats-new 0.3.0  # What's New for a version, from its release notes (--pending for the one being built)
```

The loop is **edit → `appstore-check` → `appstore-preview` → `appstore-push`**, then submit the version for
review in App Store Connect by hand. The push never renames the app by accident: it refuses when `name.txt`
differs from the store's, until `HMK_APPSTORE_ALLOW_RENAME=1`.

## The API key

Everything that talks to Apple goes through `scripts/appstore-fastlane`, with an **App Store Connect API key**,
never an Apple ID (an Apple ID means a two-factor prompt no script can answer). The key, and App Review's contact,
go in `marketing/.env.appstore` (gitignored; in a worktree, the main checkout's is used), one `NAME=value` a line:

- `ASC_KEY_ID`, `ASC_ISSUER_ID`, and `ASC_KEY_FILE` (the .p8's path) or `ASC_PRIVATE_KEY` (its contents)
- `ASC_REVIEW_FIRST_NAME`, `ASC_REVIEW_LAST_NAME`, `ASC_REVIEW_EMAIL`, `ASC_REVIEW_PHONE`: App Review's contact,
  also TestFlight's feedback email. Personal data, so never in git; `appstore-check` fails if any of it is.

Any value can be a 1Password reference (`op://vault/item/field`): the script then runs itself under `op run`,
which resolves them. App Store Connect's calls need the script to reach 1Password, so run them outside an agent's
sandbox.

## The words

They describe what the app does today; check them when it changes. `appstore-check` holds them to App Store
Connect's limits and a few rules: keywords comma-separated without spaces, no keyword repeating a word of the
name or subtitle (both are searched anyway), no other companies' product names as keywords, HTTPS URLs, and no
words App Review rejects (other platforms' stores, "beta", "TestFlight").

**What's New** isn't in `metadata/`: the push writes it from the iPhone's release notes (NEW and FIXED, never
INTERNAL) for the version App Store Connect is editing, including those not yet cut when it's the version being
built. A first version has none, so nothing is sent for it. Never commit a `release_notes.txt` under `metadata/`.

**The age rating**: everything is "none" except **unrestricted web access**, since the app shows the Mac's browser
panes, which can go to any page. That's the honest answer, and it raises the rating.

**The App Privacy label** is entered by hand from `app-privacy.md` (Data Not Collected, and why). Update both
together when the app starts sending anything new. The privacy policy the listing links to is
homegrownsw.com/hammock/privacy/.

**App Review** gets `review_information/notes.txt`: there's no sign-in, and the app's demo ("See a demo" on the
first screen) lets a reviewer try it without a Mac, with the demo video at homegrownsw.com/hammock/demo.mp4.

## The screenshots

```bash
just appstore-screenshots                  # every scene, light and dark, on the iPhone, the iPad and iPhone Duo
just appstore-screenshots 02-allow         # …or one scene
just appstore-screenshots --device iphone  # …or one device (iphone, ipad, duo; repeatable); --light or --dark
just appstore-screenshots --skip-build     # reuse the last Simulator build
```

`scripts/appstore-screenshots` builds the Debug iPhone app for the Simulator and launches it once per scene in
`scenes.json`, into its demo (`apple/iOS/Hammock/DemoMac.swift`: a made-up Mac standing in for the daemon, so
nothing personal is ever in a screenshot). A scene is the app's own `HMK_DEBUG_*` variables (AGENTS.md: which tab,
which screens pushed); `HMK_DEBUG_SCREENSHOT=1` hides the demo's bar and writes `HammockScreenshot: done` to stderr
once the screen has settled, and the picture is taken then. The app is installed afresh on each simulator first,
and the status bar reads 9:41 with full battery and signal.

Each scene is taken in light and dark, on three simulators of the script's own ("Hammock App Store iphone",
"… ipad", "… duo", made when missing, so yours are never touched), one at a time:

| Device | Simulator | Size | File |
|---|---|---|---|
| 6.9" iPhone | iPhone 17 Pro Max | 1320 × 2868 | `screenshots/en-GB/NN-<scene>-iphone69.png` |
| 13" iPad | iPad Pro 13-inch (M5) | 2064 × 2752 | `screenshots/en-GB/NN-<scene>-ipad13.png` |
| iPhone Duo, unfolded | iPhone Duo (iOS 27.1) | 2853 × 2007 | `duo/screenshots/en-GB/NN-<scene>-duo.png` |

**The iPhone Duo is shown unfolded**, on its inner display in landscape, where the sidebar and a pane sit side by
side (`HMK_DUO_ORIENTATION=portrait` for 2007 × 2853). The script opens it with Hammock's own simulator guest
helper (`helpers/simulator/guest`: `hinge 180`), built if it's missing. deliver has no iPhone Duo display type
yet, so that set is kept apart, in `duo/`.

The app's words are English only, so one set, in `en-GB` (the listing's primary language), serves every locale.
All of it is GENERATED and gitignored, with a `.captured` stamp naming the app version and commit.

## The marketing images

```bash
just appstore-marketing                # every device and panel, and a contact sheet of each set
just appstore-marketing --only allow   # …or one panel; --device iphone|ipad|duo for one device
```

What the listing shows: each screenshot in its device's frame ([frames-cli](https://github.com/viticci/frames-cli),
`FRAMES_BIN` or `frames` on the PATH), under a headline and a subline, in the website's colours and type (Space
Grotesk and Inter; Hammock's spectrum on the headline's *starred* words), rendered by Playwright's Chromium at
exactly the device's size and flattened to opaque RGB. `scripts/appstore-marketing.ts` only draws what
`marketing.json` says: each panel's screenshot, theme (light or dark: which screenshot and which ground) and copy,
and each device's type sizes, layout (copy above the device, or beside it on the landscape Duo) and frame colour.
It refuses a headline that wraps onto more lines than it's written on, or a subline over two.

The images go to `marketing/<locale>/NN-<panel>-<device>.png` (the iPhone and iPad sets, which deliver uploads)
and `duo/marketing/<locale>/` (the iPhone Duo's), with `preview-<device>.png` beside them, each set side by side.
GENERATED and gitignored. `just appstore-install` installs Playwright's Chromium (pnpm, into `marketing/node_modules`).

The push leaves App Store Connect's screenshots as they are (`skip_screenshots`) until uploading them is wired
in (next), and the app preview after that.
