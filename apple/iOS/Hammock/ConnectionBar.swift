import HammockKit
import SwiftUI

/// What the connection bar shows, worked out once for the whole app
/// (`watchesLink`) and handed down to every screen that shows the bar.
struct LinkBarState {
    let model: AppModel
    let host: String
    let status: LinkStatus?
    /// The clock it was worked out at: "Last updated".
    let now: Date
}

extension EnvironmentValues {
    @Entry var linkBar: LinkBarState?
    /// The link's been down a moment (the connection bar shows): what's
    /// on screen may be out of date, and answering from it can't work.
    @Entry var linkDown = false
}

extension View {
    /// Works out the link's status (LinkStatus) for the screens under it,
    /// on a clock while the link's down, and says it to VoiceOver as it
    /// changes. Once, at the top (CockpitView).
    func watchesLink(_ model: AppModel, host: String) -> some View {
        modifier(LinkWatcher(model: model, host: host))
    }

    /// The connection bar under this screen's navigation bar, while the
    /// link's down: on each tab's list and every screen pushed from it.
    func connectionBar() -> some View {
        safeAreaInset(edge: .top, spacing: 0) { ConnectionBar() }
    }

    /// Live parts of a row (its state, how long ago) dimmed while the link's
    /// down: they may be out of date.
    func staleWhenLinkDown() -> some View { modifier(StaleWhenLinkDown()) }
}

private struct LinkWatcher: ViewModifier {
    let model: AppModel
    let host: String
    @State private var now = Date()

    func body(content: Content) -> some View {
        let status = LinkStatus.shown(connected: model.connected, everConnected: model.everConnected, downSince: model.downSince, now: now)
        content
            .environment(\.linkBar, LinkBarState(model: model, host: host, status: status, now: now))
            .environment(\.linkDown, status != nil)
            // A clock while it's down: when to show the bar, and "Last updated".
            .task(id: model.downSince) {
                now = Date()
                guard model.downSince != nil else { return }
                while !Task.isCancelled {
                    try? await Task.sleep(for: .seconds(1))
                    now = Date()
                }
            }
            .onChange(of: status) { old, new in
                if let words = LinkStatus.announcement(from: old, to: new, host: host) {
                    AccessibilityNotification.Announcement(words).post()
                }
            }
    }
}

/// The link to the computer while it isn't connected: a card saying it's
/// connecting, reconnecting, or can't reach it (why, and how old what's
/// shown is), with Try Again. Nothing while connected, or for a blip.
struct ConnectionBar: View {
    @Environment(\.linkBar) private var bar
    @Environment(\.phoneTheme) private var theme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(spacing: 0) {
            if let bar, let s = bar.status {
                card(s, bar)
                    .padding(.horizontal, 16)
                    .padding(.top, 2)
                    .padding(.bottom, 8)
                    .transition(reduceMotion ? .opacity : .move(edge: .top).combined(with: .opacity))
            }
        }
        .frame(maxWidth: .infinity)
        .clipped()
        .animation(reduceMotion ? .easeInOut(duration: 0.2) : .snappy(duration: 0.3), value: bar?.status)
    }

    private func card(_ s: LinkStatus, _ bar: LinkBarState) -> some View {
        HStack(spacing: 12) {
            Group {
                if s == .unreachable {
                    Image(systemName: "wifi.exclamationmark")
                        .font(.body.weight(.semibold))
                        .foregroundStyle(theme.accent)
                } else {
                    ProgressView().controlSize(.small).tint(theme.accent)
                }
            }
            .frame(width: 22)
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(s.title(host: bar.host))
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                if let detail = s.detail(reason: bar.model.linkError, lastLive: bar.model.lastLive, now: bar.now) {
                    Text(detail)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            if s == .unreachable {
                Button("Try Again") { bar.model.reconnectNow() }
                    .font(.subheadline.weight(.semibold))
                    .buttonStyle(.bordered)
                    .buttonBorderShape(.capsule)
                    .controlSize(.small)
                    .tint(theme.accent)
                    .accessibilityHint("Connects to \(bar.host) now")
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
    }
}

private struct StaleWhenLinkDown: ViewModifier {
    @Environment(\.linkDown) private var down
    func body(content: Content) -> some View {
        content
            .opacity(down ? 0.45 : 1)
            .animation(.easeInOut(duration: 0.3), value: down)
    }
}
