# Remote video over QUIC

How simulator and browser panes reach a remote viewer (the iPhone, or a guest's Mac in a shared Group) as video. Decision: [ADR-0012](../adr/0012-remote-video-over-quic.md); screen sharing builds on it ([ADR-0014](../adr/0014-screen-sharing-and-menu-bar.md)). Code:

- [`internal/daemon/media.go`](../../internal/daemon/media.go): sessions, tickets, the QUIC listener, pacing and loss recovery.
- [`internal/daemon/hostmedia.go`](../../internal/daemon/hostmedia.go): the daemon's media connection to a helper that owns pixels.
- [`internal/daemon/datagram.go`](../../internal/daemon/datagram.go): frames as QUIC datagrams (screens only).
- [`internal/daemon/peermedia.go`](../../internal/daemon/peermedia.go): a guest's daemon relaying a host's video to its app.
- [`helpers/media/encoder.m`](../../helpers/media/encoder.m) (`HammockEncoder`): VideoToolbox encoding, shared by the helpers.
- [`apple/Sources/HammockKit/MediaStream.swift`](../../apple/Sources/HammockKit/MediaStream.swift): the viewer.

## The path

1. The viewer sends `OpenMedia` on its control link and gets a `MediaTicket`: a session id, a 32-byte random ticket and the QUIC port. Remote access must be on. A ticket is good for 30 seconds and one use.
2. The daemon asks the helper that owns the pane's pixels (Hammock Simulator or Hammock Browser) to encode for that session alone. It opens a second connection to the helper's socket, sends `{"t":"media"}`, then sends `encode`, `rate`, `keyframe`, `refresh`, `ltr`, `hold` and `stop` lines, and reads back binary frame records.
3. The viewer connects over QUIC (ALPN `hmk-media/1`, TLS 1.3), pinned to the same certificate as the control link: the viewer's verify block compares the SHA-256 of the leaf with the pin. It sends the ticket on a bidirectional control stream.
4. The daemon sends each frame on its own server-initiated unidirectional stream. The viewer acks each frame on the control stream.

A viewer on the local socket (a guest's app, whose daemon relays the host's video) takes frames in band on its control link as `MediaFrame` messages instead. The guest's daemon is the one that connects to the host over QUIC and acks, so the host paces against the real network link.

## Wire format

- **Frame record** (stream, in band, or cut into datagrams): u32 sequence number, u8 flags, u64 presentation time (µs, host clock), u16 width, u16 height (17 bytes, big-endian), then four u16 crop fractions if cropped, then the picture as AVCC (4-byte big-endian NAL lengths). Flags: 1 keyframe, 2 refresh (predicted from an acknowledged long-term reference), 4 HEVC (else H.264), 8 cropped. A keyframe carries its parameter sets as NALs in front, so a viewer can start from any keyframe.
- **Control stream, viewer to daemon:** record 1 is an ack (u32 sequence number); 2 asks for a keyframe; 3 is a NACK for datagram shards (u32 sequence number, u8 count, that many u16 shard indices). Anything else closes the connection.

## Network.framework quirks

The viewer uses Network.framework's QUIC (`NWConnectionGroup` over `NWMultiplexGroup`). The daemon uses quic-go. Both sides rely on these:

- **The group opens an empty bidirectional stream of its own** before the first `NWConnection(from: group)`. So the daemon reads every stream the viewer opens concurrently and takes the first that carries a valid ticket. A second ticket on one connection ends that session. With no ticket in 5 seconds the connection is closed.
- **It never sends MAX_STREAMS.** Once `initialMaxStreamsUnidirectional` streams have been opened, the server can open no more, even with every stream read and finished. The viewer sets it to 2^40.
- **`send(content:completion:)` defaults to `isComplete: true`**, which finishes the stream. Every send on the control stream passes `contentContext: .defaultStream, isComplete: false`.
- **The group reports `.waiting` (`ENETDOWN`) briefly before `.ready`.** Waiting isn't failure.
- In a sandboxed shell, UDP to the Mac's Tailscale address fails with `ENETDOWN`; test outside the sandbox.

## Pacing

At about 80 ms round trip and 1% loss, a loss-based congestion controller gets well under the bitrate a fixed-rate encoder would want, so the encoder has to follow the achievable rate. (Measured once, with synthetic 60 fps video: a fixed 7 Mbit/s sender that answered every late frame with a keyframe showed 12 fps; the adaptive sender below showed 55.)

- The bitrate follows queueing delay, measured from the viewer's acks: between 300 kbit/s and 16 Mbit/s for panes, starting at 4 Mbit/s, at up to 60 fps.
- The encoder is held while more than about 100 ms of video is unacked, so queues stay short. A held encoder skips frames; the next is whatever the pane shows when it resumes.
- A frame unacked after 400 ms is given up, with everything after it, and the viewer recovers (below). A recovery frame that hasn't come in 300 ms is asked for again.
- **Holds are serialised** (`syncHold`). A hold and its release are sent from different goroutines; on a fast link the release could reach the helper before the hold, leaving the encoder held with nothing in flight to give up. Each call takes a lock and sends the current state.

## Loss recovery with long-term references

Every frame references the previous one, so frame N+1 can't be shown until a lost packet of frame N is resent, on QUIC as on TCP. QUIC's advantage is that N can be given up: the next frame instead references a frame the viewer is known to have, or is a keyframe.

- The low-latency encoder runs with real-time mode, no frame reordering, low-latency rate control (`EnableLowLatencyRateControl`), keyframes only when asked, and `EnableLTR`.
- Frames carrying `RequireLTRAcknowledgementToken` pass the token to the daemon with the frame.
- The daemon reports a token back (`AcknowledgedLTRTokens`) only when the viewer has that frame and every frame before it (`readyLTRs`). QUIC delivers out of order, and a frame received after a missing one may reference it. Before giving frames up, the daemon forgets tokens received after the first missing frame (`forgetUnsure`). Tokens from before the latest keyframe, or from an encoder since restarted, are dropped too.
- After giving frames up, the daemon asks for a refresh (`ForceLTRRefresh`) rather than a keyframe. The encoder turns a refresh into a keyframe when nothing has been acknowledged yet, and asks again if the recovery frame is dropped.
- The viewer skips the gap on a refresh frame. A viewer that threw frames away itself (a decoder error, a gap it gave up on) asks for a keyframe instead, and the daemon then forgets every token.
- The decoder reports no error when a reference is missing: it conceals it, and the picture is garbage from then on. Pixels are the only honest check when changing this. Measured once: a refresh frame was about half again the size of a squeezed keyframe and restored full quality at once, where the keyframe took about ten frames to.

## Screens: datagrams

A screen (ADR-0014) takes frames as QUIC datagrams, since a stream holds a whole frame for a round trip when one packet is lost. Each record is cut into 1080-byte shards with XOR parity (it fits a Tailscale packet, MTU 1280, with the headers). A lost shard is rebuilt from the rest of its stripe; what can't be is asked for with a NACK and resent only while the frame is fresh (one second). The datagram layout is in `datagram.go`'s header comment. Screens also have a sharp tier, HEVC 4:4:4 from VideoToolbox's normal mode, which has no long-term references and recovers with keyframes.

## Simulator pixels

The simulator host has the device's framebuffer as an IOSurface and hands it to the encoder directly. The framebuffer stays in the device's portrait after a rotation, so the encoder turns frames (`VTPixelRotationSession`) by the device's rotation: see [simulator-panes.md](simulator-panes.md).

## Browser pixels: the DevTools screencast

Browser panes run windowed Chromium ([browser-panes.md](browser-panes.md)), whose pixels live in its GPU process's `CAContext`. There's no IOSurface to hand the encoder. ScreenCaptureKit on the shadow window would be cheap, but needs Screen Recording permission (a prompt, then macOS's periodic re-confirmation) for a background helper the person never sees. So the host uses the DevTools screencast (`Page.startScreencast`, through CEF's in-process DevTools channel), which needs no permission:

- Chromium reads back each compositor frame and JPEG-encodes it (quality 85). The host decodes it with ImageIO into an IOSurface-backed BGRA pixel buffer, off the main thread (every page runs on it), the newest frame winning while one is in the decoder. `HammockEncoder` then encodes it as H.264.
- A still page sends no frames and costs nothing. The newest frame is kept, for a keyframe asked for while the page is still.
- **Acks are paced to 60 a second**, each in its own 1/60 s slot. Chromium captures the next frame only once acked, and keeps more than one unacked, so this caps the work on a 120 Hz display. (Measured once, on a page animating every frame: pacing halved the host's CPU without losing delivered frames.)
- **Acks are sent even after the screencast stops.** Chromium counts unacked frames across a stop, so a quickly restarted screencast (a viewer joining at another size) would otherwise stall for seconds.
- The screencast is sized to the biggest viewer's `maxPixels` (at most 4096 a side) and restarted when that or the page's size changes.
- **Input** goes the other way as DevTools `Input.*` events (`rinput`, from `BrowserInput`), positions as fractions of the page. Menus the page opens (`<select>`, right-click) go to whoever gave the latest input: Hammock Browser tracks whether that was the Mac showing the page or a remote viewer, and sends a remote one's through the daemon (`BrowserMenu`, answered by `BrowserMenuChoice`). Copy and cut read the selection with `Runtime.evaluate` and send it back (`BrowserClipboard`). A guest's Mac composes text with its own text input system and sends marked text as `Input.imeSetComposition`.

## Diagnosing

The apps log to subsystem `com.homegrownsw.hammock`, category `video`, at notice level (kept by macOS): state changes, keyframe requests and why, decoder failures, renderer flushes, and a line for each 5 seconds without frames saying what the stream was waiting for. On a Mac: `/usr/bin/log show --predicate 'subsystem == "com.homegrownsw.hammock"' --last 10m` (zsh has its own `log` builtin). For the simulator's app: `xcrun simctl spawn <udid> log show …`. The daemon logs each session's transport (`quic`, `quic datagrams` or `in band`).
