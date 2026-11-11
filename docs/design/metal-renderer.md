# Metal terminal renderer

Status: Proposed, 2026-11-11 ([#18](https://github.com/homegrownsw/hammock/issues/18))

The Mac draws terminal panes with CoreText (`TerminalNSView`, ADR-0009). Since #18's first step it redraws only the rows that changed, so typing is cheap whatever is on screen. What's still slow is everything that changes every row: output scrolling past, and full-screen programs redrawing, in big panes and on screens full of colour or Unicode. This plan replaces the drawing with Metal while keeping what the CoreText renderer gets right.

## Where the time goes

From `just render-bench` (`docs/benchmarks.md`), CoreText, p50 per draw, Apple M4 Pro, 14 pt:

| Screen | 200×60 | 400×100 |
|---|---|---|
| Plain text, scrolling | 5.6 ms | 17.9 ms |
| 256-colour words, scrolling | 16.1 ms | 53.5 ms |
| Full-screen TUI redrawing | 8.2 ms | |
| CJK, emoji, Nerd Font icons | 31.1 ms | |

Live, a flood into a full-screen pane on a 6K display (328×86) draws at 15 ms a frame, about 18 frames a second. A 120 Hz display gives a frame 8.3 ms.

A profile of the 400×100 colour screen (`sample` during the bench) splits a draw roughly as follows:

| Part | Share | What Metal does about it |
|---|---|---|
| Rasterising and compositing glyphs (`CTFontDrawGlyphs`) | ~27% | Gone: glyphs come from an atlas, drawn by the GPU |
| Shaping (`CTLineCreateWithAttributedString`) | ~12% (~39% for Unicode, with font fallback) | Only if shaping is cached |
| Building attributed strings and attributes, `NSColor` to `CGColor`, italic fonts, every run of every draw | ~20% | Only if styles and shaping are cached |
| Swift bookkeeping per run (arrays, strings, column maps) | most of the rest | Only if the layout is cached |
| Copying the screen out of libghostty (`frame()`) | 2–8% | Unchanged |

**So a glyph atlas alone removes at most a third of the cost.** The plan caches shaped lines too, keyed by their content rather than their row, so a line that scrolls up keeps its shaping: only the new lines at the bottom are shaped. A real flood scrolls a whole screen or more between frames, though, so there every line is new; shaping a new line has to get cheaper as well.

## Design

### As built

1. **Line layout (`TerminalLineLayout`, HammockKit).** It turns a row of `TerminalCell`s into a `TerminalLine`: backgrounds merged into runs; rectangles for block elements (pixel-snapped) and underlines; and glyphs placed by column, with box drawing stretched to the cell, powerline separators fitted to it, Nerd Font icons shrunk to fit, combining marks kept on their cluster, a space kept between runs so contextual alternates don't join across a gap, ligatures off, and wide cells. CoreText shapes each row once, with only its fonts as attributes, and picks fallback fonts; colours are looked up per glyph afterwards.
2. **Line cache.** In the same class: layouts kept by a hash of the row's text and styles (not its index), checked against the row on a hit, two generations sized to twice the pane's rows. A font, size or palette change clears it.
3. **Two renderers of the same lines.** `TerminalNSView` (input, selection, IME, links: unchanged) draws them with Metal (`metalScene`, then `TerminalMetalRenderer`) or with CoreGraphics (`draw(_:)`), which is what `terminal-renderer = coretext`, a Mac without Metal and the debug snapshots use. There's no protocol between them: the scene is built in the view, as `draw(_:)` is.

### The Metal renderer

- **A Metal sublayer**, not a CAMetalLayer backing the view: AppKit never asks a view backed by one to update, so the pane keeps an ordinary layer and draws into the sublayer from `updateLayer` (on the iPhone, from `display(_:)`). Its contents stay at the top left while a pane animates, and frames are presented in step with Core Animation (`presentsWithTransaction`), two drawables per pane.
- **One glyph atlas** for the whole app, RGBA: text as white with its coverage as alpha (tinted when drawn), colour glyphs as they are. CoreText rasterises each glyph with the same settings the pane draws with (no font smoothing unless `font-thicken`, and then dark text as dark on light), placed as CoreGraphics places glyphs: on a whole pixel vertically and a quarter pixel across. Glyphs are keyed by font, glyph and their whole transform (so the backing scale too); when the atlas fills it starts afresh and every pane rebuilds its rows.
- **One instanced draw per frame**, split where the texture changes: Kitty images below the backgrounds, the cells' backgrounds, images below the text, the text and its rectangles, images above the text, text being composed, the cursor and a link's underline, the character under a block cursor. Blending is premultiplied over, in encoded sRGB, as CoreGraphics blends text.
- **Rows built once:** what each line becomes on the GPU is kept by the line's id, so a frame builds only the rows that are new; the whole pane is drawn every frame.
- **No display link:** a frame is drawn when the pane is asked to update, as before; after #59 output arrives in batches, so that's already about once per batch. On the main thread, where the engine lives.

### Measuring

- `just render-bench` with `HMK_DEBUG_RENDER_BENCH_RENDERER=metal` draws each screen with both renderers, Metal offscreen into a texture waiting for the GPU, reporting CPU and GPU time separately and the share of pixels that differ visibly, with each renderer's last frame as a PNG. `HMK_DEBUG_RENDER_BENCH_REPLAY` does the same for recordings of real programs.
- Live: `HMK_DEBUG_RENDER_STATS`, and keystroke-to-photon (#19) when it's built.
- Debug snapshots (`HMK_DEBUG_SNAPSHOT`) render the window's layers, which can't see Metal: while they render, each Metal pane shows its CoreGraphics drawing as its layer's contents instead, so it lands under whatever sits above it. The views dump says which renderer each pane uses.

## Phases

1. **Line layout and line cache, drawn by CoreText.** Done 2026-11-11 (`TerminalLineLayout`): pixel for pixel the old output, and 3–5× faster on scrolling screens (colour at 400×100: 53.5 → 16.3 ms), 1.8× on floods (53.5 → 29.9 ms), where every row is new and nothing can be kept (`docs/benchmarks.md`). Two findings: in a flood the cache never hits, so what made floods faster was shaping each row once with fonts only, a cheap row hash, and attributes built once; and with rows kept, what's left is CoreGraphics rasterising glyphs, which is Metal's to remove.
2. **Metal for plain text,** behind a config key (`terminal-renderer = coretext | metal`, CoreText by default): backgrounds, text, bold, italic, faint, cursor, decorations, blocks. Bench and PNGs alongside CoreText. Done 2026-11-11 (`TerminalMetalRenderer`): it draws every bench screen and the recordings of a powerline prompt, Claude Code and vim the same as CoreText (no pixel differs visibly on 15 of 17; a few emoji edges on the other two), at 1.6–2 ms of CPU and 0.6 ms of GPU for a full 400×100 pane instead of 12–16 ms; a flood into a full-screen 6K pane draws ~77 frames a second instead of ~18. What made it match: glyphs placed as CoreGraphics places them (origins on a whole pixel vertically, a quarter pixel across), and blending in encoded sRGB. A pane backed by a CAMetalLayer is never asked to update by AppKit, so the pane keeps an ordinary layer and draws into a Metal sublayer from `updateLayer`. Not yet drawn by Metal: Kitty images and IME text being composed; debug snapshots (`cacheDisplay`) don't see it.
3. **Parity.** Done 2026-11-11:
   - Kitty images in their three layers (below the cells' backgrounds, between them and the text, above the text), uploaded once per image (`TerminalImage` now carries the whole image, the part shown and a key) and drawn smoothed, as CoreGraphics draws them: a bench screen with one in each layer matches CoreText.
   - Text being composed in an input method, at the cursor and underlined, drawn before the cursor so the cursor shows on it. It's laid out on the grid (CJK two cells), where it will land once committed, rather than at its natural width as CoreText draws it.
   - `font-thicken`: CoreGraphics thickens dark text on light differently from light text on dark, so with it on, dark text's glyphs are rasterised as dark on light. Plain text matches exactly; coloured text within 64/255 here and there.
   - Debug snapshots (`HMK_DEBUG_SNAPSHOT`): they render the window's layers, which can't see a Metal layer, so a Metal pane draws itself with CoreGraphics into the snapshot.
   - Already right from phase 2: fallback fonts and colour emoji, box drawing, powerline and icon fitting, selection, links, themes, `adjust-cell-height`, pane animations, zoom and fold (checked by hand), and a window moving to a display of another scale (glyphs are keyed by their transform, which includes the scale).

4. **Default to Metal on the Mac,** with CoreText still selectable for a release, then removed (or kept as the snapshot fallback, if that turns out simpler). Done 2026-11-11: `terminal-renderer` is `metal` unless set to `coretext`, and a Mac where Metal can't start uses CoreGraphics. Each pane keeps two drawables, not Core Animation's three (85 MB each for a full-screen pane on a 6K display). The CoreGraphics drawing stays: it's what debug snapshots and `terminal-renderer = coretext` use, and both draw the same `TerminalLine`s, so keeping it costs little.
5. **The iPhone.** `TerminalScreen.swift` has its own, simpler CoreText renderer without the icon fitting or block elements. Moving it onto the shared line layout and the Metal renderer brings it to parity with the Mac. Done 2026-11-11: `TerminalUIView` lays its rows out with `TerminalLineLayout` and draws them with `TerminalMetalRenderer`, from `display(_:)` (so every `setNeedsDisplay` still works) into a Metal sublayer; its own geometry stays (the font shrunk to fit a pane the Mac sized, the safe area, its cursor), and its CoreGraphics drawing is what's left where Metal can't start. It gains what it never drew: italics, block elements, box drawing, powerline and icon fitting, strikethrough, selection colours and Kitty images. The demo's shell pane (`HMK_DEBUG_SAMPLE`, `HMK_DEBUG_PATH=p_shell`) looks the same as before, glyph for glyph, with the keyboard down and up.

## Goals

- Scrolling colour at 400×100 under 4 ms a frame, p99, on CPU and GPU; Unicode at 200×60 under 4 ms once its glyphs are cached.
- A flood in a full-screen pane on a 6K display at the display's rate, as far as output arrives.
- Typing no slower than now (0.2–0.4 ms).
- Text that looks the same as CoreText's, signed off by eye.
- Atlases under 64 MB at 2x.

## Risks

- **Text that looks different.** Weight, gamma and the positions of glyphs are where custom renderers go wrong. Mitigated by rasterising with CoreText, blending as CoreGraphics does, quarter-pixel positions, and side-by-side PNGs at every phase.
- **Resize and animation jank** with `CAMetalLayer`: the layer has to present in step with Core Animation transactions. Built in phase 2, not left to the end.
- **Memory:** a big atlas per font, size and scale; shared between panes with the same font.
- **Two renderers for a while.** Phase 1 keeps the shared part (layout, shaping, the special cases) in one place, so fixes land once.

## Questions for later

- Should `terminal-renderer` be a visible setting in Settings, or stay a config-file key for going back to CoreGraphics?
- If phase 1 alone brings scrolling colour at 200×60 under 8 ms, is Metal still worth it before the iPhone needs it? The bench will say after phase 1.
