import Foundation

/// A notification about an agent that finished or waits on you: its Reply
/// action types a message to it, as the agent view's composer would, on
/// the iPhone and on a paired Apple Watch (whose long look offers your
/// quick replies). The notification extension gives an agent's note this
/// category; one with Allow and Deny (AgentPromptNotice) keeps its own.
enum AgentReplyNotice {
    static let category = "agentreply"
    static let replyAction = "agentreply.reply"
    /// Said when the reply couldn't go because a question or permission
    /// is on its screen: typing there would answer it.
    static let answerInApp = "Couldn't send: it's asking something. Open it to answer."
    /// Said when its pane has closed since.
    static let paneClosed = "Couldn't send: that agent's pane has closed."

    /// The notes that get Reply: an agent's (not another program's)
    /// needs-input or finished note about a pane, with no actions of its own.
    static func offers(_ note: [String: Any]) -> Bool {
        let kind = note["k"] as? String ?? ""
        let pane = note["pane"] as? String ?? ""
        return (kind == "needs_input" || kind == "finished") && !pane.isEmpty && (note["c"] as? String ?? "").isEmpty
    }
}
