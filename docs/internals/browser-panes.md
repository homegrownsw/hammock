# Browser panes

How browser panes run Chromium in a separate process and still feel native. Decision: [ADR-0011](../adr/0011-browser-panes-hidden-window-host.md). Code: [`helpers/browser/host.mm`](../../helpers/browser/host.mm) (the host, Hammock Browser), [`helpers/browser/helper.cc`](../../helpers/browser/helper.cc) (Chromium's sub-processes), [`apple/Sources/HammockMac/BrowserView.swift`](../../apple/Sources/HammockMac/BrowserView.swift) (the Mac's pane) and [`RemoteBrowserView.swift`](../../apple/Sources/HammockMac/RemoteBrowserView.swift) (a remote viewer's). Remote video of a page is in [remote-media.md](remote-media.md).

## Why a hidden window, not offscreen rendering

CEF's supported embedding is offscreen rendering (OSR): each frame handed over as an IOSurface. Hammock doesn't use it, because its input path can't carry the trackpad. `SendMouseWheelEvent` has no gesture phases (so no rubber-banding or momentum) and there is no pinch API. Text input would have to be driven through CEF's `Ime*` calls, and popups, menus and IME windows are drawn offscreen, each needing its own code.

Instead each pane runs windowed (Alloy) CEF in a hidden "shadow" window, and the app shows Chromium's own GPU output directly.

## The host

- **One host per daemon.** Chromium can't share a profile directory between processes. Every browser pane runs in it, sharing one persistent profile (cookies, logins, local storage, session cookies kept), passed as `--profile`. The daemon starts the host; it outlives the app and the daemon, so pages keep running and a new client reattaches. It quits after 10 minutes with no clients, or 5 minutes with no browsers.
- **Protocol:** newline-delimited JSON over a Unix socket (0600), any number of clients, every per-browser message carrying the pane id. The message list is in the header comment of `host.mm`.
- **Socket paths must be under 104 bytes** (`sun_path`); a longer one is silently truncated by the kernel, so the host refuses it.
- **Command-line switches:** `disable-backgrounding-occluded-windows`, `disable-renderer-backgrounding` and `disable-background-timer-throttling` (shadow windows are transparent and usually covered, and Chromium must never treat the page as hidden), `use-mock-keychain` (no keychain prompt for the cookie key) and `disable-notifications`.
- **Bundling:** inside `Hammock.app/Contents/Helpers`, Chromium would take the outer app for its own bundle and fail to start, so the host sets `main_bundle_path` and `framework_dir_path` explicitly. The four `Hammock Browser Helper*.app` bundles run `helper.cc`, which enters the macOS sandbox before loading the CEF framework, as CEF requires. `helpers/browser/build.sh` builds it all against the CEF build pinned by `scripts/fetch-cef.sh`.
- **DevTools** calls go through CEF's in-process channel. No debugging port is opened, so only clients of the socket (the daemon) can drive pages. Console, log, network and page events are enabled from the moment a page exists, so an agent that looks later sees what already happened.

## Showing the page

- **The shadow window** is borderless, has alpha 0, ignores the mouse and is kept exactly behind the pane. It reports itself key while its pane is focused, so Chromium draws a caret and treats the page as active. Anything Chromium positions on screen (IME candidates, drag images) lands in the right place because the window sits behind the pane.
- **The layer.** Chromium already shows its GPU output in that window through a `CALayerHost`. The host finds it, reads its `contextId` and backing scale, and sends them to the app showing the page (`layer`). The app copies Chromium's structure: a geometry-flipped container with a `CALayerHost` at the origin, the remote tree in points. WindowServer composites Chromium's frames straight into Hammock's window: no copies, no hop through the host. The container is sized to the view by hand and clipped, so the page never covers the pane's controls.
- **Context swaps.** Chromium moves to a new GPU context in normal use (surface recreated, GPU process restart, some navigations). The host polls for it 30 times a second. The app puts the new `CALayerHost` on top and drops the old one 250 ms later, so a swap never shows a blank frame.
- `CAContext` and `CALayerHost` are private, but Chromium relies on them on every Mac, so they're as stable as private APIs get.

## Input

- **Events** go from the app as `CGEventCreateData` bytes plus the pane-local point. The host rebuilds the `NSEvent` and calls the matching responder method on Chromium's `RenderWidgetHostViewCocoa` directly, not through `NSWindow`, which would try to activate the host on a click. Phases, momentum, precise deltas and magnify arrive intact.
- **Scroll and gesture events** have no public constructor, and one rebuilt from CGEvent data has no window (its location is in screen space). They're wrapped in an `NSProxy` (`EventProxy`) that supplies the shadow window and a window-relative location and forwards everything else.
- **Text input.** The host is never the active app, so the macOS text system can't run there: it stalls 10–50 ms a key and loses dead-key state. The app calls `interpretKeyEvents` itself, records the resulting `NSTextInputClient` calls (insert, set marked text, unmark, command) and sends them with the key. In the host, Chromium's `inputContext` is swapped for a replay context, so Chromium gets those calls from inside its own `keyDown`, as it would natively. The host sends the caret and composition rectangle back, so the app can answer IME queries (where the candidate window goes) without a round trip.
- **Cursors** come back through `OnCursorChange`; the app owns the real cursor.
- **⌘ shortcuts** go to the page first, as in Chrome. Editing and zoom commands the page doesn't consume run in the host (it has no key window for a menu to target). Anything else goes back to the app (`unhandled`), so its own menus still work while a page is focused.

## Native UI the host can't show

A background app can't keep a menu or a sheet open, and must never come to the front. So the app shows these, and sends the answer back:

- **`<select>` popups.** Chromium's `WebMenuRunner` (`runMenuInView:withBounds:initialIndex:`) is swizzled: the host sends the items and the element's rectangle, pumps the run loop until the choice comes back, and applies it through the item's own action, as if it had been clicked there. If the class is missing (a CEF update), the host logs that `<select>` will misbehave.
- **Context menus** (`CefContextMenuHandler`), **JS dialogs** (`CefJSDialogHandler`; "Leave site?" never blocks), **file pickers** (`CefDialogHandler`) and **downloads** (a save panel; with no app showing the page, the download is cancelled). Downloaded files are marked as from the web, so Gatekeeper checks them.
- **New windows.** The host never opens a window: it would belong to the background process. `OnBeforePopup` cancels every popup. One the person asked for (a click, a key) opens in a Hammock browser tab, or for a plain new-tab link from their own click, possibly in their own browser (the daemon decides, `browser-new-tab`). One a page opens by itself is dropped, as a popup blocker would.
- **Links for other apps** (`mailto:`, `zoommtg:` and so on): the app asks first, then opens them; one a page follows by itself is dropped.
- **Menus from a remote viewer.** Menus the page opens go to whoever gave the latest input: the Mac showing the page, or a remote viewer through the daemon. A remote viewer's Copy, Cut and Copy Link Address go to their clipboard, and nothing reads this Mac's pasteboard for them.

## Drags out of the page

A drag the page starts (text, a link, an image) would be an AppKit drag session of the background host, begun from a replayed event. The real mouse-up goes to the app, so macOS never ends it, and every window is stuck in it until Esc. So the host swizzles `WebContentsViewCocoa`'s `beginDraggingSessionWithItems:event:source:`: the data and image go to the app, which runs the drag from the pane with the real mouse and reports how it ended (`dragEnd`). That is passed to Chromium's drag source as AppKit would have.

Chromium's drag source sets a global "drag running" flag in `draggingSession:willBeginAtPoint:` and clears it in `draggingSession:endedAtPoint:operation:`, with a CHECK that it changes: the host crashes otherwise. With no real session, the hook calls `willBegin` itself, so every end has its begin. A drag still waiting when the app goes away or the pane closes is ended, or Chromium keeps the page in its drag. File promises are left out (the app can't keep them), and drags from a remote viewer's input are refused.

## Known gaps

- **Drops onto the page** (files from Finder, in-page HTML5 drag and drop from outside) aren't forwarded.
- **Permission prompts** (camera, microphone, location) have no handler; they get CEF's default.
- **Accessibility:** the remote layer is opaque to VoiceOver. Chromium bridges its own process boundary with remote accessibility elements; Hammock would need the same.
- Each browser has its own hidden window; the cost per pane (memory, GPU) hasn't been measured.
