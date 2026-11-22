import AppIntents

/// Allow or Deny on a Live Activity, for the agent it shows waiting on a
/// permission prompt. Compiled into the app and the widget extension (whose
/// buttons name it); iOS runs it in the app, which answers through the Mac
/// (LiveActivities.answer). The phone is unlocked first.
struct LiveAnswerIntent: LiveActivityIntent {
    static let title: LocalizedStringResource = "Answer an Agent"
    static let isDiscoverable = false
    static let authenticationPolicy = IntentAuthenticationPolicy.requiresAuthentication

    @Parameter(title: "Mac") var host: String
    @Parameter(title: "Pane") var pane: String
    @Parameter(title: "Item") var item: String
    @Parameter(title: "Prompt") var prompt: String
    @Parameter(title: "Allow") var allow: Bool

    init() {}

    init(host: String, pane: String, item: String, prompt: String, allow: Bool) {
        self.host = host
        self.pane = pane
        self.item = item
        self.prompt = prompt
        self.allow = allow
    }

    /// The app's answer (set as it launches; never in the widget).
    @MainActor static var answer: ((String, String, String, String, Bool) async -> Void)?

    func perform() async throws -> some IntentResult {
        await Self.answer?(host, pane, item, prompt, allow)
        return .result()
    }
}
