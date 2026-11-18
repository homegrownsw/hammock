# Hammock for Android

The Android app: what the iPhone app does, for Android phones, foldables and tablets (Android 13 and
later). Kotlin and Jetpack Compose, Material 3 coloured by Hammock's themes.

## Building

```sh
just android          # libghostty-vt for Android, then a debug build
just android-device   # …installed on the emulator or phone adb sees, and opened
just android-test     # JVM tests and lint
```

It needs a JDK 17 or later (`JAVA_HOME`, else Android Studio's), the Android SDK (`ANDROID_HOME`, else
`~/Library/Android/sdk`) with NDK 27.1, Zig and Go (as the rest of Hammock does). libghostty-vt is built
once per pinned Ghostty commit into the cache every checkout shares (`scripts/android-vt.sh`), as the Linux
builds are. libhmkquic.so, the video's QUIC (the daemon's own quic-go: `cmd/hmkquic`, `internal/mediaview`),
is built by `:media`'s Gradle task (`scripts/android-quic.sh`). A debug build is `com.homegrownsw.hammock.dev`, so it sits beside a release build.

## Modules

| Module | What it is |
| --- | --- |
| `:proto` | The daemon's protocol, generated from `../proto` at build time (nothing generated is committed). |
| `:client` | The connection (a WebSocket over TLS pinned to the computer's certificate, raced over its addresses), signing in and pairing, and the model. Plain Kotlin on the JVM: its tests run anywhere. |
| `:terminal` | libghostty-vt through JNI, a GLES 3 renderer, and input (the keyboard, IME, the accessory bar). |
| `:agentview` | The agent view: the conversation, prompt cards and the composer. |
| `:panes` | Editor and diff panes (the Mac's and iPhone's CodeMirror pages in a WebView) and log panes. |
| `:media` | Browser, simulator and screen-sharing panes: the computer's H.264 video over QUIC (screens as datagrams), or in band on the WebSocket where UDP doesn't get through. |
| `:app` | The app: pairing, Sessions, Groups, Devices, Settings, notifications, the Hammock Key, the demo. |

## Trying it against a daemon

`just android-dev-daemon up` starts a scratch daemon (from `bin/`: `just build`) that can't touch your own
Hammock or config, listening for phones on 127.0.0.1:7499, which `adb reverse` gives the emulator. It's
started with `hmkd --dev-pairing` (development builds only), so a script or an agent can make its pairing
codes. A debug build pairs straight from a link:

```sh
dir=$(just android-dev-daemon up)
adb shell am start -n com.homegrownsw.hammock.dev/com.homegrownsw.hammock.MainActivity \
  --es pair "'$(just android-dev-daemon pair "$dir")'"
just android-dev-daemon hmk "$dir" ls
just android-dev-daemon down "$dir"
```

`LiveDaemonTest` pairs, signs in and follows an update against one: `HAMMOCK_PAIR=<link> ./gradlew :client:test`.

## Notifications

Pushes come through the relay (`relay/push`) and Firebase Cloud Messaging, end-to-end encrypted: the app
decrypts each and builds the notification itself. Firebase is set up from build-time values, not a
`google-services.json`: `HMK_FCM_PROJECT_ID`, `HMK_FCM_APP_ID`, `HMK_FCM_API_KEY` and `HMK_FCM_SENDER_ID`
(or `hammock.FCM_*` Gradle properties). A build without them has no notifications while the app is closed,
and Settings says so. `HMK_PUSH_RELAY` points a build at another relay.

## Releasing

`just android-release` builds `dist/android/Hammock-<version>.aab` for Google Play and `.apk` for GitHub,
signed with the upload key `android/keystore.properties` names (git-ignored, never committed):

```properties
storeFile=/path/to/upload.jks
storePassword=…
keyAlias=upload
keyPassword=…
```

The version is `HAMMOCK_ANDROID_VERSION` in `apple/iOS/Config/Version.xcconfig`; the build number counts
commits, as the Apple apps' do. Its release notes are `release-notes/android/` (`just note android …`).

## Not done yet

Uploading to Play and freezing the notes (`scripts/release` releases the Apple apps alone).
