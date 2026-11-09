# Spike: browser panes that feel native

Status: in progress · 2026-10-30 · Code: [`spikes/browser/`](../../spikes/browser) · Decision: [ADR-0011](../adr/0011-browser-panes-hidden-window-host.md) (proposed)

## Question

Can a browser pane in Hammock feel indistinguishable from a native browser view locally, while Chromium runs in a separate process that outlives the app? The priorities, in order, are trackpad scrolling, latency/frame rate, text input, and surviving app quit. The main use is dev previews and agent-driven checks, not signed-in general browsing.

## Designs compared

| | A. Offscreen (CEF OSR) | B. Hidden window (built here) |
|---|---|---|
| Where Chromium draws | Offscreen. Each frame is handed over as an IOSurface (`OnAcceleratedPaint`). | Its normal GPU path, into its own `CAContext`, exactly as in Chrome. |
| How the app shows it | Our layer shows the IOSurface, then a second layer hop to the app. | The app hosts Chromium's GPU context directly with `CALayerHost`. Zero copies, and no hop through the helper. |
| Frame pacing | Our own begin-frame clock (`external_begin_frame`). Must be driven at the display rate, otherwise it caps at 60. | Chromium's own display link. |
| Trackpad input | CEF's `SendMouseWheelEvent` has **no gesture phases**, so no rubber-banding, and no pinch API. | The app sends raw `CGEvent` bytes, and the host rebuilds the `NSEvent` and hands it to Chromium's own `NSView`. Phases, momentum, precise deltas and magnify all arrive intact. |
| Text input / IME | CEF's `Ime*` API, which we would have to drive ourselves. | macOS's text system runs in the app, which is the active app. The resulting calls are replayed inside Chromium's own `keyDown` (see below). |
| Popups, menus, IME windows | Drawn offscreen; each one needs extra code. | Native, in the right place on screen, because the hidden window sits exactly behind the pane. |
| Survives app quit | Yes (separate process) | Yes (separate process) |

Design A is what CEF supports officially. Design B is what this spike built, because A's input path can't carry trackpad gestures, and you put trackpad feel first.

## How design B works

- **BrowserHost** (`spikes/browser/host/host.mm`) runs CEF in windowed (Alloy) mode inside a borderless `NSWindow`. That window:
  - has alpha 0 and ignores the mouse;
  - is kept exactly behind the viewer's pane;
  - runs with `--disable-backgrounding-occluded-windows`, so Chromium never treats the page as hidden.
- Chromium already shows its GPU output in that window through a `CALayerHost`. The host finds that layer, reads its context ID and sends the ID to the viewer. The viewer copies Chromium's layer structure (a geometry-flipped container with the `CALayerHost` at the origin). WindowServer then composites Chromium's frames straight into Hammock's window.
- **Input:** the viewer sends each event as `CGEventCreateData` bytes plus the pane-local point.
  - The host rebuilds the event and calls the matching responder method on Chromium's `RenderWidgetHostViewCocoa` directly, without going through `NSWindow`, which would try to activate the host app.
  - Scroll and gesture events have no public constructor, so they go through a small `NSProxy` that supplies the window and the window-relative location and forwards everything else.
- **Text input:** a background app can't use the macOS text system. In the first run it took 12–50 ms per key and lost dead-key state.
  - The viewer calls `interpretKeyEvents` itself, records the resulting `NSTextInputClient` calls (insert, set marked text, unmark, command) and sends them with the key.
  - In the host, Chromium's `inputContext` is swapped for a replay context, so Chromium gets those calls from inside its own `keyDown`, exactly as it would natively.
  - The host sends the caret rectangle back, so IME candidate windows open next to the text.
- **Cursors:** relayed through `OnCursorChange`.
- **⌘ shortcuts:** go to the page first, as in Chrome. Editing commands the page doesn't use run in the host (Copy, Paste and so on). Any other unhandled shortcut bounces back to the app's menus.

## Results so far (measured without a screen: the Mac was locked)

| Check | Result |
|---|---|
| Chromium's GPU layer can be hosted by another process on macOS 27 | ✅ The `CAContext` ID was found and sent; the `CALayerHost` class exists. **Not yet seen on screen.** |
| Click → page `pointerdown` | ~3 ms end to end. The viewer → host hop is 0.4–0.7 ms. |
| Key → page `keydown` | ~1 ms per key once text input is replayed (12–50 ms before). |
| Typing, Backspace, ⌘A, ⌘C (copy event with the right selection) | ✅ |
| Dead key and IME (´ → é, か → 漢字) | ✅ The page gets the correct `composition*` events and value. The live ⌥e path needs the viewer to be the active app, which it can't be while the screen is locked. |
| Trackpad scroll with phases and momentum | ✅ The page receives precise pixel wheel deltas and scrolls. Rubber-banding is only visible on screen. |
| Pinch | ⚠️ The magnify events arrive with the correct phases, and Chromium turns them into ctrl-wheel for the page, but the page didn't zoom from my **hand-built** pinch. To check with a real trackpad. |
| Resize | ✅ The page saw its new size 8 ms after the window changed; the layer ID stayed the same. |
| Frame pacing | 60 fps, p99 ≈ 18.6 ms, with no viewer attached too. Locked displays run at 60 Hz, so 120 Hz is unmeasured. |
| Survives viewer quit | ✅ The page kept rendering at 60 fps with no viewer. Relaunching reattached to the same process with form state intact. |

## Still to check at the desk

Run `spikes/browser/try.sh b`, `a`, `cef` and `chrome` with the same page (or your own dev server: `try.sh b http://localhost:3000`) and compare:

1. **Does it display at all?** The pane should show the test page with a spinning square. If it's blank or upside down, the layer setup is wrong, which is a quick fix.
2. **Trackpad feel:**
   - Momentum.
   - Rubber-banding at the top and bottom.
   - Pinch-zoom.
   - Two-finger back/forward (not wired yet).
   - Smart zoom (double-tap with two fingers).
3. **Frame rate** on a 120 Hz display: the header shows fps.
4. **Text input:** ⌥e e, a Japanese or Chinese IME (where the candidate window opens), Press-and-Hold accents, emoji picker (⌃⌘Group).
5. **Things Chromium shows itself from the background app:**
   - the `<select>` popup and right-click menu (do they open, take keyboard input, and leave Hammock's window active?);
   - link hover cursors;
   - tooltips.
6. **Window moves and resizes:** drag the window and resize it live. Popups opened afterwards should still line up.
7. **Groups / full screen / Mission Control:** the hidden window must never show up or pull focus.

## Known gaps in design B (not built in the spike)

- **File pickers, JS dialogs, permission prompts:** route them through CEF handlers (`CefDialogHandler`, `CefJSDialogHandler`, `CefPermissionHandler`) to native sheets in Hammock, so the host never has to come to the front.
- **New windows (fixed):** `target=_blank` and `window.open` made CEF open a real window owned by the host, and closing it made the pane flash. `OnBeforePopup` now cancels them and sends the URL to the viewer (Hammock: a new browser tab; the spike follows the link in the same pane).
- **Layer swaps (fixed):** Chromium moves to a new GPU context in normal use (twice in a few minutes of testing, e.g. on navigation). The viewer now puts the new `CALayerHost` on top and drops the old one 250 ms later, so a swap never shows a blank frame.
- **`<select>` popups (fixed):** a menu can't stay open in a background app, so it flashed and closed. The host now hooks Chromium's `WebMenuRunner`: the viewer shows the menu natively over the element and sends the choice back, which the host applies through the menu item's own action.
- **Context menus:** likely the same problem as `<select>`; prefer `CefContextMenuHandler` → a menu drawn by Hammock, for better integration (and fewer surprises from the background app).
- **Drag and drop:** dropping files from Finder onto the page means forwarding `NSDraggingDestination` events. Dragging out of the page (text, links, images) runs in the app: the host overrides `WebContentsViewCocoa`'s `beginDraggingSessionWithItems:event:source:`, sends the data and image to the app, which drags from the pane, and passes back how it ended (begun in the host, macOS never ended it). Not yet: drops onto the page (so in-page HTML5 drag and drop ends with none) and dragging out as files (promises).
- **Accessibility:** the remote layer is opaque to VoiceOver. Chromium bridges its own process boundary with remote accessibility elements; we'd need the same trick.
- **Several panes per host:** one window per browser, all hidden behind their panes. Cost per pane needs measuring (memory, GPU).
- **Private API:** `CAContext`/`CALayerHost` are private, but Chromium itself relies on them on every Mac, so they're as stable as private APIs get. Fallback: design A's IOSurface path.

## Also learned

- CEF 156 macOS offscreen rendering does support shared IOSurface textures (`shared_texture_io_surface`), whatever the header comments say. `cefclient` runs that path at 60 fps here.
- Unix socket paths must stay under 104 bytes (the scratchpad path was 111 and got silently truncated). The host now refuses paths that are too long.
