import SwiftUI
import UIKit

extension View {
    /// Runs `work` while this view is on screen (in a window) and `on`,
    /// cancelling it when either stops, and starting it afresh when `id`
    /// changes. For a pane's live video: SwiftUI's own appear and disappear
    /// (and so `.task`) don't always match what's on screen, and a browser
    /// pane opened after screen sharing was told it had gone as it
    /// appeared, its video stopped and never started again. iOS takes a
    /// view out of its window when it's pushed over, on another tab, or
    /// gone, so that's what this goes by.
    func whileShown(_ on: Bool, id: AnyHashable = 0, _ work: @escaping @MainActor () async -> Void) -> some View {
        background { WhileShown(on: on, id: id, work: work).frame(width: 0, height: 0) }
    }
}

private struct WhileShown: UIViewRepresentable {
    let on: Bool
    let id: AnyHashable
    let work: @MainActor () async -> Void

    func makeUIView(context: Context) -> Probe { Probe() }

    func updateUIView(_ v: Probe, context: Context) {
        v.work = work
        v.set(on: on, id: id)
    }

    static func dismantleUIView(_ v: Probe, coordinator: ()) { v.stop() }

    final class Probe: UIView {
        var work: (@MainActor () async -> Void)?
        private var on = false
        private var id: AnyHashable?
        private var task: Task<Void, Never>?

        func set(on: Bool, id: AnyHashable) {
            if id != self.id { stop() }
            self.on = on
            self.id = id
            sync()
        }

        override func didMoveToWindow() {
            super.didMoveToWindow()
            sync()
        }

        private func sync() {
            if on, window != nil {
                guard task == nil, let work else { return }
                task = Task { @MainActor in await work() }
            } else {
                stop()
            }
        }

        func stop() {
            task?.cancel()
            task = nil
        }
    }
}
