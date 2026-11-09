import Foundation

/// The coding agents the daemon knows and what Hammock can do with each
/// (ADR-0015): pickers, "Continue in…" menus and usage toggles come from
/// here, not from a list in the app, so an agent added to the daemon shows
/// up without a new app.
///
/// A daemon too old to send the catalogue knows exactly Claude Code and
/// Codex, with everything: `kinds` is those two then.
@MainActor
@Observable
public final class AgentCatalog {
    /// The one the app's model keeps up to date (`AppModel.agentKinds`), so
    /// static helpers like `AgentMark.name(of:)` can read it.
    public static let shared = AgentCatalog()

    /// As the daemon last sent it (empty: it sent none).
    public internal(set) var received: [Hammock_V1_AgentKind] = []

    /// The agents, in the daemon's order (Claude Code first).
    public var kinds: [Hammock_V1_AgentKind] { Self.resolve(received) }

    // MARK: Lookups (the shared catalogue)

    public static func kind(_ id: String) -> Hammock_V1_AgentKind? { shared.kinds.first { $0.id == id } }
    /// "Claude Code", "Codex", "Gemini CLI".
    public static func name(of id: String) -> String { name(of: id, in: shared.kinds) }
    /// "Claude", "Codex", "Gemini": for "Continue in Claude".
    public static func shortName(of id: String) -> String { shortName(of: id, in: shared.kinds) }
    /// The agent a session continues in by default (see `other(of:in:)`).
    public static func other(of id: String) -> String? { other(of: id, in: shared.kinds) }

    /// Whether the agent view reads this agent's sessions.
    public static func hasAgentView(_ id: String) -> Bool { kind(id)?.agentView ?? false }
    /// Whether a conversation in this agent can continue in another.
    public static func canSwitch(_ id: String) -> Bool { kind(id)?.canSwitch ?? false }

    /// Agents a conversation can continue in, as the "Continue in…" menus
    /// list them: Claude last, where those menus have always had it.
    public static var switchable: [Hammock_V1_AgentKind] {
        let k = shared.kinds.filter(\.canSwitch)
        return k.filter { $0.id != "claude" } + k.filter { $0.id == "claude" }
    }
    /// Agents scheduled tasks can run.
    public static var schedulable: [Hammock_V1_AgentKind] { shared.kinds.filter(\.schedulable) }
    /// Agents whose usage limits can be shown.
    public static var withUsage: [Hammock_V1_AgentKind] { shared.kinds.filter(\.usage) }
    /// Agents whose usage limits can be asked of their own service (usage-live).
    public static var withLiveUsage: [Hammock_V1_AgentKind] { shared.kinds.filter(\.liveUsage) }
    /// Agents installed where the daemon runs.
    public static var installed: [Hammock_V1_AgentKind] { shared.kinds.filter(\.installed) }

    /// What a new agent tab can start: those installed (installedIDs, when
    /// the computer's folder list says, else the catalogue's), Claude Code
    /// and Codex always (as before: the tab says if they're missing), and
    /// the one chosen.
    public static func startable(installedIDs: [String]?, chosen: String) -> [Hammock_V1_AgentKind] {
        shared.kinds.filter { k in
            builtIn.contains(k.id) || k.id == chosen || (installedIDs?.contains(k.id) ?? k.installed)
        }
    }

    // MARK: The rules, on any list (tested without the shared one)

    /// The agents Hammock always knew, offered whether installed or not.
    public nonisolated static let builtIn = ["claude", "codex"]

    /// received, or Claude Code and Codex when the daemon sent none.
    public nonisolated static func resolve(_ received: [Hammock_V1_AgentKind]) -> [Hammock_V1_AgentKind] {
        guard received.isEmpty else { return received }
        return builtIn.map { id in
            .with {
                $0.id = id
                $0.name = known[id]?.name ?? id
                $0.shortName = known[id]?.short ?? id
                $0.installed = true
                $0.canResume = true
                $0.agentView = true
                $0.canSwitch = true
                $0.schedulable = true
                $0.usage = true
                $0.liveUsage = id == "claude"
                $0.hooks = true
                $0.sessions = true
            }
        }
    }

    public nonisolated static func name(of id: String, in kinds: [Hammock_V1_AgentKind]) -> String {
        if let k = kinds.first(where: { $0.id == id }), !k.name.isEmpty { return k.name }
        if id.isEmpty { return "Agent" }
        return known[id]?.name ?? id
    }

    public nonisolated static func shortName(of id: String, in kinds: [Hammock_V1_AgentKind]) -> String {
        if let k = kinds.first(where: { $0.id == id }) {
            if !k.shortName.isEmpty { return k.shortName }
            if !k.name.isEmpty { return known[id]?.short ?? k.name }
        }
        if id.isEmpty { return "Agent" }
        return known[id]?.short ?? id
    }

    /// The agent a session continues in by default: Claude's in Codex, any
    /// other's that can switch in Claude, and none for one that can't (as
    /// the daemon's `agents.Other`).
    public nonisolated static func other(of id: String, in kinds: [Hammock_V1_AgentKind]) -> String? {
        guard let k = kinds.first(where: { $0.id == id }), k.canSwitch else { return nil }
        let target = id == "claude" ? "codex" : "claude"
        return kinds.contains { $0.id == target && $0.canSwitch } ? target : nil
    }

    /// Names for agents the daemon may know, so they read well before (or
    /// without) a catalogue that names them: an older daemon's harness from
    /// a pane, a newer one's agents on an older app.
    nonisolated static let known: [String: (name: String, short: String)] = [
        "claude": ("Claude Code", "Claude"),
        "codex": ("Codex", "Codex"),
        "gemini": ("Gemini CLI", "Gemini"),
        "opencode": ("OpenCode", "OpenCode"),
        "cursor": ("Cursor Agent", "Cursor"),
        "copilot": ("GitHub Copilot CLI", "Copilot"),
        "droid": ("Factory Droid", "Droid"),
        "amp": ("Amp", "Amp"),
        "pi": ("Pi", "Pi"),
        "qwen": ("Qwen Code", "Qwen"),
        "crush": ("Crush", "Crush"),
        "grok": ("Grok CLI", "Grok"),
        "kiro": ("Kiro CLI", "Kiro"),
        "goose": ("Goose", "Goose"),
        "aider": ("Aider", "Aider"),
        "kilo": ("Kilo CLI", "Kilo"),
        "cline": ("Cline", "Cline"),
        "kimi": ("Kimi Code", "Kimi"),
        "antigravity": ("Antigravity CLI", "Antigravity"),
    ]
}
