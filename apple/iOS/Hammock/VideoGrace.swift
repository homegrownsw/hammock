import SwiftUI
import UIKit

/// Whether panes' live video (browser and simulator panes) runs: while the
/// app's in front, and for a while after it leaves, so a quick look at
/// another app (a code from Messages) finds the page as it was, not a
/// spinner while a new stream starts. The app root tells it the scene's
/// phase (RootView); background time is asked of iOS meanwhile, as screen
/// sharing does.
@MainActor
@Observable
final class VideoGrace {
    static let shared = VideoGrace()

    /// How long video goes on with the app away.
    static let away: Duration = .seconds(30)

    private(set) var live = true
    private let grace = BackgroundGrace()
    private var ending: Task<Void, Never>?

    func phase(_ p: ScenePhase) {
        switch p {
        case .active:
            ending?.cancel()
            ending = nil
            grace.end()
            live = true
        case .background:
            guard live, ending == nil else { return }
            grace.begin()
            ending = Task { @MainActor [weak self] in
                try? await Task.sleep(for: Self.away)
                guard let self, !Task.isCancelled else { return }
                self.live = false
                self.ending = nil
                self.grace.end()
            }
        default:
            break  // inactive: the app switcher, Control Center; still shown
        }
    }
}
