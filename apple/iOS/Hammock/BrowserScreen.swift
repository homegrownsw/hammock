import HammockKit
import SwiftUI
import UIKit

/// A browser pane on the phone: the Mac's page as live video (MediaStream),
/// at the size it has on the Mac, scaled to fit (pinch to zoom). A tap clicks, a drag
/// scrolls, and the keyboard types into the page.
struct BrowserScreen: View {
    let model: AppModel
    let paneID: String
    let active: Bool
    /// View only (a shared Group without Drive): shown, not driven.
    var canDrive = true

    @State private var stream: MediaStream?
    @State private var playing = false
    @State private var failed = false
    @State private var pixels = CGSize(width: 1600, height: 1000) // the video's size
    @State private var typing = false
    /// Changes as iPhone Duo folds and unfolds (its displays differ), when
    /// the video's restarted for the display it's on.
    @Environment(\.horizontalSizeClass) private var hSize

    private var pane: Pane? { model.panes[paneID] }

    var body: some View {
        VStack(spacing: 12) {
            GeometryReader { geo in
                ZStack {
                    if let stream, playing {
                        // All the space, so a pinch can zoom into it; the page sits fitted in the middle.
                        RemotePageView(stream: stream, pageSize: pixels) { op, args in if canDrive { model.browserInput(paneID, op, args) } }
                    } else if failed {
                        ContentUnavailableView("Can't See This Page", systemImage: "globe",
                                               description: Text("Its Mac isn't answering."))
                    } else {
                        ProgressView()
                    }
                    KeyCatcher(active: $typing, onText: text, onKey: key)
                        .frame(width: 1, height: 1)
                        .opacity(0)
                }
                .frame(width: geo.size.width, height: geo.size.height)
            }
        }
        .padding(.horizontal)
        // The system's bar, not one of our own: at the bottom of an iPhone,
        // down the side of iPhone Duo (where it leaves the page its height,
        // and keeps out of the fold).
        .toolbar {
            ToolbarItemGroup(placement: .bottomBar) {
                Button("Back", systemImage: "chevron.left") { model.navigate(paneID, "back") }
                    .disabled(!canDrive || pane?.canGoBack != true)
                Button("Forward", systemImage: "chevron.right") { model.navigate(paneID, "forward") }
                    .disabled(!canDrive || pane?.canGoForward != true)
                Button("Reload", systemImage: "arrow.clockwise") { model.navigate(paneID, "reload") }
                    .disabled(!canDrive)
                Button(typing ? "Hide Keyboard" : "Type", systemImage: typing ? "keyboard.chevron.compact.down" : "keyboard") { typing.toggle() }
                    .disabled(!canDrive)
                if let url = pane.flatMap({ URL(string: $0.url) }), url.scheme?.hasPrefix("http") == true {
                    Link(destination: url) { Label("Open in Safari", systemImage: "safari") }
                }
            }
        }
        .navigationTitle(pane.map { $0.title.isEmpty ? "Browser" : $0.title } ?? "Browser")
        .task(id: "\(active) \(String(describing: hSize))") {
            guard active else { return }
            await watchVideo()
        }
        .sheet(isPresented: Binding(get: { model.browserMenus[paneID] != nil }, set: { shown in
            if !shown, let m = model.browserMenus[paneID] { model.answerBrowserMenu(m, index: -1) }
        })) {
            if let m = model.browserMenus[paneID] { PageMenuSheet(menu: m) { model.answerBrowserMenu(m, index: $0) } }
        }
    }

    /// Video while this pane is showing; a stream that fails is replaced.
    private func watchVideo() async {
        // No bigger than the phone shows it across the screen it's on (not
        // UIScreen.main: iPhone Duo has two).
        let screen = UIApplication.shared.activeScreen?.nativeBounds.size ?? .zero
        let maxPixels = UInt32(screen.width * screen.height)
        while !Task.isCancelled {
            guard let s = model.videoStream(for: paneID, maxPixels: maxPixels) else {
                try? await Task.sleep(for: .seconds(1))
                continue
            }
            let states = AsyncStream<MediaStream.State> { cont in
                s.onState = { cont.yield($0) }
            }
            s.onSize = { px in pixels = px }
            stream = s
            s.start()
            for await st in states {
                if st == .playing {
                    playing = true
                    failed = false
                }
                if case .failed = st {
                    failed = !playing
                    break
                }
                if Task.isCancelled { break }
            }
            s.stop()
            playing = false
            stream = nil
            if Task.isCancelled { break }
            try? await Task.sleep(for: .seconds(2))
        }
        stream?.stop()
        stream = nil
        playing = false
    }

    private func text(_ s: String) {
        model.browserInput(paneID, "text", ["text": s])
    }

    /// Return and Delete, as the page expects them (forms submit on Enter).
    private func key(_ k: KeyCatcher.Key) {
        let (name, vk, text): (String, Int, String) = switch k {
        case .enter: ("Enter", 13, "\r")
        case .backspace: ("Backspace", 8, "")
        }
        var down: [String: Any] = ["type": "down", "key": name, "code": name, "vk": vk]
        if !text.isEmpty { down["text"] = text }
        model.browserInput(paneID, "key", down)
        model.browserInput(paneID, "key", ["type": "up", "key": name, "code": name, "vk": vk])
    }

}

extension UIApplication {
    /// The screen the app is showing on now, read when it's needed: iPhone
    /// Duo has two (and UIScreen.main is going away).
    var activeScreen: UIScreen? {
        connectedScenes.lazy.compactMap { $0 as? UIWindowScene }.first { $0.activationState == .foregroundActive }?.screen
    }
}

/// A view that takes the keyboard while active and passes on what's typed.
struct KeyCatcher: UIViewRepresentable {
    enum Key { case enter, backspace }

    @Binding var active: Bool
    var onText: (String) -> Void
    var onKey: (Key) -> Void

    func makeUIView(context: Context) -> CatcherView {
        let v = CatcherView()
        v.onText = onText
        v.onKey = onKey
        v.onResign = { active = false }
        return v
    }

    func updateUIView(_ v: CatcherView, context: Context) {
        v.onText = onText
        v.onKey = onKey
        if active, !v.isFirstResponder {
            DispatchQueue.main.async { _ = v.becomeFirstResponder() }
        } else if !active, v.isFirstResponder {
            DispatchQueue.main.async { _ = v.resignFirstResponder() }
        }
    }

    final class CatcherView: UIView, UIKeyInput {
        var onText: ((String) -> Void)?
        var onKey: ((Key) -> Void)?
        var onResign: (() -> Void)?

        override var canBecomeFirstResponder: Bool { true }
        var hasText: Bool { true } // so Delete always reaches the page
        var autocorrectionType: UITextAutocorrectionType = .no
        var autocapitalizationType: UITextAutocapitalizationType = .none
        var smartQuotesType: UITextSmartQuotesType = .no
        var smartDashesType: UITextSmartDashesType = .no

        func insertText(_ text: String) {
            if text == "\n" { onKey?(.enter) } else { onText?(text) }
        }

        func deleteBackward() { onKey?(.backspace) }

        override func resignFirstResponder() -> Bool {
            let r = super.resignFirstResponder()
            if r { onResign?() }
            return r
        }
    }
}

/// A menu the page opened (a <select>'s options or a right-click), as a
/// list to pick from.
struct PageMenuSheet: View {
    let menu: BrowserMenu
    let pick: (Int) -> Void

    var body: some View {
        NavigationStack {
            List {
                ForEach(Array(menu.items.enumerated()), id: \.offset) { i, item in
                    if !item.separator {
                        Button {
                            pick(i)
                        } label: {
                            HStack {
                                Text(item.title)
                                    .padding(.leading, CGFloat(item.indent) * 16)
                                Spacer()
                                if item.checked || (menu.kind == "select" && Int(menu.selected) == i) {
                                    Image(systemName: "checkmark").foregroundStyle(.tint)
                                }
                            }
                        }
                        .disabled(!item.enabled)
                    }
                }
            }
            .navigationTitle(menu.kind == "select" ? "Choose" : "Page")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { pick(-1) } }
            }
        }
        .presentationDetents([.medium, .large])
    }
}
