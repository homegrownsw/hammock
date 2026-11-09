import AppKit
import SwiftUI

/// The results list of the ⌘K palette and the session finder: moved through
/// with the arrow keys from the search field, picked by pointer or click.
///
/// The keyboard scrolls it only when the selected row isn't fully in view,
/// just far enough to show it with the list's margin above or below, and at
/// once: an animated scroll per key repeat stutters, each one cutting the
/// last short. A new query starts it from the top. Only a moving pointer
/// selects: not rows scrolling under a still one, nor the list opening
/// under it.
struct KeyboardList<Item, ID: Hashable, Row: View>: View {
    let items: [Item]
    let id: KeyPath<Item, ID>
    @Binding var selection: Int
    /// What's typed: a change scrolls back to the top (with selection reset
    /// to the first row by the caller).
    let query: String
    let maxHeight: CGFloat
    /// A click on row i.
    let choose: (Int) -> Void
    /// A row, and whether it's selected.
    @ViewBuilder let row: (Item, Bool) -> Row

    private static var margin: CGFloat { 6 }

    @State private var position = ScrollPosition()
    /// The part of the list's content in view, in its own coordinates.
    @State private var shown = CGRect.zero
    /// Where each row built so far sits in the list's content. Rows the
    /// LazyVStack hasn't built yet aren't here.
    @State private var frames: [ID: CGRect] = [:]
    /// The row under the pointer.
    @State private var hovered: ID?
    /// Where the pointer was on screen when the list opened, then where it
    /// last moved to: it selects only by moving from there. On screen, not in
    /// the list, whose own coordinates shift under a still pointer as the
    /// palette scales in or the rows scroll.
    @State private var pointer = NSEvent.mouseLocation
    /// The pointer made the last selection: the row's under it already, so
    /// the list mustn't scroll it into full view under the pointer.
    @State private var pointerSelected = false

    var body: some View {
        let ids = items.map { $0[keyPath: id] }
        ScrollView {
            LazyVStack(spacing: 2) {
                ForEach(Array(ids.enumerated()), id: \.element) { i, key in
                    row(items[i], i == selection)
                        .id(key)
                        .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(KeyboardListSpace.content)) } action: { frames[key] = $0 }
                        .onHover { inside in
                            if inside { hovered = key } else if hovered == key { hovered = nil }
                        }
                        .onTapGesture { choose(i) }
                }
            }
            .scrollTargetLayout()
            .padding(Self.margin)
            .coordinateSpace(.named(KeyboardListSpace.content))
        }
        .scrollPosition($position)
        .onScrollGeometryChange(for: CGRect.self) { $0.visibleRect } action: { _, r in shown = r }
        .frame(maxHeight: maxHeight)
        .onContinuousHover { phase in
            switch phase {
            case .active:
                let p = NSEvent.mouseLocation
                guard p != pointer else { return }
                pointer = p
                // After this event's rows have had their onHover.
                DispatchQueue.main.async {
                    guard let hovered, let i = items.firstIndex(where: { $0[keyPath: id] == hovered }), i != selection else { return }
                    pointerSelected = true
                    selection = i
                }
            case .ended:
                break
            }
        }
        .onChange(of: selection) { old, new in
            if pointerSelected { pointerSelected = false } else { reveal(ids, from: old) }
        }
        .onChange(of: query) { position.scrollTo(edge: .top) }
        // Built rows report where they move to; drop the ones gone.
        .onChange(of: ids) { let now = Set(ids); frames = frames.filter { now.contains($0.key) } }
    }

    private func reveal(_ ids: [ID], from old: Int) {
        guard selection < ids.count else { return }
        let key = ids[selection]
        guard let f = frames[key], shown.height > 0 else {
            // Not built yet (far off, after wrapping around): bring it in by
            // the edge it's coming from.
            position.scrollTo(id: key, anchor: selection > old ? .bottom : .top)
            return
        }
        if f.minY - Self.margin < shown.minY {
            position.scrollTo(y: max(0, f.minY - Self.margin))
        } else if f.maxY + Self.margin > shown.maxY {
            position.scrollTo(y: f.maxY + Self.margin - shown.height)
        }
    }
}

private enum KeyboardListSpace: Hashable { case content }
