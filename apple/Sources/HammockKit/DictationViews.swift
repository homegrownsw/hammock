import SwiftUI
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// The composer's mic: tap to dictate, tap again to stop. Listening, it's
/// filled in the accent colour and pulses with your voice.
struct DictationButton: View {
    let dictation: ComposerDictation
    let action: () -> Void
    @Environment(\.agentAccent) private var accent

    private var on: Bool { dictation.active }
    private var listening: Bool { dictation.state == .listening }

    var body: some View {
        Button(action: action) {
            Image(systemName: on ? "mic.fill" : "mic")
                .font(.body.weight(.medium))
                .contentTransition(.symbolEffect(.replace))
                .frame(width: 22, height: 22)
                .background {
                    // The level, inside: a light that fills the button as
                    // you speak (the glass clips it to the button).
                    Circle()
                        .fill(.white.opacity(0.35))
                        .frame(width: 40, height: 40)
                        .scaleEffect(0.25 + dictation.level * 0.85)
                        .opacity(listening ? 1 : 0)
                        .animation(.easeOut(duration: 0.1), value: dictation.level)
                        .allowsHitTesting(false)
                }
        }
        .agentGlassButton(prominent: on)
        .buttonBorderShape(.circle)
        .tint(on ? accent : nil)
        .foregroundStyle(on ? AnyShapeStyle(.white) : AnyShapeStyle(.primary))
        .background {
            // The level: a soft halo behind the button that swells past its
            // edge as you speak. Outside the button, not in its label, where
            // the accent fill hid it and the glass clipped it.
            Circle()
                .fill(accent.opacity(0.35))
                .scaleEffect(1 + dictation.level * 0.6)
                .opacity(listening ? 1 : 0)
                .animation(.easeOut(duration: 0.1), value: dictation.level)
                .allowsHitTesting(false)
        }
        .help(on ? "Stop Dictating" : DictationRequests.hint("Dictate"))
        .accessibilityLabel(on ? "Stop dictating" : "Dictate")
        .animation(.snappy(duration: 0.2), value: on)
    }
}

/// Above the composer when dictation can't start yet: the speech model
/// downloading, or why it can't listen (with Settings when it's the
/// microphone's permission).
struct DictationNotice: View {
    let dictation: ComposerDictation
    let openSettings: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            if let p = dictation.installing {
                Image(systemName: "arrow.down.circle").foregroundStyle(.secondary)
                Text("Getting dictation ready…").foregroundStyle(.secondary)
                ProgressView(value: p).frame(width: 60)
                Spacer(minLength: 4)
            } else if let problem = dictation.problem {
                Image(systemName: "mic.slash").foregroundStyle(.secondary)
                Text(problem).lineLimit(2).foregroundStyle(.secondary)
                Spacer(minLength: 4)
                if dictation.problemIsPermission {
                    Button("Open Settings", action: openSettings).agentGlassButton()
                }
                Button {
                    dictation.problem = nil
                } label: {
                    Image(systemName: "xmark").font(.caption.weight(.semibold))
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .accessibilityLabel("Dismiss")
            }
        }
        .font(.caption)
        .padding(.horizontal, 12).padding(.vertical, 6)
        .agentGlass(in: Capsule())
    }
}

extension DictationRequests {
    /// The Mac's shortcut for Dictate ("⇧⌘M"), from its keybinds; nil on the iPhone.
    public static var shortcutLabel: @MainActor () -> String? = { nil }

    static func hint(_ title: String) -> String {
        shortcutLabel().map { "\(title) (\($0))" } ?? title
    }
}

extension Composer {
    /// The microphone's privacy setting (System Settings on the Mac, the
    /// app's page in Settings on the iPhone).
    func openMicrophoneSettings() {
        #if os(macOS)
        let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone")!
        NSWorkspace.shared.open(url)
        #else
        if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
        #endif
    }
}
