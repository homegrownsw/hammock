import Observation
import SwiftUI
#if canImport(AppKit)
import AppKit
#else
import UIKit
#endif

/// The program running in a terminal pane, as an icon: vim, htop,
/// docker, a Python script… The daemon names the program in front
/// (`Pane.program`, most specific name first); the first name with an icon
/// wins, else the terminal glyph (a shell at its prompt, or a program Hammock
/// doesn't know). An agent shows its logo (`AgentMark`). Icons are
/// monochrome, drawn in the text's secondary colour like SF Symbols:
/// Simple Icons' logos where there's one, SF Symbols for the rest (the
/// tables are generated: ProgramIconData.swift). Colourful
/// (program-icons = colorful), each sits on a squircle in its brand's
/// colours, agents' logos too (in white on theirs); off, every program but
/// an agent shows the terminal glyph.
public struct ProgramIcon: View {
    let names: [String]
    var size: CGFloat
    /// The style to draw in, instead of program-icons' (Settings' pictures).
    var style: ProgramIconStyle?

    @Environment(\.programSquircleScale) private var squircleScale
    /// An agent's mark, whatever its harness (an unknown one gets the
    /// generic chip).
    var agent: String?

    public init(program names: [String], size: CGFloat = 15, style: ProgramIconStyle? = nil) {
        self.names = names
        self.size = size
        self.style = style
    }

    /// An agent's logo, sized and styled like a program's icon (on a
    /// squircle in the colourful style).
    public init(agent harness: String, size: CGFloat = 15, style: ProgramIconStyle? = nil) {
        self.init(program: [], size: size, style: style)
        agent = harness
    }

    public var body: some View {
        let style = style ?? Overrides.shared.style
        let (glyph, key) = agent.map { (Glyph.agent($0), nil) } ?? Self.resolveIcon(names, style: style)
        // A squircle is bigger than a plain glyph (its glyph must still read
        // inside it): it takes more room, rather than spilling onto the name.
        let squircle = style == .colorful
        let side = squircle ? (size * squircleScale).rounded() : size
        ZStack {
            if squircle {
                Squircle(glyph: glyph, colors: Self.builtInColors[key ?? "terminal"] ?? Self.builtInColors["terminal"]!, size: side)
            } else {
                plain(glyph)
            }
        }
        .frame(width: side, height: side)
        // Beside the name it stands for: VoiceOver reads the name.
        .accessibilityHidden(true)
    }

    @ViewBuilder
    private func plain(_ glyph: Glyph) -> some View {
            switch glyph {
            case .agent(let harness):
                AgentMark(harness: harness, size: size - 1)
            case .path(let key):
                // A template image, not a filled shape: in a vibrant
                // sidebar it gets the same treatment as SF Symbols (a
                // shape filled .secondary comes out much fainter there).
                if let image = Self.templateImage(key) {
                    image
                        .resizable()
                        .renderingMode(.template)
                        .scaledToFit()
                        .padding(size * 0.1)
                        .modifier(ProgramTint())
                }
            case .symbol(let name):
                Image(systemName: name)
                    .font(.system(size: size - 2))
                    .modifier(ProgramTint())
            }
    }

    /// How much bigger the colourful style's squircle is than a plain icon
    /// (`programSquircleScale` can make it less, where rows are tight).
    public static let squircleScale: CGFloat = 1.35

    enum Glyph: Equatable {
        case agent(String)
        case path(String)
        case symbol(String)

        var isAgent: Bool { if case .agent = self { true } else { false } }
    }

    /// The agents' harnesses (the daemon's knownAgents): drawn by AgentMark.
    static let harnesses: Set<String> = ["claude", "codex", "opencode", "gemini"]

    /// What an editor pane shows (icons.json maps it to "document").
    public static let editorPane = "hammock-editor"

    /// An icon's colours in the colourful style, 0xRRGGBB.
    struct Colors: Equatable {
        let background: UInt32
        let foreground: UInt32
    }

    /// What `names` shows: an agent's mark, an icon's path (by its key) or
    /// a symbol.
    @MainActor static func resolve(_ names: [String]) -> Glyph { resolveIcon(names, style: Overrides.shared.style).glyph }

    /// What `names` shows, and the built-in icon it is (for its colours;
    /// nil for an agent, or a pane-icon naming an SF Symbol). Off, agents
    /// still show their mark and everything else the terminal.
    @MainActor static func resolveIcon(_ names: [String], style: ProgramIconStyle) -> (glyph: Glyph, key: String?) {
        let terminal = (Glyph.symbol("apple.terminal"), Optional("terminal"))
        if style == .off {
            return names.first(where: harnesses.contains).map { (.agent($0), nil) } ?? terminal
        }
        let overrides = Overrides.shared.glyphs
        for name in names {
            if let g = overrides[name] { return (g, Overrides.shared.keys[name]) }
            if harnesses.contains(name) { return (.agent(name), nil) }
            if let key = builtInPrograms[name], let g = glyph(key: key) { return (g, key) }
        }
        return terminal
    }

    private static func glyph(key: String) -> Glyph? {
        switch builtInGlyphs[key] {
        case .path: .path(key)
        case .symbol(let s): .symbol(s)
        case .agent, nil: nil
        }
    }

    /// The built-in paths, parsed once.
    static let paths: [String: Path] = builtInGlyphs.compactMapValues {
        if case .path(let d) = $0 { SVGPath.parse(d) } else { nil }
    }

    @MainActor private static var templates: [String: Image] = [:]

    /// A built-in path as a template image (24×24 points), made once.
    @MainActor static func templateImage(_ key: String) -> Image? {
        if let image = templates[key] { return image }
        guard let path = paths[key] else { return nil }
        let side: CGFloat = 24
        let fitted = SVGShape(path: path).path(in: CGRect(x: 0, y: 0, width: side, height: side)).cgPath
        #if canImport(AppKit)
        // Drawn on demand, so it's sharp at any size and scale.
        let ns = NSImage(size: NSSize(width: side, height: side), flipped: true) { _ in
            guard let cg = NSGraphicsContext.current?.cgContext else { return false }
            cg.addPath(fitted)
            cg.setFillColor(NSColor.black.cgColor)
            cg.fillPath()
            return true
        }
        ns.isTemplate = true
        let image = Image(nsImage: ns)
        #else
        let format = UIGraphicsImageRendererFormat()
        format.scale = 4
        let ui = UIGraphicsImageRenderer(size: CGSize(width: side, height: side), format: format).image { ctx in
            ctx.cgContext.addPath(fitted)
            ctx.cgContext.setFillColor(UIColor.black.cgColor)
            ctx.cgContext.fillPath()
        }.withRenderingMode(.alwaysTemplate)
        let image = Image(uiImage: ui)
        #endif
        templates[key] = image
        return image
    }

    private static func symbolExists(_ name: String) -> Bool {
        #if canImport(AppKit)
        NSImage(systemSymbolName: name, accessibilityDescription: nil) != nil
        #else
        UIImage(systemName: name) != nil
        #endif
    }

    /// pane-icon in ~/.config/hammock/config (the Mac's AppConfig sets it), as
    /// what each program shows. Observed, so an edit redraws the icons.
    @MainActor @Observable final class Overrides {
        static let shared = Overrides()
        var glyphs: [String: Glyph] = [:]
        /// The built-in icon each override names, for its colours.
        var keys: [String: String] = [:]
        var style = ProgramIconStyle.plain
    }

    /// program-icons in ~/.config/hammock/config.
    @MainActor public static func setStyle(_ style: ProgramIconStyle) {
        if style != Overrides.shared.style { Overrides.shared.style = style }
    }

    /// Sets pane-icon's values: "btop=monitor", or several separated by
    /// commas (the key can also be given more than once). Each names a
    /// built-in icon (icons.json's names: vim, git, python, monitor…) or an
    /// SF Symbol; others are ignored.
    @MainActor public static func setOverrides(_ values: [String]) {
        var out: [String: Glyph] = [:]
        var keys: [String: String] = [:]
        for (program, icon) in parseOverrides(values) {
            if let g = glyph(key: icon) {
                out[program] = g
                keys[program] = icon
            } else if symbolExists(icon) {
                out[program] = .symbol(icon)
            }
        }
        if out != Overrides.shared.glyphs { Overrides.shared.glyphs = out }
        if keys != Overrides.shared.keys { Overrides.shared.keys = keys }
    }

    static func parseOverrides(_ values: [String]) -> [String: String] {
        var out: [String: String] = [:]
        for value in values {
            for part in value.split(separator: ",") {
                let kv = part.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
                if kv.count == 2, !kv[0].isEmpty, !kv[1].isEmpty { out[kv[0].lowercased()] = kv[1] }
            }
        }
        return out
    }
}

/// How program icons are drawn: program-icons in ~/.config/hammock/config.
public enum ProgramIconStyle: String, CaseIterable, Sendable {
    /// The terminal glyph for everything but an agent.
    case off
    /// Monochrome, in the text's colour.
    case plain
    /// On a squircle in the program's colours.
    case colorful

    /// program-icons' value: off (or false), plain (or true, or unset),
    /// colorful (or colourful).
    public init(config value: String?) {
        switch value?.lowercased() {
        case "off", "false", "none": self = .off
        case "colorful", "colourful", "color", "colour": self = .colorful
        default: self = .plain
        }
    }
}

/// A program's glyph on a squircle in its colours.
private struct Squircle: View {
    let glyph: ProgramIcon.Glyph
    let colors: ProgramIcon.Colors
    let size: CGFloat

    var body: some View {
        let fg = Color(rgb: colors.foreground)
        ZStack {
            if case .agent(let harness) = glyph {
                RoundedRectangle(cornerRadius: size * 0.27, style: .continuous)
                    .fill(AgentMark.squircleFill(harness))
            } else {
                RoundedRectangle(cornerRadius: size * 0.27, style: .continuous)
                    .fill(Color(rgb: colors.background))
            }
            switch glyph {
            case .path(let key):
                if let image = ProgramIcon.templateImage(key) {
                    image
                        .resizable()
                        .renderingMode(.template)
                        .scaledToFit()
                        .padding(size * 0.16)
                        .foregroundStyle(fg)
                }
            case .symbol(let name):
                Image(systemName: name)
                    .font(.system(size: size * 0.58, weight: .semibold))
                    .foregroundStyle(fg)
            case .agent(let harness):
                AgentMark(harness: harness, size: size * 0.64, tint: .white)
            }
        }
    }
}

private extension Color {
    init(rgb: UInt32) {
        self.init(.sRGB, red: Double((rgb >> 16) & 0xFF) / 255, green: Double((rgb >> 8) & 0xFF) / 255, blue: Double(rgb & 0xFF) / 255)
    }
}

/// Program icons' colour, logos and symbols alike: the text's colour,
/// faded most of the way to the secondary colour (about 50%). Logos have
/// fine detail, so the secondary colour is too faint for them (and
/// symbols beside them must match).
private struct ProgramTint: ViewModifier {
    func body(content: Content) -> some View {
        content.foregroundStyle(.primary).opacity(0.6)
    }
}

extension AppModel {
    /// The program in a tab's focused pane, when it's a running terminal
    /// (else none: a page shows its favicon, a simulator the terminal glyph).
    public func focusedProgram(of tab: WorkspaceTab) -> [String] {
        if let p = panes[tab.focusedPaneID], p.isEditor { return [ProgramIcon.editorPane] }
        guard let p = panes[tab.focusedPaneID], p.kind == .terminal || p.kind == .unspecified, !p.exited else { return [] }
        return p.program
    }
}

extension EnvironmentValues {
    /// How much bigger a colourful program icon's squircle is than a plain
    /// one: less in tight rows (the Mac's compact sidebar), so it doesn't
    /// touch the row's edges.
    @Entry public var programSquircleScale: CGFloat = ProgramIcon.squircleScale
}
