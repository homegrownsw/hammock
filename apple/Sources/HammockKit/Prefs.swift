import Foundation
import Observation
import SwiftUI

/// A setting: on the Mac a key in Hammock's config file (AppConfig), so
/// people and agents can read, edit and sync it; on the iPhone, which has no
/// config file, a UserDefaults key. The Mac kept these in UserDefaults
/// before 0.3.1; AppConfig moves them into the file once.
public struct Pref: Hashable, Sendable {
    public enum Kind: Sendable { case string, double, bool }

    /// The config file's key.
    public let key: String
    /// The UserDefaults key: the iPhone's, and the Mac's before 0.3.1.
    public let defaultsKey: String
    public let kind: Kind
    /// The value when unset, as the config file would hold it.
    public let fallback: String
    /// UserDefaults values spelt differently in the config file (from → to).
    public let renamed: [String: String]

    public init(_ key: String, defaults: String, _ kind: Kind, _ fallback: String, renamed: [String: String] = [:]) {
        self.key = key
        defaultsKey = defaults
        self.kind = kind
        self.fallback = fallback
        self.renamed = renamed
    }

    public init(_ key: String, defaults: String, _ fallback: Bool) {
        self.init(key, defaults: defaults, .bool, fallback ? "true" : "false")
    }

    public init(_ key: String, defaults: String, _ fallback: Double) {
        self.init(key, defaults: defaults, .double, Self.format(fallback))
    }

    /// 14, not 14.0; 1.5 as it is.
    public static func format(_ v: Double) -> String {
        v == v.rounded() && abs(v) < 1e9 ? String(Int(v)) : String(v)
    }

    /// A UserDefaults value as the config file would hold it.
    public func configValue(fromDefaults v: Any) -> String? {
        let s: String? = switch kind {
        case .string: v as? String
        case .bool: (v as? Bool).map { $0 ? "true" : "false" }
        case .double: (v as? Double).map(Self.format)
        }
        return s.map { renamed[$0] ?? $0 }
    }

    /// A config value as UserDefaults holds it.
    func defaultsValue(_ s: String) -> Any {
        switch kind {
        case .string: s
        case .bool: s == "true"
        case .double: Double(s) ?? Double(fallback) ?? 0
        }
    }
}

/// The settings' values, observed: SwiftUI views reading them update when
/// they change. On the Mac, the config file's (AppConfig hands them over on
/// every load, and writes changes to the file); on the iPhone, UserDefaults'.
@MainActor
@Observable
public final class Prefs {
    public static let shared = Prefs()

    /// Each set value, by config key.
    private var values: [String: String] = [:]
    /// Fallbacks that follow another setting (the Mac's agent code font
    /// follows the terminal's), by config key.
    private var fallbacks: [String: String] = [:]
    @ObservationIgnored private var write: (([(String, String?)]) -> Void)?

    private init() {
        #if !os(macOS)
        loadDefaults()
        NotificationCenter.default.addObserver(forName: UserDefaults.didChangeNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { Prefs.shared.loadDefaults() }
        }
        #endif
    }

    /// The Mac: the config file's values, the fallbacks that depend on
    /// other settings, and how to write a change (AppConfig, on every load).
    public func useConfig(_ values: [String: String], fallbacks: [String: String], write: @escaping ([(String, String?)]) -> Void) {
        self.write = write
        // Only on a change: every view reading a setting redraws for a set.
        if values != self.values { self.values = values }
        if fallbacks != self.fallbacks { self.fallbacks = fallbacks }
    }

    #if !os(macOS)
    private func loadDefaults() {
        var v: [String: String] = [:]
        for p in Pref.shared {
            if let o = UserDefaults.standard.object(forKey: p.defaultsKey), let s = p.configValue(fromDefaults: o) { v[p.key] = s }
        }
        if v != values { values = v }
    }
    #endif

    private func fallback(_ p: Pref) -> String { fallbacks[p.key] ?? p.fallback }

    public func string(_ p: Pref) -> String { values[p.key] ?? fallback(p) }

    public func bool(_ p: Pref) -> Bool {
        switch string(p) {
        case "true": true
        case "false": false
        default: fallback(p) == "true"
        }
    }

    public func double(_ p: Pref) -> Double { Double(string(p)) ?? Double(fallback(p)) ?? 0 }

    /// Sets a value; its fallback unsets it (the config file keeps only
    /// what you've changed).
    public func set(_ p: Pref, _ value: String) {
        #if os(macOS)
        write?([(p.key, value == fallback(p) ? nil : value)])
        #else
        UserDefaults.standard.set(p.defaultsValue(value), forKey: p.defaultsKey)
        #endif
    }

    public func set(_ p: Pref, _ value: Bool) { set(p, value ? "true" : "false") }
    public func set(_ p: Pref, _ value: Double) { set(p, Pref.format(value)) }

    public func toggle(_ p: Pref) { set(p, !bool(p)) }

    // Bindings, for Settings' controls.
    public func boolBinding(_ p: Pref) -> Binding<Bool> {
        Binding(get: { self.bool(p) }, set: { self.set(p, $0) })
    }

    public func doubleBinding(_ p: Pref) -> Binding<Double> {
        Binding(get: { self.double(p) }, set: { self.set(p, $0) })
    }

    public func stringBinding(_ p: Pref) -> Binding<String> {
        Binding(get: { self.string(p) }, set: { self.set(p, $0) })
    }
}

// MARK: The settings both apps have

extension Pref {
    public static let agentFont = Pref("agent-font-family", defaults: AgentViewPrefs.fontName, .string, "")
    public static let agentFontSize = Pref("agent-font-size", defaults: AgentViewPrefs.fontSize, AgentViewPrefs.defaultSize)
    public static let agentLineSpacing = Pref("agent-line-spacing", defaults: AgentViewPrefs.lineSpacing, AgentViewPrefs.defaultSpacing)
    /// "" is the system's monospaced font; unset on the Mac, the terminal's.
    public static let agentCodeFont = Pref("agent-code-font-family", defaults: AgentViewPrefs.codeFontName, .string, "")
    public static let agentCodeFontSize = Pref("agent-code-font-size", defaults: AgentViewPrefs.codeFontSize, AgentViewPrefs.defaultSize - 1)
    /// Which view agent panes open in: "agent" or "terminal".
    #if os(macOS)
    public static let agentDefaultView = Pref("agent-default-view", defaults: AgentViewPrefs.defaultView, .string, "terminal")
    #else
    public static let agentDefaultView = Pref("agent-default-view", defaults: AgentViewPrefs.defaultView, .string, "agent")
    #endif

    /// An agent's usage limits shown (the Mac's sidebar, the iPhone's sessions).
    public static func usageLimits(_ harness: String) -> Pref {
        Pref("usage-limits-\(harness)", defaults: UsageLimitsPrefs.key(harness), false)
    }

    /// The settings HammockKit knows (the Mac app adds its own).
    public static var shared: [Pref] {
        [agentFont, agentFontSize, agentLineSpacing, agentCodeFont, agentCodeFontSize, agentDefaultView]
            + AgentCatalog.known.keys.sorted().map { usageLimits($0) }
            + editor
    }
}
