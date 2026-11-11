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

### Layers

1. **Line layout (HammockKit, renderer-agnostic).** It turns a row of `TerminalCell`s into positioned glyphs: font, glyph id, column, colour, and the special cases the CoreText renderer handles today, which are box drawing stretched to the cell, powerline separators fitted to it, Nerd Font icons shrunk to fit, block elements drawn as pixel-snapped rectangles, combining marks kept on their cluster, spaces kept between runs so contextual alternates don't join across a gap, ligatures off, and wide cells. Also backgrounds merged into runs, and decorations. CoreText still shapes and picks fallback fonts, exactly as now; this layer moves that logic out of `drawText` so both renderers share it.
2. **Line cache.** Keyed by a hash of the row's text and styles (not its index), with bounded memory (LRU). It's invalidated by a font, size, thicken or theme change.
3. **Renderers.** The existing CoreText one, reworked to draw from the line layout, and a new Metal one. A `TerminalRenderer` protocol sits between `TerminalNSView` (input, selection, IME, links: unchanged) and the drawing.

### The Metal renderer

- **A `CAMetalLayer`** backing the pane. Its contents stay at the top left while a pane animates (as `layerContentsPlacement = .topLeft` does now), and frames are presented in step with window resizes and pane animations (`presentsWithTransaction` while `PaneMotion` is moving).
- **Two glyph atlases:** an 8-bit coverage one for text, rasterised by CoreText with the same settings as now (no font smoothing unless `font-thicken`), and a colour one for emoji. Glyphs are keyed by font, glyph, and a quarter-pixel horizontal offset, because cells are half points wide and glyphs mustn't snap. Atlases grow, then reset when full, and are rebuilt when the backing scale changes (a window moving to another display).
- **One pass per frame:**
  - cell backgrounds, selection and inverse;
  - Kitty images below the text;
  - text as instanced quads;
  - decorations, block elements and the cursor as rectangles;
  - images above the text;
  - the hover-link underline and IME text.

  Blending matches CoreGraphics (sRGB, not linear), so text has the same weight it has now.
- **Per-row instance data** is rebuilt only for dirty rows (the `takeChanges` from #18's first step) and drawn whole every frame, which is cheap on the GPU.
- **Pacing:** draws are coalesced to the display's refresh (a display link) rather than one per batch of output.
- **First on the main thread,** where the engine lives. Moving encoding to its own thread is a later step, only if measurements say so.

### Measuring

- `just render-bench` gains a renderer column. The Metal renderer draws offscreen into a texture and waits for the GPU, reporting CPU and GPU time separately.
- Each screen's last frame is written from both renderers as PNGs side by side, for comparison by eye. They won't match pixel for pixel (blending and rasterisation differ slightly), so a perceptual diff flags anything larger than anti-aliasing.
- Live: `HMK_DEBUG_RENDER_STATS` as now, and keystroke-to-photon (#19) if it's built by then.
- The debug snapshots (`HMK_DEBUG_SNAPSHOT`) use `cacheDisplay`, which can't see Metal layers. The Metal renderer gets a read-back of its last frame so snapshots and agents' UI checks keep working.

## Phases

1. **Line layout and line cache, drawn by CoreText.** Done 2026-11-11 (`TerminalLineLayout`): pixel for pixel the old output, and 3–5× faster on scrolling screens (colour at 400×100: 53.5 → 16.3 ms), 1.8× on floods (53.5 → 29.9 ms), where every row is new and nothing can be kept (`docs/benchmarks.md`). Two findings: in a flood the cache never hits, so what made floods faster was shaping each row once with fonts only, a cheap row hash, and attributes built once; and with rows kept, what's left is CoreGraphics rasterising glyphs, which is Metal's to remove.
2. **Metal for plain text,** behind a config key (`terminal-renderer = coretext | metal`, CoreText by default): backgrounds, text, bold, italic, faint, cursor, decorations, blocks. Bench and PNGs alongside CoreText.
3. **Parity:**
   - fallback fonts and colour emoji;
   - the box-drawing, powerline and icon fitting;
   - selection, hover links and IME;
   - Kitty images;
   - `font-thicken`, `adjust-cell-height` and themes;
   - pane animations, zoom and fold;
   - moving between displays;
   - the snapshot read-back.

   Checked against a list of screens, the bench's eight plus Claude Code, nvim, htop and a powerline prompt, light and dark.
4. **Default to Metal on the Mac,** with CoreText still selectable for a release, then removed (or kept as the snapshot fallback, if that turns out simpler).
5. **The iPhone.** `TerminalScreen.swift` has its own, simpler CoreText renderer without the icon fitting or block elements. Moving it onto the shared line layout and the Metal renderer brings it to parity with the Mac.

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

- Should `terminal-renderer` be a visible setting in Settings, or only a config key while it's experimental?
- If phase 1 alone brings scrolling colour at 200×60 under 8 ms, is Metal still worth it before the iPhone needs it? The bench will say after phase 1.
