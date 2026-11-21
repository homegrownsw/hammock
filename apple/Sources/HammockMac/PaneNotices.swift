import AppKit
import HammockKit
import SwiftUI

// What an editor or diff pane has to tell you, kept to that pane: an error
// is a banner along its top, with what to do about it (Retry, Save As…),
// until it's dismissed or done; a question that needs an answer (unsaved
// changes, a file changed on disk) is a sheet on the window the pane is in,
// not an alert that stops every window; a passing note (PanePopup) fades.

@MainActor
@Observable
final class PaneNotices {
    static let shared = PaneNotices()

    struct Action: Identifiable {
        let title: String
        let run: () -> Void
        var id: String { title }
    }

    struct Banner: Identifiable {
        let id = UUID()
        let message: String
        let detail: String
        let actions: [Action]
    }

    /// Each pane's error, the latest.
    private(set) var banners: [String: Banner] = [:]

    /// An error in the pane: message says what failed ("Couldn't save
    /// notes.md"), the error why. Running an action dismisses it.
    func error(_ paneID: String, _ message: String, _ error: Error? = nil, actions: [Action] = []) {
        banners[paneID] = Banner(message: message, detail: error.map(Self.why) ?? "", actions: actions)
    }

    func dismiss(_ paneID: String) { banners[paneID] = nil }

    /// An error's description, without the daemon's "rpc error: …" wrapping.
    static func why(_ error: Error) -> String {
        error.localizedDescription.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    // MARK: Questions

    /// The windows panes are shown in, as their views last saw (PaneWindowReader).
    @ObservationIgnored private var windows: [String: Weak] = [:]
    private struct Weak { weak var window: NSWindow? }

    func shown(_ paneID: String, in window: NSWindow?) {
        windows[paneID] = Weak(window: window)
    }

    /// The window to ask in: the pane's, else the one you're in.
    func window(for paneID: String?) -> NSWindow? {
        paneID.flatMap { windows[$0]?.window } ?? WorkspaceWindow.current
    }

    /// Asks a question as a sheet on the pane's window: true if you chose
    /// button (Cancel is the other). Without a window, an alert.
    func ask(_ paneID: String?, _ message: String, info: String, button: String) async -> Bool {
        let a = NSAlert()
        a.messageText = message
        a.informativeText = info
        a.addButton(withTitle: button)
        a.addButton(withTitle: "Cancel")
        return await present(a, in: window(for: paneID)) == .alertFirstButtonReturn
    }

    /// Shows an alert as a sheet on window (or on its own without one), and
    /// waits for its answer. shown runs once it's up (to select in a field).
    func present(_ a: NSAlert, in window: NSWindow?, shown: (() -> Void)? = nil) async -> NSApplication.ModalResponse {
        guard let window else {
            DispatchQueue.main.async { shown?() }
            return a.runModal()
        }
        if window.attachedSheet != nil {
            // One question at a time on a window: this one waits its turn.
            try? await Task.sleep(for: .milliseconds(200))
            return await present(a, in: window, shown: shown)
        }
        return await withCheckedContinuation { done in
            a.beginSheetModal(for: window) { done.resume(returning: $0) }
            shown?()
        }
    }
}

/// Along the top of an editor or diff pane: its error, what to do about it,
/// and ✕.
struct PaneErrorBanner: View {
    let paneID: String
    private var notices: PaneNotices { .shared }

    var body: some View {
        if let b = notices.banners[paneID] {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
                Text("\(Text(b.message).fontWeight(.semibold))\(Text(b.detail.isEmpty ? "" : "  " + b.detail).foregroundStyle(.secondary))")
                    .lineLimit(3)
                    .textSelection(.enabled)
                Spacer(minLength: 6)
                ForEach(b.actions) { a in
                    Button(a.title) {
                        notices.dismiss(paneID)
                        a.run()
                    }
                }
                .controlSize(.small)
                Button { notices.dismiss(paneID) } label: {
                    Image(systemName: "xmark").font(.system(size: 10, weight: .semibold))
                }
                .buttonStyle(.borderless)
                .tooltip("Dismiss", label: "Dismiss")
            }
            .font(.callout)
            .padding(.horizontal, 12).padding(.vertical, 6)
            .background(Color.red.opacity(0.1))
            .id(b.id)
            .transition(.opacity)
        }
    }
}

/// A passing note over the bottom of a pane (a comment held, an editor that
/// couldn't start): it fades after a few seconds, or with its ✕.
struct PanePopup: View {
    @Binding var text: String

    var body: some View {
        if !text.isEmpty {
            HStack(spacing: 6) {
                Text(text)
                    .lineLimit(3)
                Button { withAnimation { text = "" } } label: {
                    Image(systemName: "xmark").font(.system(size: 9, weight: .bold))
                }
                .buttonStyle(.borderless)
                .tooltip("Dismiss", label: "Dismiss")
            }
            .font(.callout)
            .padding(.leading, 12).padding(.trailing, 8).padding(.vertical, 6)
            .background(.regularMaterial, in: Capsule())
            .padding(10)
            .transition(.opacity)
            .task(id: text) {
                let shown = text
                try? await Task.sleep(for: .seconds(4))
                guard !Task.isCancelled, text == shown else { return }
                withAnimation(.easeOut(duration: 0.3)) { text = "" }
            }
        }
    }
}

/// Tells PaneNotices which window the pane is in, for its questions' sheets.
struct PaneWindowReader: NSViewRepresentable {
    let paneID: String

    final class Probe: NSView {
        var paneID = ""
        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window else { return }
            MainActor.assumeIsolated { PaneNotices.shared.shown(paneID, in: window) }
        }
    }

    func makeNSView(context: Context) -> Probe {
        let v = Probe()
        v.paneID = paneID
        return v
    }

    func updateNSView(_ v: Probe, context: Context) {
        v.paneID = paneID
        if let w = v.window { PaneNotices.shared.shown(paneID, in: w) }
    }
}
