import AppKit
import HammockKit
import SwiftUI

// Floating panes: a tab's panes floating over its splits as cards (the
// daemon's Tab.floating, back to front; frames in fractions of the tab's
// area), and the shelf of minimised ones along the bottom. A float moves by
// its header; dragged with ⌥ held, it docks into the split under the pointer,
// on the side nearest it. A float stays within its zoomed size (the tab, less
// a margin). Floating a pane grows its card out of the pane's place in the
// splits; docking one moves the card into its new place first (FloatMotion).

/// A tab's area, the float layer and its shelf: where cards and chips meet.
let tabSpace = NamedCoordinateSpace.named("hammock.tab")

/// The smallest a float is drawn, in points.
private let minFloat = CGSize(width: 260, height: 140)
/// A zoomed float's margin inside the tab.
private let zoomInset: CGFloat = 14
private let cardRadius: CGFloat = 10

/// A float being moved by its header, and where it would dock.
@MainActor @Observable
final class FloatDrag {
    static let shared = FloatDrag()
    private(set) var moving: String?
    private(set) var translation: CGSize = .zero
    /// The split pane it would dock beside, and on which side of it.
    private(set) var dock: (paneID: String, edge: Edge)?
    /// Each tab's pane area, in points (the layer measures it).
    var areas: [String: CGSize] = [:]

    func drag(_ paneID: String, _ g: DragGesture.Value, model: AppModel) {
        moving = paneID
        translation = g.translation
        guard NSEvent.modifierFlags.contains(.option), let tab = model.panes[paneID].flatMap({ model.tabs[$0.tabID] }) else {
            dock = nil
            return
        }
        // The split pane under the pointer (not the floats over it), and its nearest side.
        let splits = Set(AppModel.paneIDs(tab.layout))
        let p = g.location
        guard let (id, r) = PaneSwap.shared.frames.first(where: { splits.contains($0.key) && $0.value.contains(p) }) else {
            dock = nil
            return
        }
        let dx = (p.x - r.minX) / max(r.width, 1), dy = (p.y - r.minY) / max(r.height, 1)
        let sides: [(Edge, CGFloat)] = [(.leading, dx), (.trailing, 1 - dx), (.top, dy), (.bottom, 1 - dy)]
        dock = (id, sides.min { $0.1 < $1.1 }!.0)
    }

    func end(model: AppModel) {
        defer { moving = nil; translation = .zero; dock = nil }
        guard let id = moving else { return }
        if let (target, edge) = dock {
            FloatMotion.shared.dock(model, id, beside: target, edge: edge)
            return
        }
        guard let tab = model.panes[id].flatMap({ model.tabs[$0.tabID] }), let area = areas[tab.id],
              let f = model.floating(id) else { return }
        let r = FloatGeometry.rect(f, in: area).offsetBy(dx: translation.width, dy: translation.height)
        model.setFloatFrame(id, FloatGeometry.frame(FloatGeometry.clamp(r, in: area), in: area))
    }

    /// Where a dock would put the pane: the half of the target on that side.
    func dockRect() -> CGRect? {
        guard let (id, edge) = dock, let r = PaneSwap.shared.frames[id] else { return nil }
        return FloatGeometry.half(r, edge)
    }
}

/// Floating and docking a pane, animated: a pane floated grows its card out
/// of its place in the splits (the splits closing up around it as it goes);
/// one docked has its card moved into the place it will take, then docks.
@MainActor @Observable
final class FloatMotion {
    static let shared = FloatMotion()
    /// Where a pane floating now was in the splits: its card starts there.
    @ObservationIgnored private var from: [String: CGRect] = [:]
    /// Cards on their way into the splits, and where to.
    private(set) var docking: [String: CGRect] = [:]
    /// The splits animate their change while a pane floats.
    private(set) var animatesLayout = false
    static let duration = 0.26

    /// Floats a split pane, or docks a floating one.
    func toggle(_ model: AppModel, _ id: String) {
        if model.floating(id) != nil { dock(model, id) } else { float(model, id) }
    }

    func float(_ model: AppModel, _ id: String) {
        guard model.canFloat(id) else { return }
        guard PaneMotion.enabled else { return model.arrange(id, .float) }
        from[id] = PaneSwap.shared.frames[id]
        animatesLayout = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { self.animatesLayout = false }
        // The splits close up around it: their terminals hold still meanwhile.
        PaneMotion.shared.hold(for: Self.duration + 0.15)
        model.arrange(id, .float)
    }

    /// Cards on their way to the shelf, and the chip each shrinks into (in
    /// the tab's space, tabSpace, which the float layer shares).
    private(set) var minimising: [String: CGRect] = [:]
    /// Cards coming back off the shelf: the chip each grows out of.
    @ObservationIgnored private var restoring: [String: CGRect] = [:]
    /// The shelf's chips, where they are (tabSpace).
    @ObservationIgnored var chips: [String: CGRect] = [:]

    /// Minimises a float: it shrinks into its chip on the shelf, then goes
    /// there. Focus moves off it first, so its view (which keeps what it
    /// last knew as it animates out) doesn't take the keyboard back.
    func minimise(_ model: AppModel, _ id: String) {
        guard let tab = model.panes[id].flatMap({ model.tabs[$0.tabID] }) else { return }
        model.focusAway(from: [id])
        shrink(model, tab, [id]) { model.arrange(id, .minimise) }
    }

    /// Minimises every float showing in a tab, each into its chip, or brings
    /// them back out of theirs (⌘J).
    func toggleAll(_ model: AppModel, _ tab: WorkspaceTab) {
        let shown = tab.floating.filter { !$0.minimised }.map(\.paneID)
        if shown.isEmpty {
            for f in tab.floating where f.minimisedByAll { restoring[f.paneID] = chips[f.paneID] }
            withAnimation(PaneMotion.when(.snappy(duration: 0.2))) { model.toggleFloats(tab.id) }
            return
        }
        model.focusAway(from: Set(shown))
        shrink(model, tab, shown) { model.toggleFloats(tab.id) }
    }

    /// Brings a float back off the shelf, growing out of its chip.
    func restore(_ model: AppModel, _ id: String) {
        if PaneMotion.enabled { restoring[id] = chips[id] }
        model.arrange(id, .restore)
    }

    /// The chip a restored card grows out of, taken once.
    func takeRestore(_ id: String) -> CGRect? { restoring.removeValue(forKey: id) }

    /// Shrinks ids' cards into where their chips will be, then runs commit.
    private func shrink(_ model: AppModel, _ tab: WorkspaceTab, _ ids: [String], commit: @escaping () -> Void) {
        guard PaneMotion.enabled, let area = FloatDrag.shared.areas[tab.id] else {
            DispatchQueue.main.async { commit() }
            return
        }
        // New chips go after those there; the shelf sits along the tab's
        // bottom, 32 high, with its chips 24 high in it.
        let onShelf = tab.floating.filter(\.minimised).compactMap { chips[$0.paneID] }
        let tabHeight = area.height + (onShelf.isEmpty ? 0 : 32)
        var x = (onShelf.map(\.maxX).max() ?? 2) + 6
        var targets: [String: CGRect] = [:]
        for id in ids {
            targets[id] = CGRect(x: x, y: tabHeight - 28, width: 150, height: 24)
            x += 156
        }
        DispatchQueue.main.async {
            withAnimation(.snappy(duration: Self.duration)) { self.minimising.merge(targets) { $1 } }
            DispatchQueue.main.asyncAfter(deadline: .now() + Self.duration) {
                commit()
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) { for id in ids { self.minimising[id] = nil } }
            }
        }
    }

    /// The card's start, taken once.
    func takeStart(_ id: String) -> CGRect? { from.removeValue(forKey: id) }

    /// Docks a float beside target (on edge), or with none where it came
    /// from, else right of the tab's last focused split pane.
    func dock(_ model: AppModel, _ id: String, beside target: String? = nil, edge: Edge? = nil) {
        guard let f = model.floating(id), let tab = model.panes[id].flatMap({ model.tabs[$0.tabID] }) else { return }
        let splits = AppModel.paneIDs(tab.layout)
        var target = target, edge = edge
        if target == nil {
            if splits.contains(f.originPaneID) {
                target = f.originPaneID
                let v = f.originDirection == .vertical
                edge = f.originBefore ? (v ? .top : .leading) : (v ? .bottom : .trailing)
            } else {
                target = splits.contains(tab.focusedPaneID) ? tab.focusedPaneID : splits.first
                edge = .trailing
            }
        }
        guard let target, let edge else { return }
        let commit = {
            model.dock(id, beside: target, direction: edge == .top || edge == .bottom ? .vertical : .horizontal,
                       before: edge == .leading || edge == .top)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { self.docking[id] = nil }
        }
        guard PaneMotion.enabled, let r = PaneSwap.shared.frames[target] else { commit(); return }
        withAnimation(.snappy(duration: Self.duration)) { docking[id] = FloatGeometry.half(r, edge) }
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.duration) { commit() }
    }
}

/// Converting between a float's frame (fractions of its tab) and points.
enum FloatGeometry {
    /// Where floats may go: the tab less a margin, which is a zoomed float's size.
    static func bounds(_ area: CGSize) -> CGRect {
        CGRect(origin: .zero, size: area).insetBy(dx: min(zoomInset, area.width / 4), dy: min(zoomInset, area.height / 4))
    }

    static func rect(_ f: FloatingPane, in area: CGSize) -> CGRect {
        if f.zoomed { return bounds(area) }
        let r = CGRect(x: CGFloat(f.frame.x) * area.width, y: CGFloat(f.frame.y) * area.height,
                       width: CGFloat(f.frame.w) * area.width, height: CGFloat(f.frame.h) * area.height)
        return clamp(r, in: area)
    }

    /// At least minFloat, at most the zoomed size, and inside it.
    static func clamp(_ r: CGRect, in area: CGSize) -> CGRect {
        let b = bounds(area)
        let w = min(max(r.width, minFloat.width), b.width), h = min(max(r.height, minFloat.height), b.height)
        return CGRect(x: min(max(r.minX, b.minX), b.maxX - w), y: min(max(r.minY, b.minY), b.maxY - h), width: w, height: h)
    }

    /// The half of r on edge's side (where a pane docked there goes).
    static func half(_ r: CGRect, _ edge: Edge) -> CGRect {
        switch edge {
        case .leading: CGRect(x: r.minX, y: r.minY, width: r.width / 2, height: r.height)
        case .trailing: CGRect(x: r.midX, y: r.minY, width: r.width / 2, height: r.height)
        case .top: CGRect(x: r.minX, y: r.minY, width: r.width, height: r.height / 2)
        case .bottom: CGRect(x: r.minX, y: r.midY, width: r.width, height: r.height / 2)
        }
    }

    static func frame(_ r: CGRect, in area: CGSize) -> Hammock_V1_Frame {
        .with {
            $0.x = Float(r.minX / max(area.width, 1)); $0.y = Float(r.minY / max(area.height, 1))
            $0.w = Float(r.width / max(area.width, 1)); $0.h = Float(r.height / max(area.height, 1))
        }
    }
}

/// A tab's floats, over its splits (in the same space, paneSpace).
struct FloatLayer: View {
    let model: AppModel
    let tab: WorkspaceTab
    let focusedPaneID: String
    private var drag: FloatDrag { .shared }

    var body: some View {
        GeometryReader { geo in
            let shown = tab.floating.filter { !$0.minimised }
            ZStack(alignment: .topLeading) {
                Color.clear.allowsHitTesting(false)
                if let r = drag.dockRect() {
                    DockZone().frame(width: r.width, height: r.height).offset(x: r.minX, y: r.minY)
                }
                // In a fixed order, stacked by zIndex: raising one (a click on
                // it) mustn't rebuild the cards, or the click is lost.
                ForEach(shown.sorted { $0.paneID < $1.paneID }, id: \.paneID) { f in
                    let z = shown.firstIndex { $0.paneID == f.paneID } ?? 0
                    FloatCard(model: model, float: f, area: geo.size, isFocused: f.paneID == focusedPaneID,
                              isFront: z == shown.count - 1)
                        .zIndex(Double(z))
                        .transition(CardTransition(id: f.paneID))
                }
            }
            .onChange(of: geo.size, initial: true) { _, s in drag.areas[tab.id] = s }
        }
        .animation(PaneMotion.when(.snappy(duration: 0.2)), value: shown.map(\.paneID))
    }

    private var shown: [FloatingPane] { tab.floating.filter { !$0.minimised } }
}

/// A card coming or going: it grows in, or shrinks away. Docked or
/// minimised, it goes at once: it's already where its split pane or its
/// chip appears. Decided as it goes, not as it came: a transition chosen in
/// the view's body is the one it was shown with.
private struct CardTransition: Transition {
    let id: String
    func body(content: Content, phase: TransitionPhase) -> some View {
        let motion = FloatMotion.shared
        let moving = motion.docking[id] != nil || motion.minimising[id] != nil
        let shown = phase.isIdentity || moving
        content
            .scaleEffect(shown ? 1 : 0.9, anchor: .bottom)
            .opacity(shown ? 1 : 0)
    }
}

/// Where a docking float would go.
private struct DockZone: View {
    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 6).fill(Color.themeAccent.opacity(0.14))
            RoundedRectangle(cornerRadius: 6).strokeBorder(Color.themeAccent, lineWidth: 2)
            Label("Dock Here", systemImage: "rectangle.split.2x1")
                .font(.callout.weight(.semibold))
                .padding(.horizontal, 12).padding(.vertical, 6)
                .background(.regularMaterial, in: Capsule())
        }
        .padding(3)
        .allowsHitTesting(false)
    }
}

/// One floating pane: the pane as in a split, as a card with a border and
/// a shadow, moved by its header and resized from its edges and corners.
private struct FloatCard: View {
    let model: AppModel
    let float: FloatingPane
    let area: CGSize
    let isFocused: Bool
    let isFront: Bool
    @State private var resizing: CGRect?
    /// Where it was in the splits, while it grows out of there.
    @State private var start: CGRect?
    /// The chip it's coming back out of.
    @State private var unshelving: CGRect?
    @Environment(\.colorScheme) private var colorScheme
    private var drag: FloatDrag { .shared }

    private var rect: CGRect {
        if let r = FloatMotion.shared.docking[float.paneID] { return r }
        if let start { return start }
        if let resizing { return resizing }
        let r = FloatGeometry.rect(float, in: area)
        guard drag.moving == float.paneID else { return r }
        return FloatGeometry.clamp(r.offsetBy(dx: drag.translation.width, dy: drag.translation.height), in: area)
    }

    var body: some View {
        let r = rect
        let shape = RoundedRectangle(cornerRadius: cardRadius, style: .continuous)
        PaneView(model: model, paneID: float.paneID, isFocused: isFocused)
            .frame(width: r.width, height: r.height)
            .clipShape(shape)
            // Dim inactive panes (Settings): a card you're not in, too.
            .overlay {
                shape.fill(Color.black.opacity(colorScheme == .dark ? 0.22 : 0.07))
                    .opacity(AppConfig.shared.dimInactivePanes && !isFocused ? 1 : 0)
                    .animation(PaneLook.fade, value: isFocused)
                    .allowsHitTesting(false)
            }
            .overlay {
                shape.strokeBorder(isFocused ? PaneLook.borderColor(dark: colorScheme == .dark).opacity(0.9) : Color.primary.opacity(colorScheme == .dark ? 0.28 : 0.18),
                                   lineWidth: isFocused ? 1.5 : 1)
                    .allowsHitTesting(false)
            }
            // Drawn behind the card (a shape's shadow, not the pane's: its
            // terminal and pages are their own layers).
            .background {
                shape.fill(Color(nsColor: .windowBackgroundColor))
                    .shadow(color: .black.opacity(colorScheme == .dark ? 0.55 : (isFront ? 0.28 : 0.18)),
                            radius: isFront ? 22 : 12, y: isFront ? 10 : 5)
            }
            .overlay { if !float.zoomed { ResizeHandles(rect: r, area: area, resizing: $resizing) { done in model.setFloatFrame(float.paneID, FloatGeometry.frame(done, in: area)) } } }
            .opacity(drag.moving == float.paneID && drag.dock != nil ? 0.55 : 1)
            // Into (or out of) its chip on the shelf: scaled, not resized, so
            // its terminal keeps its size.
            .modifier(Shrunk(card: r, into: FloatMotion.shared.minimising[float.paneID] ?? unshelving))
            .offset(x: r.minX, y: r.minY)
            .onAppear {
                if let chip = FloatMotion.shared.takeRestore(float.paneID) {
                    unshelving = chip
                    DispatchQueue.main.async { withAnimation(.snappy(duration: FloatMotion.duration)) { unshelving = nil } }
                }
                guard let from = FloatMotion.shared.takeStart(float.paneID) else { return }
                start = from
                DispatchQueue.main.async { withAnimation(.snappy(duration: FloatMotion.duration)) { start = nil } }
            }
    }
}

/// A card drawn shrunk into a chip's place (both in the layer's space), and
/// faded as it gets there.
private struct Shrunk: ViewModifier {
    let card: CGRect
    let into: CGRect?

    func body(content: Content) -> some View {
        let t = into ?? card
        content
            .scaleEffect(x: t.width / max(card.width, 1), y: t.height / max(card.height, 1), anchor: .topLeading)
            .offset(x: t.minX - card.minX, y: t.minY - card.minY)
            .opacity(into == nil ? 1 : 0.25)
    }
}

/// Invisible handles along a card's edges and corners that resize it.
private struct ResizeHandles: View {
    let rect: CGRect
    let area: CGSize
    @Binding var resizing: CGRect?
    let commit: (CGRect) -> Void
    @State private var start: CGRect?

    /// Which edges a handle moves: x -1 left, 1 right; y -1 top, 1 bottom.
    private struct Grip: Hashable { let x: Int; let y: Int }
    private let grips = [Grip(x: -1, y: 0), Grip(x: 1, y: 0), Grip(x: 0, y: -1), Grip(x: 0, y: 1),
                         Grip(x: -1, y: -1), Grip(x: 1, y: -1), Grip(x: -1, y: 1), Grip(x: 1, y: 1)]
    private let t: CGFloat = 6 // thickness

    var body: some View {
        ZStack(alignment: .topLeading) {
            Color.clear.allowsHitTesting(false)
            ForEach(grips, id: \.self) { g in handle(g) }
        }
        .frame(width: rect.width, height: rect.height)
    }

    private func handle(_ g: Grip) -> some View {
        let w = g.x == 0 ? rect.width - 2 * t * 2 : t * (g.y == 0 ? 1 : 2)
        let h = g.y == 0 ? rect.height - 2 * t * 2 : t * (g.x == 0 ? 1 : 2)
        let x = g.x == -1 ? -t / 2 : g.x == 1 ? rect.width - w + t / 2 : t * 2
        // The top edge leaves the header's own drag alone except at its very edge.
        let y = g.y == -1 ? -t / 2 : g.y == 1 ? rect.height - h + t / 2 : t * 2
        return Color.clear
            .frame(width: max(w, 1), height: max(h, 1))
            .contentShape(Rectangle())
            .offset(x: x, y: y)
            .onHover { inside in
                if inside { cursor(g).push() } else { NSCursor.pop() }
            }
            .gesture(
                DragGesture(minimumDistance: 1, coordinateSpace: paneSpace)
                    .onChanged { v in
                        let s = start ?? rect
                        start = s
                        var r = s
                        if g.x == -1 { r.origin.x = min(s.minX + v.translation.width, s.maxX - minFloat.width); r.size.width = s.maxX - r.minX }
                        if g.x == 1 { r.size.width = max(s.width + v.translation.width, minFloat.width) }
                        if g.y == -1 { r.origin.y = min(s.minY + v.translation.height, s.maxY - minFloat.height); r.size.height = s.maxY - r.minY }
                        if g.y == 1 { r.size.height = max(s.height + v.translation.height, minFloat.height) }
                        resizing = r.intersection(FloatGeometry.bounds(area))
                    }
                    .onEnded { _ in
                        if let r = resizing { commit(r) }
                        start = nil
                        // Kept until the model has the new frame (no snap back).
                        DispatchQueue.main.async { resizing = nil }
                    }
            )
    }

    private func cursor(_ g: Grip) -> NSCursor {
        switch (g.x, g.y) {
        case (_, 0): return .resizeLeftRight
        case (0, _): return .resizeUpDown
        case (-1, -1), (1, 1): return .frameResize(position: .topLeft, directions: .all)
        default: return .frameResize(position: .topRight, directions: .all)
        }
    }
}

/// The shelf along the bottom of a tab: its minimised floats, each a chip
/// with its kind or agent state; click one to bring it back.
struct FloatShelf: View {
    let model: AppModel
    let tab: WorkspaceTab

    var body: some View {
        let minimised = tab.floating.filter(\.minimised)
        if !minimised.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 6) {
                    ForEach(minimised, id: \.paneID) { f in
                        ShelfChip(model: model, paneID: f.paneID)
                            .onGeometryChange(for: CGRect.self) { $0.frame(in: tabSpace) } action: { FloatMotion.shared.chips[f.paneID] = $0 }
                    }
                }
                .padding(.horizontal, 8)
            }
            .frame(height: 32)
            .frame(maxWidth: .infinity)
            .background(Color(nsColor: .windowBackgroundColor))
            .overlay(alignment: .top) { Rectangle().fill(Color(nsColor: .separatorColor)).frame(height: 1) }
            .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }
}

private struct ShelfChip: View {
    let model: AppModel
    let paneID: String
    @State private var hover = false

    private var pane: Pane? { model.panes[paneID] }
    private var name: String {
        guard let p = pane else { return "Pane" }
        if p.kind == .browser || p.kind == .simulator { return p.title.isEmpty ? (p.url.isEmpty ? "Pane" : p.url) : p.title }
        return p.displayName
    }

    var body: some View {
        Button { FloatMotion.shared.restore(model, paneID) } label: {
            HStack(spacing: 6) {
                icon.frame(width: 14)
                Text(name).lineLimit(1).truncationMode(.middle)
                if let p = pane, p.hasAgent, p.agent.state == .needsInput {
                    Circle().fill(Color.orange).frame(width: 6, height: 6)
                }
            }
            .font(.callout)
            .padding(.horizontal, 10)
            .frame(height: 24)
            .frame(maxWidth: 220)
            .background(RoundedRectangle(cornerRadius: 7).fill(Color.primary.opacity(hover ? 0.10 : 0.06)))
            .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.primary.opacity(0.10)))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
        .tooltip("Restore \(name)")
        .contextMenu {
            Button("Restore") { FloatMotion.shared.restore(model, paneID) }
            Button("Dock Pane") { model.arrange(paneID, .dock) } // from the shelf: no card to move
            Divider()
            Button("Close Pane", role: .destructive) { CloseGuard.confirm(model, panes: [paneID]) { Task { await model.closePane(paneID) } } }
        }
    }

    @ViewBuilder private var icon: some View {
        switch pane?.kind {
        case .browser: Image(systemName: "globe")
        case .simulator: Image(systemName: "iphone")
        case .editor: Image(systemName: "doc.text")
        default:
            if let p = pane, p.hasAgent, p.agent.state != .unspecified {
                StatusIcon(state: p.agent.shownState, size: 12, asleep: p.asleep)
            } else {
                Image(systemName: "apple.terminal")
            }
        }
    }
}

/// A pane's header buttons for floating: float or dock (the same button in
/// every pane, highlighted on a floating one), and minimise on a float.
/// Shown on a float, and on a split pane that can float (not a tab's only
/// split pane, even with floats over it).
struct FloatButtons: View {
    let model: AppModel
    let paneID: String

    var body: some View {
        let floats = model.floating(paneID) != nil
        if floats {
            button("minus", Keys.hint("Minimise", "minimise_float"), on: false) {
                FloatMotion.shared.minimise(model, paneID)
            }
        }
        if floats || model.canFloat(paneID) {
            button("macwindow.on.rectangle", floats ? Keys.hint("Dock Pane", "float_or_dock") + "; or drag it with ⌥ held" : Keys.hint("Float Pane", "float_or_dock"), on: floats) {
                FloatMotion.shared.toggle(model, paneID)
            }
        }
    }

    private func button(_ symbol: String, _ help: String, on: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 11, weight: .medium))
                .frame(width: 22, height: 20)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .foregroundStyle(on ? Color.themeAccent : .secondary)
        .tooltip(help)
    }
}

/// Float or dock, and minimise, for a pane's menus.
struct FloatPaneItems: View {
    let model: AppModel
    let paneID: String

    var body: some View {
        if model.floating(paneID) != nil {
            Button("Dock Pane") { FloatMotion.shared.dock(model, paneID) }
            Button("Minimise Pane") { FloatMotion.shared.minimise(model, paneID) }
        } else {
            Button("Float Pane") { FloatMotion.shared.float(model, paneID) }
                .disabled(!model.canFloat(paneID))
        }
    }
}

/// The floating-pane commands in the menu bar's Pane menu, with their shortcuts.
struct FloatCommands: View {
    let model: AppModel
    let paneID: String?

    private var floats: Bool { paneID.flatMap { model.floating($0) } != nil }

    var body: some View {
        Button("New Floating Pane") { if let paneID { Task { await model.newFloating(over: paneID) } } }
            .keyboardShortcut(Keys.newFloatingPane)
            .disabled(paneID == nil)
        Button(floats ? "Dock Pane" : "Float Pane") { if let paneID { FloatMotion.shared.toggle(model, paneID) } }
            .keyboardShortcut(Keys.floatOrDock)
            .disabled(paneID.map { !floats && !model.canFloat($0) } ?? true)
        Button("Minimise Floating Pane") { if let paneID { FloatMotion.shared.minimise(model, paneID) } }
            .keyboardShortcut(Keys.minimiseFloat)
            .disabled(!floats)
        Button(allMinimised ? "Show Floating Panes" : "Hide Floating Panes") {
            if let tab = model.selectedTab { FloatMotion.shared.toggleAll(model, tab) }
        }
        .keyboardShortcut(Keys.toggleFloats)
        .disabled(model.selectedTab?.floating.isEmpty ?? true)
    }

    private var allMinimised: Bool { model.selectedTab.map { !$0.floating.isEmpty && $0.floating.allSatisfy(\.minimised) } ?? false }
}
