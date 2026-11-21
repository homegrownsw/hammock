import AppKit
import HammockKit
import SwiftUI

/// Hiding the sidebar (View ▸ Hide Sidebar, ⌃⌘S): kept in defaults, so the
/// menu, the palette and the window's own sidebar button agree.
enum SidebarHiding {
    static let key = "sidebarHidden"

    @MainActor static func toggle() {
        UserDefaults.standard.set(!UserDefaults.standard.bool(forKey: key), forKey: key)
    }
}

/// With the sidebar hidden, the pointer resting a moment at the window's
/// very left edge brings it in, floating over the panes (as in Arc and Dia):
/// the same sidebar, in a rounded panel, to use until the pointer leaves it
/// (or leaves the edge without going into it).
struct SidebarPeek: View {
    let model: AppModel
    // HMK_DEBUG_PEEK=1: open from the start, to look at it.
    @State private var shown = ProcessInfo.processInfo.environment["HMK_DEBUG_PEEK"] == "1"
    @State private var inside = false
    @State private var atEdge = false
    @State private var opening: Task<Void, Never>?
    @State private var menuOpen = false
    @State private var closing: Task<Void, Never>?
    @Environment(\.colorScheme) private var scheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private let width: CGFloat = 272

    var body: some View {
        ZStack(alignment: .leading) {
            // The panes dim a little behind it, to set it apart (still
            // usable: clicks go through).
            if shown {
                Color.black.opacity(scheme == .dark ? 0.22 : 0.08)
                    .allowsHitTesting(false)
                    .transition(.opacity)
            }
            // The edge that brings it in: a few points, the panes keep the rest.
            Color.clear
                .frame(width: 3)
                .frame(maxHeight: .infinity)
                .contentShape(Rectangle())
                .onHover { here in
                    atEdge = here
                    if here { openSoon() } else { opening?.cancel(); if shown { closeSoon() } }
                }
            if shown {
                SidebarView(model: model)
                    // Out of a split view, a sidebar list draws its own
                    // background, over the window theme's tint: the column
                    // supplies it, so here the panel does.
                    .scrollContentBackground(.hidden)
                    .background { SidebarTint() }
                    .background { SidebarGlass(blending: .withinWindow) }
                    .frame(width: width)
                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(scheme == .dark ? Color.white.opacity(0.16) : Color.black.opacity(0.16), lineWidth: 0.5))
                    // A tight shadow for its edge, a wide one to lift it.
                    .shadow(color: .black.opacity(scheme == .dark ? 0.4 : 0.14), radius: 2, x: 0, y: 1)
                    .shadow(color: .black.opacity(scheme == .dark ? 0.5 : 0.28), radius: 22, x: 4, y: 8)
                    .padding(.vertical, 8)
                    .padding(.leading, 8)
                    .onHover { inside = $0; if !$0 { closeSoon() } }
                    // Reduce Motion: a fade, not a slide.
                    .transition(reduceMotion ? .opacity : .move(edge: .leading).combined(with: .opacity))
            }
        }
        .animation(reduceMotion ? .easeInOut(duration: 0.18) : .snappy(duration: 0.22), value: shown)
        // A menu (right-click) or sheet from it keeps it: the pointer leaving
        // for the menu isn't leaving it.
        .onReceive(NotificationCenter.default.publisher(for: NSMenu.didBeginTrackingNotification)) { _ in menuOpen = true }
        .onReceive(NotificationCenter.default.publisher(for: NSMenu.didEndTrackingNotification)) { _ in
            menuOpen = false
            closeSoon()
        }
        .onDisappear { closing?.cancel(); opening?.cancel() }
    }

    /// Comes in once the pointer has rested at the edge a moment, not as it
    /// passes on its way somewhere else (the window's buttons, another app).
    private func openSoon() {
        closing?.cancel()
        guard !shown else { return }
        opening?.cancel()
        opening = Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(180))
            guard !Task.isCancelled, atEdge else { return }
            shown = true
        }
    }

    /// Goes a moment after the pointer leaves, unless it comes back, or a
    /// menu or sheet of its own is open (then once that's done).
    private func closeSoon() {
        closing?.cancel()
        closing = Task { @MainActor in
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(350))
                if Task.isCancelled || inside || atEdge { return }
                if !menuOpen, NSApp.keyWindow?.attachedSheet == nil, WorkspaceWindow.window?.attachedSheet == nil {
                    shown = false
                    return
                }
            }
        }
    }
}
