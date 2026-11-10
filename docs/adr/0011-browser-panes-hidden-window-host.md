# ADR-0011: Browser panes run windowed Chromium behind a hidden window

Status: Proposed · 2026-10-30 (pending the at-desk feel test in [the spike notes](../internals/browser-panes.md))

## Context
ADR-0005 chose daemon-owned CEF helpers with remote CoreAnimation layers for local display. CEF's supported way to render outside its own window is offscreen rendering (OSR), and its input API has no trackpad gesture phases or pinch. That rules out rubber-banding and native-feeling scrolling, which are the top priority for "feels transparent".

## Decision
- Each browser pane is a CEF browser in windowed (Alloy) mode, inside a transparent, mouse-ignoring window owned by the `browser-host` helper. The window is kept exactly behind the pane.
- Hammock.app shows Chromium's own GPU `CAContext` through `CALayerHost`. No frames are copied or re-encoded locally.
- Input is forwarded as raw `CGEvent` bytes and replayed into Chromium's `NSView`.
- Text input runs in Hammock.app and is replayed into Chromium through a swapped `NSTextInputContext`.
- OSR with shared IOSurfaces (design A) stays as the fallback if remote layers break, and is the source for the remote (video) tier.

## Consequences
- Native scrolling, gestures, popups and IME positioning come from Chromium itself.
- Depends on Chromium internals: the `RenderWidgetHostViewCocoa` responder methods, and finding its `CALayerHost`. Pin the CEF version and test on every CEF update.
- Dialogs, file pickers and permission prompts must be routed to Hammock through CEF handlers, so the helper never comes to the front.
- Accessibility needs remote-element bridging (not yet designed).
