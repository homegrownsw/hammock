import AppKit
import HammockKit

/// A pane's agent session on the web (Claude Code's Remote Control link):
/// copied for the menu bar, the palette and the pane's menu.
@MainActor
enum SessionLink {
    static func copy(_ model: AppModel, _ pane: Hammock_V1_Pane?) {
        guard let url = pane?.sessionURL else { return }
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(url.absoluteString, forType: .string)
        model.showToast("Session link copied")
    }
}
