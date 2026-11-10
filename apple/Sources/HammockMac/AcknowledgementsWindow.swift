import AppKit
import HammockKit
import SwiftUI
import WebKit

// Acknowledgements: the third-party software in Hammock and its licences
// (internal/acknowledgements/Acknowledgements.json, which the build bundles),
// in a window of their own. Opened from Settings ▸ About, Help ▸
// Acknowledgements and the command palette.

@MainActor
final class AcknowledgementsUI {
    static let shared = AcknowledgementsUI()
    private var window: NSWindow?

    func show() {
        if let window {
            window.makeKeyAndOrderFront(nil)
            NSApp.activate()
            return
        }
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 860, height: 600),
                         styleMask: [.titled, .closable, .miniaturizable, .resizable], backing: .buffered, defer: false)
        w.title = "Acknowledgements"
        w.isReleasedWhenClosed = false
        w.contentMinSize = NSSize(width: 560, height: 360)
        w.contentView = NSHostingView(rootView: AcknowledgementsView().themeTint())
        w.center()
        w.setFrameAutosaveName("HammockAcknowledgements")
        window = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }
}

/// The list of components beside the one chosen's licence; Chromium's
/// credits (a page from its build, too long to list here) are one more row.
struct AcknowledgementsView: View {
    private enum Row: Hashable {
        case component(String)
        case chromiumCredits
    }

    private let items = Acknowledgements.load()
    @State private var selection: Row?
    private let credits = AcknowledgementsView.chromiumCreditsURL

    init(initial: String? = nil) {
        if initial == "chromium-credits" {
            _selection = State(initialValue: .chromiumCredits)
        } else {
            let items = Acknowledgements.load()
            let first = initial.flatMap { name in items.first { $0.name.localizedCaseInsensitiveContains(name) } } ?? items.first
            _selection = State(initialValue: first.map { .component($0.name) })
        }
    }

    /// Chromium's credits, which the browser host's build copies from CEF's.
    static var chromiumCreditsURL: URL? {
        let url = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/Hammock Browser.app/Contents/Resources/Chromium Credits.html")
        return FileManager.default.fileExists(atPath: url.path) ? url : nil
    }

    var body: some View {
        HStack(spacing: 0) {
            List(selection: $selection) {
                Section {
                    ForEach(items) { item in
                        row(item.name, item.licenseName, detail: item.detail).tag(Row.component(item.name))
                        if item.credits == "chromium", credits != nil {
                            row("Chromium's credits", "Chromium and what it includes", detail: nil).tag(Row.chromiumCredits)
                        }
                    }
                } header: {
                    Text("Hammock is built with:")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .textCase(nil)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .listStyle(.inset)
            .frame(width: 280)
            Divider()
            Group {
                switch selection {
                case .component(let name):
                    if let item = items.first(where: { $0.name == name }) {
                        AcknowledgementText(item)
                    }
                case .chromiumCredits:
                    if let credits { CreditsPage(url: credits) }
                case nil:
                    Text(items.isEmpty ? "This build has no acknowledgements file." : "Choose one.")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    private func row(_ name: String, _ license: String, detail: String?) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(name).lineLimit(1)
            Text([license, detail].compactMap { $0 }.joined(separator: " · "))
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(.vertical, 2)
    }
}

/// Chromium's credits page, a local HTML file of its build's.
private struct CreditsPage: NSViewRepresentable {
    let url: URL

    func makeNSView(context: Context) -> WKWebView {
        let web = WKWebView()
        web.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent())
        return web
    }

    func updateNSView(_ web: WKWebView, context: Context) {
        if web.url != url { web.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent()) }
    }
}
