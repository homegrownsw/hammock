import AppKit
import SwiftUI

/// Full Disk Access for Hammock, which programs in panes need as they do in
/// any terminal. macOS records privacy decisions about a pane's programs
/// against Hammock (the app enclosing the Hammock Pane helper), and some of
/// them it refuses without asking: 1Password's CLI couldn't reach the desktop
/// app, because reading another app's data was refused and no dialog ever
/// showed. Full Disk Access covers those, so onboarding and Settings ▸ General
/// suggest it (never require it: it reaches every program in every pane).
@MainActor
enum FullDiskAccess {
    /// Files only an app with Full Disk Access can open. TCC's own database
    /// is on every Mac; the others in case it ever moves.
    private static let probes = [
        "Library/Application Support/com.apple.TCC/TCC.db",
        "Library/Safari/Bookmarks.plist",
        "Library/Safari/CloudTabs.db",
    ]

    /// Whether this app has Full Disk Access: it can open a protected file.
    /// `HMK_DEBUG_DISK_ACCESS=0` or `1` pretends, for screenshots.
    static var isGranted: Bool {
        if let d = ProcessInfo.processInfo.environment["HMK_DEBUG_DISK_ACCESS"] { return d == "1" }
        let home = FileManager.default.homeDirectoryForCurrentUser
        for probe in probes {
            let path = home.appendingPathComponent(probe).path
            guard FileManager.default.fileExists(atPath: path) else { continue }
            let fd = open(path, O_RDONLY)
            if fd >= 0 { close(fd); return true }
            return false
        }
        return false
    }

    /// System Settings ▸ Privacy & Security ▸ Full Disk Access.
    static func openSettings() {
        NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_AllFiles")!)
    }

    /// Opens the Full Disk Access list, with the drag panel beside it unless
    /// Hammock has access already (to turn it off, say).
    static func openSettingsWithGuide() {
        openSettings()
        if !isGranted { FullDiskAccessGuide.shared.show() }
    }
}

// MARK: - Drag panel

/// A small floating panel beside System Settings holding Hammock's icon, to
/// drag into the Full Disk Access list. It stays above other windows while
/// Hammock isn't active. Once Hammock is dropped in (or access is granted)
/// it says to choose Quit & Reopen, which macOS asks for next: onboarding
/// opens on its Full Disk Access step again after that.
@MainActor
@Observable
final class FullDiskAccessGuide {
    static let shared = FullDiskAccessGuide()
    /// Hammock was dropped on System Settings, or access was granted.
    var dropped = false
    @ObservationIgnored private var panel: NSPanel?
    @ObservationIgnored private var watch: Task<Void, Never>?
    /// Where the panel was last put, to leave it alone once it's been moved.
    @ObservationIgnored private var placed: NSPoint?

    func show() {
        dropped = false
        if panel == nil {
            let p = NSPanel(contentRect: NSRect(x: 0, y: 0, width: 320, height: 112),
                            styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: false)
            p.isFloatingPanel = true
            p.level = .floating
            p.hidesOnDeactivate = false
            p.isMovableByWindowBackground = true
            p.backgroundColor = .clear
            p.isOpaque = false
            p.hasShadow = true
            p.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
            let host = NSHostingView(rootView: FullDiskAccessGuideView(guide: self))
            host.frame = p.contentLayoutRect
            p.contentView = host
            panel = p
        }
        place(beside: nil)
        panel?.orderFrontRegardless()
        watch?.cancel()
        watch = Task { [weak self] in
            // Go beside System Settings once its window is up (for the first
            // 15 seconds, unless the panel has been moved), then wait for the
            // grant. It stays up to say Quit & Reopen, which macOS asks for
            // then (and which closes it), for 30 seconds in case macOS
            // doesn't; with no grant, until it's closed.
            var grantedAt: Date?
            var besideSettings = false
            let start = Date()
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(besideSettings ? 1000 : 300))
                guard let self, !Task.isCancelled else { return }
                if !besideSettings, Date().timeIntervalSince(start) < 15, !self.wasMoved, let frame = Self.systemSettingsFrame() {
                    self.place(beside: frame)
                    besideSettings = true
                }
                if grantedAt == nil, FullDiskAccess.isGranted {
                    grantedAt = Date()
                    withAnimation(.smooth) { self.dropped = true }
                }
                if let g = grantedAt, Date().timeIntervalSince(g) >= 30 { break }
            }
            self?.close()
        }
    }

    func close() {
        watch?.cancel()
        watch = nil
        panel?.orderOut(nil)
        panel = nil
        placed = nil
    }

    /// Whether a drop at `point` (screen coordinates) landed on System Settings.
    func isOnSystemSettings(_ point: NSPoint) -> Bool {
        Self.systemSettingsFrame()?.contains(point) ?? false
    }

    private var wasMoved: Bool {
        guard let panel, let placed else { return false }
        return panel.frame.origin != placed
    }

    /// Beside System Settings' window (to its right if there's room on its
    /// screen, else its left, near its top), or in the corner of the screen
    /// until it's open.
    private func place(beside settings: NSRect?) {
        guard let panel else { return }
        let onScreen = settings.flatMap { s in NSScreen.screens.max { $0.frame.intersection(s).area < $1.frame.intersection(s).area } }
        guard let screen = (onScreen ?? NSScreen.main)?.visibleFrame else { return }
        let size = panel.frame.size
        var origin: NSPoint
        if let s = settings {
            let right = s.maxX + 16, left = s.minX - 16 - size.width
            origin = NSPoint(x: right + size.width <= screen.maxX ? right : max(screen.minX, left),
                             y: max(screen.minY, min(s.maxY - size.height - 60, screen.maxY - size.height)))
        } else {
            origin = NSPoint(x: screen.maxX - size.width - 24, y: screen.maxY - size.height - 24)
        }
        panel.setFrameOrigin(origin)
        placed = origin
    }

    /// System Settings' frontmost window, in AppKit's coordinates. Window
    /// bounds and owners are readable without Screen Recording (titles
    /// aren't); its owner is found by process, as its name is localised.
    private static func systemSettingsFrame() -> NSRect? {
        let pids = Set(NSRunningApplication.runningApplications(withBundleIdentifier: "com.apple.systempreferences").map(\.processIdentifier))
        guard !pids.isEmpty,
              let list = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]],
              let mainHeight = NSScreen.screens.first?.frame.height else { return nil }
        for w in list where (w[kCGWindowOwnerPID as String] as? pid_t).map(pids.contains) == true && (w[kCGWindowLayer as String] as? Int) == 0 {
            guard let b = w[kCGWindowBounds as String] as? [String: CGFloat], let x = b["X"], let y = b["Y"],
                  let width = b["Width"], let height = b["Height"], width > 200 else { continue }
            return NSRect(x: x, y: mainHeight - y - height, width: width, height: height)
        }
        return nil
    }
}

private struct FullDiskAccessGuideView: View {
    let guide: FullDiskAccessGuide

    var body: some View {
        HStack(spacing: 14) {
            AppIconDragSource(size: 56) { point in
                if guide.isOnSystemSettings(point) { withAnimation(.smooth) { guide.dropped = true } }
            }
            .frame(width: 56, height: 56)
            VStack(alignment: .leading, spacing: 3) {
                Text(guide.dropped ? "Now choose Quit & Reopen" : "Drag Hammock into the list")
                    .font(.system(size: 13, weight: .semibold))
                Text(guide.dropped ? (Onboarding.shared.isShowing ? "When macOS asks. Setup carries on where you left off." : "When macOS asks.") : "Then choose Quit & Reopen when macOS asks.")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .contentTransition(.opacity)
            Spacer(minLength: 0)
        }
        .padding(.leading, 16).padding(.trailing, 28).padding(.vertical, 14)
        .frame(width: 320, height: 112)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Color.primary.opacity(0.1)))
        .overlay(alignment: .topTrailing) {
            Button {
                guide.close()
            } label: {
                Image(systemName: "xmark").font(.system(size: 9, weight: .bold)).foregroundStyle(.secondary)
                    .frame(width: 18, height: 18)
                    .background(Color.primary.opacity(0.08), in: Circle())
            }
            .buttonStyle(.plain)
            .help("Close")
            .padding(8)
        }
    }
}

// MARK: - Proxy icon

/// Hammock's icon, which drags as the app itself (its file URL), as a
/// document's proxy icon does: drop it into a System Settings privacy list.
struct AppIconDragSource: NSViewRepresentable {
    var size: CGFloat = 64
    /// Called with where it was dropped (screen coordinates), when what it
    /// was dropped on took it.
    var onDrop: (NSPoint) -> Void = { _ in }

    func makeNSView(context: Context) -> DragView {
        let v = DragView()
        v.size = size
        v.onDrop = onDrop
        return v
    }

    func updateNSView(_ v: DragView, context: Context) {
        v.size = size
        v.onDrop = onDrop
    }

    final class DragView: NSView, NSDraggingSource {
        var size: CGFloat = 64 { didSet { invalidateIntrinsicContentSize(); needsDisplay = true } }
        var onDrop: (NSPoint) -> Void = { _ in }
        private var down: NSPoint?

        override var intrinsicContentSize: NSSize { NSSize(width: size, height: size) }
        override var mouseDownCanMoveWindow: Bool { false }
        override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

        override func draw(_ dirtyRect: NSRect) {
            NSApp.applicationIconImage.draw(in: iconRect)
        }

        private var iconRect: NSRect {
            NSRect(x: (bounds.width - size) / 2, y: (bounds.height - size) / 2, width: size, height: size)
        }

        override func resetCursorRects() { addCursorRect(bounds, cursor: .openHand) }

        override func mouseDown(with event: NSEvent) { down = event.locationInWindow }

        override func mouseDragged(with event: NSEvent) {
            guard let start = down else { return }
            let p = event.locationInWindow
            guard hypot(p.x - start.x, p.y - start.y) > 3 else { return }
            down = nil
            let item = NSDraggingItem(pasteboardWriter: Bundle.main.bundleURL as NSURL)
            item.setDraggingFrame(iconRect, contents: NSApp.applicationIconImage)
            beginDraggingSession(with: [item], event: event, source: self)
        }

        override func mouseUp(with event: NSEvent) { down = nil }

        func draggingSession(_ session: NSDraggingSession, sourceOperationMaskFor context: NSDraggingContext) -> NSDragOperation {
            context == .outsideApplication ? [.copy, .link, .generic] : []
        }

        func draggingSession(_ session: NSDraggingSession, endedAt screenPoint: NSPoint, operation: NSDragOperation) {
            if !operation.isEmpty { onDrop(screenPoint) }
        }
    }
}

private extension NSRect {
    var area: CGFloat { isNull ? 0 : width * height }
}
