# Contributing to Hammock

Thanks for wanting to help. Hammock is a small project with one maintainer, who reviews every change, so
contributions are welcome but kept deliberately small.

## What's welcome

- **Bug fixes**, ideally with a test that fails without the fix.
- **Small improvements:** a rough edge smoothed, a missing option, support for another coding agent,
  better docs.

For anything bigger, such as a new feature, a new kind of pane or a change to how something works, **open an
issue first** and wait for a yes before writing the code. Large features may be turned down even when they're
good ideas, because each one is something to maintain for good. Pull requests that arrive without that
conversation may be closed.

Security problems don't go in issues or pull requests: see [SECURITY.md](SECURITY.md).

## Making a change

1. Build it and run it as the [README](README.md#build-from-source) says. Test in the development copy (`just
   beta`), which has its own state, so your installed Hammock is never touched.
2. Keep the pull request to one change, with its tests: `just test` (Go) and `(cd apple && swift test)` (Swift)
   must pass.
3. **Add a release note** for each app the change reaches: `just note <mac|ios|both> <new|fixed|internal> "What
   changed."` NEW and FIXED notes are read by the people using Hammock, so write them for them. `just note-check`
   says whether your change needs one ([release-notes/README.md](release-notes/README.md)).
4. Think beyond the Mac: an action usually belongs in the menu bar, the command palette, `hmk` and the TUI, and
   sometimes the iPhone and the MCP tools too. [AGENTS.md](AGENTS.md) has the checklist for new features, new
   daemon messages and new settings.
5. Rebase on `main` rather than merging it in: `main` stays linear.

Using a coding agent is fine. [AGENTS.md](AGENTS.md) is written for it, and you're responsible for what it
writes, so read the diff before you open the pull request.

## Licence

Hammock is under the [MIT licence](LICENSE). By opening a pull request you agree that your contribution is under
it too, so it can ship in Hammock's builds, the App Store ones included. There's no separate agreement to sign.
The Hammock name and icon aren't part of the licence.
