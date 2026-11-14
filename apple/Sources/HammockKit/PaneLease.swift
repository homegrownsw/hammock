import Foundation

/// A view's hold on a pane's session (a diff's, an editor's): released when
/// it's let go, so a view keeps its session for as long as its state lives,
/// whatever order SwiftUI says it appeared and disappeared in. (Released in
/// onDisappear instead, a screen opened again on the iPhone got its state
/// back without another onAppear, and stayed blank.) AgentLease does the same
/// for agent sessions.
@MainActor
public final class PaneLease<Session: AnyObject> {
    public let session: Session
    private let release: @MainActor @Sendable () -> Void

    init(_ session: Session, release: @escaping @MainActor @Sendable () -> Void) {
        self.session = session
        self.release = release
    }

    deinit {
        let release = release
        Task { @MainActor in release() }
    }
}

extension AppModel {
    /// The pane's diff session, held until the lease is let go.
    public func leaseDiff(_ paneID: String) -> PaneLease<DiffSession> {
        PaneLease(acquireDiff(paneID)) { [self] in releaseDiff(paneID) }
    }

    /// The pane's editor session, held until the lease is let go.
    public func leaseEditor(_ paneID: String) -> PaneLease<EditorSession> {
        PaneLease(acquireEditor(paneID)) { [self] in releaseEditor(paneID) }
    }
}
