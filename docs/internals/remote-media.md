# Spike: video to remote viewers over QUIC

2026-10-31. Code in `spikes/quic/` (Go server, Swift Network.framework client). Decision: [ADR-0012](../adr/0012-remote-video-over-quic.md).

## Result

quic-go (v0.64) and Network.framework's QUIC interoperate with the daemon's kind of pinned self-signed certificate (TLS 1.3, verify block comparing the leaf's SHA-256), a bidirectional control stream carrying a 32-byte ticket, and one server-initiated unidirectional stream per frame. Over the Mac's Tailscale address, synthetic 12 Mbit/s 60 fps video (keyframes 8× a delta) arrives with p50 2–3 ms, p99 6–9 ms, the same as TLS over TCP.

With quic-go dropping and delaying its own outgoing packets (40 ms one way, 1% loss):

| Sender | Shown | Latency p50 / p90 / p99 |
|---|---|---|
| Fixed 7 Mbit/s; on a late frame cancel everything and send a keyframe | 12 fps | 100 / 173 / 191 ms |
| Adaptive bitrate from queueing delay, skip frames when >100 ms in flight, abandon a frame after 400 ms and recover from the last frame shown | 55 fps | 44 / 91 / 199 ms |

At 1% loss and ~80 ms RTT, Cubic's throughput is ≈1.4 Mbit/s (what got through): with any loss-based transport, the encoder has to follow the achievable rate.

Not measured: QUIC against TCP under the same loss. dummynet on this Mac didn't shape traffic to its own Tailscale address (loopback), twice; a real iPhone with Network Link Conditioner is the way to do it.

## Network.framework gotchas

- `NWConnectionGroup` over `NWMultiplexGroup` opens an empty bidirectional stream of its own (stream 0) before the first `NWConnection(from: group)`. The server must take the first stream that carries a ticket, reading candidates concurrently.
- It never sends MAX_STREAMS: after `initialMaxStreamsUnidirectional` streams the server can open no more, even with every stream read to the end and cancelled. Set it to 2^40.
- `send(content:completion:)` defaults to `isComplete: true`, which finishes the stream. Use `contentContext: .defaultStream, isComplete: false` on the control stream.
- The group reports `.waiting(ENETDOWN)` briefly before `.ready`; don't treat waiting as failure.
- In a sandboxed shell, UDP to the Tailscale address fails with ENETDOWN; run outside the sandbox.

## Why frames may be abandoned

With every frame referencing the previous one, frame N+1 can't be shown until frame N's lost packet is resent, on QUIC as on TCP. QUIC's advantage is that N can be given up: the next frame references a frame the viewer has (VideoToolbox long-term references: `kVTCompressionPropertyKey_EnableLTR`, `kVTEncodeFrameOptionKey_AcknowledgedLTRTokens`, low-latency rate control) or is a keyframe.

## Browser capture: DevTools screencast

Browser panes run windowed Chromium (ADR-0011), whose pixels live in its GPU process's `CAContext`: there's no IOSurface to hand the encoder, as Hammock Simulator has. Two ways to get them:

- **ScreenCaptureKit** on the shadow window: zero-copy and cheap, but needs Screen Recording permission for Hammock Browser (a prompt, then macOS's periodic re-confirmation), for a background helper the user never sees. Ruled out on that alone.
- **DevTools screencast** (`Page.startScreencast`, through CEF's in-process DevTools channel): no permission. Chromium reads back each compositor frame and JPEG-encodes it; Hammock Browser decodes it (ImageIO into an IOSurface-backed pixel buffer, off the main thread, newest frame winning) and encodes H.264 with HammockEncoder. Chosen.

Measured on this Mac (M-series, 120 Hz display), a page animating every frame, 1000×700 pt at 2x:

| Screencast size | Captured | Delivered | JPEG | Decode | Chromium frame → pixel buffer | Hammock Browser CPU |
|---|---|---|---|---|---|---|
| 2000×1400, decoding on the main thread | 61–77 fps | 33 fps | 190 KB | 9 ms | 45–60 ms | 230% |
| 2000×1400, decoding off the main thread | 77–82 fps | 52 fps | 190 KB | 9 ms | 16–19 ms | 160% |
| 2000×1400, acks paced to 60/s | 58 fps | 51 fps | 188 KB | 10 ms | 16 ms | 100% |
| 1464×1025 (a phone's `maxPixels`), paced | 60 fps | 55 fps | 114 KB | 5 ms | 6 ms | 52% |

That's the worst case: a still page sends no frames and costs nothing. "Delivered" includes the session's first second. Chromium keeps more than one frame unacked, so pacing gives each ack its own 1/60 s slot; the screencast is sized to the biggest viewer's `maxPixels` and restarted when that changes.

Input goes the other way as DevTools `Input.*` events (`BrowserInput`), positions and scroll deltas as fractions of the page. Menus the page opens (`<select>`, right-click) go to whoever gave the latest input: Hammock Browser tracks whether that was the Mac showing the page or a remote viewer, and sends a remote one's menus through the daemon (`BrowserMenu`, answered by `BrowserMenuChoice`). Copy and cut read the selection with `Runtime.evaluate` and send it back (`BrowserClipboard`). A guest's Mac composes text with its own text input system and sends marked text as `Input.imeSetComposition`.

## Two freezes, found with the video log

- The daemon held the encoder when too much video was in flight and released it on the ack, from different goroutines. On a fast link the release reached the helper before the hold, leaving the encoder held with nothing in flight to give up (`frames=2, gave_up=0`). `syncHold` serializes them and always sends the current state.
- Paced screencast acks that fired after the screencast stopped were dropped. Chromium counts unacked frames across a stop, so a quickly restarted screencast (a viewer joining at another size) stalled for seconds.

The apps log to `com.homegrownsw.hammock` / `video` at notice level (kept by macOS): state changes, keyframe requests and why, decoder failures, renderer flushes, and one line per 5 s silence with what the stream was waiting for. `/usr/bin/log show --predicate 'subsystem == "com.homegrownsw.hammock"' --last 10m` on a Mac; `xcrun simctl spawn <udid> log show …` for the simulator's app. (zsh has its own `log` builtin.)

## Loss recovery with long-term references

`spikes/ltr/main.m` encodes 90 synthetic 1206×2622 frames at 12 Mbit/s, loses frames 40–49, recovers at frame 50, and decodes what a viewer would get with VTDecompressionSession, comparing each picture with its source (PSNR). The decoder reports no errors in any case (it conceals missing references), so pixels are the only honest check:

| Recovery at frame 50 | Frame 50 | Shown after the loss |
|---|---|---|
| None | | 6 dB from then on: garbage, with no decode error |
| Keyframe | 21 KB (squeezed by rate control) | 38 dB, back to 46 dB over ~10 frames |
| LTR refresh | 32 KB | 47 dB at once, as before the loss |

How it's used: the encoder runs with `EnableLTR`; frames carrying `RequireLTRAcknowledgementToken` pass their token to the daemon with the frame. The daemon reports a token back (`AcknowledgedLTRTokens`) only when the viewer has that frame and every frame before it (QUIC delivers out of order; a frame after a missing one may reference it), and on giving frames up forgets tokens after the first missing frame, then asks for `ForceLTRRefresh` instead of a keyframe. A refresh is flagged on the wire (flags bit 2), so the viewer skips the gap; a viewer that threw frames away itself waits for a keyframe instead. The encoder turns a refresh into a keyframe when nothing is acknowledged yet, and asks again if the refresh frame is dropped.

End to end (the Go probe over QUIC, losing every frame for 1 s of the animated page): one refresh, 54 frames skipped, 276 frames shown, all decoding cleanly in ffmpeg, and no keyframe after the first.
