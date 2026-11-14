import AVFoundation
import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

#if canImport(AppKit)
import AppKit
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

private struct AgentAccentKey: EnvironmentKey { static let defaultValue = Color.accentColor }
// The iPhone's: Hammock's forest green, as the palette has it (the app accent turns light in
// dark mode, too light under white text).
private struct AgentAutomaticKey: EnvironmentKey {
    #if os(macOS)
    static let defaultValue = Color.accentColor
    #else
    static let defaultValue = AgentColor.named("Hammock") ?? .accentColor
    #endif
}
private struct AgentBubbleKey: EnvironmentKey { static let defaultValue = Color.accentColor }
private struct AgentAutomaticBubbleKey: EnvironmentKey {
    #if os(macOS)
    static let defaultValue = Color.accentColor
    #else
    static let defaultValue = AgentColor.bubble("Hammock") ?? .accentColor
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
/// the session referenced.
public struct AgentConversationView: View {
    let model: AppModel
    let paneID: String
    var onShowTerminal: () -> Void = {}
    var onOpenFile: ((String) -> Void)?
    /// The pane's session, held for as long as this view exists (AgentLease).
    @State private var lease: AgentLease?
    private var session: AgentSession? { lease?.session }
    /// A subagent or background shell being looked at instead (read only),
    /// and its session.
    @State private var viewing: AgentSideView?
    @State private var subLease: AgentLease?
    private var subSession: AgentSession? { subLease?.session }
    @State private var shownAt: Date?
    /// Just loaded, while rows settle: the end stays anchored in view.
    @State private var settling = true
    /// Kept at the end for a moment (holdEnd): the keyboard coming or going
    /// with the conversation at its end, so it moves with the keyboard
    /// (followed by a scroll, it jumped the keyboard's height in a frame
    /// while the keyboard slid; going, the scroll view clamped it at once),
    /// or jumped there (Latest), while the rows passed measure.
    @State private var holdingEnd = false
    @State private var holdTick = 0
    /// The conversation has come to rest at its end since it was loaded
    /// (or switched to): until then its rows are kept out of sight, so it
    /// first shows where it lands, not partway up and jumping down as lazy
    /// rows measure their real heights.
    @State private var landed = false
    /// Counts scroll geometry changes while landing: landed once they stop.
    @State private var landingTick = 0
    /// Scrolled to the end of the conversation (as the scroll view last said).
    @State private var atBottom = true
    /// A glide to the end (following what arrives) waiting for new rows to
    /// measure. A reference, so setting it redraws nothing.
    @State private var glide = GlideState()
    private final class GlideState { var pending = false; var tick = 0; var gap: CGFloat = 0 }
    /// The width the conversation is laid out at. While the pane changes
    /// size (a zoom animating, a divider or the window dragged), it stays
    /// at the old width, so the text doesn't reflow every frame (each reflow
    /// re-estimated the lazy rows and the view scrolled about), and is laid
    /// out once at the new width when the size has settled.
    @State private var layoutWidth: CGFloat?
    @State private var resize = ResizeState()
    private final class ResizeState { var tick = 0 }
    /// The first row of the live end (liveStart), in which session.
    @State private var live: (session: ObjectIdentifier, from: String)?
    /// Scrolls rows you open into view.
    @State private var revealer = RowRevealer()
    /// Where the conversation's scrolled to. Not pinned to its bottom edge
    /// (ScrollPosition(edge:)): pinned, SwiftUI put it back at the end on
    /// some layout passes, rows that came while you watched snapping into
    /// view before the glide to them, until a finger scrolled it. It opens
    /// at the end by defaultScrollAnchor.
    @State private var position = ScrollPosition(idType: String.self)
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

    public init(model: AppModel, paneID: String, onShowTerminal: @escaping () -> Void = {}, onOpenFile: ((String) -> Void)? = nil) {
        self.model = model
        self.paneID = paneID
        self.onShowTerminal = onShowTerminal
        self.onOpenFile = onOpenFile
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
            landed = false
            subLease = new.map { AgentLease(model, paneID, subagent: $0.subagent, shell: $0.shell) }
        }
    }

    /// Shown once the conversation's scroll has stopped changing at its
    /// end (rows above it measured), faded in.
    private func land(at gap: CGFloat) {
        landingTick += 1
        let tick = landingTick
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(80))
            guard !landed, tick == landingTick, gap < 2 else { return }
            DevLog.agent("pane \(paneID): landed at its end")
            withAnimation(.easeOut(duration: 0.15)) { landed = true }
        }
    }

    /// However it's going, shown after a moment: rows that never settle
    /// (or a scroll that never says so) don't stay hidden.
    private func landAtLatest() {
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(450))
            if !landed { DevLog.agent("pane \(paneID): landed by timeout, still settling"); withAnimation(.easeOut(duration: 0.15)) { landed = true } }
        }
    }

    /// The pane changed width: hold the conversation at its old width until
    /// the size stops changing, then lay it out once at the new one. At the
    /// end, it stays there throughout (anchored while the rows measure again).
    private func resized(to w: CGFloat) {
        guard let held = layoutWidth else { layoutWidth = w; return }
        guard abs(w - held) > 0.5 else { return }
        let atEnd = glide.gap < 60
        if atEnd { settling = true }
        resize.tick += 1
        let tick = resize.tick
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(180))
            guard resize.tick == tick else { return }
            layoutWidth = w
            guard atEnd else { return }
            await Task.yield()
            revealer.proxy?.scrollTo("bottom", anchor: .bottom)
            try? await Task.sleep(for: .milliseconds(400))
            guard resize.tick == tick else { return }
            revealer.proxy?.scrollTo("bottom", anchor: .bottom)
            settling = false
        }
    }

    private func holdEnd(for ms: Int) {
        holdingEnd = true
        holdTick += 1
        let tick = holdTick
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(ms))
            if holdTick == tick { holdingEnd = false }
        }
    }

    /// To the end, at once (an animated scroll aims at an estimate of rows
    /// not yet laid out, overshooting into blank space and bouncing back as
    /// they measure, worse while messages arrive), then again once they have.
    /// To the marker under the rows, laid out in full: scrollTo(edge:) aims
    /// at the lazy rows' estimated end, and with earlier messages loaded it
    /// overshot into blank space for a moment.
    private func jumpToEnd(_ proxy: ScrollViewProxy) {
        if JankProbe.enabled { JankProbe.shared.jumped() }
        holdEnd(for: 500)
        proxy.scrollTo("bottom", anchor: .bottom)
        atBottom = true
        Task { @MainActor in
            for ms in [80, 300] {
                try? await Task.sleep(for: .milliseconds(ms))
                proxy.scrollTo("bottom", anchor: .bottom)
            }
        }
    }

    private func glideToEnd() {
        glide.pending = false
        // A reply taller than what's in view: to its top, and no further
        // (to the end, it ran past its own start, and you came to its last
        // lines). It stops following there; Latest is for the rest.
        let height = revealer.visible.bottom - revealer.visible.top
        if glide.gap > height * 0.85, let s = viewing != nil ? subSession : session,
           let reply = s.items.last(where: { $0.block?.kind == .text && (s.arrived[$0.id].map { Date.now.timeIntervalSince($0) < 2 } ?? false) }) {
            withAnimation(.smooth(duration: 0.35)) { revealer.proxy?.scrollTo(reply.id, anchor: .top) }
            return
        }
        // To the end as laid out (the marker under the lazy rows):
        // scrollTo(edge:) aims at their estimated height, even once they've
        // measured, overshooting into blank space and springing back.
        withAnimation(.smooth(duration: 0.3)) { revealer.proxy?.scrollTo("bottom", anchor: .bottom) }
    }

    /// What floats at the bottom: jump to latest (scrolled up), then the
    /// prompt to answer, the message box, or why there's neither.
    @ViewBuilder
    private func bottomBar(_ s: AgentSession, _ proxy: ScrollViewProxy) -> some View {
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

    /// Links in the conversation: web and mail links open, file links go to
    /// onOpenFile, and any other scheme (which could open any app) asks first.
    private var linkAction: OpenURLAction {
        OpenURLAction { url in
            if AgentLink.opensDirectly(url) { return .systemAction }
            if url.isFileURL, let open = onOpenFile {
                open(url.path)
                return .handled
            }
            askingToOpen = url
            return .handled
        }
    }

    @ViewBuilder
    private func content(_ s: AgentSession) -> some View {
        if !s.shellID.isEmpty {
            VStack(spacing: 0) {
                ShellHeader(shell: session?.shells.first { $0.id == s.shellID }) { viewing = nil }
                ShellOutputView(session: s)
            }
        } else {
            conversation(s)
        }
    }

    /// Where the live end of the conversation starts in its rows: as it was
    /// loaded, all of it, back to `liveText` of text (kept from then, so a
    /// row doesn't move out of it, and from one stack to the other, as each
    /// new one comes). Even a few lazy rows left the view landing at its end
    /// while their estimated heights corrected themselves, over half a
    /// second and more, coming into view partway up and jumping down; and a
    /// correction after it had landed left it past the end for a frame.
    private func liveStart(_ s: AgentSession) -> Int {
        if let live, live.session == ObjectIdentifier(s), let i = s.items.firstIndex(where: { $0.id == live.from }) { return i }
        return freshLiveStart(s)
    }

    /// Where the live end would start now: back to `liveText` of text from
    /// the end, the last row at least.
    private func freshLiveStart(_ s: AgentSession) -> Int {
        var text = 0
        for i in s.items.indices.reversed() {
            text += s.items[i].textSize
            if text > Self.liveText { return min(i + 1, s.items.count - 1) }
        }
        return 0
    }

    /// How much text the live end starts with, at most: a page from the Mac
    /// (up to 256 KB) can be more than lays out at once without a pause.
    static let liveText = 64_000

    /// How much text, with earlier messages loaded, is still all laid out
    /// in full (a pause as they come, not as they're scrolled through).
    static let allLiveText = 192_000

    /// Keeps where the live end starts (liveStart) from now on, from where
    /// it would be (a new session, or the rows it started at gone).
    private func keepLiveStart(_ s: AgentSession) {
        let i = liveStart(s)
        guard i < s.items.count else { return }
        let from = s.items[i].id
        if live?.session != ObjectIdentifier(s) || live?.from != from { live = (ObjectIdentifier(s), from) }
    }

    /// A row of the conversation.
    private func row(_ item: AgentItem, _ s: AgentSession) -> some View {
        // Equatable: a row only re-renders when its own blocks change, not
        // whenever the session does.
        AgentItemView(item: item, session: s)
            .equatable()
            .id(item.id)
            .onAppear {
                // DevLog: when the newest row first shows, after it was asked for.
                if item.id == s.items.last?.id, !s.shownSinceAsked, let asked = s.askedAt {
                    s.shownSinceAsked = true
                    DevLog.agent("pane \(s.paneID): rows shown after \(DevLog.since(asked)) (\(s.items.count) rows)")
                }
            }
            .jankRow(item.id)
    }

    @ViewBuilder
    private func conversation(_ s: AgentSession) -> some View {
        let split = liveStart(s)
        VStack(spacing: 0) {
            if !s.subagentID.isEmpty {
                SubagentHeader(subagent: session?.subagents.first { $0.id == s.subagentID }, working: s.working) { viewing = nil }
            } else if s.subagents.contains(where: { !$0.finished }) || s.shells.contains(where: { !$0.finished }) {
                BackgroundStrip(subagents: s.subagents.filter { !$0.finished }, shells: s.shells.filter { !$0.finished }) { viewing = $0 }
            }
            ScrollViewReader { proxy in
                ScrollView {
                    // History in a lazy stack, and the live end under it (what
                    // was loaded, back to liveText of it, everything since,
                    // then the tail: what you've sent on its way, Working…)
                    // laid out in full. Only rows a lazy stack has measured have real
                    // heights; the rest are estimates (the average row's,
                    // hundreds of points for a one-line row), and rows arriving
                    // or growing at its end, while the view followed it there,
                    // left it laying out from those estimates: blank space
                    // past the end, the view jumping back, or rows made above
                    // what's in view with nothing showing but the tail until
                    // you scrolled up. What changes as you watch is never
                    // lazy now, and the history above it doesn't change.
                    VStack(alignment: .leading, spacing: 14) {
                        // Left out with nothing in it, or the stack's spacing
                        // would add a gap at the top.
                        if split > 0 || s.hasMore || !s.error.isEmpty && s.blocks.isEmpty {
                            LazyVStack(alignment: .leading, spacing: 14) {
                                if !s.error.isEmpty, s.blocks.isEmpty {
                                    ContentUnavailableView("No conversation yet", systemImage: "bubble.left.and.text.bubble.right", description: Text(s.error))
                                        .frame(maxWidth: .infinity)
                                        .padding(.top, 60)
                                }
                                if s.hasMore {
                                    // On tap only: loading as it appears could chain
                                    // through the whole history whenever the list is
                                    // laid out from the top (as it is mid-push).
                                    Button(s.loadingOlder ? "Loading…" : "Show earlier messages") { s.loadOlder() }
                                        .buttonStyle(.borderless)
                                        .frame(maxWidth: .infinity)
                                }
                                ForEach(s.items[..<split]) { row($0, s) }
                            }
                        }
                        VStack(alignment: .leading, spacing: 14) {
                            ForEach(s.items[split...]) { row($0, s) }
                        }
                        TranscriptTail(session: s)
                            .jankRow("tail")
                        // The very end, the margin under the last row included,
                        // so scrolling to it shows all of that.
                        Color.clear.frame(height: 1).padding(.bottom, 15).id("bottom")
                    }
                    .padding(.horizontal, 18)
                    .padding(.top, 16)
                    .frame(maxWidth: 860, alignment: .leading)
                    .frame(maxWidth: .infinity)
                    .coordinateSpace(.named(RowRevealer.space))
                    .environment(\.agentRowRevealer, revealer)
                    // Working… going (the turn done) or coming: the rows above
                    // move with it, not dropping its height in a frame (at the
                    // end, the scroll view clamped them down at once).
                    .animation(.easeOut(duration: 0.25), value: s.working)
                    .opacity(landed || s.items.isEmpty ? 1 : 0)
                }
                // Open at the bottom, and kept there while what's loaded settles
                // (rows measuring their real heights). Not anchored there after:
                // what arrives while you watch glides into view (below), where
                // keeping the end in view snapped everything up a row's height
                // in one frame.
                .scrollPosition($position)
                .jankScroll()
                .defaultScrollAnchor(.bottom, for: .initialOffset)
                .defaultScrollAnchor(settling || holdingEnd ? .bottom : nil, for: .sizeChanges)
                // Drag down into the keyboard to pull it away with your finger.
                .scrollDismissesKeyboard(.interactively)
                // Follow the conversation while you're at the bottom: a new
                // message (or one finishing its layout, which comes after
                // the jump to it) grows the content below the fold, and the
                // scroll to the newest block alone landed short of it. Leave
                // you be once you've scrolled up to read.
                .onScrollGeometryChange(for: ScrollEdge.self) { g in
                    ScrollEdge(content: g.contentSize.height,
                               gap: g.contentSize.height - g.visibleRect.maxY,
                               container: g.containerSize.height,
                               insets: g.contentInsets.top + g.contentInsets.bottom, top: g.contentInsets.top)
                } action: { old, new in
                    glide.gap = new.gap
                    if !landed, !s.items.isEmpty { land(at: new.gap) }
                    if JankProbe.enabled {
                        JankProbe.shared.viewport = new.container
                        JankProbe.shared.contentHeight = new.content
                        JankProbe.shared.shown = new.top...max(new.top, new.container - new.insets + new.top)
                    }
                    // Not with a picture open: it zooms back to where its picture is.
                    let wasNear = old.gap < 60 && viewedImage == nil
                    if JankProbe.enabled, abs(new.content - old.content) > 0.5, wasNear { DevLog.agent("grow \(Int(new.content - old.content)) gap \(Int(old.gap))→\(Int(new.gap)) settling=\(settling) holding=\(holdingEnd) follow=\(revealer.holdingFollow) pending=\(glide.pending)") }
                    if settling {
                        // Just loaded: the anchor keeps the end in view as rows
                        // measure their real heights.
                    } else if revealer.holdingFollow {
                        // A row opening: it scrolls itself into view (RowRevealer).
                    } else if new.content > old.content + 0.5, wasNear || glide.pending, new.gap > old.gap + 0.5 {
                        // More below the end while you watch: a glide to it,
                        // once it's measured. A row arriving below the fold is
                        // first given an estimated height, the average of the
                        // rows measured so far (hundreds of points while a long
                        // first message of yours is most of them), corrected on
                        // the next layout pass. A glide aimed at the estimate
                        // ran on into blank space past the end, then sprang
                        // back, or snapped further up. So it waits for the
                        // correction (below), or a moment if none comes. Only
                        // from near the end, so not starting again partway
                        // through a glide (which stepped back).
                        glide.pending = true
                        glide.tick += 1
                        let tick = glide.tick
                        Task { @MainActor in
                            try? await Task.sleep(for: .milliseconds(50))
                            if glide.pending, glide.tick == tick { glideToEnd() }
                        }
                    } else if new.content < old.content - 0.5, glide.pending {
                        // The estimate corrected: to the real end, once the
                        // scroll view has caught up (asked in this pass, it
                        // still aimed at the estimate, and sprang back).
                        glide.tick += 1
                        let tick = glide.tick
                        Task { @MainActor in
                            await Task.yield()
                            if glide.pending, glide.tick == tick { glideToEnd() }
                        }
                    } else if new.container < old.container - 0.5, wasNear, !holdingEnd {
                        // Shorter (a prompt card, the message box growing; not
                        // the keyboard, held at the end meanwhile): to the very
                        // end, moving, not jumping the height in a frame.
                        withAnimation(.smooth(duration: 0.35)) { proxy.scrollTo("bottom", anchor: .bottom) }
                    }
                    // Latest once well up from the end; gone again near it (a gap
                    // between the two, so it doesn't flicker at the line).
                    if new.gap > 350 { atBottom = false } else if new.gap < 150 { atBottom = true }
                }
                .onScrollGeometryChange(for: RowRevealer.Visible.self) { g in
                    RowRevealer.Visible(top: g.visibleRect.minY + g.contentInsets.top,
                                        bottom: g.visibleRect.maxY - g.contentInsets.bottom,
                                        gap: g.contentSize.height - g.visibleRect.maxY)
                } action: { old, new in
                    revealer.visible = new
                    if JankProbe.enabled, abs(new.top - old.top) > 20 { DevLog.agent("scrolled \(Int(new.top - old.top)) to \(Int(new.top))") }
                }
                // Switching to this view with the conversation already loaded:
                // the anchor alone can land short of the end while lazy rows
                // above measure their real heights, so land again once they have.
                .onAppear {
                    revealer.proxy = proxy
                    let loaded = !s.blocks.isEmpty
                    // Watched a long time, the live end can grow long: shown
                    // again, it starts afresh, with what's well above lazy.
                    if freshLiveStart(s) > liveStart(s) + 20 { live = nil; keepLiveStart(s) }
                    if !s.items.isEmpty { landAtLatest() }
                    proxy.scrollTo("bottom", anchor: .bottom)
                    Task { @MainActor in
                        for ms in [60, 250] {
                            try? await Task.sleep(for: .milliseconds(ms))
                            proxy.scrollTo("bottom", anchor: .bottom)
                        }
                        // Settled, as when it loads (below): left anchored
                        // at the end (back to a conversation loaded before),
                        // what arrived after snapped into view, not gliding.
                        try? await Task.sleep(for: .seconds(1.2))
                        if loaded { settling = false }
                    }
                }
                .onChange(of: s.items.count, initial: true) { keepLiveStart(s) }
                // Earlier messages loaded (at the top, where you asked for
                // them): what you were reading stays in view, with them
                // above it, not the top of what came in its place.
                .onChange(of: s.items.first?.id) { old, new in
                    guard let old, new != old, let was = s.items.firstIndex(where: { $0.id == old }), was > 0 else { return }
                    // Laid out in full with the rest, while it all was and
                    // there's not too much: lazy, it came in at estimated
                    // heights, rows drawn over each other as you scrolled up
                    // through it, and Latest from there overshooting the end.
                    if liveStart(s) == was, s.items.reduce(0, { $0 + $1.textSize }) <= Self.allLiveText, let first = new {
                        live = (ObjectIdentifier(s), first)
                    }
                    guard glide.gap > 60 else { return }
                    if JankProbe.enabled { JankProbe.shared.jumped() }
                    proxy.scrollTo(old, anchor: .top)
                }
                .onChange(of: landed, initial: true) { if JankProbe.enabled { JankProbe.shared.hidden = !landed } }
                .onDisappear { if JankProbe.enabled { JankProbe.shared.hidden = true } }
                #if os(iOS)
                .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillChangeFrameNotification)) { _ in
                    if glide.gap < 60 { holdEnd(for: 600) }
                    if JankProbe.enabled { JankProbe.shared.jumped(for: 0.6) }
                }
                #endif
                .onChange(of: s.blocks.last?.id) { old, _ in
                    if old == nil {
                        landed = false
                        live = nil
                        keepLiveStart(s)
                        landAtLatest()
                        proxy.scrollTo("bottom", anchor: .bottom)
                        shownAt = .now
                        settling = true
                        Task { @MainActor in
                            try? await Task.sleep(for: .seconds(1.5))
                            settling = false
                        }
                    }
                    // New rows while you're at the end are followed as the
                    // content grows (onScrollGeometryChange): a second animated
                    // scroll here fought that one, the picture stepping back.
                }
                #if DEBUG
                // Dev aid: the iPhone demo's takes (DemoDirector: scroll) scroll the conversation, as a finger would.
                .onReceive(NotificationCenter.default.publisher(for: Notification.Name("HammockDebugScroll"))) { n in
                    guard let to = n.userInfo?["to"] as? String else { return }
                    if to == "latest" { return jumpToEnd(proxy) }  // as the Latest button does
                    withAnimation(.easeInOut(duration: 0.6)) {
                        switch to {
                        case "top": position.scrollTo(y: 0)
                        case "bottom": proxy.scrollTo("bottom", anchor: .bottom)
                        default: position.scrollTo(y: max(0, revealer.visible.top + (Double(to) ?? 0)))  // by that many points
                        }
                    }
                }
                #endif
                .onChange(of: s.pendingSends.count) { old, new in
                    // Sent while scrolled up: down to see it go.
                    if new > old, !atBottom { jumpToEnd(proxy) }
                }
                .onChange(of: s.prompt.options) {
                    // A prompt that came with the first load is already in view.
                    guard let shownAt, Date.now.timeIntervalSince(shownAt) > 0.5 else { return }
                    withAnimation(.snappy) { proxy.scrollTo("bottom", anchor: .bottom) }
                }
                // What you answer or send with (the model and context in it):
                // glass floating over the conversation, which scrolls beneath.
                .overlay(alignment: .bottom) {
                    if !atBottom, landed {
                        Button {
                            jumpToEnd(proxy)
                        } label: {
                            Label("Latest", systemImage: "arrow.down")
                                .font(.callout.weight(.semibold))
                        }
                        .agentGlassButton()
                        .buttonBorderShape(.capsule)
                        .help("Jump to the latest")
                        .padding(.bottom, 10)
                        .transition(.scale(scale: 0.6).combined(with: .opacity))
                    }
                }
                .animation(.snappy(duration: 0.2), value: atBottom)
                .safeAreaInset(edge: .bottom, spacing: 0) { bottomBar(s, proxy) }
                .agentSoftEdges()
                // Held at its width while the pane resizes (layoutWidth), the
                // scroll view and all: narrower meanwhile, it's clipped at the
                // sides. (Rows wider than the scroll view they're in weren't
                // drawn at all.) The pane's width whatever the held view's, so
                // the pane narrowing is seen.
                .frame(width: layoutWidth)
                .frame(minWidth: 0, maxWidth: .infinity)
                .clipped()
            }
        }
        // The whole height (a prompt card may take most of it).
        .background {
            GeometryReader { g in
                Color.clear
                    .onAppear { height = g.size.height; resized(to: g.size.width) }
                    .onChange(of: g.size.height) { _, h in height = h }
                    .onChange(of: g.size.width) { _, w in resized(to: w) }
            }
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

/// A row of the conversation: one block, or a run of tool calls grouped.
/// Where a scroll view's content ends, and how far down you've scrolled.
struct ScrollEdge: Equatable {
    var content: CGFloat
    /// How far the end of the conversation is below what's in view (0 or
    /// less at the end, counting what's under the floating bar).
    var gap: CGFloat
    var container: CGFloat = 0
    /// What's under the bars (the floating message box, the keyboard) at the
    /// bottom and top, together, and at the top alone.
    var insets: CGFloat = 0
    var top: CGFloat = 0
}

/// Brings a row you've just opened into view: all of it if it fits, its top
/// if it doesn't. Opened at the end of the conversation, it stays at the end
/// if the whole row still shows from there. One per conversation, handed to
/// rows in the environment: a reference, so what it's told as you scroll
/// redraws nothing.
@MainActor final class RowRevealer {
    /// What's in view, in the content's coordinates, less what's under the
    /// floating bar, and how far the end is below it.
    struct Visible: Equatable { var top: CGFloat = 0, bottom: CGFloat = 0, gap: CGFloat = 0 }
    var visible = Visible()
    var proxy: ScrollViewProxy?
    /// While a row opens, the conversation doesn't glide to the end as it
    /// grows: where to go is decided here, once the row's laid out.
    var holdingFollow: Bool { Date.now < holdUntil }
    private var holdUntil = Date.distantPast
    /// How far the end was below the view when the row was opened.
    private var gapAtOpen: CGFloat = 0
    static let space = "agentContent"

    func opening() {
        gapAtOpen = visible.gap
        holdUntil = .now + 0.6
    }

    /// The row's frame, opened and laid out, in the content's coordinates.
    func reveal(_ id: String, _ frame: CGRect) {
        holdUntil = .distantPast
        guard let proxy, frame.height > 0 else { return }
        let height = visible.bottom - visible.top
        // At the end when opened: back there, if the whole row shows from there.
        let toEnd = visible.gap - gapAtOpen
        withAnimation(.smooth(duration: 0.3)) {
            if gapAtOpen < 60, frame.minY >= visible.top + toEnd {
                proxy.scrollTo("bottom", anchor: .bottom)
            } else if frame.height > height || frame.minY < visible.top {
                proxy.scrollTo(id, anchor: .top)
            } else if frame.maxY > visible.bottom {
                proxy.scrollTo(id, anchor: .bottom)
            }
        }
    }
}

private struct RowRevealerKey: EnvironmentKey { static let defaultValue: RowRevealer? = nil }
extension EnvironmentValues {
    var agentRowRevealer: RowRevealer? {
        get { self[RowRevealerKey.self] }
        set { self[RowRevealerKey.self] = newValue }
    }
}

/// A row that opens: scrolled into view when it does (RowRevealer).
private struct RevealsWhenOpened: ViewModifier {
    let id: String
    let open: Bool
    @Environment(\.agentRowRevealer) private var revealer
    /// Where the row is, kept out of view state: it changes as it lays out.
    @State private var frame = FrameBox()
    private final class FrameBox { var rect = CGRect.zero }

    func body(content: Content) -> some View {
        content
            .id("reveal:" + id)
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(RowRevealer.space)) } action: { frame.rect = $0 }
            .onChange(of: open) { _, now in
                guard now, let revealer else { return }
                revealer.opening()
                // Once it's laid out open (its frame's the final one at once;
                // what's in it, measuring, a moment later).
                Task { @MainActor in
                    try? await Task.sleep(for: .milliseconds(60))
                    revealer.reveal("reveal:" + id, frame.rect)
                }
            }
    }
}

extension View {
    func revealsWhenOpened(_ id: String, _ open: Bool) -> some View { modifier(RevealsWhenOpened(id: id, open: open)) }
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

struct AgentItemView: View, Equatable {
    let item: AgentItem
    let session: AgentSession

    static func == (a: Self, b: Self) -> Bool { a.item == b.item && a.session === b.session }

    var body: some View {
        let _ = JankProbe.shared.body()
        if let b = item.block {
            switch b.kind {
            case .user:
                UserBubble(text: b.text, rich: b.richText, attachments: b.images.map { .remote($0) }, session: session)
            case .text:
                MarkdownView(text: b.text)
                    .modifier(RevealsReply(arrived: session.arrived[b.id], length: b.text.count))
            case .thinking:
                ThinkingRow(block: b, seconds: item.thoughtSeconds, session: session)
            case .tool where b.tool == "SendUserFile":
                SentFiles(block: b, session: session)
            case .tool:
                ToolRow(block: b, session: session)
            case .question:
                QuestionCard(block: b)
            case .plan:
                PlanCard(block: b)
            case .subagent:
                SubagentCard(block: b, session: session)
            case .note:
                NoteRow(block: b, session: session)
            case .done:
                DoneRow(block: b)
            default:
                EmptyView()
            }
        } else if item.tools.count == 1, let only = item.tools.first {
            ToolRow(block: only, session: session)
        } else {
            ToolGroup(id: item.id, tools: item.tools, session: session)
        }
    }
}

/// A reply that arrives while you watch shows from the top down, under a
/// soft edge sliding down it, as if being written: Claude Code writes a
/// reply to its transcript only once it's finished, so it otherwise came all
/// at once. It's a mask over the reply laid out at its full height, so
/// nothing around it moves while it shows. At once with Reduce Motion, for a
/// short reply, and for one loaded rather than arriving.
struct RevealsReply: ViewModifier {
    /// When the reply arrived (AgentSession.arrived); nil, loaded.
    let arrived: Date?
    let length: Int
    @State private var shown: CGFloat = 1
    @State private var started = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        if arrived == nil {
            content
        } else {
            content
                .modifier(RevealMask(progress: shown))
                .onAppear {
                    guard !started else { return }
                    started = true
                    guard !reduceMotion, let arrived, Date.now.timeIntervalSince(arrived) < 1, length > 80 else { return }
                    shown = 0
                    withAnimation(.easeOut(duration: Self.duration(length))) { shown = 1 }
                }
        }
    }

    /// Longer for a longer reply, but never so long it's in the way.
    static func duration(_ length: Int) -> Double { min(0.8, max(0.2, Double(length) / 3000)) }
}

/// Shows the top `progress` of what it's on, fading out under a soft edge
/// below that.
private struct RevealMask: ViewModifier, Animatable {
    var progress: CGFloat
    var animatableData: CGFloat {
        get { progress }
        set { progress = newValue }
    }

    func body(content: Content) -> some View {
        content.mask {
            GeometryReader { g in
                let h = max(g.size.height, 1)
                let edge = min(90, h * 0.5) / h
                // From the edge wholly above the top to wholly below the bottom.
                let end = progress * (1 + edge)
                LinearGradient(stops: [
                    .init(color: .black, location: min(1, max(0, end - edge))),
                    .init(color: .clear, location: min(1, max(0.0001, end))),
                ], startPoint: .top, endPoint: .bottom)
            }
        }
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
        /// Any other scheme, or a file on another host: ask first.
        case ask
    }

    public static func opening(_ url: URL) -> Opening {
        switch url.scheme?.lowercased() ?? "" {
        case "http", "https": return .web
        case "mailto": return .direct
        case "file":
            // file://server/share… would reach out to another machine.
            let host = url.host(percentEncoded: false) ?? ""
            guard host.isEmpty || host.lowercased() == "localhost", !url.path.isEmpty else { return .ask }
            return .file(url.path)
        default: return .ask
        }
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

struct UserBubble: View {
    @Environment(\.agentBubble) private var accent
    let text: String
    /// The text with a mark where each attachment sat (AgentBlock.richText):
    /// shown with their chips there. "": no chips.
    var rich = ""
    let attachments: [BubbleAttachment]
    let session: AgentSession
    var pending = false
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        HStack {
            Spacer(minLength: 60)
            VStack(alignment: .trailing, spacing: 6) {
                ForEach(Array(attachments.enumerated()), id: \.element.id) { i, a in
                    AttachmentTile(attachment: a, number: i + 1, session: session)
                }
                if !rich.isEmpty || !text.isEmpty {
                    Group {
                        if rich.isEmpty {
                            LinkedText(markdown: text)
                        } else {
                            RichBubbleText(rich: rich, attachments: attachments, session: session)
                        }
                    }
                    .font(fonts.body())
                    .lineSpacing(fonts.spacing)
                    .textSelection(.enabled)
                    .foregroundStyle(.white)
                    .tint(.white)
                    .padding(.horizontal, 15)
                    .padding(.vertical, 10)
                    .background(accent.gradient, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                }
            }
            .environment(\.previewGroup, attachments.map { $0.preview(session) })
            .opacity(pending ? 0.7 : 1)
        }
    }
}

struct MarkdownView: View {
    @Environment(\.agentAccent) private var accent
    let text: String
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(Array(runs.enumerated()), id: \.offset) { n, run in
                switch run {
                case .prose(let text, let links):
                    // Headings, paragraphs, lists and quotes in a row are one
                    // Text, so a selection can run across them.
                    LinkedText(text, key: "md|\(fonts.name)|\(fonts.size)|\(n)|" + self.text)
                        .font(fonts.body())
                        .lineSpacing(fonts.spacing)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .help(links)
                case .block(let block):
                    blockView(block)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Prose comes with where its links go (for a tooltip).
    enum Run { case prose(AttributedString, links: String), block(MDBlock) }

    private var runs: [Run] { Self.runs(text, fonts) }

    /// Parsed and styled once per text and font, not each time the row
    /// renders (and for new messages, ahead of time: see prepare).
    nonisolated static func runs(_ text: String, _ fonts: AgentFonts) -> [Run] {
        RenderCache.get("md|\(fonts.name)|\(fonts.size)|" + text) {
            var out: [Run] = []
            var run: [MDBlock] = []
            func flush() {
                if !run.isEmpty {
                    let text = prose(run, fonts)
                    out.append(.prose(text, links: AgentLink.destinations(text)))
                    run = []
                }
            }
            for b in Markdown.parse(text) {
                switch b {
                case .heading, .paragraph, .list, .quote:
                    run.append(b)
                default:
                    flush()
                    out.append(.block(b))
                }
            }
            flush()
            return out
        }
    }

    /// Renders text into the cache off the main thread, so the row doesn't
    /// parse it as it scrolls into view.
    nonisolated static func prepare(_ texts: [String], _ fonts: AgentFonts) {
        guard !texts.isEmpty else { return }
        Task.detached(priority: .utility) {
            for t in texts {
                for case .block(.code(let lang, let code)) in runs(t, fonts) {
                    _ = CodeBlock.highlighted(code, lang)
                }
            }
        }
    }

    nonisolated private static func prose(_ blocks: [MDBlock], _ fonts: AgentFonts) -> AttributedString {
        var out = AttributedString()
        func styled(_ s: String, _ color: Color) -> AttributedString {
            var a = AttributedString(s)
            a.foregroundColor = color
            return a
        }
        for (n, b) in blocks.enumerated() {
            if n > 0 {
                // A short blank line between blocks, standing in for the
                // VStack's spacing.
                out += AttributedString("\n")
                var gap = AttributedString("\n")
                gap.font = .system(size: fonts.size * 0.45)
                out += gap
            }
            switch b {
            case .heading(let level, let t):
                var h = Markdown.inline(t)
                h.font = fonts.body([1.45, 1.25, 1.1, 1, 1, 1][min(level - 1, 5)], weight: .semibold)
                out += h
            case .paragraph(let t):
                out += Markdown.inline(t)
            case .list(let items):
                for (i, it) in items.enumerated() {
                    if i > 0 { out += AttributedString("\n") }
                    out += AttributedString(String(repeating: "    ", count: it.indent))
                    if let checked = it.checked {
                        out += styled(checked ? "☑︎ " : "☐ ", checked ? .primary : .secondary)
                    } else {
                        out += styled(it.marker + " ", .secondary)
                    }
                    out += Markdown.inline(it.text)
                }
            case .quote(let t):
                for (i, line) in t.components(separatedBy: "\n").enumerated() {
                    if i > 0 { out += AttributedString("\n") }
                    out += styled("▍ ", Color.secondary.opacity(0.5))
                    var q = Markdown.inline(line)
                    q.foregroundColor = .secondary
                    out += q
                }
            default:
                break
            }
        }
        return out
    }

    @ViewBuilder
    private func blockView(_ b: MDBlock) -> some View {
        switch b {
        case .code(let lang, let code):
            CodeBlock(code: code, language: lang)
        case .table(let header, let rows):
            ScrollView(.horizontal, showsIndicators: false) {
                Grid(alignment: .leading, horizontalSpacing: 16, verticalSpacing: 6) {
                    GridRow {
                        ForEach(Array(header.enumerated()), id: \.offset) { _, h in
                            LinkedText(markdown: h).fontWeight(.semibold)
                        }
                    }
                    Divider()
                    ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                        GridRow {
                            ForEach(Array(row.enumerated()), id: \.offset) { _, c in
                                LinkedText(markdown: c).textSelection(.enabled)
                                    .help(AgentLink.destinations(Markdown.inline(c)))
                            }
                        }
                    }
                }
                .font(fonts.body(0.93))
                .padding(12)
                .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 10))
            }
        case .rule:
            Divider()
        default:
            EmptyView()
        }
    }
}

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
    static let markdownLanguages: Set<String> = ["markdown", "md"]

    private var lang: String { language.lowercased() }
    private var key: CodeBlockChoices.Key { .init(language: lang, code: code) }
    private var isMarkdown: Bool { Self.markdownLanguages.contains(lang) }
    private var rendered: Bool { isMarkdown && choices.rendered.contains(key) }

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
                if isMarkdown {
                    Button {
                        choices.toggleRendered(key)
                    } label: {
                        Label(rendered ? "Source" : "Preview",
                              systemImage: rendered ? "chevron.left.forwardslash.chevron.right" : "doc.richtext")
                            .font(.caption)
                    }
                    .help(rendered ? "Show the Markdown source" : "Show the Markdown rendered")
                }
                if !rendered {
                    Button {
                        choices.wrap[key] = !wraps
                    } label: {
                        Label("Wrap", systemImage: "arrow.turn.down.left").font(.caption)
                            .foregroundStyle(wraps ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                    }
                    .help(wraps ? "Don't wrap long lines" : "Wrap long lines")
                    .accessibilityValue(wraps ? "On" : "Off")
                }
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
        if rendered {
            ScrollView(.vertical, showsIndicators: true) {
                MarkdownView(text: code).padding(12)
            }
            .defaultScrollAnchor(.top)
        } else if wraps {
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

/// What you've switched on a code block (wrapped or not, Markdown shown
/// rendered), kept by what the block holds, so it lasts as the row scrolls
/// out of view and back, until the app quits.
@MainActor @Observable final class CodeBlockChoices {
    static let shared = CodeBlockChoices()
    struct Key: Hashable { var language: String; var code: String }
    var wrap: [Key: Bool] = [:]
    var rendered: Set<Key> = []

    func toggleRendered(_ k: Key) {
        if rendered.contains(k) { rendered.remove(k) } else { rendered.insert(k) }
    }
}

func copy(_ s: String) {
    #if canImport(AppKit)
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(s, forType: .string)
    #else
    UIPasteboard.general.string = s
    #endif
}

/// Thinking: its words as a message (Claude's narration), or, when it has
/// none, how long it took.
struct ThinkingRow: View {
    let block: AgentBlock
    let seconds: Int?
    let session: AgentSession
    private var open: Bool { session.isOpen(block.id) }
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        let text = block.text.trimmingCharacters(in: .whitespacesAndNewlines)
        if !text.isEmpty {
            // Narrated thinking reads as a message, as Claude Code and the
            // Claude app show it.
            MarkdownView(text: text)
        } else {
            silent
        }
    }

    private var silent: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button { withAnimation(.snappy) { session.setOpen(block.id, !open) } } label: {
                HStack(spacing: 6) {
                    Image(systemName: "brain").font(.caption)
                    Text(seconds.map { $0 > 1 ? "Thought for \($0)s" : "Thought briefly" } ?? "Thinking")
                    if !block.text.isEmpty { Image(systemName: open ? "chevron.down" : "chevron.right").font(.caption2) }
                }
                .foregroundStyle(.secondary)
                .font(fonts.body(0.9))
            }
            .buttonStyle(.plain)
            .disabled(block.text.isEmpty)
            if open {
                Text(block.text)
                    .font(fonts.body(0.9))
                    .foregroundStyle(.secondary)
                    .lineSpacing(fonts.spacing)
                    .textSelection(.enabled)
                    .padding(.leading, 18)
            }
        }
        .revealsWhenOpened(block.id, open)
    }
}

/// A system note, centred; a compaction's summary opens under it.
struct NoteRow: View {
    let block: AgentBlock
    let session: AgentSession
    private var open: Bool { session.isOpen(block.id) }
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button { withAnimation(.snappy) { session.setOpen(block.id, !open) } } label: {
                HStack(spacing: 4) {
                    Text(block.text)
                    if !block.output.isEmpty { Image(systemName: open ? "chevron.down" : "chevron.right").font(.caption2) }
                }
                .font(.caption)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
            }
            .buttonStyle(.plain)
            .disabled(block.output.isEmpty)
            if open {
                MarkdownView(text: block.output)
                    .font(fonts.body(0.9))
                    .foregroundStyle(.secondary)
            }
        }
        .revealsWhenOpened(block.id, open)
    }
}

/// Files the agent sent you (Claude's SendUserFile): its caption and the
/// images themselves, not a collapsed tool call.
struct SentFiles: View {
    let block: AgentBlock
    let session: AgentSession
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label(block.title, systemImage: "paperclip")
                .font(fonts.body(0.88))
                .foregroundStyle(.secondary)
            if !block.text.isEmpty {
                MarkdownView(text: block.text)
            }
            ForEach(block.images, id: \.ref) { AgentImageView(ref: $0.ref, session: session, maxHeight: 360) }
            let others = block.input.split(separator: "\n").filter { name in !block.images.contains { $0.ref.hasSuffix("/" + name) } }
            if !others.isEmpty {
                Text(others.joined(separator: ", "))
                    .font(fonts.code(0.95))
                    .foregroundStyle(.secondary)
            }
        }
    }
}

/// What follows the transcript: messages on their way, and what Claude's
/// doing. Its own view, so the status line ticking re-renders only this.
struct TranscriptTail: View {
    let session: AgentSession

    var body: some View {
        // A stack of its own, leading, at the conversation's spacing: a Group
        // measured (onGeometryChange, where it's used) is put in one SwiftUI
        // makes, centred, so Working… sat in the middle under what was sent.
        VStack(alignment: .leading, spacing: 14) {
            ForEach(Array(session.pendingSends.enumerated()), id: \.offset) { _, p in
                UserBubble(text: p.text, rich: p.rich, attachments: p.attachments, session: session, pending: true)
                    // Sent: up from the message box, growing into place. It goes
                    // at once when the transcript shows it (in the same place).
                    .transition(.asymmetric(
                        insertion: .offset(y: 80).combined(with: .scale(scale: 0.6, anchor: .bottomTrailing)).combined(with: .opacity),
                        removal: .identity))
            }
            if session.working {
                WorkingRow(status: session.status)
                    .transition(.opacity.combined(with: .offset(y: 6)))
            }
        }
        .animation(.spring(response: 0.42, dampingFraction: 0.72), value: session.pendingSends.count)
        .animation(.easeOut(duration: 0.25), value: session.working)
    }
}

/// A turn's end, as Claude Code says it: "Worked for 30m 31s · done 9:47 AM".
struct DoneRow: View {
    let block: AgentBlock
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "sparkle").accessibilityHidden(true)
            Text(Self.words(durationMs: block.durationMs, doneUnixMs: block.timeUnixMs))
        }
        .font(fonts.body(0.88))
        .foregroundStyle(.secondary)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// "Worked for 30m 31s · done 9:47 AM", with the day too when it wasn't today.
    static func words(durationMs: Int64, doneUnixMs: Int64, now: Date = .now) -> String {
        var s = "Worked for " + duration(ms: durationMs)
        if doneUnixMs > 0 {
            let done = Date(timeIntervalSince1970: TimeInterval(doneUnixMs) / 1000)
            let same = Calendar.current.isDate(done, inSameDayAs: now)
            s += " · done " + done.formatted(date: same ? .omitted : .abbreviated, time: .shortened)
        }
        return s
    }

    /// "45s", "30m 31s", "2h 5m".
    static func duration(ms: Int64) -> String {
        let s = max(Int(ms / 1000), 0)
        if s < 60 { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m \(s % 60)s" }
        return "\(s / 3600)h \((s % 3600) / 60)m"
    }
}

struct WorkingRow: View {
    let status: String
    @Environment(\.agentFonts) private var fonts
    @State private var on = false

    var body: some View {
        // At the prose's size, and only a little dimmed: it's the live line of
        // the conversation, not a footnote. Not quite primary, so the shimmer's
        // light still shows over it in dark mode.
        HStack(spacing: 8) {
            // Turning and breathing, so it reads as at work at a glance.
            Image(systemName: "sparkle")
                .foregroundStyle(Color.orange)
                .symbolEffect(.rotate.clockwise, options: .repeat(.continuous))
                .symbolEffect(.breathe.pulse, options: .repeat(.continuous))
            Text(status.isEmpty ? "Working…" : status)
                .foregroundStyle(.primary.opacity(0.75))
                .lineLimit(1)
                .shimmering()
                .contentTransition(.opacity)
                .animation(.easeOut(duration: 0.2), value: status)
        }
        .font(fonts.body())
    }
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

/// How long a tool call has been running, as the terminal shows it: "12s",
/// "1m 05s", "1h 02m". Ticks each second from the call's own time (a fixed
/// start, so redrawing the row doesn't restart its timer).
struct RunningTime: View {
    let sinceUnixMs: Int64

    var body: some View {
        if sinceUnixMs > 0 {
            let start = Date(timeIntervalSince1970: TimeInterval(sinceUnixMs) / 1000)
            TimelineView(.periodic(from: start, by: 1)) { ctx in
                Text(Self.format(ctx.date.timeIntervalSince(start)))
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.tertiary)
            }
        }
    }

    static func format(_ seconds: TimeInterval) -> String {
        let s = max(Int(seconds), 0)
        if s < 60 { return "\(s)s" }
        if s < 3600 { return String(format: "%dm %02ds", s / 60, s % 60) }
        return String(format: "%dh %02dm", s / 3600, (s % 3600) / 60)
    }
}

struct StatusDot: View {
    let block: AgentBlock

    var body: some View {
        if block.pending {
            HStack(spacing: 6) {
                RunningTime(sinceUnixMs: block.timeUnixMs)
                ProgressView().controlSize(.mini)
            }
        } else if block.error {
            Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
        } else {
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green.opacity(0.8))
        }
    }
}

struct ToolRow: View {
    let block: AgentBlock
    let session: AgentSession
    var compact = false
    private var open: Bool { session.isOpen(block.id) }
    @Environment(\.agentFonts) private var fonts

    /// The command or input shown when open (a Read's path is in its title already).
    private var showsInput: Bool { !block.input.isEmpty && (block.tool == "Bash" || !block.tool.hasPrefix("Read")) }
    /// Opening shows something more than the row (and its pictures) does.
    private var opens: Bool { !block.diff.isEmpty || showsInput || !block.output.isEmpty }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Button { if opens { withAnimation(.snappy) { session.setOpen(block.id, !open) } } } label: {
                HStack(spacing: 8) {
                    Image(systemName: toolIcon(block)).frame(width: 18).foregroundStyle(.tertiary)
                    Text(Markdown.inlineText(block.title.isEmpty ? block.tool : block.title))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    if block.added > 0 || block.removed > 0 {
                        Text("+\(block.added)").foregroundStyle(.green)
                        Text("−\(block.removed)").foregroundStyle(.red)
                    }
                    Spacer(minLength: 4)
                    StatusDot(block: block).font(.caption)
                    Image(systemName: open ? "chevron.down" : "chevron.right").font(.caption2).foregroundStyle(.tertiary)
                        .opacity(opens ? 1 : 0)
                }
                .font(fonts.body(0.88))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            // Pictures it read or made show without opening it, as in the Claude app.
            if !block.images.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(block.images, id: \.ref) { AgentImageView(ref: $0.ref, session: session, maxHeight: 300) }
                }
                .padding(.leading, 26)
            }
            if !open, !block.diff.isEmpty {
                // An edit shows what changed, as the terminal does: its first
                // lines, and the rest a tap away.
                DiffView(diff: block.diff, preview: compact ? 4 : 8)
                    .contentShape(Rectangle())
                    .onTapGesture { withAnimation(.snappy) { session.setOpen(block.id, true) } }
                    .padding(.leading, 26)
            }
            if open, opens {
                VStack(alignment: .leading, spacing: 8) {
                    if !block.diff.isEmpty {
                        DiffView(diff: block.diff)
                    } else if showsInput {
                        CodeBlock(code: block.input, language: block.tool == "Bash" ? "bash" : "", maxHeight: 200, wrapsByDefault: true)
                    }
                    if !block.output.isEmpty {
                        CodeBlock(code: block.output, language: "output", maxHeight: 260, wrapsByDefault: true)
                    }
                }
                .padding(.leading, 26)
            }
        }
        .padding(.vertical, compact ? 1 : 3)
        .revealsWhenOpened(block.id, open)
    }
}

struct ToolGroup: View {
    let id: String
    let tools: [AgentBlock]
    let session: AgentSession
    private var open: Bool { session.isOpen(id) }
    @Environment(\.agentFonts) private var fonts

    private var summary: String {
        "Used \(tools.count) tools"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Button { withAnimation(.snappy) { session.setOpen(id, !open) } } label: {
                HStack(spacing: 8) {
                    Image(systemName: "square.stack.3d.up").frame(width: 18).foregroundStyle(.tertiary)
                    Text(summary).foregroundStyle(.secondary)
                    if let last = tools.last, last.pending {
                        Text("· " + last.title).foregroundStyle(.tertiary).lineLimit(1).truncationMode(.middle)
                    }
                    Spacer(minLength: 4)
                    if let running = tools.last(where: \.pending) {
                        RunningTime(sinceUnixMs: running.timeUnixMs)
                        ProgressView().controlSize(.mini)
                    } else if tools.contains(where: \.error) {
                        Image(systemName: "exclamationmark.circle.fill").foregroundStyle(.orange).font(.caption)
                    }
                    Image(systemName: open ? "chevron.down" : "chevron.right").font(.caption2).foregroundStyle(.tertiary)
                }
                .font(fonts.body(0.88))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if open {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(tools, id: \.id) { ToolRow(block: $0, session: session, compact: true) }
                }
                .padding(.leading, 26)
            } else if tools.contains(where: { !$0.images.isEmpty }) {
                // Closed, its tools' pictures still show (each tool shows its own open).
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(tools.flatMap(\.images), id: \.ref) { AgentImageView(ref: $0.ref, session: session, maxHeight: 300) }
                }
                .padding(.leading, 26)
            }
        }
        .revealsWhenOpened(id, open)
    }
}

struct DiffView: View {
    let diff: String
    /// Show only this many lines (and how many more there are), unscrolled.
    var preview: Int? = nil
    @Environment(\.agentFonts) private var fonts
    /// The card's width: each line's tint runs all the way across it.
    @State private var width: CGFloat = 0

    private var allLines: [Substring] {
        var l = diff.split(separator: "\n", omittingEmptySubsequences: false)
        if l.last?.isEmpty == true { l.removeLast() }
        return l
    }

    private func linesView(_ shown: [Substring], more: Int) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(shown.enumerated()), id: \.offset) { _, line in
                let s = String(line)
                Text(s.isEmpty ? " " : s)
                    .font(fonts.code(0.95))
                    .foregroundStyle(s.hasPrefix("@@") ? Color.secondary : Color.primary)
                    .padding(.horizontal, 10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    // Square rows that meet, one band per run of lines (a
                    // plain colour here can take the card's rounded corners).
                    .background {
                        Rectangle().fill(s.hasPrefix("+") ? Color.green.opacity(0.14) : s.hasPrefix("-") ? Color.red.opacity(0.14) : .clear)
                    }
            }
        }
        .frame(minWidth: width, alignment: .leading)
        .fixedSize(horizontal: true, vertical: false)
        .padding(.top, 8)
        .padding(.bottom, more > 0 ? 4 : 8)
    }

    var body: some View {
        let all = allLines
        let shown = Array(all.prefix(preview ?? 600))
        let more = preview == nil ? 0 : all.count - shown.count
        VStack(alignment: .leading, spacing: 0) {
            if preview != nil {
                // A glance: no scrolling of its own (a tap shows it all).
                ScrollView(.horizontal, showsIndicators: false) { linesView(shown, more: more) }
                    .scrollDisabled(true)
                if more > 0 {
                    Text(more == 1 ? "1 more line" : "\(more) more lines")
                        .font(fonts.body(0.8))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 10)
                        .padding(.bottom, 8)
                }
            } else {
                ScrollView([.horizontal, .vertical]) { linesView(shown, more: more).textSelection(.enabled) }
                    .frame(maxHeight: 360)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .background(.quaternary.opacity(0.45))
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

// MARK: Cards

struct Card<Content: View>: View {
    var tint: Color?
    @ViewBuilder var content: Content
    @Environment(\.agentAccent) private var accent

    var body: some View {
        VStack(alignment: .leading, spacing: 10) { content }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background((tint ?? accent).opacity(0.07), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder((tint ?? accent).opacity(0.18)))
    }
}

struct QuestionCard: View {
    @Environment(\.agentAccent) private var accent
    let block: AgentBlock
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        Card {
            Label("Claude asked", systemImage: "questionmark.bubble").font(.caption).foregroundStyle(.secondary)
            ForEach(Array(block.questions.enumerated()), id: \.offset) { _, q in
                VStack(alignment: .leading, spacing: 4) {
                    Text(q.question).font(fonts.body(weight: .medium))
                    if !q.answer.isEmpty {
                        Label(q.answer, systemImage: "arrowshape.turn.up.left.fill")
                            .font(fonts.body(0.93))
                            .foregroundStyle(accent)
                        if !q.notes.isEmpty {
                            Label(q.notes, systemImage: "text.bubble")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    } else if block.pending {
                        Text("Waiting for your answer").font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            if !block.output.isEmpty, block.error {
                Text(block.output).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

struct PlanCard: View {
    let block: AgentBlock

    var body: some View {
        Card(tint: .purple) {
            HStack {
                Label("Plan", systemImage: "list.bullet.clipboard").font(.caption).foregroundStyle(.secondary)
                Spacer()
                if block.pending {
                    Text("Waiting for approval").font(.caption).foregroundStyle(.secondary)
                } else if block.error {
                    Label("Not approved", systemImage: "arrow.uturn.backward").font(.caption).foregroundStyle(.secondary)
                } else {
                    Label("Approved", systemImage: "checkmark").font(.caption).foregroundStyle(.green)
                }
            }
            MarkdownView(text: block.text)
        }
    }
}

struct SubagentCard: View {
    let block: AgentBlock
    let session: AgentSession
    private var open: Bool { session.isOpen(block.id) }
    @Environment(\.agentFonts) private var fonts

    var body: some View {
        Card(tint: .teal) {
            Button { withAnimation(.snappy) { session.setOpen(block.id, !open) } } label: {
                HStack(spacing: 8) {
                    Image(systemName: "person.2.wave.2").foregroundStyle(.teal)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(block.title.isEmpty ? "Subagent" : block.title).font(fonts.body(weight: .medium))
                        if !block.text.isEmpty { Text(block.text).font(.caption).foregroundStyle(.secondary) }
                    }
                    Spacer()
                    StatusDot(block: block).font(.caption)
                    Image(systemName: open ? "chevron.down" : "chevron.right").font(.caption2).foregroundStyle(.tertiary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            if open {
                if !block.input.isEmpty {
                    Text("Asked to").font(.caption).foregroundStyle(.secondary)
                    MarkdownView(text: block.input)
                }
                if !block.output.isEmpty {
                    Divider()
                    Text("Reported").font(.caption).foregroundStyle(.secondary)
                    MarkdownView(text: block.output)
                }
            }
        }
        .revealsWhenOpened(block.id, open)
    }
}

struct AgentImageView: View {
    let ref: String
    let session: AgentSession
    var maxHeight: CGFloat = 260
    /// A video's ("video/…"): shown by its first frame.
    var mediaType = ""
    @State private var image: PlatformImage?
    @State private var failed = false

    init(ref: String, session: AgentSession, maxHeight: CGFloat = 260, mediaType: String = "") {
        self.ref = ref
        self.session = session
        self.maxHeight = maxHeight
        self.mediaType = mediaType
        _image = State(initialValue: session.decodedImage(ref))
    }
    /// Opens it in the conversation's viewer (which outlives this row: new
    /// messages regroup rows, and the viewer stays open through that); on
    /// the Mac, QuickLook, among the message's other attachments.
    @Environment(\.openAgentImage) private var open
    @Environment(\.agentImageZoom) private var zoom
    @Environment(\.previewGroup) private var group

    private var isVideo: Bool { mediaType.hasPrefix("video/") }
    private var name: String {
        let last = (ref as NSString).lastPathComponent
        return (last as NSString).pathExtension.isEmpty ? "attachment." + PreviewFiles.ext(mediaType.isEmpty ? "image/png" : mediaType) : last
    }

    var body: some View {
        Group {
            if let image {
                #if canImport(AppKit)
                Image(nsImage: image).resizable().scaledToFit()
                #else
                Image(uiImage: image).resizable().scaledToFit()
                #endif
            } else {
                RoundedRectangle(cornerRadius: 10).fill(.quaternary).frame(width: 120, height: 80)
                    .overlay {
                        if failed {
                            Image(systemName: isVideo ? "film" : "photo").foregroundStyle(.secondary)
                        } else {
                            ProgressView().controlSize(.small)
                        }
                    }
            }
        }
        .frame(maxHeight: maxHeight)
        .overlay { if isVideo, image != nil { PlayBadge() } }
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        #if os(iOS)
        .modifier(ZoomSource(id: ref, in: zoom))
        #endif
        .task(id: ref) {
            guard image == nil else { return }
            if isVideo {
                if let d = await session.file(ref) { image = await VideoPoster.image(data: d, name: name, maxPixels: 800) }
            } else {
                image = await session.image(ref)
            }
            failed = image == nil
        }
        .contentShape(Rectangle())
        .onTapGesture {
            guard image != nil || isVideo else { return }
            openAttachment(PreviewItem(key: ref, name: name) { await session.file(ref) }, group: group, image: image, video: isVideo, open: open)
        }
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
    @FocusState private var focus: Field?
    @Environment(\.agentFonts) private var fonts

    private enum Field: Hashable { case other, notes }

    private var p: AgentPrompt { session.prompt }
    private var pending: AgentBlock? { session.blocks.last { $0.id == p.blockID } }

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
    }

    private func reset() {
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
                if let (i, n) = questionPosition { Text("· \(i) of \(n)").font(.caption).foregroundStyle(.tertiary) }
                Spacer()
                if let (i, _) = questionPosition {
                    // Through the questions, as ← and → do in Claude's form
                    // (past the last is its Submit step).
                    HStack(spacing: 2) {
                        Button { session.model?.send(session.paneID, Data("\u{1b}[D".utf8)) } label: { Image(systemName: "chevron.left").frame(width: 28, height: 24) }
                            .disabled(i <= 1)
                            .help("Previous question")
                        Button { session.model?.send(session.paneID, Data("\u{1b}[C".utf8)) } label: { Image(systemName: "chevron.right").frame(width: 28, height: 24) }
                            .help("Next question")
                    }
                    .buttonStyle(.borderless)
                    .font(.callout.weight(.semibold))
                }
            }
            if let b = pending, p.kind == .permission, !b.input.isEmpty {
                CodeBlock(code: b.input, language: b.tool == "Bash" ? "bash" : "", maxHeight: 140, wrapsByDefault: true)
            }
            if !p.title.isEmpty {
                Text(p.title).font(fonts.body(weight: .semibold)).fixedSize(horizontal: false, vertical: true)
            }
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
                    Button("Continue") { Task { await session.answer(ticking: ticked.sorted()) } }
                        .prominentButton()
                        .keyboardShortcut(.defaultAction)
                }
            }
            if let err = session.answerError {
                HStack {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                    Text(err).font(.caption)
                    Spacer()
                    Button("Answer in Terminal") { session.answerError = nil; onShowTerminal() }
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
        let picked = p.notes && n == chosen
        let (text, isRecommended) = Self.recommended(label)
        Button {
            if isOther {
                typing = true
                focus = .other
            } else if p.multiSelect {
                if ticked.contains(n) { ticked.remove(n) } else { ticked.insert(n) }
            } else if p.notes {
                chosen = n // answered with Answer, under its preview
            } else {
                tapped = n
                Task { await session.answer(option: n) }
            }
        } label: {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                if p.multiSelect, !isOther {
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
                        if isRecommended {
                            Text("Recommended").font(.caption2.weight(.semibold)).foregroundStyle(accent)
                                .padding(.horizontal, 6).padding(.vertical, 1)
                                .background(Capsule().fill(accent.opacity(0.15)))
                        }
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
                .onSubmit(sendOther)
                .onChange(of: otherText) { _, t in
                    // Return in a field that grows: send, not a new line.
                    if t.contains("\n") {
                        otherText = t.replacingOccurrences(of: "\n", with: "")
                        sendOther()
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
                .onSubmit(sendChosen)
                .onChange(of: notes) { _, t in
                    if t.contains("\n") {
                        notes = t.replacingOccurrences(of: "\n", with: "")
                        sendChosen()
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
            .keyboardShortcut(.defaultAction)
            .disabled(chosen < 1)
        }
    }

    private func sendChosen() {
        guard chosen >= 1, !session.answering else { return }
        let n = notes.trimmingCharacters(in: .whitespacesAndNewlines)
        focus = nil
        Task { await session.answer(option: chosen, notes: n) }
    }

    private func sendOther() {
        let t = otherText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty, !session.answering else { return }
        tapped = Int(p.other)
        focus = nil
        Task { await session.answer(option: Int(p.other), text: t) }
    }
}

/// Debug: the prompt card with a made-up question and no daemon
/// (HMK_DEBUG_PROMPT=plain|other|preview|tasks, DebugSnapshot): plain, one
/// with its "Type something" option being typed, one with previews, or the
/// plain one under a task list and a session's links.
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
            if kind == "preview" {
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
    static func at(_ cursor: String.Index, in text: String) -> Range<String.Index>? {
        guard cursor >= text.startIndex, cursor <= text.endIndex else { return nil }
        if cursor < text.endIndex, !text[cursor].isWhitespace { return nil }
        var start = cursor
        while start > text.startIndex, !text[text.index(before: start)].isWhitespace { start = text.index(before: start) }
        guard start < cursor, text[start] == "/" else { return nil }
        return start..<cursor
    }

    /// The token starts the message: nothing but spaces and line breaks
    /// before it. Agents' built-in commands only run there.
    static func startsMessage(_ token: Range<String.Index>, in text: String) -> Bool {
        text[..<token.lowerBound].allSatisfy(\.isWhitespace)
    }

    /// The commands to offer for the token: up to eight that it begins,
    /// leaving out those that only run at the start of a message (built-ins,
    /// sent as text later in one) when it isn't there.
    static func suggestions(for token: Range<String.Index>, in text: String, from commands: [String], startOnly: Set<String>) -> [String] {
        let q = text[token].lowercased()
        let skip = startsMessage(token, in: text) ? [] : startOnly
        return Array(commands.filter { !skip.contains($0) && $0.lowercased().hasPrefix(q) }.prefix(8))
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
    @State private var sendError: String?
    /// What was last taken from the terminal's input box into this one (kept
    /// in step while it's unedited), and whether a send is waiting for the
    /// terminal's box to empty.
    @State private var tookDraft = ""
    @State private var awaitingClear = false
    /// What was just sent, and until when anything of it coming back (the
    /// keyboard committing an autocorrection, the terminal briefly holding
    /// the message as it's typed there) is a ghost, not something you typed.
    @State private var justSent: (text: String, until: Date)?
    /// A video being made smaller to send.
    @State private var shrinking = 0
    @State private var focused = false
    /// Words going in as you say them (the mic button).
    @State private var dictation = ComposerDictation()
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
    private var slashToken: Range<String.Index>? {
        var at = text.endIndex
        if let cursor {
            guard cursor <= text.utf16.count, let i = String.Index(String.Index(utf16Offset: cursor, in: text), within: text) else { return nil }
            at = i
        }
        return SlashToken.at(at, in: text)
    }

    private var suggestions: [String] {
        guard let r = slashToken else { return [] }
        return SlashToken.suggestions(for: r, in: text, from: session.commands, startOnly: session.startCommands)
    }

    /// Sets the text from code, not typing.
    private func setDoc(_ d: ComposerDoc) {
        doc = d
        cursor = nil
        controller.set(d)
    }

    private func replaceText(_ t: String) { setDoc(ComposerDoc(plain: t)) }

    private func complete(_ command: String) {
        guard let r = slashToken else { return }
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
    private static let shellTint = Color(red: 0.99, green: 0.36, blue: 0.69)

    private var canSend: Bool { !doc.plain.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !doc.chips.isEmpty }
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
            doc = d
            cursor = at
        }
        c.onSubmit = send
        c.onTab = {
            guard let s = suggestions.first else { return false }
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
        return c
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

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !suggestions.isEmpty {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(suggestions, id: \.self) { s in
                        Button { complete(s) } label: {
                            HStack {
                                Text(s).font(fonts.code())
                                Spacer(minLength: 8)
                                // Tab takes the top one: shown as the command
                                // palette shows a shortcut.
                                if s == suggestions.first {
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
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .help(s == suggestions.first ? "Complete (Tab)" : "")
                    }
                }
                .padding(.vertical, 6)
                .agentGlass(in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if dictation.problem != nil || dictation.installing != nil {
                DictationNotice(dictation: dictation, openSettings: openMicrophoneSettings)
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
                ComposerTextView(controller: configured(), chipsKey: chipsKey, text: text)
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
                    .help("Attach images")
                    if shrinking > 0 {
                        ProgressView().controlSize(.small)
                        Text("Preparing video…").font(.caption).foregroundStyle(.secondary)
                    }
                    // The model and how full its context is, beside the attach
                    // button.
                    // Dictating, your voice instead.
                    if dictation.active {
                        DictationLevels(dictation: dictation)
                            .padding(.horizontal, 4)
                            .transition(.opacity)
                    } else {
                        if shrinking == 0, !session.agentModel.isEmpty {
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
                    // stopping too (its icon says so).
                    .tint(shellCommand && !stops ? Self.shellTint : bubble)
                    .foregroundStyle(.white)
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
            Task {
                for item in items {
                    if item.supportedContentTypes.contains(where: { $0.conforms(to: .movie) }) {
                        if let movie = try? await item.loadTransferable(type: PickedMovie.self) {
                            await attachVideo(movie.url, name: "video-\(attachments.count + 1).mp4")
                            try? FileManager.default.removeItem(at: movie.url)
                        } else {
                            sendError = "Couldn't load that video."
                        }
                    } else if let d = try? await item.loadTransferable(type: Data.self) {
                        attach(name: "photo-\(attachments.count + 1).jpg", data: d)
                    }
                }
            }
        }
        .alert("Couldn't attach it", isPresented: Binding(get: { sendError != nil }, set: { if !$0 { sendError = nil } })) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(sendError ?? "")
        }
        .dropDestination(for: Data.self) { items, _ in
            for d in items where PlatformImage(data: d) != nil { attach(name: "dropped-\(attachments.count + 1).png", data: d) }
            return true
        }
        .onAppear {
            if text.isEmpty && attachments.isEmpty {
                if let initialText { sample(initialText) } else { loadDraft() }
            }
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
        #if os(iOS)
        // A new chat (a Claude tab just started): straight to typing, without
        // a tap on the box first. Once it's arrived, not while it loads.
        .task(id: session.heard) {
            guard session.heard, session.blocks.isEmpty, session.pastSession == nil else { return }
            // After the push into the view: focus taken mid-transition is lost.
            try? await Task.sleep(for: .milliseconds(350))
            if !Task.isCancelled, session.blocks.isEmpty { controller.focus() }
        }
        #endif
        .onChange(of: session.paneID) { old, _ in
            // What was being said stays in the pane it was said in.
            dictation.stopNow()
            DictationRequests.shared.set(old, listening: false)
            DictationRequests.shared.set(old, composer: false)
            DictationRequests.shared.set(session.paneID, composer: true)
            saveDraft(key: "agentDraft." + old, pane: old)
            attachments = []
            thumbs = [:]
            loadDraft()
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
            if !Task.isCancelled { saveDraft(key: draftKey, pane: session.paneID) }
        }
        .onDisappear {
            dictation.stopNow()
            DictationRequests.shared.set(session.paneID, listening: false)
            DictationRequests.shared.set(session.paneID, composer: false)
            saveDraft(key: draftKey, pane: session.paneID)
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
                              onOpen: { openTray(a) },
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
                await attachVideo(url, name: url.deletingPathExtension().lastPathComponent + ".mp4")
            } else if let d = try? Data(contentsOf: url) {
                // A picture can go by its path too, when it's not made smaller.
                attach(name: url.lastPathComponent, data: d, file: local ? url : nil)
            }
        }
    }

    /// Attaches a video, made small enough to send (540p H.264): a phone's
    /// own is usually too big for a message to the Mac.
    private func attachVideo(_ url: URL, name: String) async {
        shrinking += 1
        defer { shrinking -= 1 }
        do {
            let data = try await shrinkVideo(url)
            guard data.count <= 30 << 20 else {
                sendError = "That video is too long to send (\(data.count >> 20) MB after shrinking it; 30 MB at most)."
                return
            }
            attach(name: name, data: data)
        } catch {
            sendError = "Couldn't prepare that video: \(error.localizedDescription)"
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

    // MARK: Drafts

    /// What a saved draft depends on: saved again when it changes.
    private var draftSignature: String {
        doc.text + "|" + doc.chips.map(\.uuidString).joined() + "|" + attachments.map { "\($0.id)\($0.size?.rawValue ?? "")" }.joined()
    }

    private func saveDraft(key: String, pane: String) {
        let d = UserDefaults.standard
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
        guard canSend else { return }
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
                    Task { await session.send("/model", pending: false) }
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
                let tint: Color = fraction > 0.9 ? .red : fraction > 0.75 ? .orange : .secondary
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
                .help("\(Self.tokens(session.contextTokens)) of \(Self.tokens(session.contextWindow)) tokens")
                .animation(.snappy, value: fraction)
            }
        }
        .font(fonts.body(inline ? 0.76 : 0.82))
        .foregroundStyle(inline ? .secondary : .primary)
        .modifier(StatusChrome(inline: inline))
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
            .overlay { if attachment.isVideo, thumbnail != nil { Image(systemName: "play.fill").foregroundStyle(.white).shadow(radius: 2) } }
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
        ]
        session.apply(v)
        session.setOpen("b1", true)
        session.setOpen("b2", true)
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if pictures.isEmpty {
                ForEach(session.blocks, id: \.id) { b in
                    if b.kind == .tool { ToolRow(block: b, session: session) } else { UserBubble(text: b.text, attachments: [], session: session).frame(maxWidth: .infinity, alignment: .trailing) }
                }
            } else {
                // A sent message with its chips, as from the composer.
                let sent = pictures.prefix(2).enumerated().map { BubbleAttachment.local(id: UUID(uuidString: "00000000-0000-0000-0000-00000000000\($0.offset)")!, name: $0.element.name, data: $0.element.data) }
                UserBubble(text: "Compare with", rich: "Compare \u{FFFC} with \u{FFFC}: which has the bug?", attachments: sent, session: session)
            }
            Spacer(minLength: 0)
            Composer(session: session, harness: "claude", initialText: text, initialAttachments: pictures, debugDictating: dictating)
        }
        .padding(16)
    }
}

/// Debug: code blocks with long lines, with no daemon (HMK_DEBUG_CODEBLOCKS=1,
/// DebugSnapshot): a Swift block (not wrapped), a text block (wrapped), and a
/// Markdown file in four backticks, holding a fenced block of its own, shown
/// as source and rendered.
public struct CodeBlockSample: View {
    static let markdown = """
    # Release plan

    Ship the **wrap toggle** first, then the preview. This line is long enough that it runs well past the edge of the block, so it has to wrap.

    ```swift
    let wraps = CodeBlock.proseLanguages.contains(lang)
    ```

    - [x] Parse nested fences
    - [ ] Ask for a review
    """
    static let text = """
    ````markdown
    \(markdown)
    ````

    ```swift
    let block = CodeBlock(code: source, language: "swift", maxHeight: nil) // a long line of code that doesn't wrap by default
    ```

    ```text
    Plain text in a code block wraps by default, because it's words rather than code, and scrolling sideways to read a sentence is no fun.
    ```
    """

    public init() {
        let md = Markdown.parse(Self.text).compactMap { b -> String? in
            if case .code(let l, let c) = b, l == "markdown" { return c } else { return nil }
        }
        if let c = md.first {
            // The rendered copy: the same Markdown, keyed apart by a trailing newline.
            CodeBlockChoices.shared.rendered.insert(.init(language: "markdown", code: c + "\n"))
        }
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                MarkdownView(text: Self.text)
                CodeBlock(code: Self.markdown + "\n", language: "markdown")
            }
            .padding(16)
        }
        .environment(\.agentFonts, AgentFonts.current)
    }
}
