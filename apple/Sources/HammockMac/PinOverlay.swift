import AppKit
import HammockKit
import SwiftUI

/// Design notes over a browser's page or a simulator's screen (DesignNotes.swift
/// in HammockKit has the rest): the pane's pins, always; in pin mode (Pane ▸
/// Pin Notes), a click drops one and opens its note, hovering a page
/// outlines the element under the pointer (as DevTools' inspector does), Esc
/// leaves, and a bar along the bottom sends them to an agent.
///
/// Out of pin mode only the pins take clicks: the page or device works
/// under the rest.
struct PinOverlay: View {
    let model: AppModel
    let paneID: String
    /// Where the page or screen is in this view (nil: all of it).
    var area: CGRect?
    /// A page's: hovering asks the daemon what's under the pointer.
    var inspects = false

    @State private var hover = PinHover()
    @State private var escape: Any?

    private var on: Bool { PinMode.shared.isOn(paneID) }
    private var pins: [Pin] { model.pins(paneID) }
    private var editing: Pin? {
        guard let id = PinMode.shared.editing else { return nil }
        return pins.first { $0.id == id }
    }

    var body: some View {
        GeometryReader { geo in
            let a = area ?? CGRect(origin: .zero, size: geo.size)
            ZStack(alignment: .topLeading) {
                if on {
                    catcher(a)
                    // Pin mode shows: the page or screen ringed in the theme's colour.
                    Rectangle()
                        .strokeBorder(.tint.opacity(0.7), lineWidth: 2)
                        .frame(width: a.width, height: a.height)
                        .position(x: a.midX, y: a.midY)
                        .allowsHitTesting(false)
                    if let h = hover.found, let r = h.rect {
                        highlight(r, h.what, in: a)
                    }
                }
                if let p = PinPointer.shared.at[paneID] {
                    // The director's pointer (a recording has no cursor).
                    Image(systemName: "cursorarrow")
                        .font(.system(size: 20))
                        .foregroundStyle(.black)
                        .shadow(color: .white, radius: 0.5)
                        .position(x: a.minX + p.x * a.width + 6, y: a.minY + p.y * a.height + 9)
                        .allowsHitTesting(false)
                        .zIndex(10)
                }
                if !pins.isEmpty {
                    PinMarks(pins: pins, area: a, editing: editing?.id) { pin in
                        model.viewFocused(paneID)
                        PinMode.shared.editing = pin.id
                    }
                }
                if let pin = editing {
                    // The note's popover hangs off its badge.
                    Color.clear
                        .frame(width: PinBadge.size, height: PinBadge.size)
                        .position(PinMarks(pins: [pin], area: a, onSelect: { _ in }).badgeCenter(pin))
                        .popover(isPresented: Binding(get: { PinMode.shared.editing == pin.id },
                                                      set: { if !$0, PinMode.shared.editing == pin.id { PinMode.shared.editing = nil } }),
                                 arrowEdge: .bottom) {
                            PinNoteEditor(model: model, paneID: paneID, pin: pin) { PinMode.shared.editing = nil }
                                .tint(Color.themeAccent)
                        }
                }
                if on || !pins.isEmpty {
                    PinSendBar(model: model, paneID: paneID) { PinMode.shared.set(paneID, false) }
                        .fixedSize()
                        .position(x: a.midX, y: max(a.maxY - 30, a.minY + 30))
                        .transition(.opacity.combined(with: .move(edge: .bottom)))
                }
            }
            .frame(width: geo.size.width, height: geo.size.height, alignment: .topLeading)
        }
        .clipped() // outlines and the hover's label stay in the pane
        .tint(Color.themeAccent)
        .animation(.snappy(duration: 0.18), value: on)
        .animation(.snappy(duration: 0.18), value: pins.count)
        .onChange(of: on, initial: true) { _, on in
            hover.found = nil
            watchEscape(on)
        }
        .onDisappear { watchEscape(false) }
        .onChange(of: PinPointer.shared.at[paneID]) { _, p in
            if let p, on, inspects { hover.point(p, model: model, paneID: paneID) } else if p == nil { hover.found = nil }
        }
    }

    /// Takes the clicks (and, on a page, the hovering) in pin mode.
    private func catcher(_ a: CGRect) -> some View {
        Rectangle()
            .fill(Color.white.opacity(0.001)) // hit-testable, unseen
            .frame(width: a.width, height: a.height)
            .contentShape(Rectangle())
            .onContinuousHover(coordinateSpace: .local) { phase in
                switch phase {
                case .active(let p):
                    NSCursor.crosshair.set()
                    if inspects { hover.point(CGPoint(x: p.x / a.width, y: p.y / a.height), model: model, paneID: paneID) }
                case .ended:
                    NSCursor.arrow.set()
                    hover.found = nil
                }
            }
            .gesture(SpatialTapGesture().onEnded { v in
                model.viewFocused(paneID)
                let p = CGPoint(x: min(max(v.location.x / a.width, 0), 1), y: min(max(v.location.y / a.height, 0), 1))
                model.addPin(paneID, at: p)
            })
            .position(x: a.midX, y: a.midY)
    }

    /// The element under the pointer, outlined, with what it is above it
    /// (below it at the top of the page), from its left edge.
    private func highlight(_ r: CGRect, _ what: String, in a: CGRect) -> some View {
        let box = CGRect(x: a.minX + r.minX * a.width, y: a.minY + r.minY * a.height, width: r.width * a.width, height: r.height * a.height)
        let above = box.minY > a.minY + 22
        return Rectangle()
            .fill(.tint.opacity(0.12))
            .strokeBorder(.tint, lineWidth: 1)
            .frame(width: box.width, height: box.height)
            .overlay(alignment: above ? .topLeading : .bottomLeading) {
                if !what.isEmpty {
                    Text(what)
                        .font(.caption.monospaced())
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .foregroundStyle(.white)
                        .background(RoundedRectangle(cornerRadius: 4).fill(.tint))
                        .fixedSize()
                        .frame(maxWidth: max(a.maxX - max(box.minX, a.minX) - 8, 40), alignment: .leading)
                        .offset(x: max(a.minX - box.minX, 0) + 4, y: above ? -20 : 20)
                }
            }
            .position(x: box.midX, y: box.midY)
            .allowsHitTesting(false)
    }

    /// Esc leaves pin mode while it's on in the focused pane (and no note is
    /// open: the popover has Esc then).
    private func watchEscape(_ on: Bool) {
        if let e = escape {
            NSEvent.removeMonitor(e)
            escape = nil
        }
        guard on else { return }
        escape = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [model, paneID] e in
            guard e.keyCode == 53, PinMode.shared.isOn(paneID), PinMode.shared.editing == nil,
                  model.focusedPaneID == paneID else { return e }
            PinMode.shared.set(paneID, false)
            return nil
        }
    }
}

/// What's under the pointer on a page in pin mode: asked of the daemon as
/// the pointer moves, one question at a time (the latest point next).
@MainActor
@Observable
final class PinHover {
    struct Found: Equatable {
        var what: String
        var rect: CGRect? // fractions of the page
    }

    var found: Found?
    @ObservationIgnored private var asking = false
    @ObservationIgnored private var next: CGPoint?

    func point(_ p: CGPoint, model: AppModel, paneID: String) {
        next = p
        guard !asking else { return }
        ask(model: model, paneID: paneID)
    }

    private func ask(model: AppModel, paneID: String) {
        guard let p = next else { return }
        next = nil
        asking = true
        Task {
            let r = await model.inspectPin(paneID, at: p)
            asking = false
            guard PinMode.shared.isOn(paneID) else {
                found = nil
                return
            }
            if let r {
                let rect = r.hasRect ? CGRect(x: r.rect.x, y: r.rect.y, width: r.rect.w, height: r.rect.h) : nil
                found = Found(what: r.what, rect: rect)
            }
            if next != nil { ask(model: model, paneID: paneID) }
        }
    }
}

/// The debug director's pointer over a pane (DebugDirector's pins point):
/// where it hovers, in fractions of the page, for recordings.
@MainActor
@Observable
final class PinPointer {
    static let shared = PinPointer()
    var at: [String: CGPoint] = [:]
    func set(_ pane: String, _ p: CGPoint?) { at[pane] = p }
}
