# Simulator panes

How simulator panes show and drive an iOS simulator with no Simulator.app window, through Xcode's private CoreSimulator and SimulatorKit frameworks plus an XCTest runner. Code:

- [`helpers/simulator/host.m`](../../helpers/simulator/host.m): the host, Hammock Simulator.
- [`helpers/simulator/runner/`](../../helpers/simulator/runner/): the XCTest runner, for rotation and multi-finger gestures.
- [`helpers/simulator/guest/main.m`](../../helpers/simulator/guest/main.m): the guest helper run inside a foldable, for its fold.
- [`helpers/simulator/build.sh`](../../helpers/simulator/build.sh): builds all three.
- [`internal/daemon/simulator.go`](../../internal/daemon/simulator.go): the daemon's side.
- [`apple/Sources/HammockMac/SimulatorView.swift`](../../apple/Sources/HammockMac/SimulatorView.swift): the Mac's pane and device frame.

Everything here is private API, written against Xcode 27 and iOS 27 runtimes. Expect to revisit it with each Xcode release; it's kept in this one helper so nothing else breaks when it does.

## The host

- **One host per daemon**, like the browser host ([browser-panes.md](browser-panes.md)). Newline-delimited JSON over a Unix socket (`run/simulator-host.sock`), any number of clients; the message list is in `host.m`'s header comment. The daemon owns which panes show which device. The Mac app talks to the host directly for the picture and touches; agents' screenshots and input go through the daemon. The host outlives the app and the daemon, and the devices outlive both.
- **Loading the frameworks.** Library validation is off, since the host loads Xcode's private frameworks. So it loads only code signed by Apple (`anchor apple`), checked before `dlopen`: CoreSimulator from `/Library/Developer/PrivateFrameworks`, SimulatorKit from the Xcode `xcode-select` chose (read from `/var/db/xcode_select_link` without running it), else `/Applications/Xcode.app`. Never from `DEVELOPER_DIR`: whoever could set the host's environment could have it load their code.
- A device that isn't booted is booted with `xcrun simctl boot`, and the host waits for its display to come up.

## Display

- A device's IO ports (`-[SimDevice io]`, `ioPorts`) include descriptors conforming to `SimDisplayIOSurfaceRenderable`, one per screen. The screens are the ones whose `framebufferSurface` isn't nil. It's the live IOSurface at full resolution, updated in place as the device renders.
- The host puts that IOSurface in a layer of its own `CAContext`, and the app shows the context through `CALayerHost`, as browser panes do: frames are never copied. New frames call the private `-[CALayer setContentsChanged]` so the layer re-reads the surface.
- `-[SimScreen registerScreenCallbacksWithUUID:callbackQueue:frameCallback:surfacesChangedCallback:propertiesChangedCallback:]` reports new frames, surface swaps and property changes (orientation). Older runtimes without it get `registerCallbackWithUUID:damageRectanglesCallback:` and `…ioSurfacesChangeCallback:`.
- **The ports, descriptors and screen properties are ROCK remote proxies.** They don't support KVC (`valueForKey:` throws), so the host calls selectors directly.
- **Device frames.** `-[SimScreen screenProperties]` (a `SimScreenProperties`) gives `screenID`, `name`, `uiOrientation`, `chromeIdentifier` and `framebufferMaskIdentifier`. The chrome identifier (falling back to the device type's, e.g. `com.apple.dt.devicekit.chrome.phone11`) names `/Library/Developer/DeviceKit/Chrome/<last part>.devicechrome`, whose `chrome.json` (nine-slice image names, `sizing`, side buttons with anchors and offsets) and PDFs the Mac draws the frame from. The screen's shape is the mask PDF: the screen's own (`<device type bundle>/Contents/Resources/<id>.pdf`), else the device type's `framebufferMaskURL`.
- Points: a framebuffer whose short side is 1000 pixels or more is taken as 3x, else 2x.

## Orientation

- **The framebuffer never turns.** After a rotation the IOSurface stays in the device's portrait, with the content drawn sideways. (`simctl io screenshot` returns it turned.)
- The host reads each screen's `uiOrientation` (1 portrait, 2 upside down, 3 and 4 landscape) and turns the picture by 0°, 180°, 90° or 270° clockwise to match: the layer, screenshots (CIImage) and video (`VTPixelRotationSession` in the encoder, see [remote-media.md](remote-media.md)). Touches and gesture points are mapped back to portrait. Reading the orientation, rather than tracking rotations, keeps it right after a host restart, follows apps that don't rotate (Settings on an iPhone stays portrait), and covers a foldable's inner screen, which sits a quarter turn round.
- After a rotation the host re-reads the screens a few times as it settles.

## Input

- **Touches** go through SimulatorKit's Swift class `SimDeviceLegacyHIDClient` (`initWithDevice:error:`, `sendWithMessage:freeWhenDone:completionQueue:completion:`), which takes Indigo HID messages built by SimulatorKit's exported C functions. A touch is `IndigoHIDMessageForMouseNSEvent` with target `0x32` (the touch screen), sent as down, moves and up.
- **`IndigoHIDMessageForMouseNSEvent`'s signature changed.** In Xcode 27 it is `(CGPoint *point, CGPoint *point2, uint32 target, NSEventType type, uint32 edge, double width, double height)`. The point is divided by the size, so the host passes a 0–1 ratio and a size of 1×1. The older five-argument form other tools use (ending in a `BOOL`) sends nothing that registers.
- **A second point** builds a larger two-finger message, but devices ignore it through the legacy HID client. Pinches go through the runner.
- **Buttons:** `IndigoHIDMessageForButton(source, down/up, 0x33)`. Sources: home `0x0`, lock `0x1`, side `0xbb8`, Siri `0x400002`, and `0x3f0`, which shows or hides the software keyboard (as DeviceHub's ⌘K).
- **Keys:** `IndigoHIDMessageForKeyboardArbitrary(usage, down/up)` with HID keyboard usages (typed text is mapped to a US layout).
- **Clipboard:** `xcrun simctl pbcopy UDID` then ⌘V (usages 227 + 25) pastes; ⌘C (227 + 6) then `simctl pbpaste` copies. Accents and emoji survive both ways.

## The XCTest runner: rotation and gestures

Only an XCTest session `xcodebuild` started may rotate an iOS 27 simulator or synthesise multi-finger gestures. Rotation needs DeviceHub's accessibility service (Apple-internal), `BKSHIDServicesSetDeviceOrientationForAutomation` needs an entitlement launchd won't grant an ad-hoc binary, and testmanagerd refuses `XCUIDevice` calls from a process it didn't start. So:

- The runner is a UI-test bundle (`HammockSimRunner`, built with xcodegen and `xcodebuild build-for-testing`). The helper bundles only Hammock's parts of it: the test bundle, its host app and the xctestrun.
- The runner app around the test bundle is Apple's XCTRunner, from Xcode, which isn't Hammock's to redistribute. The host makes it at first use from the template in the Mac's own Xcode (`Platforms/iPhoneSimulator.platform/Developer/Library/Xcode/Agents/XCTRunner.app`), as `xcodebuild` does: copied, its executable named after the runner app and its Info.plist's placeholders filled in (`prepareRunner`). It goes into a temporary folder of the host's own, so two hosts never share one.
- The host starts it per device on first use with `xcodebuild test-without-building -xctestrun … -destination id=UDID`, passing a socket path as `TEST_RUNNER_HMK_SIM_RUNNER_SOCKET` (xcodebuild hands it to the test as `HMK_SIM_RUNNER_SOCKET`). The test connects back and takes one JSON command per line. Starting takes several seconds; commands then take well under a second. Closing the socket ends the test, and `xcodebuild` with it; the runner stops when the device's last pane closes.
- **Rotation:** `XCUIDevice.sharedDevice.orientation` (public API); `left` and `right` turn a quarter turn from where the device is. Every reply carries the device's `UIDeviceOrientation`.
- **Gestures:** XCTest's private `XCSynthesizedEventRecord` and `XCPointerEventPath`, any number of fingers, points 0–1 of the device's native portrait, interface orientation 1, display 0. This follows agent-device's approach (MIT); the code is Hammock's own.

## Foldables (the iPhone Duo)

- **Two screens.** The Duo's display ports include two integrated screens with framebuffers: the cover (`LCD`) and the larger inner one (`LCD-1`). Both stay powered; the one not in use is black. So the host picks the lit one by sampling a grid of pixels, keeps the current one while both are dark (the device asleep), and checks again as the hinge settles.
- **Touches on the inner screen.** SimulatorKit's touch target for a screen is `0x40000000 | screenID`; `0x32` only reaches the main screen. Coordinates are 0–1 of that screen's framebuffer. Arbitrary targets can crash the device's SpringBoard.
- **The guest helper** (`hmk-sim-guest`, started with `xcrun simctl spawn UDID`) registers a virtual HID service (`HIDVirtualEventService`) in the device, as DeviceHub's controls do, and sends vendor-defined events (usage page `0xff61`, usage `0x5b`) carrying an OSSerialized `{provider, source, type, value}`. locationd's device state relay (`CMDeviceStateRelayManager`) reads them; regular iPhones have no such relay, which is why they rotate through the runner instead.
  - **The fold:** `source = hinge-slider-control`, `type = range`, the angle in degrees (0 closed, 180 flat) as an OSSerialized number with DeviceHub's header (`0x3f`, `0x84`) then a little-endian double. SpringBoard swaps screens past about 85° opening and below 20° closing.
  - **Rotation:** `source = orientation-picker-control`, `type = enum`. Quicker than the runner, and works open or closed. This technique follows serve-sim's Duo helper (Apache-2.0); the code is Hammock's own.
- **Not supported:** pinch on the inner screen (the runner's event record uses display 0).
