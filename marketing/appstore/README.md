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

## Screenshots and previews (next)

Not made yet, so the push leaves App Store Connect's as they are (`skip_screenshots`). The plan: capture them from
the real app in the Simulator, against the demo (made-up Groups and agents, nothing personal), in light and dark,
on the 6.9" iPhone, the 13" iPad and iPhone Duo unfolded (its inner display, where Hammock's sidebar and panes
show at their best); frame them under a headline and subline for the marketing images; and record the app
preview the same way. All generated and gitignored, with their scenes and copy committed here.
