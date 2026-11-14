#if DEBUG
import Foundation
import HammockKit
import UIKit

/// Dev aid for the App Store's videos (marketing/scripts/appstore-preview-video): HMK_DEBUG_TAKE plays a take in the
/// demo (DemoMac), a list of steps separated by ";", once the app has its state. The script records the Simulator
/// meanwhile, and cuts the edit (marketing/appstore/preview.json) on the marks it writes to stderr:
///
///   wait:MS             do nothing for MS milliseconds
///   mark:NAME           "HammockTake: mark NAME <ms since the epoch>", a moment the edit can cut on
///   tab:sessions|groups|devices|settings
///   open:ID             Sessions shows ID (a pane, a tab, "schedules"): as HMK_DEBUG_PATH
///   back                Sessions back to its list
///   allow:PANE          Allow what PANE's agent is waiting on (its Needs You card's Allow)
///   type:PANE:TEXT      TEXT typed into PANE's terminal, a key at a time
///   say:PANE:TEXT       TEXT sent to PANE's agent as a message
///   compose:PANE:TEXT   TEXT typed into the agent view's message box (PANE's, on screen), keyboard up
///   submit:PANE         that box sent, as its send button does
///   dismiss             the keyboard put away
///   scroll:top|bottom|N the conversation on screen scrolled to its top, its end, or by N points (up if negative)
///
/// and at the end "HammockTake: done". The cockpit's views follow the tab and path through notifications.
@MainActor
enum DemoDirector {
    static let navigate = Notification.Name("HammockDemoDirectorNavigate")
    private static var started = false

    static var steps: [String]? {
        ProcessInfo.processInfo.environment["HMK_DEBUG_TAKE"].map { $0.split(separator: ";").map(String.init) }
    }

    static func start(_ model: AppModel) {
        guard let steps, !started else { return }
        started = true
        Task { @MainActor in
            for step in steps { await play(step.trimmingCharacters(in: .whitespaces), model) }
            say("done")
        }
    }

    private static func say(_ line: String) {
        FileHandle.standardError.write(Data("HammockTake: \(line)\n".utf8))
    }

    private static func post(_ info: [String: Any]) {
        NotificationCenter.default.post(name: navigate, object: nil, userInfo: info)
    }

    private static func play(_ step: String, _ model: AppModel) async {
        let parts = step.split(separator: ":", maxSplits: 2).map(String.init)
        let verb = parts.first ?? "", arg = parts.count > 1 ? parts[1] : "", rest = parts.count > 2 ? parts[2] : ""
        switch verb {
        case "wait":
            try? await Task.sleep(for: .milliseconds(Int(arg) ?? 0))
        case "mark":
            say("mark \(arg) \(Int64(Date().timeIntervalSince1970 * 1000))")
        case "tab":
            post(["tab": arg])
        case "open":
            post(["path": [arg]])
        case "back":
            post(["path": [String]()])
        case "allow":
            if let item = model.attention.first(where: { $0.paneID == arg }) { model.decide(item, allow: true) }
        case "type":
            for ch in rest {
                model.send(arg, Data(String(ch == "\n" ? "\r" : ch).utf8))
                try? await Task.sleep(for: .milliseconds(85))
            }
        case "compose":
            NotificationCenter.default.post(name: Notification.Name("HammockDebugCompose"), object: nil, userInfo: ["pane": arg, "text": rest])
        case "submit":
            NotificationCenter.default.post(name: Notification.Name("HammockDebugCompose"), object: nil, userInfo: ["pane": arg])
        case "scroll":
            NotificationCenter.default.post(name: Notification.Name("HammockDebugScroll"), object: nil, userInfo: ["to": arg])
        case "dismiss":
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
        case "say":
            let s = model.acquireAgent(arg)
            await s.send(rest)
            model.releaseAgent(arg)
        default:
            say("unknown step \(step)")
        }
    }
}
#endif
