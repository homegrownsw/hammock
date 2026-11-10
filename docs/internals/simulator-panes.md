# Spike: simulator display and input without Simulator.app

2026-10-31, Xcode 27.1, iOS 27.0 runtime. Code in `spikes/simulator/`.

## Result

Both halves work headlessly, with no Simulator.app window:

- **Display.** CoreSimulator's device IO ports include `SimDisplayIOSurfaceRenderable` descriptors (one per screen; the device's main screen is the one with a non-nil surface). `framebufferSurface` returns the live IOSurface at full resolution (1206×2622 for an iPhone 18 Pro), updated in place as the device renders. `SimScreen`'s `registerScreenCallbacksWithUUID:callbackQueue:frameCallback:surfacesChangedCallback:propertiesChangedCallback:` reports new frames and surface swaps (rotation, resize).
- **Input.** SimulatorKit's Swift class `SimDeviceLegacyHIDClient` (`initWithDevice:error:`, `sendWithMessage:freeWhenDone:completionQueue:completion:`) takes Indigo HID messages built by SimulatorKit's exported C functions. A touch is `IndigoHIDMessageForMouseNSEvent` with target `0x32` (the touch screen), sent as a down then an up.

## Gotchas

- The ports and descriptors are ROCK remote proxies: they don't support KVC (`valueForKey:` throws); call selectors directly.
- `IndigoHIDMessageForMouseNSEvent` changed signature. In Xcode 27 it is `(CGPoint *point, CGPoint *point2, uint32 target, NSEventType type, uint32 edge, double width, double height)`: the point is divided by the size, so pass a 0–1 ratio with a size of 1×1. idb's older five-argument form (`…, BOOL`) sends nothing that registers.
- Everything is private API: pin per Xcode release, keep it in one helper, and fall back to `simctl io screenshot` plus `simctl` where it breaks.

## Plan

One `Hammock Simulator` helper per daemon, like the browser host (newline JSON over a Unix socket, outlives the app):

- shows a device's screen to the Mac app as a remote CoreAnimation layer (`CALayerHost`, the browser panes' path), the layer's contents being the framebuffer IOSurface;
- injects touches, swipes, buttons and keyboard input sent by the app or the daemon;
- answers screenshots and `simctl` work (boot, install, launch, logs) for the MCP tools.

## Later findings (Xcode 27, iOS 27)

- **Rotation.** Simulator.app is gone; DeviceHub rotates a device through the accessibility service inside it (`AXAuditRemoteDevice`, "Could not update rotation, AXAuditRemoteDevice not available" in DeviceKit). The old route, a GSEvent of type 50 (orientation) sent as mach message 0x7B to the device's `PurpleWorkspacePort` (`-[SimDevice lookup:error:]`), is delivered (`KERN_SUCCESS`, natural or packed layout) but Safari doesn't rotate.
- **Pinch.** `IndigoHIDMessageForMouseNSEvent`'s second point builds a larger, two-finger message (0x200 bytes, three entries), but a pinch sent that way through `SimDeviceLegacyHIDClient` doesn't zoom Safari.
- **Clipboard.** `xcrun simctl pbcopy/pbpaste UDID` plus ⌘V/⌘C (HID usages 227 + 25/6) copy and paste both ways, accents and emoji included.
- **Device frames.** `SimDeviceType.chromeIdentifier` (e.g. `com.apple.dt.devicekit.chrome.phone11`) names `/Library/Developer/DeviceKit/Chrome/phone11.devicechrome`: `chrome.json` (nine-slice image names, `sizing` = the frame's width, side buttons with anchor and offsets) and PDFs. `framebufferMaskURL` is the screen's shape as a PDF at pixel size.

## Rotation and pinch (2026-11-01)

What was tried, on an iOS 27.0 iPhone 18 Pro and the iOS 27.1 iPhone Duo:

- **A virtual HID service in the guest** (a helper run with `xcrun simctl spawn`, registering `HIDVirtualEventService` from HID.framework). A vendor-defined event, usage page 0xff61 usage 0x5b, carrying an OSSerialized `{provider, source = orientation-picker-control, type = enum, value}` (DeviceHub's own, as serve-sim's Duo helper sends it) rotates the **Duo** only: locationd's `CMDeviceStateRelayManager`, the foldable's hinge and orientation relay, consumes it. On a regular iPhone backboardd receives the event and nothing acts on it.
- **A virtual accelerometer** (Apple vendor page 0xff00, usage 3, built in): backboardd adds the service and counts the events, but doesn't take its orientation from it ("missing accel HID service plugin").
- **`BKSHIDServicesSetDeviceOrientationForAutomation`** and `-[SBSSystemServiceClient setDeviceOrientation:]` from the guest: silently refused without the `com.apple.backboardd.setDeviceOrientation` entitlement, and launchd won't spawn an ad-hoc binary that claims it ("Security policy issue").
- **`XCUIDevice.orientation` from a plain spawned process**: testmanagerd answers "Not authorized for performing UI testing actions": it only serves a runner an `xcodebuild` test session started.
- **DeviceHub** rotates through `AXAuditRemoteDevice` (AccessibilityAudit.framework) over a RemoteXPC connection to axauditd's `remoteAXService`, which requires `AppleInternal`.

What Hammock does: an **XCTest runner** (`helpers/simulator/runner`, a UI-test bundle built with xcodegen and `xcodebuild build-for-testing`). The helper bundles only Hammock's parts of it: the test bundle, its host app and the xctestrun. The runner app around the test bundle is Apple's XCTRunner, from Xcode, which isn't Hammock's to redistribute, so the host makes it at first use from the template in the Mac's own Xcode (`Agents/XCTRunner.app`), as `xcodebuild` does: copied, its executable renamed and its Info.plist's three placeholders filled in (`prepareRunner` in `host.m`). The host starts it per device on first use with `xcodebuild test-without-building -xctestrun … -destination id=UDID`, passing a socket path as `TEST_RUNNER_HMK_SIM_RUNNER_SOCKET`; the test connects back and takes JSON lines. Startup is about 10 s; commands then take well under a second. It rotates with `XCUIDevice.sharedDevice.orientation` (left/right relative to the current orientation) and plays gestures with `XCSynthesizedEventRecord` + `XCPointerEventPath` (any number of fingers; points in the device's portrait, interface orientation 1). This is agent-device's approach (MIT); the code is our own.

- **The framebuffer doesn't turn.** After a rotation the IOSurface stays in the device's portrait with the content drawn sideways (DeviceHub turns its window). `simctl io screenshot` returns it turned. So the host keeps each device's rotation (the runner reports `UIDeviceOrientation` with every reply: landscape right = 90° clockwise) and turns the CAContext layer, screenshots (CIImage) and video (`VTPixelRotationSession` in the encoder), and maps touches and gesture points back to portrait.
- **Software keyboard:** `IndigoHIDMessageForButton(0x3f0, down/up, 0x33)` toggles it, as DeviceHub's ⌘K does.
- A device's rotation is learnt from its runner, so a host that restarts with a device already turned shows it in portrait until the next rotation.

## Foldables (the iPhone Duo, 2026-11-01)

- **The fold.** The guest helper (`helpers/simulator/guest`, spawned in the device) sends the same vendor event as rotation, with `source = hinge-slider-control`, `type = range` and the angle in degrees as an OSSerialized number: header `0x3f`/`0x84` (DeviceHub's), then a little-endian double. SpringBoard swaps screens past about 85° opening and below 20° closing ("Swap boundary 20.0° -> 85.0°").
- **Two screens.** The Duo's display ports include two integrated screens with framebuffers: `LCD` (screen 1, the cover, 1398×2034) and `LCD-1` (screen 3, the inner one, 2007×2853). Both stay powered; the one not in use is black, so the host picks the lit one by sampling pixels, and checks again as the hinge settles.
- **Screen properties.** `-[SimScreen screenProperties]` (a `SimScreenProperties`; ROCK proxies, so call the selectors, not KVC) gives `screenID`, `name`, `uiOrientation`, `chromeIdentifier` and `framebufferMaskIdentifier` (a PDF in the device type's bundle, `Contents/Resources/<id>.pdf`) per screen. The inner screen has its own frame (phone14) and mask.
- **Orientation.** `uiOrientation` is the screen's interface orientation (1 portrait, 2 upside down, 3 and 4 landscape); the framebuffer never turns, and the content is drawn turned by 0°, 180°, 90° or 270° clockwise for those. The host reads it instead of tracking rotations, so it's right after a restart, follows apps that don't rotate (Settings on an iPhone stays portrait), and covers the inner screen, which sits a quarter turn round (it reports 3 when the device is portrait).
- **Touches on another screen.** SimulatorKit's touch target for a screen is `0x40000000 | screenID` (1 for TV-out and wireless screens); `0x32`, the usual target, only reaches the main screen. Coordinates are 0–1 of that screen's framebuffer. Arbitrary targets can crash the device's SpringBoard (3 did).
- Not yet: pinch on the inner screen (the XCTest runner's event record uses display 0).
