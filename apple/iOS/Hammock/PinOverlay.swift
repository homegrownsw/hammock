import HammockKit
import SwiftUI
import UIKit

// Design notes on the phone: a browser's page or a simulator's screen shows
// its pane's pins. In pin mode, a touch and hold drops one (its note opens
// to write) and the page or device gets no touches; a tap on a pin opens
// its note. Out of it, the pins are only drawn. The marks, the note editor
// and the Send bar are HammockKit's (DesignNotes.swift).

/// A pane's pins over `area` (the page or screen as drawn, in this view's
/// coordinates), and pin mode's touch and hold.
struct PinLayer: View {
    let model: AppModel
    let paneID: String
    let area: CGRect
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        let on = PinMode.shared.isOn(paneID)
        let pins = model.pins(paneID)
        ZStack(alignment: .topLeading) {
            if on {
                // Takes every touch, so the page or device gets none.
                Color.clear
                    .contentShape(Rectangle())
                    .gesture(PinHold { drop(at: $0) })
            }
            if !pins.isEmpty {
                PinMarks(pins: pins, area: area, editing: PinMode.shared.editing) { PinMode.shared.editing = $0.id }
                    .allowsHitTesting(on)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .clipped()
        .tint(theme.accent)
    }

    private func drop(at p: CGPoint) {
        guard area.width > 0, area.height > 0, area.contains(p) else { return }
        Haptics.pin()
        model.addPin(paneID, at: CGPoint(x: (p.x - area.minX) / area.width, y: (p.y - area.minY) / area.height))
    }
}

/// A touch and hold, with where it was: SwiftUI's long press doesn't say.
private struct PinHold: UIGestureRecognizerRepresentable {
    let began: (CGPoint) -> Void

    func makeUIGestureRecognizer(context: Context) -> UILongPressGestureRecognizer {
        let g = UILongPressGestureRecognizer()
        g.minimumPressDuration = 0.4
        return g
    }

    func handleUIGestureRecognizerAction(_ g: UILongPressGestureRecognizer, context: Context) {
        if g.state == .began { began(context.converter.localLocation) }
    }
}

/// Pin mode around a browser's page or a simulator's screen: the Send bar
/// along its bottom while on, a pin's note in a sheet, and pin mode off once
/// the pane leaves the screen.
struct PinNotesSupport: ViewModifier {
    let model: AppModel
    let paneID: String
    @Environment(\.phoneTheme) private var theme
    @Environment(\.paneChrome) private var chrome

    func body(content: Content) -> some View {
        let on = PinMode.shared.isOn(paneID)
        content
            .overlay(alignment: .bottom) {
                if on {
                    PinSendBar(model: model, paneID: paneID) { PinMode.shared.set(paneID, false) }
                        .padding(.horizontal, 8)
                        .padding(.bottom, 8)
                        .tint(theme.accent)
                        .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
            .animation(.snappy(duration: 0.25), value: on)
            .sheet(isPresented: Binding(get: { editingPin != nil }, set: { if !$0 { PinMode.shared.editing = nil } })) {
                if let pin = editingPin {
                    VStack(spacing: 0) {
                        PinNoteEditor(model: model, paneID: paneID, pin: pin) { PinMode.shared.editing = nil }
                            .id(pin.id)
                        Spacer(minLength: 0)
                    }
                    .padding(.top, 12)
                    .padding(.horizontal, 8)
                    .presentationDetents([.medium, .large])
                    .tint(theme.accent)
                }
            }
            .onChange(of: chrome) { _, shown in if !shown { PinMode.shared.set(paneID, false) } }
            .onDisappear { PinMode.shared.set(paneID, false) }
    }

    /// The pin of this pane whose note is open, once the board has it.
    private var editingPin: Pin? {
        guard let id = PinMode.shared.editing else { return nil }
        return model.pins(paneID).first { $0.id == id }
    }
}

/// Pin mode's switch, for a screen's bar.
struct PinNotesButton: View {
    let paneID: String

    var body: some View {
        Toggle(isOn: Binding(get: { PinMode.shared.isOn(paneID) }, set: { PinMode.shared.set(paneID, $0) })) {
            Label("Pin Notes", systemImage: "mappin.and.ellipse")
        }
        .toggleStyle(.button)
    }
}

extension View {
    func pinNotes(model: AppModel, paneID: String) -> some View {
        modifier(PinNotesSupport(model: model, paneID: paneID))
    }
}
