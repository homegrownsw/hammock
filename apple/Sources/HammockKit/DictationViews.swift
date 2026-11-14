import SwiftUI
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// The composer's mic: tap to dictate, tap again to stop. While it's on,
/// it's a stop button, and `DictationLevels` beside it shows your voice.
struct DictationButton: View {
    let dictation: ComposerDictation
    let action: () -> Void

    private var on: Bool { dictation.active }

    var body: some View {
        Button(action: action) {
            Image(systemName: on ? "stop.fill" : "mic")
                .font(on ? .caption.weight(.bold) : .body.weight(.medium))
                .contentTransition(.symbolEffect(.replace))
                .frame(width: 22, height: 22)
        }
        .agentGlassButton()
        .buttonBorderShape(.circle)
        .foregroundStyle(.primary)
        .help(on ? "Stop Dictating" : DictationRequests.hint("Dictate"))
        .accessibilityLabel(on ? "Stop dictating" : "Dictate")
        .animation(.snappy(duration: 0.2), value: on)
    }
}

/// While dictating, in place of the model and context: the last few
/// seconds of your voice as bars, newest on the right, a dot when it's quiet.
struct DictationLevels: View {
    let dictation: ComposerDictation

    static let bar: CGFloat = 3
    static let gap: CGFloat = 2.5

    var body: some View {
        Canvas { ctx, size in
            let step = Self.bar + Self.gap
            let count = Int((size.width + Self.gap) / step)
            // The newest reading at the trailing edge; dots before there's
            // history to fill the width.
            let heard = dictation.levels.suffix(count)
            let levels = Array(repeating: 0, count: max(0, count - heard.count)) + heard
            let start = size.width - CGFloat(levels.count) * step + Self.gap
            for (i, level) in levels.enumerated() {
                let h = max(Self.bar, CGFloat(level) * size.height)
                let rect = CGRect(x: start + CGFloat(i) * step, y: (size.height - h) / 2, width: Self.bar, height: h)
                ctx.fill(Path(roundedRect: rect, cornerRadius: Self.bar / 2), with: .style(.primary))
            }
        }
        .frame(height: 22)
        .frame(maxWidth: .infinity)
        .opacity(dictation.state == .listening ? 1 : 0.4)
        .accessibilityHidden(true)
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
