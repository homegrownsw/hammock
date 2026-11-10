# ADR-0014: Screen sharing on Hammock's own host stack, and a menu bar helper

Status: Accepted · 2026-11-04 · Amends [ADR-0012](0012-remote-video-over-quic.md)'s transport (media transport v2; for screens so far)

## Context
Away from the Mac, you sometimes need its screen: a macOS permission prompt an agent is stuck behind, a Keychain dialog, something only the GUI does. The bar is that it feels as responsive and sharp as macOS's own High Performance screen sharing, adapts to poor links, and works at the lock screen.

Research (2026-11-04):
- **Apple's server** (`screensharingd`) gives third-party clients plain RFB: sharp but laggy under motion. Its High Performance mode (HEVC 4:4:4 over UDP/SRTP, 60 fps, ~75 Mbit/s for 4K, a virtual display) is undocumented, Apple-silicon Mac to Mac only, has no iOS client, and the reverse-engineered clients break across updates. Since macOS 12.1 an app can't turn Screen Sharing on.
- **Third-party hosts** reach the login window the way Chrome Remote Desktop does: a root LaunchDaemon for the network and a LaunchAgent with `LimitLoadToSessionType` Aqua + LoginWindow that captures (ScreenCaptureKit works at the login window from macOS 14.4) and posts input (a binary needs an empty `__CGPreLoginApp` section for CGEventPost to reach loginwindow). Apple DTS endorses the split. Costs: an admin install, Screen Recording and Accessibility grants by hand, upkeep each macOS release (AnyDesk's changelog shows the login path breaking on 15 and 26). FileVault's pre-boot unlock is out of reach for everyone.
- **Top-tier protocols** (Parsec, Apple HP, Jump Fluid, DCV, RDP, Moonlight) share: 4:4:4 for text, refining still content to near-lossless, a client-drawn cursor, video that never waits on retransmission (unreliable packets plus FEC, Sunshine defaults to 20% Reed-Solomon), delay-based rate control, and cutting frame rate before resolution on desktops.
- **Measured on an M4 Pro:** VideoToolbox's hardware HEVC and H.264 encoders do true 4:4:4 (`HEVC_Main444_AutoLevel`, `H264_High444Predictive_AutoLevel`; ~15 ms for a worst-case 3024×1964 frame), but only outside low-latency mode; the low-latency encoders are 4:2:0 only (they have LTR and slices). iPhone hardware decode of HEVC 4:4:4 is unverified.

## Decision

### Scope
- Viewers: your paired iPhones and linked computers. Not guests, not agents (agentCheck refuses it; computer use can be a later feature with its own grants).
- The whole display, one at a time, with a picker when there are several.
- While you're logged in, locked or not. After a reboot nobody is logged in and there's no user hmkd to authenticate devices: the phone says to log in at the Mac. A root listener for that is deferred.
- Clipboard both ways (as built so far: the phone pastes into the Mac, typed as text; the Mac's clipboard doesn't come back yet). No audio, curtain mode or file transfer.

### Host
As built: no root parts yet. The host is a per-user app inside Hammock.app (`Contents/Helpers/Hammock Screen.app`, `helpers/screen/host.m`) that hmkd starts through Launch Services (`open`), so macOS keeps Screen Recording (attributed to Hammock.app, its enclosing app) and Accessibility (its own) for it. It runs while Screen Sharing is on, works at the lock screen of a logged-in session (a `__CGPreLoginApp` section, keeping the display awake while watched), and talks only to the hmkd that started it, never by what its command line says: anything else of yours, an agent in a pane, would otherwise borrow its grants, and anything of yours can run the helper itself with a socket and owner of its choosing. The socket's peer can't settle it either: its audit token (`LOCAL_PEERTOKEN`) is worked out from the pid that last used the other end, as it is when asked, so a process that connects, hands the socket to a child and execs a suspended signed hmkd in its own place passes a signature check of it (tried: it did). So the daemon hands each host it starts a secret over Mach (`--rendezvous`, `helpers/common/rendezvous.h`): it answers only the host program it chose, signed by its team, by the hello message's audit token; the host believes only an answer whose message's audit token is a running hmkd of its own team with the hardened runtime (a message's token is its sender's as it sent it, and stale once that process has exec'd). Every connection then answers a fresh challenge with an HMAC under the secret, bound to both ends' pids, before the host serves anything (`internal/daemon/screenauth.go`). It keeps the socket's signature check too (`helpers/common/peer.h`: identifier `hmkd`, the helper's own team, the hardened runtime). A restarted daemon doesn't know an old host's secret: the host refuses it, and the daemon replaces it with its own. An ad-hoc development build has no team to check, and goes by the program being the hmkd (or the host) of its own app (weaker: that bundle is yours to write). The plan it replaces, kept for the after-reboot case:
- Hammock's own stack, not Apple's server and no VNC fallback: a root LaunchDaemon and an Aqua + LoginWindow LaunchAgent (plists in `/Library`, since SMAppService has been unreliable for LoginWindow-only agents), the agent capturing with ScreenCaptureKit and posting CGEvents.
- Opt-in: nothing privileged is installed until you turn on Screen Sharing in Settings ▸ Devices, which runs the admin install and walks through the Screen Recording and Accessibility grants.
- Sessions authenticate through the user's hmkd as media does today (`OpenMedia`-style single-use ticket on an authenticated control link).

### Picture
As built (2026-11-04), after measuring on an M4 Pro and testing on an iPhone 15 Pro Max; it replaces the first plan here (one stream scaled to the viewer, a low-QP refinement frame).
- **What the viewer shows, at its resolution.** The display is captured at its own pixels (6144×3456 here). A viewer asks for a stream about its own size (the phone 3072×1728, the Mac window 2560×1440) and, zoomed in, names the part it shows (`SetMediaView`); the host cuts that part from the capture and scales it to the stream's size, which doesn't change (no new session or keyframe), never past 1:1. Frames say which part they are (flag 8 and four u16s). Zoomed in, text is the display's own pixels; while things move, frames are small (HEVC 4:4:4 at 3072×1728 encodes in ~10-15 ms, at 4096×2304 16-36 with spikes to 585).
- **Two layers on the phone.** Under the detail, a second session of the whole display at 1280×720, 15 fps (`background`: a tenth of the link, 0.3-1.5 Mbit/s), always live: a pan shows what's there at once while the detail catches up. The detail is asked for with a tenth to spare all round, ahead of a pan once the view nears its edge (a quarter second along the pan's velocity, at most four times a second), and exactly once fingers lift. The phone decodes the detail itself and sets each picture with its place in one transaction (the video layer decodes a frame or two later than it's given one, which flashed).
- **Two tiers.** A good link gets HEVC 4:4:4 from the normal-mode encoder (real time, no reordering, `MaxFrameDelayCount` 0, quality 0.6 capped by `DataRateLimits` per second and per tenth of a second, told to expect 120 fps so it doesn't stretch its work to save power); it has no LTR and recovers by keyframe. The low-latency encoder's rate control drops frames for a second after a 4K keyframe and its keyframes are ~30 dB, so a poor link gets it only at 1080p at most, H.264 4:2:0 with LTR recovery. Down on frames given up three times in 5 s or a rate under 2.5 Mbit/s for 2 s; up after 10 s over 6.
- **Size follows the rate:** the viewer's size from 12 Mbit/s, 2560×1440 from 6, 1920×1080 from 3, 1280×720 below (down at once, up after 3 s); under 6 Mbit/s a new view goes at most once a second.
- **Sharp at rest by repeats, not a refinement frame.** The encoder ignores quality and QP caps under a rate cap, but refines unchanged parts with each frame (34 → 44 dB over 18 frames at 40 Mbit/s); a still screen sends none. So once part of the screen has changed and settled (a 32×18 grid of ScreenCaptureKit's dirty rects; what never stops changing, a video or a spinner, never counts), the same picture goes again six times. Screens go up to 40 Mbit/s: still text beside a video reaches 44 dB there, 37 at 16.
- Rate control counts as queueing only the delay past a frame's own transmission at the link's measured bandwidth, against the least delay of the last 5-10 s and the median frame (Wi-Fi's jitter put the 90th percentile tens of ms up on an idle link).
- Each session scales and encodes on a queue of its own, one frame waiting at most. A process's first VideoToolbox session mustn't be the HEVC one (its first encode hangs): the host warms up with one H.264 frame.
- Input: mouse events carry deltas (the window server moves a dragged window by them); the phone's connection has Nagle off.
- The pointer isn't in the picture: the host sends its place (60 Hz, on change) and its shape (a PNG, once per connection) and the viewer draws it at once.
- Still to do: the Mac viewer's 1:1 mode as a view.

### Media transport v2
- For screen, browser and simulator video alike, superseding ADR-0012's transport: frames as QUIC datagrams (RFC 9221) sized for Tailscale's 1280-byte MTU, adaptive FEC, a resend only when it can still arrive by the frame's deadline, and a delay-based rate controller (GCC-style one-way-delay trend) in place of relying on Cubic. Late frames are still abandoned; LTR refresh stays on the 4:2:0 tier.
- Timestamps at every stage (capture, encode, send, receive, decode, present) in the video log.
- Built end to end and judged by feel, not gated on a benchmark spike.
- As built (2026-11-04), for screens so far (`datagram.go`; browser and simulator video still go a stream per frame): a viewer asks for datagrams in `OpenMedia`; each frame record is cut into ~1 KB shards with interleaved XOR parity (one parity shard per 10 data shards, up to one per 2 as the viewer's NACKs show loss, easing back as frames go without), the viewer NACKs what parity can't rebuild once a later frame shows it's lost (or after 40 ms), and the host resends only while the frame's fresh (400 ms). Rate control is still ADR-0012's ack-latency one, with the changes above; the GCC-style controller and per-stage timestamps are still to do. A host or viewer without datagrams keeps the streams.

### Sessions
- No prompt on the Mac for your own devices; the phone asks for Face ID / passcode before each session. Every session is audited.
- Several viewers at once, each with its own encode; whoever gave input last controls.
- At the lock screen you type the password yourself; nothing is stored.
- Started from the computer menu and lists (phone's computer list; the Mac footer's computer menu, the palette, View ▸ Show Screen of <Mac>). No Needs You detection of system dialogs, no CLI.
- iPhone viewer: trackpad mode by default (drag moves the cursor, tap clicks, two fingers scroll/right-click, pinch zooms the view), a toggle to direct touch. Mac viewer: its own window with full screen and fit / 1:1.

### Menu bar helper
- A small agent app inside Hammock.app, started at login with the server, so it's there whether or not Hammock.app runs. It talks to hmkd over its socket and is also the screen-sharing indicator.
- Copy says **Server**, not daemon.
- Menu: the server's state (Running / Stopped / Not responding) with uptime and pane count; Restart, Stop and Start Server; app and server versions, flagging a mismatch; remote access and its Tailscale address, and who's connected; screen viewers with End Session / End All; the Needs You count and list; Open Hammock, Settings…; Quit Hammock (the server keeps running) and Quit Hammock and Stop Server. Stopping the server warns what stops (remote access, notifications, screen sharing) and what survives (panes).
- Icon: a monochrome glyph, lit while anyone views the screen, a warning mark when the server is stopped or not responding. Shown by default; a setting hides it when idle (it still appears for a screen session or a server problem).

## Consequences
- Hammock ships a root daemon and a login-window agent for the first time: a bigger security surface, an admin install, and login-window upkeep on every macOS release.
- Transport v2 touches working pane video; it has to land without regressing it.
- 4:4:4 gives up low-latency rate control and LTR on the good-link tier; switching tiers must be quick when the link degrades.
- To verify: iPhone hardware decode of HEVC 4:4:4; ScreenCaptureKit at the login window on the macOS versions Hammock supports.

## As built (2026-11-10)
- Screen sharing, the Mac and iPhone viewers and the menu bar item are built and in use. The "As built" notes above say where each part differs from the plan.
- No root parts are installed yet, so the first consequence above hasn't happened: sharing after a reboot, before anyone logs in, is [#30](https://github.com/homegrownsw/hammock/issues/30).
- Media transport v2 is used for screens only; browser and simulator video keep ADR-0012's streams. The GCC-style rate controller and per-stage timestamps aren't built.
- Follow-ups: the shared Mac's clipboard back to the viewer ([#29](https://github.com/homegrownsw/hammock/issues/29)), trying Mac to Mac end to end ([#28](https://github.com/homegrownsw/hammock/issues/28)), viewers relayed through a linked Mac ([#15](https://github.com/homegrownsw/hammock/issues/15)).
