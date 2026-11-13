import AppKit
import HammockKit
import SwiftUI

/// Where notifications show, set in Settings ▸ Notifications (NotifyPrefs.style).
enum NotifyStyle: String, CaseIterable {
    /// Notification Center banners only.
    case system
    /// A card in the bottom-right corner of the Hammock window, never Notification Center.
    case inApp = "in-app"
    /// The card while Hammock is in front, Notification Center while it isn't. The default.
    case auto

    @MainActor static var current: NotifyStyle { NotifyStyle(rawValue: Prefs.shared.string(NotifyPrefs.style)) ?? .auto }

    /// Whether this style ever posts to Notification Center (and so needs macOS's permission).
    var usesSystem: Bool { self != .inApp }

    /// Whether a notification sent now shows in the window.
    @MainActor var showsInAppNow: Bool {
        switch self {
        case .system: false
        case .inApp: true
        // Hammock in front but its window closed or minimised: nobody would see a card.
        case .auto: InAppNotices.windowShowing
        }
    }
}

/// A notification shown in the Hammock window rather than Notification Center.
struct InAppNotice: Identifiable, Equatable {
    /// What leads the card: in Hammock's own window the app icon says nothing,
    /// so an agent's status (as its tab shows it) or what the note is about.
    enum Icon: Equatable {
        case agent(AgentState)
        case symbol(String)
    }

    let id: String
    var icon: Icon
    var title: String
    var subtitle: String
    var body: String
    /// About something that can still need you (an agent, a request): it
    /// goes once that's dealt with (Notifier.withdraw).
    var live = false
    /// What a click does (reveal the pane, open Needs You…); the card goes either way.
    var open: (@MainActor () -> Void)?

    static func == (a: InAppNotice, b: InAppNotice) -> Bool {
        a.id == b.id && a.icon == b.icon && a.title == b.title && a.subtitle == b.subtitle && a.body == b.body
    }
}

/// The cards in the window's bottom-right corner. Each stays until it has
/// been on screen, with Hammock in front, for a few seconds (so one that came in
/// while you were away, or with the window closed, waits for you), or until
/// clicked or closed. Switching away or hiding the window starts the wait over.
@MainActor @Observable
final class InAppNotices {
    static let shared = InAppNotices()

    private(set) var notices: [InAppNotice] = []
    @ObservationIgnored private var timers: [String: Task<Void, Never>] = [:]
    @ObservationIgnored private var hovered: String?
    /// About as long as a macOS banner stays.
    private static let shown: Duration = .seconds(5)
    private static let limit = 4

    /// Hammock is in front with its workspace window showing: where the cards can be seen.
    static var windowShowing: Bool {
        NSApp.isActive && WorkspaceWindow.window.map { $0.isVisible && !$0.isMiniaturized } == true
    }

    private init() {
        let nc = NotificationCenter.default
        for name in [NSApplication.didBecomeActiveNotification, NSWindow.didBecomeKeyNotification, NSWindow.didDeminiaturizeNotification] {
            nc.addObserver(forName: name, object: nil, queue: .main) { _ in
                MainActor.assumeIsolated { InAppNotices.shared.startTimers() }
            }
        }
        for name in [NSApplication.didResignActiveNotification, NSWindow.didMiniaturizeNotification, NSWindow.willCloseNotification] {
            nc.addObserver(forName: name, object: nil, queue: .main) { note in
                let window = note.object as? NSWindow
                MainActor.assumeIsolated {
                    // Another window (Settings, a panel) closing doesn't hide the cards.
                    if let window, window !== WorkspaceWindow.window { return }
                    InAppNotices.shared.stopTimers()
                }
            }
        }
    }

    func post(_ notice: InAppNotice) {
        if let i = notices.firstIndex(where: { $0.id == notice.id }) {
            notices[i] = notice
        } else {
            notices.append(notice)
            // The oldest goes when there are too many, but not the one under the pointer.
            while notices.count > Self.limit, let old = notices.first(where: { $0.id != hovered }) { dismiss(old.id) }
        }
        timers.removeValue(forKey: notice.id)?.cancel()
        startTimers()
    }

    func dismiss(_ id: String) {
        timers.removeValue(forKey: id)?.cancel()
        // Its view goes before the pointer can leave it.
        if hovered == id { hovered = nil }
        notices.removeAll { $0.id == id }
    }

    func dismissAll(where stale: (InAppNotice) -> Bool) {
        for n in notices where stale(n) { dismiss(n.id) }
    }

    func open(_ notice: InAppNotice) {
        dismiss(notice.id)
        notice.open?()
    }

    /// The pointer over a card holds it (and only it) on screen.
    func hover(_ id: String, _ inside: Bool) {
        if inside {
            hovered = id
            timers.removeValue(forKey: id)?.cancel()
        } else if hovered == id {
            hovered = nil
            startTimers()
        }
    }

    private func stopTimers() {
        timers.values.forEach { $0.cancel() }
        timers.removeAll()
    }

    private func startTimers() {
        guard Self.windowShowing else { return }
        for n in notices where timers[n.id] == nil && n.id != hovered {
            timers[n.id] = Task { [weak self] in
                try? await Task.sleep(for: Self.shown)
                guard !Task.isCancelled else { return }
                self?.dismiss(n.id)
            }
        }
    }
}

/// The window's notification cards, newest at the bottom.
struct InAppNoticeStack: View {
    private var notices: InAppNotices { .shared }

    var body: some View {
        VStack(alignment: .trailing, spacing: 2) {
            ForEach(notices.notices) { n in
                InAppNoticeCard(notice: n)
                    .transition(.move(edge: .trailing).combined(with: .opacity))
            }
        }
        .animation(.snappy(duration: 0.25), value: notices.notices)
        .padding([.bottom, .trailing], 16)
    }
}

private struct InAppNoticeCard: View {
    let notice: InAppNotice
    @State private var hovering = false
    @Environment(\.colorScheme) private var scheme
    /// The theme's toolbar colour over the material,
    /// so the card looks like part of the window.
    private var tint: Color? { AppConfig.shared.toolbarTint(dark: scheme == .dark).map { Color(nsColor: $0).opacity(0.9) } }
    private static let shape = RoundedRectangle(cornerRadius: 14, style: .continuous)

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Group {
                switch notice.icon {
                case .agent(let state): StatusIcon(state: state, size: 18)
                case .symbol(let name): Image(systemName: name).font(.system(size: 15)).foregroundStyle(.secondary)
                }
            }
            .frame(width: 20, height: 20)
            .padding(.top, 1)
            VStack(alignment: .leading, spacing: 1) {
                Text(notice.title).font(.callout.weight(.semibold)).lineLimit(1)
                if !notice.subtitle.isEmpty {
                    Text(notice.subtitle).font(.callout).lineLimit(1)
                }
                if !notice.body.isEmpty {
                    Text(notice.body).font(.callout).foregroundStyle(.secondary).lineLimit(3)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .frame(width: 320, alignment: .leading)
        .background {
            Self.shape.fill(.regularMaterial)
                .overlay { tint.map { Self.shape.fill($0) } }
        }
        .overlay(Self.shape.strokeBorder(.separator.opacity(0.5)))
        .shadow(color: .black.opacity(0.15), radius: 8, y: 3)
        .contentShape(Self.shape)
        .onTapGesture { InAppNotices.shared.open(notice) }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { InAppNotices.shared.open(notice) }
        .accessibilityAction(named: "Close") { InAppNotices.shared.dismiss(notice.id) }
        // Room for the close button at the corner, inside what tracks the pointer,
        // so moving onto it doesn't hide it.
        .padding([.top, .leading], 6)
        .overlay(alignment: .topLeading) {
            if hovering {
                Button { InAppNotices.shared.dismiss(notice.id) } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 8, weight: .bold))
                        .frame(width: 18, height: 18)
                        .background {
                            Circle().fill(.regularMaterial)
                                .overlay { tint.map { Circle().fill($0) } }
                        }
                        .overlay(Circle().strokeBorder(.separator.opacity(0.5)))
                }
                .buttonStyle(.plain)
                .help("Close")
                .accessibilityHidden(true) // the card's Close action
            }
        }
        .onHover { inside in
            hovering = inside
            InAppNotices.shared.hover(notice.id, inside)
        }
    }
}
