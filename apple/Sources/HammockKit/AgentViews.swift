import AVFoundation
import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

#if canImport(AppKit)
import AppKit
import SystemConfiguration
typealias PlatformImage = NSImage
#else
import UIKit
typealias PlatformImage = UIImage
#endif

// The agent view: an agent's conversation, as in Claude's apps, beside the
// raw terminal. Shared by the Mac and iOS apps (HammockKit).

// MARK: - Settings

/// The agent view's fonts (Settings, per device): a prose font (the system
/// font unless chosen) and a code font. The keys are UserDefaults' (the
/// iPhone's; read them through Prefs, which has the Mac's in its config file).
public enum AgentViewPrefs {
    public static let fontName = "agent.fontName"
    public static let fontSize = "agent.fontSize"
    public static let codeFontName = "agent.codeFontName"
    public static let codeFontSize = "agent.codeFontSize"
    public static let codeWrap = "agent.codeWrap"
    public static let lineSpacing = "agent.lineSpacing"
    /// Which view agent panes open in on this device: "agent" or "terminal".
    public static let defaultView = "agent.defaultView"
    /// The agent view's colour on this device from before themes ("" or an
    /// AgentColor name): only read to move it into the theme (the Mac's
    /// config, the iPhone's theme pick), then removed.
    public static let color = "agent.color"
    static let paneModes = "agent.paneModes"

    #if os(macOS)
    public static let defaultSize: Double = 14
    public static let defaultSpacing: Double = 3
    #else
    public static let defaultSize: Double = 18
    public static let defaultSpacing: Double = 4
    #endif

    /// Whether a pane shows the agent view: the pane's own choice once
    /// toggled, else this device's default.
    @MainActor public static func showsAgent(_ paneID: String) -> Bool {
        if let m = (UserDefaults.standard.dictionary(forKey: paneModes) as? [String: Bool])?[paneID] { return m }
        return Prefs.shared.string(.agentDefaultView) == "agent"
    }

    public static func setShowsAgent(_ paneID: String, _ on: Bool) {
        var d = (UserDefaults.standard.dictionary(forKey: paneModes) as? [String: Bool]) ?? [:]
        d[paneID] = on
        UserDefaults.standard.set(d, forKey: paneModes)
    }
}

struct AgentFonts: Equatable {
    var name: String
    var size: Double
    var codeName: String
    var codeSize: Double
    var spacing: Double
    /// Which code blocks wrap: "prose", "always" or "never" (.agentCodeWrap).
    var codeWrap = "prose"

    /// This device's settings, as the conversation view reads them.
    @MainActor static var current: AgentFonts {
        let p = Prefs.shared
        return AgentFonts(name: p.string(.agentFont), size: p.double(.agentFontSize),
                          codeName: p.string(.agentCodeFont), codeSize: p.double(.agentCodeFontSize),
                          spacing: p.double(.agentLineSpacing), codeWrap: p.string(.agentCodeWrap))
    }

    func body(_ scale: Double = 1, weight: Font.Weight = .regular) -> Font {
        let s = size * scale
        #if os(iOS)
        if name.isEmpty { return .system(size: s, weight: weight).leading(.standard) }
        return .custom(name, size: s, relativeTo: .body).weight(weight)
        #else
        return name.isEmpty ? .system(size: s, weight: weight) : .custom(name, size: s).weight(weight)
        #endif
    }

    func code(_ scale: Double = 1) -> Font {
        let s = codeSize * scale
        return codeName.isEmpty ? .system(size: s, design: .monospaced) : .custom(codeName, size: s)
    }
}

// The defaults until an app sets them from its theme (each app does, at its root).
private struct AgentAccentKey: EnvironmentKey { static let defaultValue = Color.accentColor } // system accent: unset default
// The iPhone's: Hammock's forest green, as the palette has it (the app accent turns light in
// dark mode, too light under white text).
private struct AgentAutomaticKey: EnvironmentKey {
    #if os(macOS)
    static let defaultValue = Color.accentColor // system accent: unset default
    #else
    static let defaultValue = AgentColor.named("Hammock") ?? .accentColor // system accent: unset default
    #endif
}
private struct AgentBubbleKey: EnvironmentKey { static let defaultValue = Color.accentColor } // system accent: unset default
private struct AgentAutomaticBubbleKey: EnvironmentKey {
    #if os(macOS)
    static let defaultValue = Color.accentColor // system accent: unset default
    #else
    static let defaultValue = AgentColor.bubble("Hammock") ?? .accentColor // system accent: unset default
    #endif
}

/// The agent view's colours to pick from (Settings), each a shade of a window
/// theme's. On the iPhone, the whole app's tint.
public enum AgentColor {
    /// `color` is for buttons and the like: in the dark, a lighter shade, so
    /// they don't look disabled (Graphite goes near white). `bubble` is for
    /// your messages, dark enough for white text in both.
    public static let all: [(name: String, color: Color, bubble: Color)] = [
        ("Hammock", hammock, rgb(0x1D6046, dark: 0x1F6A4C)),
        ("Blue", rgb(0x0A72E8, dark: 0x52A3FF), rgb(0x0A72E8)),
        ("Ocean", rgb(0x2B6CC4, dark: 0x72A8F0), rgb(0x2B6CC4)),
        ("Forest", rgb(0x2E8B4F, dark: 0x5EC786), rgb(0x2E8B4F)),
        ("Rose", rgb(0xCC4A72, dark: 0xEA7398), rgb(0xCC4A72)),
        ("Lavender", rgb(0x7352D1, dark: 0xA68EF2), rgb(0x7352D1)),
        ("Sand", rgb(0xA87A1F, dark: 0xD9A743), rgb(0xA87A1F)),
        ("Lagoon", rgb(0x0F828A, dark: 0x3BCECE), rgb(0x107884)),
        ("Apricot", rgb(0xCD641D, dark: 0xF19E55), rgb(0xB8571E)),
        ("Cocoa", rgb(0x845433, dark: 0xD0A380), rgb(0x794A2F)),
        ("Graphite", rgb(0x3E3E46, dark: 0xE2E2E8), rgb(0x5E5E68)),
    ]

    /// The colour for a pref ("" or unknown: automatic).
    public static func named(_ name: String) -> Color? { all.first { $0.name == name }?.color }

    /// Your messages' colour for a pref.
    public static func bubble(_ name: String) -> Color? { all.first { $0.name == name }?.bubble }

    /// Hammock's forest green for buttons and the like, lighter in the dark
    /// (on the iPhone, the app's own; its AccentColor matches).
    #if os(macOS)
    private static let hammock = rgb(0x1F7A57, dark: 0x3FB88A)
    #else
    private static let hammock = rgb(0x1D7050, dark: 0x5CCB9C)
    #endif

    private static func rgb(_ v: Int) -> Color {
        Color(.sRGB, red: Double((v >> 16) & 0xFF) / 255, green: Double((v >> 8) & 0xFF) / 255, blue: Double(v & 0xFF) / 255)
    }

    /// One colour in light mode, another in dark.
    private static func rgb(_ light: Int, dark: Int) -> Color {
        func c(_ v: Int) -> (CGFloat, CGFloat, CGFloat) { (CGFloat((v >> 16) & 0xFF) / 255, CGFloat((v >> 8) & 0xFF) / 255, CGFloat(v & 0xFF) / 255) }
        let (l, d) = (c(light), c(dark))
        #if os(macOS)
        return Color(nsColor: NSColor(name: nil) { a in
            let (r, g, b) = a.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? d : l
            return NSColor(srgbRed: r, green: g, blue: b, alpha: 1)
        })
        #else
        return Color(uiColor: UIColor { t in
            let (r, g, b) = t.userInterfaceStyle == .dark ? d : l
            return UIColor(red: r, green: g, blue: b, alpha: 1)
        })
        #endif
    }
}

private struct AgentFontsKey: EnvironmentKey {
    static let defaultValue = AgentFonts(name: "", size: AgentViewPrefs.defaultSize, codeName: "", codeSize: AgentViewPrefs.defaultSize - 1, spacing: AgentViewPrefs.defaultSpacing)
}

extension EnvironmentValues {
    var agentFonts: AgentFonts {
        get { self[AgentFontsKey.self] }
        set { self[AgentFontsKey.self] = newValue }
    }
    /// The agent view's colour: your messages, choices, the send button.
    var agentAccent: Color {
        get { self[AgentAccentKey.self] }
        set { self[AgentAccentKey.self] = newValue }
    }
    /// Your messages' colour, and what buttons with a white label are
    /// filled with (agentAccent, but deep enough for white in the dark).
    public var agentBubble: Color {
        get { self[AgentBubbleKey.self] }
        set { self[AgentBubbleKey.self] = newValue }
    }
    /// The agent view's colour, from the theme (set by each app: on the Mac,
    /// its theme's or Custom's agent colour; on the iPhone, PhoneTheme's).
    public var agentAutomaticColor: Color {
        get { self[AgentAutomaticKey.self] }
        set { self[AgentAutomaticKey.self] = newValue }
    }
    /// The theme's colour for your messages.
    public var agentAutomaticBubble: Color {
        get { self[AgentAutomaticBubbleKey.self] }
        set { self[AgentAutomaticBubbleKey.self] = newValue }
    }
}

// MARK: - Conversation

/// A pane's conversation with its agent, with the prompt it's showing and a
/// composer. onShowTerminal flips the pane back to the terminal (offered when
/// an answer can't be matched to what's on screen); onOpenFile opens a path
/// the session referenced, and onOpenWebLink a web link (else the system's
/// browser).
public struct AgentConversationView: View {
    let model: AppModel
    let paneID: String
    var onShowTerminal: () -> Void = {}
    var onOpenFile: ((String) -> Void)?
    var onOpenWebLink: ((URL) -> Void)?
    /// The pane's session, held for as long as this view exists (AgentLease).
    @State private var lease: AgentLease?
    private var session: AgentSession? { lease?.session }
    /// A subagent or background shell being looked at instead (read only),
    /// and its session.
    @State private var viewing: AgentSideView?
    @State private var subLease: AgentLease?
    private var subSession: AgentSession? { subLease?.session }
    /// A link with a scheme that could open any app, waiting for a yes.
    @State private var askingToOpen: URL?
    @Environment(\.openURL) private var openURL
    /// The conversation's height, so a prompt card can take most of it.
    @State private var height: CGFloat = 0
    /// A picture open in the viewer, and the namespace it zooms from.
    @State private var viewedImage: ViewedImage?
    @Namespace private var imageZoom
    @Environment(\.agentAutomaticColor) private var automaticColor
    @Environment(\.agentAutomaticBubble) private var automaticBubble
    @Environment(\.agentOpenDiff) private var openDiff
    /// The web page drawing the conversation (AgentWebView.swift).
    @State private var web = AgentWebHost()

    public init(model: AppModel, paneID: String, onShowTerminal: @escaping () -> Void = {}, onOpenFile: ((String) -> Void)? = nil,
                onOpenWebLink: ((URL) -> Void)? = nil) {
        self.model = model
        self.paneID = paneID
        self.onShowTerminal = onShowTerminal
        self.onOpenFile = onOpenFile
        self.onOpenWebLink = onOpenWebLink
    }

    public var body: some View {
        // A ZStack, not a Group: a Group hands onAppear/onDisappear to its
        // child, which changes when the session arrives, so each swap
        // released and re-acquired it, in a loop that froze iOS.
        ZStack {
            if viewing != nil, let subSession {
                content(subSession)
            } else if let session {
                content(session)
            } else {
                Color.clear
            }
        }
        .environment(\.agentFonts, AgentFonts.current)
        .environment(\.openURL, linkAction)
        .environment(\.agentAccent, automaticColor)
        .environment(\.agentBubble, automaticBubble)
        .environment(\.openAgentImage, { viewedImage = $0 })
        .environment(\.agentImageZoom, imageZoom)
        #if os(iOS)
        .fullScreenCover(item: $viewedImage) { v in
            // Grows out of its picture; drag it down to shrink it back there.
            ImageViewer(image: v.image, video: v.video).navigationTransition(.zoom(sourceID: v.ref, in: imageZoom))
        }
        #endif
        .tint(automaticColor)
        .alert("Open this link?", isPresented: Binding(get: { askingToOpen != nil }, set: { if !$0 { askingToOpen = nil } }), presenting: askingToOpen) { url in
            Button("Open") { openURL(url) }
            Button("Cancel", role: .cancel) {}
        } message: { url in
            Text(url.absoluteString)
        }
        // Taken on first appearing and given back when SwiftUI drops this
        // view's state, not on disappearing: pushed from a list, a view can
        // hear appear, appear, disappear while it stays on screen, which
        // left it holding no session, blank, message box and all.
        .onAppear { if lease == nil { lease = AgentLease(model, paneID) } }
        .onDisappear { viewing = nil }
        .onChange(of: viewing) { _, new in
            subLease = new.map { AgentLease(model, paneID, subagent: $0.subagent, shell: $0.shell) }
        }
    }


    /// What floats at the bottom: the prompt to answer, the message box,
    /// or why there's neither.
    @ViewBuilder
    private func bottomBar(_ s: AgentSession) -> some View {
        VStack(alignment: .trailing, spacing: 10) {
            if s.subagentID.isEmpty, s.shellID.isEmpty {
                SessionExtras(session: s)
            }
            Group {
                if !s.subagentID.isEmpty || !s.shellID.isEmpty {
                    // A subagent's transcript, a shell's output: nothing to answer or send.
                } else if model.group(ofPane: paneID)?.canDrive == false {
                    // Shared with you to watch: no answering or messaging.
                    Label("View only: \(model.group(ofPane: paneID).map { model.hostLabel(of: $0) } ?? "its host") can let you drive", systemImage: "eye")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 10)
                        .agentGlass(in: Capsule())
                        .frame(maxWidth: .infinity)
                } else if s.prompt.kind != .none {
                    PromptCard(session: s, onShowTerminal: onShowTerminal, maxHeight: max(300, height * 0.7))
                        .transition(.move(edge: .bottom).combined(with: .opacity))
                } else {
                    Composer(session: s, harness: model.panes[paneID]?.agent.harness ?? "claude")
                        .transition(.opacity)
                }
            }
            .frame(maxWidth: .infinity)
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 8)
        .frame(maxWidth: 884)
        .frame(maxWidth: .infinity)
        .animation(.snappy(duration: 0.25), value: s.prompt.kind)
        .animation(.snappy(duration: 0.25), value: s.tasks.isEmpty)
    }

    /// Links in the conversation: web links go to onOpenWebLink, they and
    /// mail links otherwise open, file links go to onOpenFile, and any other
    /// scheme (which could open any app) asks first.
    private var linkAction: OpenURLAction {
        OpenURLAction { url in
            switch route(url) {
            case .handled: .handled
            case .system: .systemAction
            case .discarded: .discarded
            }
        }
    }

    private enum LinkRoute { case handled, system, discarded }

    private func route(_ url: URL) -> LinkRoute {
        if AgentLink.opening(url) == .web, let open = onOpenWebLink {
            open(url)
            return .handled
        }
        if AgentLink.opensDirectly(url) { return .system }
        if url.isFileURL {
            // Never to the system as it is: that would run a script or
            // app with no further check.
            guard let open = onOpenFile, !url.path.isEmpty else { return .discarded }
            open(url.path)
            return .handled
        }
        askingToOpen = url
        return .handled
    }

    @ViewBuilder
    private func content(_ s: AgentSession) -> some View {
        if !s.shellID.isEmpty {
            VStack(spacing: 0) {
                ShellHeader(shell: session?.shells.first { $0.id == s.shellID }) { viewing = nil }
                ShellOutputView(session: s)
            }
        } else {
            webConversation(s)
        }
    }

    /// What's above the conversation: the subagent you're looking at, or
    /// the subagents and shells still running.
    @ViewBuilder
    private func strips(_ s: AgentSession) -> some View {
        if !s.subagentID.isEmpty {
            SubagentHeader(subagent: session?.subagents.first { $0.id == s.subagentID }, working: s.working) { viewing = nil }
        } else if s.subagents.contains(where: { !$0.finished }) || s.shells.contains(where: { !$0.finished }) {
            BackgroundStrip(subagents: s.subagents.filter { !$0.finished }, shells: s.shells.filter { !$0.finished }) { viewing = $0 }
        }
    }

    private var webStyle: AgentWebStyle {
        AgentWebStyle(fonts: AgentFonts.current, accent: automaticColor, bubble: automaticBubble,
                      canOpenDiff: openDiff != nil, agentName: session?.agentName ?? "Claude")
    }

    /// The conversation drawn by a web page (AgentWebView.swift), under the
    /// same bars: it runs under the navigation bar and the message box, and
    /// scrolls itself.
    private func webConversation(_ s: AgentSession) -> some View {
        let web = self.web
        return VStack(spacing: 0) {
            strips(s)
            ZStack(alignment: .bottom) {
                // The pane's own page, kept between showings (AgentWebPool).
                AgentWebTranscript(bridge: web.bridge(s.paneID + "|" + s.subagentID))
                    .id(s.paneID + "|" + s.subagentID)
                    .ignoresSafeArea(edges: .vertical)
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { web.page = $0; web.pushInsets() }
                bottomBar(s)
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { web.bar = $0; web.pushInsets() }
            }
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: {
                web.stack = $0
                height = $0.height
                web.pushInsets()
            }
            .onGeometryChange(for: CGFloat.self) { $0.safeAreaInsets.top } action: { web.stackTop = $0; web.pushInsets() }
        }
        #if DEBUG
        .onReceive(NotificationCenter.default.publisher(for: Notification.Name("HammockDebugScroll"))) { n in
            if let to = n.userInfo?["to"] as? String { web.bridge.debugScroll(to) }
        }
        #endif
        .onReceive(NotificationCenter.default.publisher(for: AgentDebug.command)) { n in
            let (cmd, arg) = AgentDebug.parse(n)
            if cmd == "list" { AgentDebug.list(s, to: arg) } else { web.bridge.debug(cmd, arg) }
        }
        .onChange(of: webStyle, initial: true) { _, st in web.bridge.configure(st) }
        .onChange(of: ObjectIdentifier(s), initial: true) {
            web.bridge.onLink = { url in if route(url) == .system { openURL(url) } }
            #if os(macOS)
            // A picture or movie dropped on the conversation: attached at the
            // end of the message (none to a subagent's, or under a prompt card).
            web.bridge.dropTarget = { [weak s] in s?.composer }
            #endif
            web.bridge.onOpenDiff = openDiff
            web.bridge.onOpenImage = { session, ref, name, video in
                Task { @MainActor in
                    let image = video ? nil : await session.image(ref)
                    openAttachment(PreviewItem(key: ref, name: name) { await session.file(ref) },
                                   group: nil, image: image, video: video, open: { viewedImage = $0 })
                }
            }
            web.bridge.attach(s)
        }
    }
}

// MARK: - Items

/// Rendered text (parsed Markdown, highlighted code) by what it was made
/// from, so a row scrolling back into view or re-rendering skips the work.
enum RenderCache {
    private final class Box { let value: Any; init(_ value: Any) { self.value = value } }
    nonisolated(unsafe) private static let cache: NSCache<NSString, Box> = {
        let c = NSCache<NSString, Box>()
        c.countLimit = 3000
        return c
    }()

    static func get<T>(_ key: String, _ make: () -> T) -> T {
        if let v = cache.object(forKey: key as NSString)?.value as? T { return v }
        let v = make()
        cache.setObject(Box(v), forKey: key as NSString)
        return v
    }
}

public struct AgentItem: Identifiable, Equatable {
    public let id: String
    var block: AgentBlock?
    var tools: [AgentBlock] = []
    var thoughtSeconds: Int?

    /// Roughly how much there is to lay out (a run of tools shows a line
    /// each, closed), for how much of the conversation lays out in full.
    var textSize: Int { (block.map { $0.text.utf8.count + $0.title.utf8.count } ?? 0) + tools.count * 100 + 100 }

    /// A turn shorter than this ends without a "Worked for" line.
    static let doneShownFromMs: Int64 = 30_000

    static func group(_ blocks: [AgentBlock]) -> [AgentItem] {
        var out: [AgentItem] = []
        var run: [AgentBlock] = []
        // A run of tool calls is one row from its first call on, whatever
        // it grows to: its identity doesn't change when a second call joins
        // (which swapped one row for another, fading out and in).
        func flush() {
            if !run.isEmpty { out.append(AgentItem(id: "g:" + run[0].id, tools: run)) }
            run = []
        }
        for (i, b) in blocks.enumerated() {
            switch b.kind {
            case .tool where !b.diff.isEmpty || b.tool == "SendUserFile":
                // Edits stand on their own, so you see what changed (the diff
                // a click away) without opening a group of tool calls. With
                // the id it had as a run's first call (an edit has no diff
                // until it's made): the same row, laid out again where it is,
                // not a new one given an estimated height below the fold.
                flush()
                out.append(AgentItem(id: "g:" + b.id, block: b))
            case .tool:
                run.append(b)
            case .thinking where !b.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty:
                // Thinking with words is Claude's narration ("Committing
                // now."), shown in the terminal: it stands on its own, even
                // between tool calls.
                flush()
                out.append(AgentItem(id: b.id, block: b))
            case .done where b.durationMs < doneShownFromMs:
                // A quick reply doesn't need to say how long it took.
                continue
            case .thinking:
                // Silent thinking between tool calls folds into the run.
                if !run.isEmpty { continue }
                var item = AgentItem(id: b.id, block: b)
                if i + 1 < blocks.count, b.timeUnixMs > 0, blocks[i + 1].timeUnixMs > b.timeUnixMs {
                    item.thoughtSeconds = Int((blocks[i + 1].timeUnixMs - b.timeUnixMs) / 1000)
                }
                out.append(item)
            case .UNRECOGNIZED:
                // A kind from a newer daemon: no row, rather than an empty one.
                continue
            default:
                flush()
                out.append(AgentItem(id: b.id, block: b))
            }
        }
        flush()
        return out
    }
}

/// What a link in the agent view may do without asking.
public enum AgentLink {
    /// Web and mail links open straight away; other schemes can launch any
    /// app that claims them, so they ask first.
    public static func opensDirectly(_ url: URL) -> Bool {
        ["http", "https", "mailto"].contains(url.scheme?.lowercased() ?? "")
    }

    /// What clicking a link in a terminal may do. Anything a program prints
    /// can be a link (OSC 8 lets its text say one thing and go elsewhere),
    /// so only web and mail links open straight away.
    public enum Opening: Equatable, Sendable {
        /// http(s): the browser, or a Hammock browser pane.
        case web
        /// mailto: opens directly.
        case direct
        /// A file on this Mac (the path): opened unless it would run something.
        case file(String)
        /// A file on another computer (file://server/…), or no path: not
        /// opened. Opening it would reach out to that server, or open the
        /// same path on this Mac, which isn't the file the link means.
        case elsewhere
        /// Any other scheme: ask first.
        case ask
    }

    /// `localNames`: this machine's host names (localHostNames), which
    /// `ls`, `eza`, `rg` and the like put in their file links.
    public static func opening(_ url: URL, localNames: [String] = localHostNames()) -> Opening {
        switch url.scheme?.lowercased() ?? "" {
        case "http", "https": return .web
        case "mailto": return .direct
        case "file":
            let host = url.host(percentEncoded: false) ?? ""
            guard isLocalHost(host, localNames: localNames), !url.path.isEmpty else { return .elsewhere }
            return .file(url.path)
        default: return .ask
        }
    }

    /// A link in a terminal of a Group on another computer (shared with
    /// you, or another of yours): its programs run there, so a file link
    /// with no host, localhost or that computer's name (`hostNames`: what
    /// it's called, as Hammock knows it) is that computer's file, the path
    /// as it is there. Any other host, this Mac's too, is `.elsewhere`.
    public static func opening(_ url: URL, onHostNamed hostNames: [String]) -> Opening {
        guard url.scheme?.lowercased() == "file" else { return opening(url) }
        return opening(url, localNames: hostNames.flatMap(hostNameForms))
    }

    /// A computer's name as programs on it give it: as it is ("Studio"),
    /// and as macOS makes its hostname from it ("Sam's MacBook Pro" →
    /// "Sams-MacBook-Pro").
    static func hostNameForms(_ name: String) -> [String] {
        let name = name.trimmingCharacters(in: .whitespaces)
        var dashed = ""
        for ch in name where ch != "'" && ch != "\u{2019}" {
            if ch.isASCII, ch.isLetter || ch.isNumber {
                dashed.append(ch)
            } else if !dashed.isEmpty, !dashed.hasSuffix("-") {
                dashed.append("-")
            }
        }
        while dashed.hasSuffix("-") { dashed.removeLast() }
        return Array(Set([name, dashed])).filter { !$0.isEmpty }
    }

    /// Whether a file URL's host names this machine: empty, localhost, or
    /// one of its names. Programs send `gethostname()`, which may or may not
    /// carry .local (or the domain), so names also match by their first
    /// label, as the daemon does for a shell's working directory (OSC 7).
    static func isLocalHost(_ host: String, localNames: [String]) -> Bool {
        if host.isEmpty || host.caseInsensitiveCompare("localhost") == .orderedSame { return true }
        func first(_ s: String) -> Substring { s.prefix { $0 != "." } }
        return localNames.contains { name in
            !name.isEmpty && (host.caseInsensitiveCompare(name) == .orderedSame
                || first(host).caseInsensitiveCompare(first(name)) == .orderedSame)
        }
    }

    /// This Mac's host names: its hostname (`gethostname`, what terminal
    /// programs use) and its Bonjour name (Sharing's Local Hostname, with
    /// .local). Read on each click, since either can change with the
    /// network. None on the iPhone: a file link there is the Mac's, not its.
    public static func localHostNames() -> [String] {
        #if os(macOS)
        var names: [String] = []
        var buf = [CChar](repeating: 0, count: 256)
        if gethostname(&buf, buf.count) == 0 {
            names.append(String(decoding: buf.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self))
        }
        if let local = SCDynamicStoreCopyLocalHostName(nil) as String? { names.append(local + ".local") }
        return names.filter { !$0.isEmpty }
        #else
        return []
        #endif
    }

    /// Where the links in some text go, one per line, leaving out those whose
    /// text already shows it: shown on hover.
    static func destinations(_ text: AttributedString) -> String {
        var out: [String] = []
        for run in text.runs {
            guard let url = run.link else { continue }
            let dest = url.isFileURL ? url.path : url.absoluteString
            let shown = String(text[run.range].characters)
            if shown != dest, shown != url.absoluteString, !out.contains(dest) { out.append(dest) }
        }
        return out.joined(separator: "\n")
    }
}

// MARK: - Blocks

struct CodeBlock: View {
    let code: String
    var language = ""
    var maxHeight: CGFloat? = nil
    /// Wraps unless the setting says never, like prose: a tool's command,
    /// prompt or output, read in a pane too narrow for its long lines.
    var wrapsByDefault = false
    @Environment(\.agentFonts) private var fonts
    @State private var copied = false
    private var choices: CodeBlockChoices { .shared }

    nonisolated static func highlighted(_ code: String, _ language: String) -> AttributedString {
        RenderCache.get("hl|\(language)|" + code) { CodeHighlight.attributed(code, language: language) }
    }

    /// Languages that are words rather than code, which wrap unless the
    /// setting says otherwise. Unlabelled blocks don't: they're often trees
    /// and diagrams, which wrapping breaks.
    static let proseLanguages: Set<String> = ["markdown", "md", "text", "txt", "plain", "plaintext"]

    private var lang: String { language.lowercased() }
    private var key: CodeBlockChoices.Key { .init(language: lang, code: code) }

    private var wraps: Bool {
        if let w = choices.wrap[key] { return w }
        switch fonts.codeWrap {
        case "always": return true
        case "never": return false
        default: return wrapsByDefault || Self.proseLanguages.contains(lang)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 12) {
                Text(language.isEmpty ? "code" : language).font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button {
                    choices.wrap[key] = !wraps
                } label: {
                    Label("Wrap", systemImage: "arrow.turn.down.left").font(.caption)
                        .foregroundStyle(wraps ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                }
                .help(wraps ? "Don't wrap long lines" : "Wrap long lines")
                .accessibilityValue(wraps ? "On" : "Off")
                Button {
                    copy(code)
                    copied = true
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) { copied = false }
                } label: {
                    Label(copied ? "Copied" : "Copy", systemImage: copied ? "checkmark" : "doc.on.doc").font(.caption)
                }
            }
            .buttonStyle(.borderless)
            .padding(.horizontal, 12)
            .padding(.top, 8)
            content.frame(maxHeight: maxHeight ?? 420)
        }
        .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }

    @ViewBuilder private var content: some View {
        if wraps {
            ScrollView(.vertical, showsIndicators: true) {
                Text(Self.highlighted(code, language))
                    .font(fonts.code())
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
            }
            .defaultScrollAnchor(.top)
        } else {
            ScrollView([.horizontal, .vertical], showsIndicators: true) {
                Text(Self.highlighted(code, language))
                    .font(fonts.code())
                    .textSelection(.enabled)
                    .fixedSize()
                    .padding(12)
            }
            // Short lines start at the left, not centred, and don't take the
            // conversation's bottom anchor (inherited through the environment).
            .defaultScrollAnchor(.topLeading)
        }
    }
}

/// What you've switched on a code block (wrapped or not), kept by what the
/// block holds, until the app quits.
@MainActor @Observable final class CodeBlockChoices {
    static let shared = CodeBlockChoices()
    struct Key: Hashable { var language: String; var code: String }
    var wrap: [Key: Bool] = [:]
}

func copy(_ s: String) {
    #if canImport(AppKit)
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(s, forType: .string)
    #else
    UIPasteboard.general.string = s
    #endif
}

// MARK: Tools

private func toolIcon(_ b: AgentBlock) -> String {
    switch b.tool {
    case "Bash": return "terminal"
    case "Read": return "doc.text"
    case "Write": return "doc.badge.plus"
    case "Edit", "MultiEdit", "NotebookEdit": return "pencil"
    case "Grep", "Glob", "ToolSearch": return "magnifyingglass"
    case "WebFetch", "WebSearch": return "globe"
    case "TodoWrite": return "checklist"
    case "Skill": return "wand.and.stars"
    default: return b.tool.hasPrefix("mcp__") ? "puzzlepiece.extension" : "wrench.and.screwdriver"
    }
}

/// Opens a diff pane on the repository a file the agent changed is in,
/// beside its pane (the Mac's; nil where there's no such thing).
private struct AgentOpenDiffKey: EnvironmentKey { static let defaultValue: ((String) -> Void)? = nil }
extension EnvironmentValues {
    public var agentOpenDiff: ((String) -> Void)? {
        get { self[AgentOpenDiffKey.self] }
        set { self[AgentOpenDiffKey.self] = newValue }
    }
}

/// A picture open in the viewer.
struct ViewedImage: Identifiable {
    let ref: String
    let image: PlatformImage
    /// A video's file, to play (its first frame is image).
    var video: URL?
    var id: String { ref }
}

private struct OpenAgentImageKey: EnvironmentKey { static let defaultValue: ((ViewedImage) -> Void)? = nil }
private struct AgentImageZoomKey: EnvironmentKey { static let defaultValue: Namespace.ID? = nil }

extension EnvironmentValues {
    var openAgentImage: ((ViewedImage) -> Void)? {
        get { self[OpenAgentImageKey.self] }
        set { self[OpenAgentImageKey.self] = newValue }
    }
    /// The conversation's namespace for the viewer's zoom from a picture.
    var agentImageZoom: Namespace.ID? {
        get { self[AgentImageZoomKey.self] }
        set { self[AgentImageZoomKey.self] = newValue }
    }
}

#if os(iOS)
/// Marks a picture as where the viewer zooms from and back to.
struct ZoomSource: ViewModifier {
    let id: String
    let ns: Namespace.ID?
    init(id: String, in ns: Namespace.ID?) { self.id = id; self.ns = ns }
    func body(content: Content) -> some View {
        if let ns {
            content.matchedTransitionSource(id: id, in: ns) { $0.clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous)) }
        } else {
            content
        }
    }
}
#endif

// MARK: - The prompt on screen

struct PromptCard: View {
    @Environment(\.agentAccent) private var accent
    let session: AgentSession
    let onShowTerminal: () -> Void
    /// How tall it may grow before scrolling (most of the conversation's height).
    var maxHeight: CGFloat = 460
    /// Debug (PromptCardSample): open with the "other" option being typed.
    var initialOther: String?
    /// The card's own height, as laid out in full.
    @State private var natural: CGFloat = 0
    @State private var ticked: Set<Int> = []
    /// The "other" option's text, typed in its own row.
    @State private var otherText = ""
    @State private var typing = false
    /// A question with previews: the option picked here (shown with its
    /// preview, answered with Answer), and the notes going with it.
    @State private var chosen = 0
    @State private var notes = ""
    /// The option just answered with, for its row's spinner.
    @State private var tapped = 0
    /// When this question appeared: number keys only count a moment after.
    @State private var shownAt = PromptKey.now
    /// When the card took the keyboard, and when it last saw a key.
    @State private var keysTakenAt: TimeInterval = 0
    @State private var lastKeyAt: TimeInterval = 0
    /// When one of the card's fields last took the keyboard.
    @State private var fieldFocusedAt: TimeInterval = 0
    /// Something's been typed in that field since it took the keyboard.
    @State private var fieldEdited = false
    @FocusState private var focus: Field?
    /// The card itself has the keyboard (its number keys and arrows).
    @FocusState private var keysFocused: Bool
    @Environment(\.agentFonts) private var fonts

    private enum Field: Hashable { case other, notes }

    private var p: AgentPrompt { session.prompt }
    private var pending: AgentBlock? { session.blocks.last { $0.id == p.blockID } }

    /// The terminal's prompt above its question, when the transcript has no
    /// call to show (not yet written): a permission's tool, its command and
    /// any warning. Nil for a question of the agent's own (its options say).
    private var screenContext: String? {
        guard pending == nil, p.tabs.isEmpty, !p.multiSelect, p.descriptions.allSatisfy(\.isEmpty) else { return nil }
        var body = p.body.trimmingCharacters(in: .whitespacesAndNewlines)
        if !p.title.isEmpty, body.hasSuffix(p.title) { body = String(body.dropLast(p.title.count)).trimmingCharacters(in: .whitespacesAndNewlines) }
        return body.isEmpty ? nil : body
    }

    var body: some View {
        // As tall as the card is, up to most of the view; past that (full
        // descriptions can run to several lines each) it scrolls. An exact
        // height: left flexible, it split the space with the conversation.
        ScrollViewReader { proxy in
            ScrollView {
                card.onGeometryChange(for: CGFloat.self) { $0.size.height } action: { natural = $0 }
            }
            .scrollBounceBehavior(.basedOnSize)
            .scrollDismissesKeyboard(.never)
            .frame(height: natural > 0 ? min(natural, maxHeight) : nil)
            // Whatever's being typed stays in view: as its field appears, as
            // it grows a line, and as the keyboard (a shorter maxHeight)
            // comes up.
            .onChange(of: focus) { reveal(proxy) }
            .onChange(of: otherText) { reveal(proxy) }
            .onChange(of: notes) { reveal(proxy) }
            .onChange(of: natural) { reveal(proxy) }
            .onChange(of: maxHeight) { reveal(proxy) }
        }
        .clipShape(RoundedRectangle(cornerRadius: 24, style: .continuous))
        .agentGlass(in: RoundedRectangle(cornerRadius: 24, style: .continuous))
        .onChange(of: p.options, initial: true) { reset() }
        .onChange(of: session.answering) { _, on in if !on { tapped = 0 } }
        .sensoryFeedback(.selection, trigger: chosen)
        .disabled(session.answering)
        .modifier(PromptKeys(focused: $keysFocused, handle: key, take: { takeKeys() }, paneFocused: paneFocused))
        // Back from typing an answer or notes: the keys are the card's again.
        .onChange(of: focus) { old, new in
            if new != nil { fieldFocusedAt = PromptKey.now; fieldEdited = false }
            if old != nil, new == nil, !typing { takeKeys(force: true) }
        }
        // A new prompt with the same options is still a new prompt: its own
        // state, and its own moment before number keys count.
        .onChange(of: p.promptID) { reset(); takeKeys() }
        .onChange(of: typing) { _, on in if !on, focus == nil { takeKeys(force: true) } }
        .onChange(of: keysFocused) { _, on in if on { keysTakenAt = PromptKey.now } }
    }

    // MARK: Keys

    /// Whether this pane is the one you're in (the Mac's focused pane; on a
    /// phone, the one on screen).
    private var paneFocused: Bool {
        #if os(macOS)
        session.model?.focusedPaneID == session.paneID
        #else
        true
        #endif
    }

    /// Another live text field has the keyboard (on the Mac: one in the
    /// window, a sidebar rename, say), not the message box this card has
    /// just replaced, whose text view can still be first responder as it
    /// fades out.
    private var otherFieldFocused: Bool {
        #if os(macOS)
        if let t = NSApp.keyWindow?.firstResponder as? NSText, !(t is ComposerNSTextView) { return true }
        #endif
        return false
    }

    /// The card takes the keys (1–9, ←, →, Esc) by itself as it appears in
    /// the pane you're in: at once when nothing's been typed in the message
    /// box for a second (PromptKey.keyDelay), else once that second's
    /// passed; never while another text field has the keyboard (a click or
    /// Tab gives them to it then). `force`: back from its own fields.
    private func takeKeys(force: Bool = false) {
        guard focus == nil, paneFocused, !otherFieldFocused else { return }
        if !force, let wait = PromptKey.keyDelay(lastEdit: session.composerEditedAt) {
            let prompt = p.promptID
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(wait))
                // Still this question, nothing typed meanwhile.
                if p.promptID == prompt, PromptKey.keyDelay(lastEdit: session.composerEditedAt) == nil { takeKeys(force: true) }
            }
            return
        }
        DispatchQueue.main.async { if focus == nil, !otherFieldFocused { keysFocused = true } }
    }

    /// A key pressed with the card focused: 1–9 picks that option (as a
    /// click does), ← and → move between questions (its chevrons), Esc
    /// switches to the terminal (or stops typing an answer).
    private func key(_ press: KeyPress) -> KeyPress.Result {
        guard focus == nil else { return .ignored }
        // Every key the card sees counts as typing, ignored ones too: a
        // digit only picks after a second's quiet (PromptKey.acceptsPick).
        let quiet = PromptKey.acceptsPick(shownAt: shownAt, keysTakenAt: keysTakenAt, lastKeyAt: lastKeyAt)
        lastKeyAt = PromptKey.now
        // Return, or the keypad's Enter.
        let k = PromptKey.key(press.key, characters: press.characters)
        // A held key's repeats count as typing, but only its first press acts.
        if press.phase == .repeat { return k == .left || k == .right ? .ignored : .handled }
        // Tab and ⇧Tab move on only after the quiet: else into the notes
        // field, whose Return would answer at once.
        if k == .tab { return PromptKey.tabPasses(quiet: quiet) ? .ignored : .handled }
        switch PromptKey.action(characters: press.characters, key: k,
                                hasModifiers: !press.modifiers.subtracting([.shift, .numericPad, .function, .capsLock]).isEmpty,
                                options: p.options.count, multipleQuestions: questionPosition != nil) {
        case .pick(let n):
            // Not a key typed for something else as the card came, or mid-sentence.
            guard !session.answering, quiet else { return .handled }
            pick(n)
        case .previous:
            if let (i, _) = questionPosition, i > 1 { previousQuestion() }
        case .next: nextQuestion()
        case .escape:
            // Not before the quiet either: the terminal would take the next
            // "2" or Return for the agent's own prompt there.
            guard quiet else { return .handled }
            if typing { typing = false; otherText = "" } else { onShowTerminal() }
        case .submit:
            // Return answers as Answer or Continue would: after the same quiet
            // ("…tests⏎" typed as the card came doesn't answer option 1).
            guard !session.answering, quiet else { return .handled }
            if p.notes { sendChosen() } else if p.multiSelect { sendTicked() }
        case nil: return .ignored
        }
        return .handled
    }

    private func previousQuestion() { session.model?.send(session.paneID, Data("\u{1b}[D".utf8)) }
    private func nextQuestion() { session.model?.send(session.paneID, Data("\u{1b}[C".utf8)) }

    private func reset() {
        shownAt = PromptKey.now
        ticked = Set(p.checked.enumerated().filter(\.element).map { $0.offset + 1 })
        typing = false
        otherText = ""
        notes = ""
        chosen = p.selected > 0 ? Int(p.selected) : 1
        focus = nil
        if let initialOther {
            typing = true
            otherText = initialOther
        }
    }

    private func reveal(_ proxy: ScrollViewProxy) {
        guard let f = focus else { return }
        // After this layout pass, so the field's new height counts.
        DispatchQueue.main.async {
            withAnimation(.snappy(duration: 0.2)) { proxy.scrollTo(f, anchor: .bottom) }
        }
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 6) {
                Image(systemName: icon).foregroundStyle(tint)
                Text(heading).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                if let (i, n) = questionPosition, p.review.isEmpty { Text("· \(i) of \(n)").font(.caption).foregroundStyle(.tertiary) }
                Spacer()
                if let (i, _) = questionPosition {
                    // Through the questions, as ← and → do in Claude's form
                    // (past the last is its Submit step).
                    HStack(spacing: 2) {
                        Button(action: previousQuestion) { Image(systemName: "chevron.left").frame(width: 28, height: 24) }
                            .disabled(i <= 1)
                            .help("Previous question (←)")
                            .accessibilityLabel("Previous question")
                        Button(action: nextQuestion) { Image(systemName: "chevron.right").frame(width: 28, height: 24) }
                            .disabled(!p.review.isEmpty) // the Submit step is the last
                            .help("Next question (→)")
                            .accessibilityLabel("Next question")
                    }
                    .buttonStyle(.borderless)
                    .font(.callout.weight(.semibold))
                }
            }
            if let b = pending, p.kind == .permission, !b.input.isEmpty {
                CodeBlock(code: b.input, language: b.tool == "Bash" ? "bash" : "", maxHeight: 140, wrapsByDefault: true)
            } else if let context = screenContext {
                // The call isn't in the transcript yet: what the terminal's prompt says about it.
                Text(context)
                    .font(fonts.code(0.88))
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
                    .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            }
            if !p.title.isEmpty {
                Text(p.title).font(fonts.body(weight: .semibold)).fixedSize(horizontal: false, vertical: true)
            }
            if !p.review.isEmpty { reviewList }
            VStack(spacing: 6) {
                ForEach(Array(p.options.enumerated()), id: \.offset) { i, label in
                    if i + 1 == Int(p.other), typing {
                        otherField(i + 1)
                    } else {
                        optionButton(i + 1, label)
                    }
                }
            }
            if p.notes { previewAndNotes }
            if p.multiSelect {
                HStack {
                    Spacer()
                    // Return goes through the card's keys (after a second's quiet),
                    // not the window's default button.
                    Button("Continue", action: sendTicked)
                        .prominentButton()
                        .modifier(OutOfKeyFocus(on: true))
                }
            }
            if let err = session.answerError {
                HStack {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(err).font(.caption)
                    Spacer()
                    Button("Answer in Terminal") { session.answerError = nil; onShowTerminal() }
                        .modifier(OutOfKeyFocus(on: true))
                }
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var heading: String {
        switch p.kind {
        case .permission: return pending.map { "\(session.agentName) wants to: " + $0.title } ?? "\(session.agentName) needs permission"
        case .plan: return "Approve the plan?"
        default: return "\(session.agentName) is asking"
        }
    }

    private var icon: String {
        switch p.kind {
        case .permission: return "lock.shield"
        case .plan: return "list.bullet.clipboard"
        default: return "questionmark.bubble.fill"
        }
    }

    private var tint: Color { p.kind == .permission ? .orange : p.kind == .plan ? .purple : accent }

    /// Which of several questions this is: the one shown, found among the
    /// question call's, else guessed from the tab bar ("←  ☒ Colour  ☐ Fruits
    /// ✔ Submit  →") as the first unanswered. Nil for a single question.
    private var questionPosition: (Int, Int)? {
        guard !p.tabs.isEmpty else { return nil }
        if let qs = pending?.questions, qs.count > 1, let i = qs.firstIndex(where: { $0.question == p.title }) {
            return (i + 1, qs.count)
        }
        let done = p.tabs.filter { $0 == "☒" }.count
        let total = p.tabs.filter { $0 == "☒" || $0 == "☐" }.count
        return total > 1 ? (min(done + 1, total), total) : nil
    }

    /// A label without Claude's "(Recommended)" at its end, and whether it had it.
    private static func recommended(_ label: String) -> (String, Bool) {
        for suffix in [" (Recommended)", " (recommended)"] where label.hasSuffix(suffix) {
            return (String(label.dropLast(suffix.count)), true)
        }
        return (label, false)
    }

    @ViewBuilder
    private func optionButton(_ n: Int, _ label: String) -> some View {
        let desc = n - 1 < p.descriptions.count ? p.descriptions[n - 1] : ""
        let isOther = n == Int(p.other)
        let isBox = isCheckbox(n) && !isOther
        let picked = p.notes && n == chosen
        let (text, isRecommended) = Self.recommended(label)
        Button { pick(n) } label: {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                if isBox {
                    Image(systemName: ticked.contains(n) ? "checkmark.square.fill" : "square").foregroundStyle(accent)
                } else if isOther {
                    Image(systemName: "pencil").font(.caption.weight(.semibold))
                        .frame(width: 20, height: 20)
                        .background(Circle().fill(.quaternary))
                } else {
                    numberBadge(n, filled: picked)
                }
                VStack(alignment: .leading, spacing: 3) {
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(text).font(fonts.body()).multilineTextAlignment(.leading)
                        if isRecommended { recommendedBadge }
                    }
                    if !desc.isEmpty, desc != label {
                        Text(desc).font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.leading)
                    }
                }
                Spacer(minLength: 0)
                if tapped == n, session.answering { ProgressView().controlSize(.small) }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
            .background(rowBackground(highlighted: picked))
        }
        .buttonStyle(.plain)
        // Not reachable by Tab and Space (Full Keyboard Access): those
        // would answer past the quiet rule.
        .modifier(OutOfKeyFocus(on: true))
    }

    /// Option n chosen, by a click or its number key.
    private func pick(_ n: Int) {
        let isOther = n == Int(p.other)
        if isOther {
            typing = true
            focus = .other
        } else if isCheckbox(n) {
            if ticked.contains(n) { ticked.remove(n) } else { ticked.insert(n) }
        } else if p.notes {
            chosen = n // answered with Answer, under its preview
        } else {
            tapped = n
            Task { await session.answer(option: n) }
        }
    }

    private var recommendedBadge: some View {
        Text("Recommended").font(.caption2.weight(.semibold)).foregroundStyle(accent)
            .padding(.horizontal, 6).padding(.vertical, 1)
            .background(Capsule().fill(accent.opacity(0.15)))
    }

    /// The question tool's Submit step: each question, with the answer it
    /// was given under it, apart from the next.
    private var reviewList: some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(p.review.enumerated()), id: \.offset) { i, item in
                if i > 0 { Divider().padding(.leading, 12) }
                reviewRow(item)
            }
        }
        .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(.quaternary.opacity(0.35)))
    }

    private func reviewRow(_ item: Hammock_V1_AgentReviewItem) -> some View {
        let (answer, isRecommended) = Self.recommended(item.answer)
        return VStack(alignment: .leading, spacing: 4) {
            Text(item.question).font(fonts.body(0.86)).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                if answer.isEmpty {
                    Image(systemName: "exclamationmark.circle").foregroundStyle(.orange)
                    Text("Not answered").font(fonts.body()).foregroundStyle(.orange)
                } else {
                    Image(systemName: "checkmark").font(.caption.weight(.bold)).foregroundStyle(accent)
                    Text(answer).font(fonts.body(weight: .semibold)).fixedSize(horizontal: false, vertical: true)
                    if isRecommended { recommendedBadge }
                }
            }
        }
        .textSelection(.enabled)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
    }

    private func numberBadge(_ n: Int, filled: Bool) -> some View {
        Text("\(n)").font(.caption.monospacedDigit().weight(.semibold))
            .foregroundStyle(filled ? AnyShapeStyle(.white) : AnyShapeStyle(.primary))
            .frame(width: 20, height: 20)
            .background(Circle().fill(filled ? AnyShapeStyle(accent) : AnyShapeStyle(.quaternary)))
    }

    private func rowBackground(highlighted: Bool) -> some View {
        RoundedRectangle(cornerRadius: 10, style: .continuous)
            .fill(highlighted ? AnyShapeStyle(accent.opacity(0.14)) : AnyShapeStyle(.quaternary.opacity(0.6)))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(accent, lineWidth: highlighted ? 1.5 : 0))
    }

    /// The "other" option, become the field you type your answer in, where
    /// it was: focused at once, growing a line at a time, sent with Return
    /// or its button (the agent takes one line).
    private func otherField(_ n: Int) -> some View {
        let empty = otherText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        return HStack(alignment: .bottom, spacing: 8) {
            TextField(p.kind == .plan ? "What should \(session.agentName) change?" : "Your answer", text: $otherText, axis: .vertical)
                .font(fonts.body())
                .lineLimit(1...6)
                .textFieldStyle(.plain)
                .focused($focus, equals: .other)
                .submitLabel(.send)
                .onSubmit { sendOther(fromKeys: true) }
                .onChange(of: otherText) { _, t in
                    // Return in a field that grows: send, not a new line.
                    if !t.contains("\n") { fieldEdited = true }
                    if t.contains("\n") {
                        otherText = t.replacingOccurrences(of: "\n", with: "")
                        sendOther(fromKeys: true)
                    }
                }
                .padding(.vertical, 2)
            Button {
                if empty { typing = false; focus = nil } else { sendOther() }
            } label: {
                Image(systemName: empty ? "xmark.circle.fill" : "arrow.up.circle.fill")
                    .font(.title2)
                    .foregroundStyle(empty ? AnyShapeStyle(.secondary) : AnyShapeStyle(accent))
                    .contentTransition(.symbolEffect(.replace))
            }
            .buttonStyle(.plain)
            // Not by Tab and Space: that would send past the field's rule.
            .modifier(OutOfKeyFocus(on: true))
            .help(empty ? "Cancel" : "Send")
            .keyboardShortcut(empty ? .cancelAction : nil) // (Return is the field's own onSubmit)
        }
        .padding(.leading, 12)
        .padding(.trailing, 8)
        .padding(.vertical, 8)
        .background(rowBackground(highlighted: true))
        .id(Field.other)
        .onAppear { focus = .other }
    }

    /// A question with previews: the picked option's preview (a mockup,
    /// monospaced, as Claude shows it beside the list), notes to go with
    /// it, and Answer.
    @ViewBuilder
    private var previewAndNotes: some View {
        let preview = chosen >= 1 && chosen <= p.previews.count ? p.previews[chosen - 1] : ""
        if !preview.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                Text(preview)
                    .font(.system(.footnote, design: .monospaced))
                    .fixedSize()
                    .textSelection(.enabled)
                    .padding(12)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(.black.opacity(0.18)))
            .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(.quaternary))
            .animation(nil, value: chosen)
        }
        HStack(alignment: .bottom, spacing: 8) {
            TextField("Notes (optional)", text: $notes, axis: .vertical)
                .font(fonts.body())
                .lineLimit(1...5)
                .textFieldStyle(.plain)
                .focused($focus, equals: .notes)
                .submitLabel(.send)
                .onSubmit { sendChosen(fromKeys: true) }
                .onChange(of: notes) { _, t in
                    if !t.contains("\n") { fieldEdited = true }
                    if t.contains("\n") {
                        notes = t.replacingOccurrences(of: "\n", with: "")
                        sendChosen(fromKeys: true)
                    }
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 9)
                .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(.quaternary.opacity(0.6)))
                .id(Field.notes)
            Button {
                sendChosen()
            } label: {
                HStack(spacing: 6) {
                    if session.answering { ProgressView().controlSize(.small) }
                    Text("Answer \(chosen)")
                }
            }
            .prominentButton()
            .modifier(OutOfKeyFocus(on: true)) // Return: the card's keys, after the quiet
            .disabled(chosen < 1)
        }
    }

    /// Return in one of the card's fields answers only a second after the
    /// field took the keyboard and after the card's last key: a Tab into it
    /// then Return doesn't; a click in, a note typed and Return does.
    private var fieldQuiet: Bool {
        PromptKey.fieldSubmits(edited: fieldEdited,
                               quiet: PromptKey.acceptsPick(shownAt: shownAt, keysTakenAt: keysTakenAt, lastKeyAt: lastKeyAt, fieldFocusedAt: fieldFocusedAt))
    }

    private func sendChosen(fromKeys: Bool = false) {
        guard chosen >= 1, !session.answering, !fromKeys || fieldQuiet else { return }
        let n = notes.trimmingCharacters(in: .whitespacesAndNewlines)
        focus = nil
        Task { await session.answer(option: chosen, notes: n) }
    }

    private func sendOther(fromKeys: Bool = false) {
        let t = otherText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty, !session.answering, !fromKeys || fieldQuiet else { return }
        if p.multiSelect { return sendTicked() } // with the ticked options
        tapped = Int(p.other)
        focus = nil
        Task { await session.answer(option: Int(p.other), text: t) }
    }

    /// Multi-select's Continue: the ticked options, and what's typed in the
    /// "other" option's field (ticked along with them).
    private func sendTicked() {
        guard !session.answering else { return }
        let t = typing ? otherText.trimmingCharacters(in: .whitespacesAndNewlines) : ""
        focus = nil
        Task { await session.answer(ticking: ticked.sorted(), text: t) }
    }

    /// In a multi-select question, whether option n is a checkbox: the
    /// options after them ("Chat about this") answer at once. (A daemon that
    /// doesn't say: all of them.)
    private func isCheckbox(_ n: Int) -> Bool {
        p.multiSelect && (p.checkboxes == 0 || n <= Int(p.checkboxes))
    }
}

/// What a key does on the prompt card (PromptCard's keys), apart from the
/// view so it can be tested.
enum PromptKey: Equatable {
    case pick(Int), previous, next, escape, submit

    enum Key { case escape, left, right, submit, tab, other }

    /// A key as the card reads it: Return or the keypad's Enter ("\u{3}")
    /// submits; Tab, and ⇧Tab (which the Mac sends as back-tab, "\u{19}"),
    /// are Tab.
    static func key(_ k: KeyEquivalent, characters: String) -> Key {
        switch k {
        case .escape: return .escape
        case .leftArrow: return .left
        case .rightArrow: return .right
        case .return: return .submit
        case .tab: return .tab
        default:
            if characters == "\u{3}" { return .submit }
            if characters == "\u{19}" || characters == "\t" { return .tab }
            return .other
        }
    }

    /// A field's Return or Send answers once something's been typed in the
    /// field since it took the keyboard (that's deliberate), or else after
    /// the quiet (not a Tab in and Return straight after).
    static func fieldSubmits(edited: Bool, quiet: Bool) -> Bool { edited || quiet }

    /// Tab (and ⇧Tab) moves focus on from the card only after the quiet:
    /// before it, it's taken (and counts as a key).
    static func tabPasses(quiet: Bool) -> Bool { quiet }

    /// The clock its times are on: monotonic (seconds since boot), so a wall
    /// clock jumping can't let a key through or block them; a test sets it.
    nonisolated(unsafe) static var clock: () -> TimeInterval = { ProcessInfo.processInfo.systemUptime }
    static var now: TimeInterval { clock() }

    /// How quiet the keyboard must have been for a key to answer.
    static let settle: TimeInterval = 1

    /// A key that answers (a number, Return) does only after a second's
    /// quiet: since the card appeared, since it took the keyboard, and since
    /// the last key it saw (any key, repeats too: someone still typing a
    /// sentence, "add 2 tests", keeps putting it off). Times on `clock`.
    /// `fieldFocusedAt`: when one of the card's fields (an answer, notes)
    /// took the keyboard, for its Return: not straight after a Tab into it.
    static func acceptsPick(now: TimeInterval = PromptKey.now, shownAt: TimeInterval, keysTakenAt: TimeInterval, lastKeyAt: TimeInterval,
                            fieldFocusedAt: TimeInterval = 0) -> Bool {
        now - max(shownAt, keysTakenAt, lastKeyAt, fieldFocusedAt) >= settle
    }

    /// How long typing in the message box keeps the card from taking keys.
    static let typingWindow: TimeInterval = 1

    /// How long to wait before the card takes the keyboard: nil, now
    /// (nothing typed in the message box for a second); else what's left
    /// of that second, after which it tries again. Times on `clock`.
    static func keyDelay(lastEdit: TimeInterval?, now: TimeInterval = PromptKey.now) -> TimeInterval? {
        guard let lastEdit else { return nil }
        let left = typingWindow - (now - lastEdit)
        return left > 0 ? left : nil
    }

    /// 1–9 picks that option (when there is one); ← and → move between
    /// questions, when there are several; Return submits (Answer,
    /// Continue); Esc is Esc. Nothing with ⌘, ⌥ or ⌃ held: those are the
    /// menus'.
    static func action(characters: String, key: Key, hasModifiers: Bool, options: Int, multipleQuestions: Bool) -> PromptKey? {
        guard !hasModifiers else { return nil }
        switch key {
        case .escape: return .escape
        case .submit: return .submit
        case .tab: return nil
        case .left: return multipleQuestions ? .previous : nil
        case .right: return multipleQuestions ? .next : nil
        case .other: break
        }
        guard characters.count == 1, let n = Int(characters), (1...9).contains(n), n <= options else { return nil }
        return .pick(n)
    }
}

/// Kept out of keyboard focus (Tab, Full Keyboard Access) while `on`: a
/// prompt card's buttons, the whole time it shows (Tab out of the card and
/// back would reach them otherwise), its own keys (1–9, Return, Esc, ←, →)
/// answering for them after the quiet. A click still works.
struct OutOfKeyFocus: ViewModifier {
    let on: Bool
    func body(content: Content) -> some View {
        if on { content.focusable(false) } else { content }
    }
}

/// The prompt card's keyboard: focusable (with no ring drawn), its keys
/// handled, and focused when it shows in the pane you're in.
private struct PromptKeys: ViewModifier {
    var focused: FocusState<Bool>.Binding
    let handle: (KeyPress) -> KeyPress.Result
    let take: () -> Void
    let paneFocused: Bool

    func body(content: Content) -> some View {
        content
            .focusable()
            .focusEffectDisabled()
            .focused(focused)
            .onKeyPress(phases: [.down, .repeat], action: handle)
            .onAppear(perform: take)
            .onChange(of: paneFocused) { _, on in if on { take() } }
    }
}

/// Debug: the prompt card with a made-up question and no daemon
/// (HMK_DEBUG_PROMPT=plain|other|preview|tasks|review, DebugSnapshot): plain,
/// one with its "Type something" option being typed, one with previews, the
/// plain one under a task list and a session's links, or the Submit step's
/// review of the answers.
public struct PromptCardSample: View {
    let session: AgentSession
    let typing: String?
    let extras: Bool

    public init(model: AppModel, kind: String) {
        session = AgentSession(paneID: "pn_debug", model: model)
        extras = kind == "tasks"
        typing = kind == "other" ? "Keep the old API working for a release, behind a flag, and say so in the release notes" : nil
        var v = Hammock_V1_AgentView()
        v.replace = true
        v.prompt = .with {
            $0.kind = .question
            $0.tabs = "←  ☐ Shortcuts  ☐ Vim  ✔ Submit  →"
            if kind == "review" {
                $0.title = "Review your answers"
                $0.options = ["Submit answers", "Cancel"]
                $0.descriptions = ["", ""]
                $0.selected = 1
                $0.review = [
                    .with { $0.question = "Should the old session API keep working?"; $0.answer = "Remove it now (Recommended)" },
                    .with { $0.question = "How should tablets lay out the Groups tab? Material 3's adaptive scaffolds can switch between a bottom bar and a rail, and list and detail, by window size."; $0.answer = "Adaptive scaffold, with list and detail side by side" },
                    .with { $0.question = "Which tests should run in CI?" },
                ]
            } else if kind == "preview" {
                $0.title = "Shortcuts for editor panes (each can be changed in Settings ▸ Keyboard). Which set?"
                $0.options = ["⌘P quick-open, ⌘O open panel (Recommended)", "⌘O quick-open, ⇧⌘O panel"]
                $0.descriptions = ["", ""]
                $0.selected = 1
                $0.notes = true
                $0.previews = [
                    "┌───────────────────────────────┐\n│ Quick Open…          ⌘P       │\n│ Open File…           ⌘O       │\n│ New Editor Tab       ⌥⌘E      │\n│ Split Right w/ Editor ⌃⌘E     │\n│ Save                 ⌘S       │\n└───────────────────────────────┘",
                    "┌───────────────────────────────┐\n│ Quick Open…          ⌘O       │\n│ Open File…           ⇧⌘O      │\n│ Save                 ⌘S       │\n└───────────────────────────────┘",
                ]
            } else {
                $0.title = "Should the old session API keep working?"
                $0.options = ["Remove it now (Recommended)", "Deprecate it for a release", "Type something."]
                $0.descriptions = ["Nothing outside this repo uses it.", "Log a warning when it's called, remove it next release.", ""]
                $0.other = 3
            }
        }
        if extras {
            v.tasks = .with {
                $0.tasks = [
                    .with { $0.id = "1"; $0.subject = "Read the spec"; $0.status = .completed },
                    .with { $0.id = "2"; $0.subject = "Write the parser"; $0.activeForm = "Writing the parser"; $0.status = .inProgress },
                    .with { $0.id = "3"; $0.subject = "Add tests for the task list and the links"; $0.status = .pending },
                ]
            }
            v.links = .with {
                $0.links = [
                    .with { $0.kind = .pullRequest; $0.url = "https://github.com/homegrownsw/hammock/pull/131"; $0.number = 131; $0.repo = "homegrownsw/hammock"; $0.title = "Agent view: task lists and links"; $0.state = "OPEN" },
                    .with { $0.kind = .artifact; $0.url = "https://claude.ai/artifact/x"; $0.title = "Weekly Signups"; $0.icon = "chart" },
                ]
            }
        }
        session.apply(v)
    }

    public var body: some View {
        VStack(spacing: 10) {
            if extras { SessionExtras(session: session, initiallyExpanded: true) }
            PromptCard(session: session, onShowTerminal: {}, initialOther: typing)
        }
        .padding(16)
    }
}

// MARK: - Composer

/// The "/command" being typed at the cursor, anywhere in a message (Claude
/// Code runs its skills and your commands mid-line too, though not its
/// built-ins): from a "/" at the start or after a space or
/// line break, up to the cursor, with nothing but one of those or the end
/// after it.
enum SlashToken {
    /// With marker "@", the "@file" being typed there instead.
    static func at(_ cursor: String.Index, in text: String, marker: Character = "/") -> Range<String.Index>? {
        guard cursor >= text.startIndex, cursor <= text.endIndex else { return nil }
        if cursor < text.endIndex, !text[cursor].isWhitespace { return nil }
        var start = cursor
        while start > text.startIndex, !text[text.index(before: start)].isWhitespace { start = text.index(before: start) }
        guard start < cursor, text[start] == marker else { return nil }
        return start..<cursor
    }

    /// How well a command matches what's typed after its "/" (lower is
    /// better; nil: not at all): it starts with it, then a part of its name
    /// does (after ":", "-" or "_": "/pr" for "/review-pr"), then it has it
    /// anywhere, then its letters in order ("/rvpr").
    static func rank(_ query: String, _ command: String) -> Int? {
        let q = query.lowercased().drop { $0 == "/" }
        let name = command.lowercased().drop { $0 == "/" }
        if q.isEmpty || name.hasPrefix(q) { return 0 }
        if name.split(whereSeparator: { ":-_".contains($0) }).dropFirst().contains(where: { $0.hasPrefix(q) }) { return 1 }
        if name.contains(q) { return 2 }
        var rest = Substring(name)
        for ch in q {
            guard let i = rest.firstIndex(of: ch) else { return nil }
            rest = rest[rest.index(after: i)...]
        }
        return 3
    }

    /// The token starts the message: nothing but spaces and line breaks
    /// before it. Agents' built-in commands only run there.
    static func startsMessage(_ token: Range<String.Index>, in text: String) -> Bool {
        text[..<token.lowerBound].allSatisfy(\.isWhitespace)
    }

    /// The commands to offer for the token: up to eight that match it, best
    /// first (rank; those it begins first, in their order), leaving out
    /// those that only run at the start of a message (built-ins, sent as
    /// text later in one) when it isn't there.
    static func suggestions(for token: Range<String.Index>, in text: String, from commands: [String], startOnly: Set<String>) -> [String] {
        let q = String(text[token])
        let skip = startsMessage(token, in: text) ? [] : startOnly
        let ranked = commands.enumerated().compactMap { i, c -> (Int, Int, String)? in
            guard !skip.contains(c), let r = rank(q, c) else { return nil }
            return (r, i, c)
        }
        return Array(ranked.sorted { ($0.0, $0.1) < ($1.0, $1.1) }.prefix(8).map(\.2))
    }

    /// The commands in the text it knows, to colour as Claude Code does
    /// (UTF-16 ranges): each a word of its own, those that only run at the
    /// start of a message only there.
    static func highlights(in text: String, commands: Set<String>, startOnly: Set<String>) -> [NSRange] {
        guard text.contains("/"), !commands.isEmpty else { return [] }
        let ns = text as NSString
        var out: [NSRange] = []
        for m in commandWord.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            let word = ns.substring(with: m.range)
            guard commands.contains(word) || commands.contains(word.lowercased()) else { continue }
            if startOnly.contains(word) {
                let before = ns.substring(to: m.range.location)
                guard before.allSatisfy(\.isWhitespace) else { continue }
            }
            out.append(m.range)
        }
        return out
    }

    private static let commandWord = try! NSRegularExpression(pattern: "(?<![^\\s])/[^\\s\u{FFFC}]+")

    /// The text with that token replaced by a command and a space, and where
    /// the cursor goes: after the space.
    static func complete(_ token: Range<String.Index>, with command: String, in text: String) -> (text: String, cursor: String.Index) {
        var t = text
        // Already a space after it: don't add another, step over it. (Not a
        // line break: the cursor would go to the next line.)
        let spaced = text[token.upperBound...].first.map { $0 == " " || $0 == "\t" } ?? false
        t.replaceSubrange(token, with: spaced ? command : command + " ")
        let offset = text.distance(from: text.startIndex, to: token.lowerBound) + command.count + 1
        return (t, t.index(t.startIndex, offsetBy: offset))
    }
}

/// A row offered over the composer as you type: a "/command", or a file
/// for an "@" mention.
struct ComposerSuggestion: Hashable {
    /// What replaces the token.
    var insert: String
    var label: String
    /// A file's folder.
    var detail = ""
    var file = false

    /// Whether Return takes the highlighted suggestion: only once it's been
    /// moved with ↑ or ↓ (else Return sends, or on a phone starts a line).
    static func returnTakes(suggesting: Bool, moved: Bool) -> Bool { suggesting && moved }

    /// A file as an "@" mention: quoted when it has a space in it.
    static func mention(_ path: String) -> String {
        path.contains(where: \.isWhitespace) ? "@\"" + path + "\"" : "@" + path
    }

    /// A file the daemon listed (relative to folder) as the agent would
    /// name it: relative to its own folder (cwd) when it's in there, else
    /// in full.
    static func path(_ p: String, folder: String, cwd: String) -> String {
        let full = folder.isEmpty || p.hasPrefix("/") ? p : (folder as NSString).appendingPathComponent(p)
        let base = cwd.hasSuffix("/") ? cwd : cwd + "/"
        if !cwd.isEmpty, full.hasPrefix(base) { return String(full.dropFirst(base.count)) }
        return full
    }
}

/// The files offered for an "@" being typed, and what they were looked up for.
struct MentionFiles: Equatable {
    /// Whose: a pane's files never show in another's.
    var pane = ""
    var query: String?
    var paths: [String] = []

    /// Whether these show for what's typed after "@" now: this pane's, for
    /// that, or for the start of it while the lookup for the rest is on its
    /// way (not those for another word: Tab would take one).
    func shows(pane: String, query q: String) -> Bool {
        guard pane == self.pane, let query else { return false }
        return q.hasPrefix(query)
    }
}

/// Calls take when what your other devices have in the session's box
/// changes, or the daemon first says (a modifier of its own: the
/// composer's body is long enough for the type checker as it is).
private struct OnSharedDraft: ViewModifier {
    let session: AgentSession
    let take: () -> Void
    func body(content: Content) -> some View {
        content
            .onChange(of: session.sharedDraft) { take() }
            .onChange(of: session.heard) { take() }
    }
}

struct Composer: View {
    @Environment(\.agentBubble) private var bubble
    @Environment(\.agentAccent) private var accent
    @Environment(\.colorScheme) private var scheme
    let session: AgentSession
    /// claude or codex, for its model switcher.
    var harness = "claude"
    /// Typed already (ComposerSample), in place of the saved draft.
    var initialText: String?
    /// Attached already (ComposerSample): "{n}" in initialText is the nth's
    /// chip; those without one are greyed out.
    var initialAttachments: [(name: String, data: Data)] = []
    /// initialText as if being dictated (ComposerSample, HMK_DEBUG_DICTATION).
    var debugDictating = false
    /// The text, with a mark where each attachment's chip is.
    @State private var doc = ComposerDoc()
    /// Where the cursor is in it (UTF-16), for completing the "/command"
    /// there; nil while a range is selected.
    @State private var cursor: Int?
    /// Everything attached, in the tray: those whose chip is in the text
    /// go; the others are greyed out, to put back.
    @State private var attachments: [ComposerAttachment] = []
    @State private var thumbs: [UUID: PlatformImage] = [:]
    @State private var controller = ComposerTextController()
    @State private var photos: [PhotosPickerItem] = []
    @State private var importing = false
    @State private var pickingPhotos = false
    @State private var attachError: String?
    /// What was last taken from the terminal's input box into this one (kept
    /// in step while it's unedited), and whether a send is waiting for the
    /// terminal's box to empty.
    @State private var tookDraft = ""
    @State private var awaitingClear = false
    /// This box, to your other devices (SetComposerDraft's writer), and
    /// what it last had in step with them: sent, or taken from them (nil:
    /// not yet, for this pane).
    @State private var boxID = UUID().uuidString
    @State private var synced: String?
    /// What was just sent, and until when anything of it coming back (the
    /// keyboard committing an autocorrection, the terminal briefly holding
    /// the message as it's typed there) is a ghost, not something you typed.
    @State private var justSent: (text: String, until: Date)?
    /// A video being made smaller to send.
    @State private var shrinking = 0
    @State private var focused = false
    /// Words going in as you say them (the mic button).
    @State private var dictation = ComposerDictation()
    /// Stepping back through what's been sent (↑ and ↓).
    @State private var history = ComposerHistory<ComposerDoc>()
    /// The suggestion highlighted, which Esc put away (by its token), and
    /// the files found for an "@".
    @State private var picked = 0
    @State private var dismissedToken: String?
    @State private var mentionFiles = MentionFiles()
    /// The highlight was moved with ↑ or ↓ (Return then takes it).
    @State private var pickedByKey = false
    /// An input method is composing a word in the box.
    @State private var composing = false
    @Environment(\.agentFonts) private var fonts
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.openAgentImage) private var open
    @Environment(\.agentImageZoom) private var zoom

    /// What you've typed but not sent, kept per pane: through leaving the
    /// screen, and the app quitting or being killed.
    private var draftKey: String { "agentDraft." + session.paneID }

    private var text: String { doc.text }

    /// The "/command" being typed: at the cursor, or the end before the box
    /// has one.
    private var slashToken: Range<String.Index>? { token("/") }

    /// The "@file" being typed (agents that take file mentions).
    private var mentionToken: Range<String.Index>? {
        AgentCatalog.takesFileMentions(harness) ? token("@") : nil
    }

    private func token(_ marker: Character) -> Range<String.Index>? {
        var at = text.endIndex
        if let cursor {
            guard cursor <= text.utf16.count, let i = String.Index(String.Index(utf16Offset: cursor, in: text), within: text) else { return nil }
            at = i
        }
        return SlashToken.at(at, in: text, marker: marker)
    }

    /// What's offered for the token being typed: commands for a "/", files
    /// for an "@" (unless Esc put them away).
    private var suggestions: [ComposerSuggestion] {
        if let r = slashToken, String(text[r]) != dismissedToken {
            return SlashToken.suggestions(for: r, in: text, from: session.commands, startOnly: session.startCommands)
                .map { ComposerSuggestion(insert: $0, label: $0) }
        }
        // The files last found, this pane's, until those for what's typed now
        // come (not blinking out with each key).
        if let r = mentionToken, String(text[r]) != dismissedToken, mentionFiles.shows(pane: session.paneID, query: String(text[r].dropFirst())) {
            return mentionFiles.paths.map { ComposerSuggestion(insert: ComposerSuggestion.mention($0), label: ($0 as NSString).lastPathComponent,
                                                               detail: ($0 as NSString).deletingLastPathComponent, file: true) }
        }
        return []
    }

    /// The highlighted suggestion (↑ and ↓ move it; Return and Tab take it).
    private var highlighted: ComposerSuggestion? {
        let s = suggestions
        return s.isEmpty ? nil : s[min(max(picked, 0), s.count - 1)]
    }

    /// Return takes the highlighted suggestion only once you've moved the
    /// highlight (↑ or ↓): otherwise it does what it always does ("what's
    /// in /src" ⏎ sends, not a command matching "src"). Tab takes the top one.
    private var returnTakes: Bool { ComposerSuggestion.returnTakes(suggesting: highlighted != nil, moved: pickedByKey) }

    /// The token being typed, "/…" or "@…", as typed.
    private var tokenText: String? { (slashToken ?? mentionToken).map { String(text[$0]) } }

    /// Sets the text from code, not typing.
    private func setDoc(_ d: ComposerDoc) {
        doc = d
        cursor = nil
        controller.set(d)
    }

    private func replaceText(_ t: String) { setDoc(ComposerDoc(plain: t)) }

    private func complete(_ s: ComposerSuggestion) {
        guard let r = s.file ? mentionToken : slashToken else { return }
        let command = s.insert
        let spaced = text[r.upperBound...].first.map { $0 == " " || $0 == "\t" } ?? false
        let ns = NSRange(r, in: text)
        controller.replace(ns, with: spaced ? command : command + " ")
        if spaced { controller.replace(NSRange(location: ns.location + (command as NSString).length, length: 1), with: " ") }
    }

    /// The terminal has typed text this box doesn't (you'd typed something else).
    private var showsTerminalDraft: Bool {
        !awaitingClear && !session.draft.isEmpty && text != tookDraft && !text.contains(session.draft) && !isGhost(session.draft)
    }

    /// A message starting with "!" runs as a shell command in agents that
    /// have a shell mode (Claude Code's bash mode): the box says so, as
    /// Claude Code's own prompt does, in its colour.
    /// As sent: leading space trimmed; not with an attachment in it.
    private var shellCommand: Bool {
        doc.chips.isEmpty && AgentCatalog.hasShellMode(harness)
            && text.drop(while: { $0.isWhitespace }).hasPrefix("!")
    }
    static let shellTint = Color(red: 0.99, green: 0.36, blue: 0.69)

    private var canSend: Bool {
        (!doc.plain.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !doc.chips.isEmpty) && !waitingOnAttachment
    }
    /// An attachment in the message is still being prepared: it can't go yet.
    private var waitingOnAttachment: Bool { attachments.contains { $0.preparing && doc.chips.contains($0.id) } }
    /// Anything still being prepared, for the note beside the attach button.
    private var preparing: Bool { shrinking > 0 || attachments.contains(where: \.preparing) }
    /// While the agent works and nothing's typed, the round button stops it
    /// (not while dictating: it sends what you say).
    private var stops: Bool { session.working && text.isEmpty && !dictation.active }

    /// The mic button, unless turned off in Settings.
    private var dictationOn: Bool { Prefs.shared.bool(.agentDictation) }

    private func toggleDictation() {
        dictation.toggle(into: controller, commands: session.commands)
    }

    /// Pictures go small over the network: to a pane on a linked computer,
    /// and from the iPhone.
    private var defaultSize: AttachmentSize {
        AttachmentSize(rawValue: Prefs.shared.string(isRemote ? .agentImageSizeRemote : .agentImageSize)) ?? .large
    }

    private var isRemote: Bool {
        #if os(macOS)
        session.model?.group(ofPane: session.paneID)?.isRemote == true
        #else
        true
        #endif
    }

    private func greyed(_ a: ComposerAttachment) -> Bool { !doc.chips.contains(a.id) }

    /// The text view's settings, from the composer's state.
    private func configured() -> ComposerTextController {
        let c = controller
        session.composer = c
        c.font = platformFont(code: shellCommand)
        c.lineSpacing = CGFloat(fonts.spacing)
        c.highlight = PlatformColor(shellCommand ? Self.shellTint : accent)
        let commands = Set(session.commands), startOnly = session.startCommands
        c.highlighted = { SlashToken.highlights(in: $0, commands: commands, startOnly: startOnly) }
        let info = Dictionary(uniqueKeysWithValues: attachments.map { ($0.id, ($0.number, $0.isVideo)) })
        let thumbs = thumbs, size = CGFloat(fonts.size), accent = accent, dark = scheme == .dark
        c.chipImage = { id in
            guard let (n, video) = info[id] else { return nil }
            return ChipRenderer.image(number: n, thumbnail: thumbs[id], thumbKey: id.uuidString, isVideo: video, onBubble: false,
                                      fontSize: size, accent: accent, dark: dark)
        }
        c.onChange = { d, at in
            // Editing the message that didn't go: its error's done with.
            if d.text != doc.text { session.sendError = nil }
            // An input method composing, or done: "@" lookups wait for it.
            let composingNow = controller.composing
            if composingNow != composing { composing = composingNow }
            // Typing: a prompt card coming now waits before taking the keys.
            if d.text != doc.text { session.composerEditedAt = PromptKey.now }
            doc = d
            cursor = at
        }
        c.onSubmit = send
        c.onTab = {
            guard let s = highlighted else { return false }
            complete(s)
            return true
        }
        c.onFiles = { urls in Task { await attachFiles(urls) } }
        c.onImages = { images in for d in images { attach(name: "pasted-\(attachments.count + 1).png", data: d) } }
        c.onFocus = { focused = $0 }
        c.onCancel = {
            guard dictation.active else { return false }
            Task { await dictation.finish() }
            return true
        }
        c.wantsKey = { composerWants($0) }
        c.onKey = { composerKey($0) }
        return c
    }

    /// A key the composer takes before the text view (ComposerKey).
    private func composerWants(_ k: ComposerKey) -> Bool {
        // An input method composing a word has its keys.
        if controller.composing { return false }
        if !suggestions.isEmpty {
            // The suggestions over the box: ↑ and ↓ move through them,
            // Return takes one, Esc puts them away.
            return k == .accept ? returnTakes : true
        }
        return k == .up || k == .down ? historyWants(k) : false
    }

    private func composerKey(_ k: ComposerKey) {
        let s = suggestions
        guard !s.isEmpty else { return stepHistory(k) }
        switch k {
        case .up: picked = (min(picked, s.count - 1) - 1 + s.count) % s.count; pickedByKey = true
        case .down: picked = (min(picked, s.count - 1) + 1) % s.count; pickedByKey = true
        case .accept: if let h = highlighted { complete(h) }
        case .dismiss: dismissedToken = tokenText
        }
    }

    private func platformFont(code: Bool) -> PlatformFont {
        if code {
            let s = CGFloat(fonts.codeSize)
            return fonts.codeName.isEmpty ? .monospacedSystemFont(ofSize: s, weight: .regular) : PlatformFont(name: fonts.codeName, size: s) ?? .monospacedSystemFont(ofSize: s, weight: .regular)
        }
        let s = CGFloat(fonts.size)
        return fonts.name.isEmpty ? .systemFont(ofSize: s) : PlatformFont(name: fonts.name, size: s) ?? .systemFont(ofSize: s)
    }

    /// What the chips look like now: they're drawn again when it changes.
    private var chipsKey: String {
        attachments.map { "\($0.id.uuidString.prefix(8))\($0.number)\(thumbs[$0.id] != nil)" }.joined()
            + "|\(scheme == .dark)|\(fonts.size)|\(shellCommand)|\(accent.description)"
    }

    /// What's offered for the token being typed, over the box: the
    /// highlighted one (↑ and ↓) is what Tab and Return take.
    private var suggestionList: some View {
        let shown = suggestions, top = highlighted
        return VStack(alignment: .leading, spacing: 0) {
            ForEach(shown, id: \.self) { s in
                Button { complete(s) } label: {
                    HStack(spacing: 8) {
                        if s.file {
                            Image(systemName: "doc").foregroundStyle(.secondary).accessibilityHidden(true)
                            Text(s.label).font(fonts.code()).lineLimit(1)
                            Text(s.detail).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.head)
                        } else {
                            Text(s.label).font(fonts.code()).lineLimit(1)
                        }
                        Spacer(minLength: 8)
                        // Tab (or Return) takes the highlighted one: shown as
                        // the command palette shows a shortcut.
                        if s == top {
                            Text("⇥")
                                .font(.system(size: 13))
                                .foregroundStyle(.secondary)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 2)
                                .background(RoundedRectangle(cornerRadius: 5).fill(Color.primary.opacity(0.06)))
                                .accessibilityLabel("Shortcut Tab")
                        }
                    }
                    .padding(.horizontal, 16).padding(.vertical, 7)
                    .background {
                        if s == top, shown.count > 1 {
                            RoundedRectangle(cornerRadius: 12, style: .continuous).fill(accent.opacity(0.14)).padding(.horizontal, 6)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(s == top ? "Complete (Tab; Return once picked with ↑ or ↓)" : "")
                .accessibilityAddTraits(s == top ? .isSelected : [])
            }
        }
        .padding(.vertical, 6)
        .agentGlass(in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }

    /// The text view, and what follows its text: the suggestions' highlight
    /// starting at the top of each new list, and the files for an "@",
    /// looked up once typing pauses.
    private var textView: some View {
        ComposerTextView(controller: configured(), chipsKey: chipsKey, text: text)
            .onChange(of: suggestions) { picked = 0; pickedByKey = false }
            // Esc's "put away" lasts while that token's typed, not after.
            .onChange(of: tokenText) { _, t in
                if t != dismissedToken { dismissedToken = nil }
                // The "@" word's done: its files aren't offered for the next.
                if mentionToken == nil { mentionFiles = MentionFiles() }
            }
            .onChange(of: session.paneID) { dismissedToken = nil; pickedByKey = false }
            // Again when an input method's done composing, even if committing
            // left the text as it was.
            .task(id: mentionToken.map { String(text[$0].dropFirst()) + (composing ? "\u{0}" : "") }) {
                // Not while an input method is still composing the word.
                guard let r = mentionToken, !composing else { return }
                let q = String(text[r].dropFirst())
                try? await Task.sleep(for: .milliseconds(120))
                guard !Task.isCancelled else { return }
                let pane = session.paneID
                let paths = await session.mentionFiles(q)
                if !Task.isCancelled { mentionFiles = MentionFiles(pane: pane, query: q, paths: paths) }
            }
    }

    /// The box and what's around it, with what follows its draft. (body adds the rest:
    /// one modifier chain this long is more than the type checker will take.)
    private var composerStack: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !suggestions.isEmpty {
                suggestionList
            }
            if dictation.problem != nil || dictation.installing != nil {
                DictationNotice(dictation: dictation, openSettings: openMicrophoneSettings)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if let err = session.sendError {
                SendFailedNotice(error: err, canRetry: canSend, retry: send) { session.sendError = nil }
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if showsTerminalDraft {
                HStack(spacing: 8) {
                    Image(systemName: "apple.terminal").foregroundStyle(.secondary)
                    Text("Typed in the terminal: \(session.draft)").lineLimit(1).truncationMode(.tail).foregroundStyle(.secondary)
                    Spacer(minLength: 4)
                    Button("Add") {
                        replaceText(session.draft + (text.isEmpty ? "" : " " + doc.plain))
                        tookDraft = ""
                    }
                    .agentGlassButton()
                }
                .font(.caption)
                .padding(.horizontal, 12).padding(.vertical, 6)
                .agentGlass(in: Capsule())
                .help("Sending replaces what's typed in the terminal: add it to keep it")
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            // One box, as Claude's and ChatGPT's: what's attached along the
            // top, the message the full width, and under it a row of
            // controls (attach, the model, send).
            VStack(alignment: .leading, spacing: 2) {
                if !attachments.isEmpty {
                    tray.transition(.move(edge: .top).combined(with: .opacity))
                }
                if shellCommand {
                    Label("Shell command", systemImage: "apple.terminal")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(Self.shellTint)
                        .padding(.horizontal, 16)
                        .padding(.top, 10)
                        .padding(.bottom, -6)
                        .transition(.opacity)
                }
                textView
                    .overlay(alignment: .topLeading) {
                        if text.isEmpty {
                            Text("Message \(session.agentName)…")
                                .font(shellCommand ? fonts.code() : fonts.body())
                                .foregroundStyle(.tertiary)
                                .allowsHitTesting(false)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.top, 13)
                    .padding(.bottom, 12)
                HStack(spacing: 8) {
                    Menu {
                        Button { importing = true } label: { Label("Choose File…", systemImage: "doc") }
                        // A button, not a PhotosPicker: one inside a Menu goes
                        // away with the menu on iOS, so it never opens.
                        Button { pickingPhotos = true } label: { Label("Photos…", systemImage: "photo") }
                    } label: {
                        Image(systemName: "plus")
                            .font(.body.weight(.medium))
                            .frame(width: 22, height: 22)
                    }
                    .menuIndicator(.hidden)
                    .agentGlassButton()
                    .buttonBorderShape(.circle)
                    .fixedSize()
                    .help("Attach files or photos")
                    .accessibilityLabel("Attach files or photos")
                    if preparing {
                        ProgressView().controlSize(.small)
                        Text(attachments.contains { $0.preparing && !$0.isVideo } ? "Loading…" : "Preparing video…")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    // The model and how full its context is, beside the attach
                    // button.
                    // Dictating, your voice instead.
                    if dictation.active {
                        DictationLevels(dictation: dictation)
                            .padding(.horizontal, 4)
                            .transition(.opacity)
                    } else {
                        if !preparing, !session.agentModel.isEmpty {
                            AgentStatusBar(session: session, canDrive: true, harness: harness, inline: true)
                                .transition(.opacity)
                        }
                        Spacer(minLength: 0)
                    }
                    if dictationOn || dictation.active {
                        DictationButton(dictation: dictation, action: toggleDictation)
                    }
                    Button(action: stops ? session.interrupt : send) {
                        Image(systemName: stops ? "stop.fill" : "arrow.up")
                            .font(.body.weight(.bold))
                            .contentTransition(.symbolEffect(.replace))
                            .frame(width: 22, height: 22)
                    }
                    .agentGlassButton(prominent: true)
                    .buttonBorderShape(.circle)
                    // Your messages' colour, deep enough for its white glyph;
                    // stopping too (its icon says so). Disabled, the glass
                    // drops the tint and goes pale, so a white glyph would
                    // vanish: secondary instead.
                    .tint(shellCommand && !stops ? Self.shellTint : bubble)
                    .foregroundStyle(stops || canSend ? AnyShapeStyle(.white) : AnyShapeStyle(.secondary))
                    .disabled(!stops && !canSend)
                    .keyboardShortcut(.return, modifiers: .command)
                    .help(stops ? "Interrupt (Esc)" : shellCommand ? "Run (⌘↩)" : "Send (⌘↩)")
                    .animation(.snappy(duration: 0.2), value: stops)
                }
                .padding(.horizontal, 8)
                .padding(.bottom, 8)
            }
            .contentShape(RoundedRectangle(cornerRadius: 22, style: .continuous))
            .onTapGesture { controller.focus() }
            .agentGlass(in: RoundedRectangle(cornerRadius: 22, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 22, style: .continuous)
                    .strokeBorder(Self.shellTint, lineWidth: 1.5)
                    .opacity(shellCommand ? 1 : 0)
                    .allowsHitTesting(false)
            }
            .animation(.snappy(duration: 0.2), value: shellCommand)
        }
        .animation(.snappy(duration: 0.25), value: suggestions)
        .animation(.snappy(duration: 0.25), value: showsTerminalDraft)
        .animation(.snappy(duration: 0.25), value: session.sendError)
        .animation(.snappy(duration: 0.25), value: dictation.problem)
        .animation(.snappy(duration: 0.25), value: dictation.installing == nil)
        // The menu bar's and the palette's Dictate, for this pane.
        .onChange(of: DictationRequests.shared.toggle?.id) {
            guard let r = DictationRequests.shared.toggle, r.pane == session.paneID, dictationOn || dictation.active else { return }
            toggleDictation()
        }
        .onChange(of: dictation.active) { _, on in DictationRequests.shared.set(session.paneID, listening: on) }
        .onChange(of: dictationOn) { _, on in if !on { dictation.stopNow() } }
        // What's typed in the terminal comes into the box while it's empty (or
        // still just that), so sending, which replaces it there, loses nothing.
        .onChange(of: session.draft, initial: true) { _, d in
            if dictation.active { return }
            if !d.isEmpty, isGhost(d) { return }
            if awaitingClear {
                if d.isEmpty { awaitingClear = false }
                return
            }
            if (text.isEmpty || text == tookDraft) && doc.chips.isEmpty {
                replaceText(d)
                tookDraft = d
            }
        }
        // What's typed in this pane's box on your other devices.
        .modifier(OnSharedDraft(session: session, take: takeShared))
    }

    var body: some View {
        composerStack
        .animation(.snappy(duration: 0.25), value: attachments.map(\.id))
        #if DEBUG
        // Dev aid: the iPhone demo's takes (DemoDirector: compose, submit) type and send here, keyboard and all.
        .onReceive(NotificationCenter.default.publisher(for: Self.debugCompose)) { n in
            guard (n.userInfo?["pane"] as? String) == session.paneID else { return }
            if let t = n.userInfo?["text"] as? String {
                controller.focus()
                replaceText(t)
            } else {
                send()
            }
        }
        #endif
        .fileImporter(isPresented: $importing, allowedContentTypes: [.image, .movie], allowsMultipleSelection: true) { result in
            let urls = (try? result.get()) ?? []
            Task { await attachFiles(urls, scoped: true) }
        }
        .photosPicker(isPresented: $pickingPhotos, selection: $photos, maxSelectionCount: 4, matching: .any(of: [.images, .videos]))
        .onChange(of: photos) {
            let items = photos
            photos = []
            // Each in the tray and the text straight away, in the order
            // picked, while it loads (a video from iCloud, then made smaller),
            // so a chip doesn't land in the middle of what's being typed.
            for item in items {
                let video = item.supportedContentTypes.contains { $0.conforms(to: .movie) }
                let n = (attachments.map(\.number).max() ?? 0) + 1
                let id = placeholder(name: video ? "video-\(n).mp4" : "photo-\(n).jpg")
                Task {
                    if video {
                        if let movie = try? await item.loadTransferable(type: PickedMovie.self) {
                            await attachVideo(movie.url, into: id)
                            try? FileManager.default.removeItem(at: movie.url)
                        } else {
                            failed(id, "Couldn't load that video.")
                        }
                    } else if let d = try? await item.loadTransferable(type: Data.self) {
                        fill(id, data: d)
                    } else {
                        failed(id, "Couldn't load that photo.")
                    }
                }
            }
        }
        .alert("Couldn't attach it", isPresented: Binding(get: { attachError != nil }, set: { if !$0 { attachError = nil } })) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(attachError ?? "")
        }
        .dropDestination(for: Data.self) { items, _ in
            // On the box but not its text (which takes its own drops, where
            // they land): at the end of the message.
            controller.moveToEnd()
            for d in items where PlatformImage(data: d) != nil { attach(name: "dropped-\(attachments.count + 1).png", data: d) }
            return true
        }
        .onAppear {
            if text.isEmpty && attachments.isEmpty {
                if let initialText { sample(initialText) } else { loadDraft() }
            }
            if initialText == nil { takeShared() }
            ComposerDrafts.pruneStale(livePanes: nil)
            DictationRequests.shared.set(session.paneID, composer: true)
            // Dictate from the terminal: it switched here to listen.
            // Once its text view's made.
            if DictationRequests.shared.takePending(session.paneID), dictationOn {
                Task {
                    try? await Task.sleep(for: .milliseconds(250))
                    if !dictation.active { toggleDictation() }
                }
            }
        }
        // A new chat (a Claude tab just started): straight to typing, without
        // a tap or click on the box first. Once it's arrived, not while it
        // loads; on the Mac, in the pane you're in (not one opened beside it).
        .task(id: session.heard) {
            guard session.heard, session.blocks.isEmpty, session.pastSession == nil else { return }
            // After the push into the view: focus taken mid-transition is lost.
            try? await Task.sleep(for: .milliseconds(350))
            #if os(macOS)
            guard session.model?.focusedPaneID == session.paneID else { return }
            #endif
            if !Task.isCancelled, session.blocks.isEmpty { controller.focus() }
        }
        .onChange(of: session.paneID) { old, _ in
            // What was being said stays in the pane it was said in.
            dictation.stopNow()
            DictationRequests.shared.set(old, listening: false)
            DictationRequests.shared.set(old, composer: false)
            DictationRequests.shared.set(session.paneID, composer: true)
            saveDraft(key: "agentDraft." + old, pane: old)
            attachments = []
            thumbs = [:]
            history.reset()
            synced = nil
            loadDraft()
            takeShared()
        }
        .onChange(of: text) { old, t in
            // The keyboard putting (part of) the sent message back in one go;
            // you typing starts with one character. (Dictation's words come
            // several at once too.)
            if old.isEmpty, t.count > 1, doc.chips.isEmpty, !dictation.active, isGhost(t) {
                replaceText("")
                return
            }
        }
        // Saved once typing pauses, not on each key: every default written
        // tells everything watching UserDefaults (the terminal behind this
        // view redrew for each), and the keyboard lagged.
        .task(id: draftSignature) {
            try? await Task.sleep(for: .milliseconds(400))
            if !Task.isCancelled {
                saveDraft(key: draftKey, pane: session.paneID)
                shareDraft()
            }
        }
        .onDisappear {
            if session.composer === controller { session.composer = nil }
            dictation.stopNow()
            DictationRequests.shared.set(session.paneID, listening: false)
            DictationRequests.shared.set(session.paneID, composer: false)
            saveDraft(key: draftKey, pane: session.paneID)
            shareDraft()
        }
        .onChange(of: scenePhase) { _, phase in
            // Before the app can be killed in the background.
            if phase != .active { saveDraft(key: draftKey, pane: session.paneID) }
        }
    }

    // MARK: The tray

    /// What's attached, along the top of the box: numbered, greyed out
    /// while its chip isn't in the text (click to put it back).
    private var tray: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 10) {
                ForEach(attachments) { a in
                    TrayThumb(attachment: a, thumbnail: thumbs[a.id], greyed: greyed(a), defaultSize: defaultSize,
                              onOpen: { if !a.preparing { openTray(a) } },
                              onInsert: { controller.insertChip(a.id); controller.focus() },
                              onSize: { s in setSize(a.id, s) },
                              onRemove: { remove(a.id) })
                        .transition(.scale(scale: 0.7).combined(with: .opacity))
                }
            }
            .padding(.horizontal, 12)
            .padding(.top, 12)
            .padding(.bottom, 2)
        }
    }

    private var trayGroup: [PreviewItem] {
        attachments.map { a in PreviewItem(key: a.id.uuidString, name: a.name, file: a.file) { a.data } }
    }

    private func openTray(_ a: ComposerAttachment) {
        openAttachment(PreviewItem(key: a.id.uuidString, name: a.name, file: a.file) { a.data }, group: trayGroup,
                       image: a.isVideo ? thumbs[a.id] : PlatformImage(data: a.data), video: a.isVideo, open: open)
    }

    private func setSize(_ id: UUID, _ s: AttachmentSize?) {
        guard let i = attachments.firstIndex(where: { $0.id == id }) else { return }
        attachments[i].size = s
    }

    /// Takes an attachment off for good: from the tray and the text.
    private func remove(_ id: UUID) {
        withAnimation(.snappy) { attachments.removeAll { $0.id == id } }
        thumbs[id] = nil
        // Its chips go, each with the space after it (put in with it).
        let st = doc.text
        if doc.chips.contains(id) {
            var d = ComposerDoc()
            var k = 0
            var dropSpace = false
            for ch in st {
                if ch == attachMark {
                    if k < doc.chips.count, doc.chips[k] != id {
                        d.text.append(ch)
                        d.chips.append(doc.chips[k])
                        dropSpace = false
                    } else {
                        dropSpace = true
                    }
                    k += 1
                } else {
                    if !(dropSpace && ch == " ") { d.text.append(ch) }
                    dropSpace = false
                }
            }
            setDoc(d)
        }
        if attachments.isEmpty { ComposerDrafts.remove(session.paneID) }
    }

    // MARK: Attaching

    /// Attaches a file's contents, numbered next, with its chip at the cursor.
    private func attach(name: String, data: Data, file: URL? = nil) {
        let a = ComposerAttachment(number: (attachments.map(\.number).max() ?? 0) + 1, name: name, data: data, file: file)
        attachments.append(a)
        // Its picture for the chip, now: the text view asks for it as it's put in.
        _ = configured()
        controller.insertChip(a.id)
        Task {
            if let t = await a.thumbnail() { thumbs[a.id] = t }
        }
    }

    private func attachFiles(_ urls: [URL], scoped: Bool = false) async {
        // Files that stay where they are, for this Mac's own agent.
        let local = !isRemote && !scoped
        for url in urls {
            let ok = scoped && url.startAccessingSecurityScopedResource()
            defer { if ok { url.stopAccessingSecurityScopedResource() } }
            if UTType(filenameExtension: url.pathExtension)?.conforms(to: .movie) == true {
                // To this Mac's own agent, a video goes as it is, by its
                // path (the daemon clones it).
                if local, isVideoName(url.lastPathComponent) {
                    attach(name: url.lastPathComponent, data: Data(), file: url)
                    continue
                }
                let id = placeholder(name: url.deletingPathExtension().lastPathComponent + ".mp4")
                await attachVideo(url, into: id)
            } else if let d = try? Data(contentsOf: url) {
                // A picture can go by its path too, when it's not made smaller.
                attach(name: url.lastPathComponent, data: d, file: local ? url : nil)
            }
        }
    }

    /// An attachment still being loaded or prepared: numbered next, in the
    /// tray with its chip at the cursor now; fill(_:data:) gives it its data.
    private func placeholder(name: String) -> UUID {
        let a = ComposerAttachment(number: (attachments.map(\.number).max() ?? 0) + 1, name: name, data: Data(), preparing: true)
        attachments.append(a)
        _ = configured()
        controller.insertChip(a.id)
        return a.id
    }

    /// A placeholder's data, now it's ready (nothing, if it's been taken off
    /// meanwhile).
    private func fill(_ id: UUID, data: Data) {
        guard let i = attachments.firstIndex(where: { $0.id == id }) else { return }
        let old = attachments[i]
        let a = ComposerAttachment(id: id, number: old.number, name: old.name, data: data, size: old.size)
        attachments[i] = a
        _ = configured()
        if thumbs[id] == nil {
            Task { if let t = await a.thumbnail() { thumbs[id] = t } }
        }
    }

    /// A placeholder that couldn't be made: off the tray and the text, and why.
    private func failed(_ id: UUID, _ why: String) {
        guard attachments.contains(where: { $0.id == id }) else { return }
        remove(id)
        attachError = why
    }

    /// Fills a placeholder with a video, made small enough to send (540p
    /// H.264): a phone's own is usually too big for a message to the Mac.
    /// Its first frame shows in the tray and the chip meanwhile.
    private func attachVideo(_ url: URL, into id: UUID) async {
        shrinking += 1
        defer { shrinking -= 1 }
        if thumbs[id] == nil, let t = await VideoPoster.image(url: url, maxPixels: 256),
           attachments.contains(where: { $0.id == id }) {
            thumbs[id] = t
            _ = configured()
        }
        do {
            let data = try await shrinkVideo(url)
            guard data.count <= 30 << 20 else {
                failed(id, "That video is too long to send (\(data.count >> 20) MB after shrinking it; 30 MB at most).")
                return
            }
            fill(id, data: data)
        } catch {
            failed(id, "Couldn't prepare that video: \(error.localizedDescription)")
        }
    }

    /// ComposerSample's text and attachments.
    private func sample(_ t: String) {
        attachments = initialAttachments.enumerated().map { ComposerAttachment(number: $0.offset + 1, name: $0.element.name, data: $0.element.data) }
        var d = ComposerDoc()
        var rest = Substring(t)
        while let open = rest.firstIndex(of: "{"), let close = rest[open...].firstIndex(of: "}"),
              let n = Int(rest[rest.index(after: open)..<close]), attachments.indices.contains(n - 1) {
            d.text += rest[..<open]
            d.text.append(attachMark)
            d.chips.append(attachments[n - 1].id)
            rest = rest[rest.index(after: close)...]
        }
        d.text += rest
        for a in attachments {
            thumbs[a.id] = PlatformImage(data: a.data)
        }
        _ = configured()
        // Dictating (HMK_DEBUG_DICTATION): what's after "|" not settled yet.
        if debugDictating {
            setDoc(ComposerDoc())
            let parts = t.split(separator: "|", maxSplits: 1).map(String.init)
            dictation.debugShow(into: controller, final: parts.first ?? "", volatile: parts.count > 1 ? parts[1] : "")
            return
        }
        setDoc(d)
    }

    // MARK: History

    /// ↑ and ↓ step through what's been sent from this pane, as a shell's
    /// history does (ComposerHistory): not while dictating.
    private func historyWants(_ k: ComposerKey) -> Bool {
        guard !dictation.active,
                            // ↑ steps on from anywhere on the first line of a line brought
              // back unedited (edited, from its very start); ↓ from the last
              // line of any brought back, its edit kept.
              ComposerHistory<ComposerDoc>.steps(up: k == .up, text: text, cursor: controller.caret,
                                                 browsing: k == .up ? history.unchanged(text) : history.browsing) else { return false }
        return k == .down || history.browsing || !historyEntries.isEmpty
    }

    private var historyEntries: [String] {
        ComposerHistory<ComposerDoc>.merged(sent: ComposerHistoryStore.sent(session.paneID), conversation: session.sentMessages)
    }

    private func stepHistory(_ k: ComposerKey) {
        if k == .up {
            guard let e = history.up(from: doc, text: text, entries: historyEntries) else { return }
            recall(Self.doc(e))
        } else if let s = history.down(from: doc, text: text) {
            // Past the newest: what you were typing, attachments and all.
            recall(s.entry.map(Self.doc) ?? s.draft ?? ComposerDoc())
        }
    }

    /// A line from history as the box has it: one as sent, or one you'd
    /// edited, as you left it (its attachments' chips too).
    private static func doc(_ l: ComposerHistory<ComposerDoc>.Line) -> ComposerDoc {
        switch l {
        case .sent(let t): ComposerDoc(plain: t)
        case .kept(let d): d
        }
    }

    private func recall(_ d: ComposerDoc) {
        // Brought back on purpose: not the keyboard giving back what was just sent.
        justSent = nil
        // Another message in the box: a failed send's error (and its Retry) is done with.
        session.sendError = nil
        setDoc(d)
    }

    /// What's being sent, for ↑ to bring back.
    private func rememberSent() {
        ComposerHistoryStore.record(session.paneID, ComposerHistoryStore.recordable(doc.text))
        history.reset()
        dismissedToken = nil
        mentionFiles = MentionFiles()
    }

    // MARK: Drafts

    /// What a saved draft depends on: saved again when it changes.
    private var draftSignature: String {
        doc.text + "|" + doc.chips.map(\.uuidString).joined() + "|" + attachments.map { "\($0.id)\($0.size?.rawValue ?? "")\($0.preparing)" }.joined()
    }

    private func saveDraft(key: String, pane: String) {
        let d = UserDefaults.standard
        // Stepping through what was sent: what you were typing is the draft,
        // not the message shown.
        let doc = history.savedDraft ?? self.doc
        // One still being prepared isn't kept (it has nothing to keep yet):
        // its chip goes with it if the draft's loaded before it's ready.
        let attachments = attachments.filter { !$0.preparing }
        if attachments.isEmpty {
            ComposerDrafts.remove(pane)
            let t = doc.plain.isEmpty ? "" : doc.text
            if t.isEmpty {
                if d.object(forKey: key) != nil { d.removeObject(forKey: key) }
            } else if d.string(forKey: key) != t {
                d.set(t, forKey: key)
            }
        } else {
            if d.object(forKey: key) != nil { d.removeObject(forKey: key) }
            ComposerDrafts.save(pane, doc: doc, attachments: attachments)
        }
    }

    private func loadDraft() {
        if let (d, atts) = ComposerDrafts.load(session.paneID) {
            attachments = atts
            for a in atts {
                Task { if let t = await a.thumbnail() { thumbs[a.id] = t } }
            }
            _ = configured()
            setDoc(d)
        } else {
            replaceText(UserDefaults.standard.string(forKey: draftKey) ?? "")
        }
    }

    // MARK: Your other devices

    /// What this box shares with your other devices: its text, less its
    /// attachments (which stay on this one); stepping through what was
    /// sent, what you were typing.
    private var shareText: String {
        String((history.savedDraft ?? doc).text.filter { $0 != attachMark })
    }

    /// Tells your other devices what's in the box, if it's changed since
    /// they were last in step. Not before the daemon's said what it has.
    private func shareDraft() {
        let t = shareText
        guard initialText == nil, session.heard, t != synced else { return }
        if session.shareDraft(t, writer: boxID) { synced = t }
    }

    /// What's typed in this pane's box on another of your devices comes
    /// into this one while it has nothing unsent of its own (as it was
    /// when last in step, or just opened); else what this one has goes to
    /// them. Not into a message with attachments in it, being dictated, or
    /// while stepping through what was sent.
    private func takeShared() {
        let s = session.sharedDraft
        guard initialText == nil, session.heard else { return }
        let t = shareText
        // The daemon has none (it restarted, or nothing's been typed), or
        // it's this box's own: what this box has, it's told (if anything).
        if s.rev == 0 || s.writer == boxID {
            if s.rev == 0, t.isEmpty { synced = t } else { shareDraft() }
            return
        }
        if t == s.text {
            synced = t
            return
        }
        guard synced == nil || t == synced, doc.chips.isEmpty, !dictation.active, history.savedDraft == nil else {
            shareDraft()
            return
        }
        replaceText(s.text)
        synced = s.text
    }

    /// Text that's only (part of) the message just sent, while that's recent.
    /// Spacing doesn't count: the terminal wraps a long message over lines
    /// (the draft has line breaks where it had spaces), and shows a long
    /// paste as "[Pasted text …]". Nor do its attachments, as the terminal
    /// shows them while they're pasted: "[Image #3]", or a video's path.
    private func isGhost(_ s: String) -> Bool {
        guard let j = justSent, Date.now < j.until else { return false }
        if s.contains("[Image #") || s.contains("/hammock/attachments/") { return true }
        func bare(_ t: String) -> String { String(String.UnicodeScalarView(t.unicodeScalars.filter { !CharacterSet.whitespacesAndNewlines.contains($0) })) }
        let a = bare(s), b = bare(j.text)
        if a.isEmpty { return false }
        return b.contains(a) || a.hasPrefix(b) || s.hasPrefix("[Pasted text")
    }

    // MARK: Sending

    #if DEBUG
    /// Dev aid: userInfo pane and text types text into that pane's box (focusing it); pane alone sends it.
    static let debugCompose = Notification.Name("HammockDebugCompose")
    #endif

    private func send() {
        // Dictating: the last words settle first, then it all goes.
        if dictation.active {
            Task {
                await dictation.finish()
                if !dictation.active { send() }
            }
            return
        }
        // A prompt's on screen (this box fading out as its card comes): the
        // agent's terminal is showing the prompt, so Return mustn't type
        // into it. The text stays in the box.
        guard canSend, session.prompt.kind == .none else { return }
        rememberSent()
        // A chip pasted in twice goes once, where it first is.
        var d = ComposerDoc()
        var k = 0
        for ch in doc.text {
            if ch == attachMark {
                if k < doc.chips.count, !d.chips.contains(doc.chips[k]) {
                    d.text.append(ch)
                    d.chips.append(doc.chips[k])
                }
                k += 1
            } else {
                d.text.append(ch)
            }
        }
        let byID = Dictionary(uniqueKeysWithValues: attachments.map { ($0.id, $0) })
        let sent = d.chips.compactMap { byID[$0] }
        let size = defaultSize
        // Those left out stay, greyed, numbered again for the next message.
        let kept = attachments.filter { !d.chips.contains($0.id) }
        var renumbered = kept
        for i in renumbered.indices { renumbered[i].number = i + 1 }
        let keptThumbs = thumbs.filter { id, _ in kept.contains { $0.id == id } }
        attachments = renumbered
        thumbs = keptThumbs
        setDoc(ComposerDoc())
        UserDefaults.standard.removeObject(forKey: draftKey)
        if renumbered.isEmpty { ComposerDrafts.remove(session.paneID) }
        shareDraft() // emptied on your other devices too
        // The terminal's box is emptied as this goes: don't take its old text
        // back meanwhile.
        if !session.draft.isEmpty { awaitingClear = true }
        tookDraft = ""
        justSent = (d.plain, .now + 4)
        let local = sent.map { BubbleAttachment.local(id: $0.id, name: $0.name, data: $0.data, file: $0.file) }
        Task {
            // Pictures at the size they go at (videos are already small).
            let files = await Task.detached(priority: .userInitiated) {
                sent.map { a -> (name: String, data: Data, path: String?) in
                    if let path = a.path(at: a.size ?? size) { return (a.name, Data(), path) }
                    let (n, d) = ImageResize.resized(name: a.name, data: a.data, maxEdge: (a.size ?? size).maxEdge)
                    return (n, d, nil)
                }
            }.value
            guard !(await session.send(d.text, attachments: files, inline: true, local: local)) else { return }
            // Didn't go (not connected yet): give it back, unless something
            // new has been typed meanwhile.
            justSent = nil
            awaitingClear = false
            if text.isEmpty {
                var back = sent
                for i in back.indices { back[i].number = i + 1 }
                for i in attachments.indices { attachments[i].number = back.count + i + 1 }
                attachments = back + attachments
                for a in back { Task { if let t = await a.thumbnail() { thumbs[a.id] = t } } }
                _ = configured()
                setDoc(d)
            }
        }
    }
}

/// Over the message box: the message didn't go (it's back in the box), why,
/// and Retry.
struct SendFailedNotice: View {
    let error: String
    /// Something's in the box to send again.
    let canRetry: Bool
    let retry: () -> Void
    let dismiss: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
            Text(error).lineLimit(2)
            Spacer(minLength: 4)
            if canRetry {
                Button("Retry", action: retry).agentGlassButton()
            }
            Button(action: dismiss) {
                Image(systemName: "xmark").font(.caption.weight(.semibold))
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .accessibilityLabel("Dismiss")
        }
        .font(.caption)
        .padding(.horizontal, 12).padding(.vertical, 6)
        .agentGlass(in: Capsule())
    }
}

// MARK: - Status bar

/// The agent's model and effort level (a menu to switch them) and how full
/// its context is.
struct AgentStatusBar: View {
    let session: AgentSession
    let canDrive: Bool
    /// claude or codex: Codex's /model is a picker of its own (shown as a
    /// prompt), not a list of aliases.
    var harness = "claude"
    /// In the message box: no capsule of its own, quieter.
    var inline = false
    @Environment(\.agentFonts) private var fonts

    /// Claude's /model aliases.
    static let choices: [(label: String, alias: String)] = [
        ("Opus", "opus"), ("Opus (1M context)", "opus[1m]"),
        ("Sonnet", "sonnet"), ("Sonnet (1M context)", "sonnet[1m]"),
        ("Haiku", "haiku"), ("Default", "default"),
    ]

    /// Claude's /effort levels, lowest first.
    static let efforts: [(label: String, level: String)] = [
        ("Low", "low"), ("Medium", "medium"), ("High", "high"), ("Extra high", "xhigh"), ("Max", "max"),
    ]

    /// "xhigh" as "Extra high"; one it doesn't know as it is.
    static func effortName(_ level: String) -> String {
        efforts.first { $0.level == level }?.label ?? level.capitalized
    }

    /// "claude-opus-5-5" as "Opus 5.5" (a date suffix dropped); "gpt-6-luna"
    /// as "GPT-6 Luna".
    static func displayName(_ id: String) -> String {
        // Already a name (off Claude's start screen, before a reply says).
        if id.contains(" ") { return id }
        if id.hasPrefix("gpt-") {
            let parts = id.dropFirst(4).split(separator: "-").map(String.init)
            guard let version = parts.first else { return id }
            return (["GPT-" + version] + parts.dropFirst().map { $0.capitalized }).joined(separator: " ")
        }
        var rest = id.replacingOccurrences(of: "[1m]", with: "")
        if rest.hasPrefix("claude-") { rest.removeFirst(7) }
        let parts: [String] = rest.split(separator: "-").map(String.init).filter { !($0.count == 8 && $0.allSatisfy(\.isNumber)) }
        guard let family = parts.first(where: { $0.first?.isLetter == true }) else { return id }
        let version = parts.filter { $0.allSatisfy(\.isNumber) }.joined(separator: ".")
        return ([family.capitalized] + (version.isEmpty ? [] : [version])).joined(separator: " ")
    }

    private var fraction: Double {
        session.contextWindow > 0 ? min(1, Double(session.contextTokens) / Double(session.contextWindow)) : 0
    }

    private static func tokens(_ n: Int64) -> String {
        n >= 1_000_000 ? String(format: "%.1fM", Double(n) / 1_000_000).replacingOccurrences(of: ".0M", with: "M") : "\(n / 1000)k"
    }

    var body: some View {
        HStack(spacing: 10) {
            if harness == "claude" {
                // The model, and its effort level in a submenu, as Claude's
                // own apps have it.
                Menu {
                    Section("Model") {
                        ForEach(Self.choices, id: \.alias) { c in
                            Button(c.label) { session.switchModel(to: c.alias) }
                        }
                    }
                    // Once a reply has said (models without effort levels don't).
                    if !session.agentEffort.isEmpty {
                        Divider()
                        Picker("Effort", selection: Binding(get: { session.agentEffort }, set: { session.switchEffort(to: $0) })) {
                            ForEach(Self.efforts, id: \.level) { e in Text(e.label).tag(e.level) }
                        }
                        .pickerStyle(.menu)
                    }
                } label: {
                    // One string: the menu button drops styling within its label.
                    Text(Self.displayName(session.agentModel) + (session.agentEffort.isEmpty ? "" : "  " + Self.effortName(session.agentEffort)))
                }
                .menuStyle(.borderlessButton)
                .fixedSize()
                .disabled(!canDrive)
                .help(session.agentEffort.isEmpty ? "Switch model (/model)" : "Switch model or effort (/model, /effort)")
            } else {
                // Codex picks a model (and its reasoning) in a picker of its
                // own: it shows here as a prompt to answer.
                Button {
                    session.runCommand("/model")
                } label: {
                    HStack(spacing: 3) {
                        Text(Self.displayName(session.agentModel))
                        Image(systemName: "chevron.down").font(.caption2.weight(.semibold))
                    }
                }
                .buttonStyle(.plain)
                .disabled(!canDrive)
                .help("Switch model (/model)")
            }
            if session.contextWindow > 0 {
                Divider().frame(height: 12)
                contextGauge
            }
        }
        .font(fonts.body(inline ? 0.76 : 0.82))
        .foregroundStyle(inline ? .secondary : .primary)
        .modifier(StatusChrome(inline: inline))
    }

    /// How full the context is, a menu to make room: its words say how
    /// much is left, and Compact and Clear (or Codex's New Chat) run those
    /// commands.
    private var contextGauge: some View {
        let tint: Color = fraction > 0.9 ? .red : fraction > 0.75 ? .orange : .secondary
        let actions = Self.contextActions(harness: harness, commands: session.commands)
        return Menu {
            Text(Self.tokensLeft(session.contextWindow - session.contextTokens))
            if !actions.isEmpty {
                Divider()
                ForEach(actions, id: \.command) { a in
                    Button("\(a.label) (\(a.command))", role: a.command == "/compact" ? nil : .destructive) {
                        session.runCommand(a.command)
                    }
                    .disabled(!canDrive)
                }
            }
        } label: {
            HStack(spacing: 6) {
                ZStack {
                    Circle().stroke(Color.secondary.opacity(0.25), lineWidth: 2.5)
                    Circle().trim(from: 0, to: fraction).stroke(tint, style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
                        .rotationEffect(.degrees(-90))
                }
                .frame(width: 12, height: 12)
                Text("\(Int((fraction * 100).rounded()))% context")
                    .monospacedDigit()
                    .foregroundStyle(fraction > 0.75 ? tint : .secondary)
            }
            .contentShape(Rectangle())
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .menuIndicator(.hidden)
        .fixedSize()
        .help("\(Self.tokens(session.contextTokens)) of \(Self.tokens(session.contextWindow)) tokens: click to make room")
        .accessibilityLabel("Context \(Int((fraction * 100).rounded()))% full")
        .animation(.snappy, value: fraction)
    }

    /// "~120k tokens left", "~1.2M tokens left"; under a thousand, the
    /// number itself (not "~0k").
    static func tokensLeft(_ n: Int64) -> String {
        let n = max(0, n)
        return n < 1000 ? "\(n) tokens left" : "~\(tokens(n)) tokens left"
    }

    /// What the context menu offers to make room: Compact, and Clear (Codex's
    /// New Chat), when the agent has them.
    static func contextActions(harness: String, commands: [String]) -> [(label: String, command: String)] {
        var out: [(label: String, command: String)] = []
        let has = Set(commands)
        if harness == "claude" || harness == "codex" || has.contains("/compact") { out.append(("Compact", "/compact")) }
        if harness == "claude" || (harness != "codex" && has.contains("/clear")) {
            out.append(("Clear", "/clear"))
        } else if harness == "codex" || has.contains("/new") {
            out.append(("New Chat", "/new"))
        }
        return out
    }

    /// On its own, a glass capsule; in the message box, nothing.
    private struct StatusChrome: ViewModifier {
        let inline: Bool
        func body(content: Content) -> some View {
            if inline {
                content.lineLimit(1).fixedSize()
            } else {
                content
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                    .agentGlass(in: Capsule())
                    .frame(maxWidth: .infinity)
            }
        }
    }
}

// MARK: - Subagents and background shells

/// What the conversation view can show instead of the agent's own: one of
/// its subagents' transcripts, or one of its background shells' output.
enum AgentSideView: Hashable {
    case subagent(String)
    case shell(String)

    var subagent: String { if case .subagent(let id) = self { id } else { "" } }
    var shell: String { if case .shell(let id) = self { id } else { "" } }
}

/// What the agent has running beside its conversation, above it: its
/// subagents and its background shells. Tap one to watch it.
struct BackgroundStrip: View {
    let subagents: [AgentSubagent]
    let shells: [AgentShell]
    let open: (AgentSideView) -> Void

    private var label: String {
        var parts: [String] = []
        if !subagents.isEmpty { parts.append(subagents.count == 1 ? "Subagent" : "\(subagents.count) subagents") }
        if !shells.isEmpty { parts.append(shells.count == 1 ? "Shell" : "\(shells.count) shells") }
        return parts.joined(separator: " · ")
    }

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                Text(label)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                ForEach(subagents, id: \.id) { a in
                    chip(a.description_p.isEmpty ? a.agentType : a.description_p) { WorkingDot(size: 9) } action: { open(.subagent(a.id)) }
                }
                ForEach(shells, id: \.id) { sh in
                    chip(sh.title) { Image(systemName: "terminal").font(.caption.weight(.semibold)).foregroundStyle(.secondary) } action: { open(.shell(sh.id)) }
                }
            }
            .padding(.horizontal, 18).padding(.vertical, 8)
        }
        .background(.bar)
    }

    private func chip(_ title: String, @ViewBuilder icon: () -> some View, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 6) {
                icon()
                Text(title).lineLimit(1)
                Image(systemName: "chevron.right").font(.caption2.weight(.semibold)).foregroundStyle(.tertiary)
            }
            .font(.callout)
            .padding(.horizontal, 10).padding(.vertical, 5)
            .background(.quaternary.opacity(0.6), in: Capsule())
        }
        .buttonStyle(.plain)
    }
}

extension AgentShell {
    /// What it's for, as the agent described it, else its command.
    var title: String {
        description_p.isEmpty ? String(command.prefix { $0 != "\n" }) : description_p
    }
}

/// Above a background shell's output: what it runs, whether it still is
/// (and how it ended), and the way back to the agent that started it.
struct ShellHeader: View {
    let shell: AgentShell?
    let back: () -> Void
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Button(action: back) {
                Label("Back", systemImage: "chevron.left").labelStyle(.titleAndIcon)
            }
            .buttonStyle(.borderless)
            Divider().frame(height: 16)
            Group {
                StatusIcon(state: shell?.finished == false ? .working : shell?.failed == true ? .errored : .idle, size: 14)
            }
            .frame(height: 16)
            VStack(alignment: .leading, spacing: 2) {
                Text(shell?.title ?? "Shell")
                    .font(.callout.weight(.semibold)).lineLimit(1)
                if let shell, !shell.description_p.isEmpty {
                    Text(shell.command).font(fonts.code(0.85)).foregroundStyle(.secondary).lineLimit(2).textSelection(.enabled)
                }
                Text(status).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            Spacer()
        }
        .padding(.horizontal, 18).padding(.vertical, 8)
        .background(.bar)
    }

    private var status: String {
        guard let shell else { return "Shell" }
        if !shell.finished { return "Background shell · running" }
        return shell.summary.isEmpty ? "Background shell · ended" : shell.summary
    }
}

/// A background shell's output, following the end as it's written (unless
/// you've scrolled up).
struct ShellOutputView: View {
    let session: AgentSession
    @Environment(\.agentFonts) private var fonts
    @State private var position = ScrollPosition(edge: .bottom)
    @State private var atBottom = true

    private var lines: [Substring] {
        var s = Substring(session.shellOutput)
        if s.hasSuffix("\r") { s = s.dropLast() }
        if s.hasSuffix("\n") { s = s.dropLast() }
        return s.split(separator: "\n", omittingEmptySubsequences: false)
    }

    var body: some View {
        ScrollView([.vertical]) {
            if !session.error.isEmpty {
                ContentUnavailableView("No output", systemImage: "terminal", description: Text(session.error))
                    .frame(maxWidth: .infinity)
                    .padding(.top, 60)
            } else if session.shellOutput.isEmpty {
                Text("No output yet")
                    .font(.callout).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 60)
            } else {
                let ls = lines
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(ls.indices, id: \.self) { i in
                        Text(ls[i].isEmpty ? " " : String(ls[i]))
                            .font(fonts.code())
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .textSelection(.enabled)
                .padding(.horizontal, 18)
                .padding(.vertical, 12)
            }
        }
        .scrollPosition($position)
        .defaultScrollAnchor(.bottom)
        .onScrollGeometryChange(for: Bool.self) { g in
            g.contentOffset.y + g.containerSize.height >= g.contentSize.height - 24
        } action: { _, bottom in
            atBottom = bottom
        }
        .onChange(of: session.shellOutput) {
            if atBottom { position.scrollTo(edge: .bottom) }
        }
    }
}

/// Above a subagent's transcript: what it was asked to do, whether it's
/// still at it, and the way back to the agent that started it.
struct SubagentHeader: View {
    let subagent: AgentSubagent?
    let working: Bool
    let back: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Button(action: back) {
                Label("Back", systemImage: "chevron.left").labelStyle(.titleAndIcon)
            }
            .buttonStyle(.borderless)
            Divider().frame(height: 16)
            StatusIcon(state: working ? .working : .idle, size: 14)
            VStack(alignment: .leading, spacing: 1) {
                Text(subagent.map { $0.description_p.isEmpty ? $0.agentType : $0.description_p } ?? "Subagent")
                    .font(.callout.weight(.semibold)).lineLimit(1)
                Text(working ? "Subagent · working" : "Subagent · finished")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(.horizontal, 18).padding(.vertical, 8)
        .background(.bar)
    }
}

// MARK: - Videos

/// A video picked from Photos, as a file of its own (too big to load as data).
struct PickedMovie: Transferable {
    let url: URL

    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(contentType: .movie) { movie in
            SentTransferredFile(movie.url)
        } importing: { received in
            let copy = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + "." + received.file.pathExtension)
            try FileManager.default.copyItem(at: received.file, to: copy)
            return PickedMovie(url: copy)
        }
    }
}

/// A video re-encoded small (540p H.264, MP4) for sending to the agent.
func shrinkVideo(_ url: URL) async throws -> Data {
    let asset = AVURLAsset(url: url)
    guard let export = AVAssetExportSession(asset: asset, presetName: AVAssetExportPreset960x540) else {
        throw CocoaError(.fileReadCorruptFile)
    }
    let out = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".mp4")
    defer { try? FileManager.default.removeItem(at: out) }
    try await export.export(to: out, as: .mp4)
    return try Data(contentsOf: out)
}

// MARK: - Colour

/// Your message as the agent view shows it, for Settings' preview.
public struct AgentColorPreview: View {
    let color: Color
    public init(color: Color) { self.color = color }
    public var body: some View {
        HStack {
            Spacer(minLength: 40)
            Text("Can you speed up the test suite?")
                .foregroundStyle(.white)
                .padding(.horizontal, 15).padding(.vertical, 10)
                .background(color.gradient, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        }
    }
}

/// An attachment in the composer's tray: its thumbnail and number, greyed
/// out while its chip isn't in the text. Click to look at it (or, greyed,
/// to put it back); its menu sets the size it's sent at.
struct TrayThumb: View {
    let attachment: ComposerAttachment
    let thumbnail: PlatformImage?
    let greyed: Bool
    let defaultSize: AttachmentSize
    let onOpen: () -> Void
    let onInsert: () -> Void
    let onSize: (AttachmentSize?) -> Void
    let onRemove: () -> Void
    @Environment(\.agentImageZoom) private var zoom

    private var name: String { attachment.name }

    var body: some View {
        ZStack(alignment: .topTrailing) {
            Group {
                if let thumbnail {
                    #if canImport(AppKit)
                    Image(nsImage: thumbnail).resizable().scaledToFill()
                    #else
                    Image(uiImage: thumbnail).resizable().scaledToFill()
                    #endif
                } else {
                    Image(systemName: attachment.isVideo ? "film" : "photo")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(.quaternary.opacity(0.7))
                }
            }
            .frame(width: 64, height: 64)
            .overlay {
                if attachment.preparing {
                    ProgressView().controlSize(.small).tint(.white)
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(.black.opacity(thumbnail == nil ? 0 : 0.35))
                } else if attachment.isVideo, thumbnail != nil {
                    Image(systemName: "play.fill").foregroundStyle(.white).shadow(radius: 2)
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay(alignment: .bottomLeading) { NumberBadge(number: attachment.number).padding(4) }
            .overlay(alignment: .center) {
                if greyed {
                    Image(systemName: "plus.circle.fill")
                        .font(.title3)
                        .symbolRenderingMode(.palette)
                        .foregroundStyle(.white, .black.opacity(0.5))
                }
            }
            .saturation(greyed ? 0 : 1)
            .opacity(greyed ? 0.45 : 1)
            #if os(iOS)
            .modifier(ZoomSource(id: attachment.id.uuidString, in: zoom))
            #endif
            .contentShape(Rectangle())
            .onTapGesture { greyed ? onInsert() : onOpen() }
            .help(greyed ? "\(name): not in the message. Click to put it back at the cursor" : name)
            .contextMenu { menu }
            Button(action: onRemove) {
                Image(systemName: "xmark.circle.fill")
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, .black.opacity(0.6))
                    .font(.system(size: 18))
            }
            .buttonStyle(.plain)
            .offset(x: 6, y: -6)
            .accessibilityLabel("Remove \(name)")
        }
        .padding(.top, 6).padding(.trailing, 6) // room for the ✕
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Attachment \(attachment.number)\(greyed ? ", not in the message" : "")")
    }

    @ViewBuilder private var menu: some View {
        if greyed {
            Button { onInsert() } label: { Label("Put Back in Message", systemImage: "text.insert") }
        }
        if let px = attachment.pixels {
            Section("Send at") {
                sizeButton(nil, "Default: \(defaultSize.name) (\(dims(defaultSize.scaled(px))))")
                ForEach(AttachmentSize.allCases) { s in
                    sizeButton(s, "\(s.name) (\(dims(s.scaled(px))))")
                }
            }
        }
        Divider()
        Button(role: .destructive) { onRemove() } label: { Label("Remove", systemImage: "trash") }
    }

    private func sizeButton(_ s: AttachmentSize?, _ title: String) -> some View {
        Button { onSize(s) } label: {
            if attachment.size == s { Label(title, systemImage: "checkmark") } else { Text(title) }
        }
    }

    private func dims(_ s: CGSize) -> String { "\(Int(s.width)) × \(Int(s.height))" }
}

/// Debug: "!" commands as the agent view shows them, over the composer with
/// one being typed (HMK_DEBUG_COMPOSER=<text>, DebugSnapshot), no daemon.
public struct ComposerSample: View {
    let session: AgentSession
    let text: String
    /// Made-up pictures, when the text has "{n}" chips in it.
    let pictures: [(name: String, data: Data)]
    let dictating: Bool

    public init(model: AppModel, text: String, dictating: Bool = false) {
        session = AgentSession(paneID: "pn_debug", model: model)
        self.text = text
        self.dictating = dictating
        pictures = text.contains("{") ? [Color.orange, .teal, .purple].enumerated().compactMap { i, c in
            let r = ImageRenderer(content: LinearGradient(colors: [c, c.opacity(0.4)], startPoint: .topLeading, endPoint: .bottomTrailing).frame(width: 160, height: 120))
            #if os(macOS)
            guard let tiff = r.nsImage?.tiffRepresentation, let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:]) else { return nil }
            #else
            guard let png = r.uiImage?.pngData() else { return nil }
            #endif
            return ("picture-\(i + 1).png", png)
        } : []
        var v = Hammock_V1_AgentView()
        v.replace = true
        v.model = "claude-opus-5-5"
        v.effort = "xhigh"
        v.contextTokens = 84_000
        v.contextWindow = 1_000_000
        v.blocks = [
            .with { $0.id = "u1"; $0.kind = .user; $0.text = "What changed on this branch?"; $0.turnID = "u1" },
            .with { $0.id = "b1"; $0.kind = .tool; $0.tool = "Bash"; $0.title = "Ran `git status --short`"; $0.input = "git status --short"
                $0.output = " M internal/agentview/claude.go\n M internal/agentview/codex.go\n?? notes.txt"; $0.turnID = "b1" },
            .with { $0.id = "b2"; $0.kind = .tool; $0.tool = "Bash"; $0.title = "Ran `sleep`"; $0.input = "sleep"
                $0.output = "usage: sleep number[unit] [...]"; $0.error = true; $0.turnID = "b2" },
            // An edit (its diff numbered from its hunk), then a run of tools.
            .with { $0.id = "e1"; $0.kind = .tool; $0.tool = "Edit"; $0.title = "Edited internal/agentview/claude.go"; $0.input = "internal/agentview/claude.go"
                $0.diff = "@@ -648 +648 @@\n \tswitch b.Name {\n-\tcase \"Bash\":\n+\tcase \"Bash\", \"Shell\":\n \t\tblk.Input = str(\"command\")\n"
                $0.added = 1; $0.removed = 1; $0.turnID = "u1" },
            .with { $0.id = "g1"; $0.kind = .tool; $0.tool = "Read"; $0.title = "Read a.go"; $0.input = "a.go"; $0.turnID = "u1" },
            .with { $0.id = "g2"; $0.kind = .tool; $0.tool = "Read"; $0.title = "Read b.go"; $0.input = "b.go"; $0.turnID = "u1" },
            .with { $0.id = "g3"; $0.kind = .tool; $0.tool = "Bash"; $0.title = "Ran `go test ./...`"; $0.input = "go test ./..."; $0.error = true; $0.turnID = "u1" },
            .with { $0.id = "g4"; $0.kind = .tool; $0.tool = "Edit"; $0.title = "Edited a.go"; $0.input = "a.go"; $0.turnID = "u1" },
        ]
        // For "/" completion.
        v.commands = ["/clear", "/compact", "/context", "/review", "/review-pr", "/plugin:pr-tidy", "/approve", "/prompts:draft"]
        v.startCommands = ["/clear", "/compact", "/context", "/review"]
        session.apply(v)
        session.setOpen("b1", true)
        session.setOpen("b2", true)
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Spacer(minLength: 0)
            Composer(session: session, harness: "claude", initialText: text, initialAttachments: pictures, debugDictating: dictating)
        }
        .padding(16)
    }
}
