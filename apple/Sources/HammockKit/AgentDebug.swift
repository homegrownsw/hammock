import Foundation

/// Dev aid: commands for the agent view from the Mac's director
/// (HMK_DEBUG_DIRECTOR's `agent …`), for comparing its two renderers:
/// the same row brought to the top, opened and closed, in each.
public enum AgentDebug {
    public static let command = Notification.Name("HammockDebugAgent")

    /// show ID (that row at the top), toggle ID (open or close it, as a click
    /// does), end (to the end, as Latest does), list FILE (the rows, one a line).
    public static func send(_ cmd: String, _ arg: String = "") {
        NotificationCenter.default.post(name: command, object: nil, userInfo: ["cmd": cmd, "arg": arg])
    }

    static func parse(_ n: Notification) -> (cmd: String, arg: String) {
        (n.userInfo?["cmd"] as? String ?? "", n.userInfo?["arg"] as? String ?? "")
    }

    /// Each row's index, kind and id, and the start of its text, to pick rows to compare.
    @MainActor static func list(_ s: AgentSession, to path: String) {
        let lines = s.items.enumerated().map { i, item -> String in
            let kind = item.block.map { "\($0.kind)" + ($0.tool.isEmpty ? "" : ":" + $0.tool) + ($0.diff.isEmpty ? "" : "+diff") }
                ?? "group(\(item.tools.count))"
            let text = (item.block?.text ?? item.tools.first?.title ?? "").prefix(60).replacingOccurrences(of: "\n", with: " ")
            return "\(i)\t\(kind)\t\(item.id)\t\(text)"
        }
        try? lines.joined(separator: "\n").write(toFile: path, atomically: true, encoding: .utf8)
    }
}
