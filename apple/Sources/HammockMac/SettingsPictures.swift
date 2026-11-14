import HammockKit
import SwiftUI

// Small drawings of Hammock's window for Settings (and onboarding): each
// setting with something to see shows what it does, in the theme's colours.

/// The colours a drawing is made of, in one appearance.
struct MiniLook {
    var sidebar: Color
    var toolbar: Color
    var terminal: TerminalTheme
    var accent: Color
    var bubble: Color
    var border: Color
    /// The sidebar's tint's opacity over the glass (sidebar-opacity).
    var sidebarAlpha: Double
    var dark: Bool

    private static func native(dark: Bool) -> (sidebar: Color, toolbar: Color) {
        dark ? (Color(white: 0.17), Color(white: 0.21)) : (Color(white: 0.91), Color(white: 0.96))
    }

    /// A curated theme's. `opacity`: the sidebar's, as saved unless given
    /// (only the slider's own picture follows it as it moves, so the rest
    /// don't redraw on every step).
    @MainActor static func of(_ t: AppTheme, dark: Bool, opacity: Double? = nil) -> MiniLook {
        let n = native(dark: dark)
        let opacity = opacity ?? AppConfig.shared.savedSidebarOpacity
        func c(_ p: AppTheme.Pair?) -> Color? { p.map { AppTheme.color($0.value(dark: dark)) } }
        return MiniLook(sidebar: c(t.sidebar) ?? n.sidebar, toolbar: c(t.toolbar) ?? n.toolbar, terminal: dark ? t.dark : t.light,
                        accent: c(t.accent) ?? Color.themeAccent, bubble: c(t.bubble) ?? Color.themeAccent, border: c(t.border) ?? Color.themeAccent,
                        sidebarAlpha: t.sidebar == nil ? opacity : 0.55 + 0.45 * opacity, dark: dark)
    }

    /// What's in use now: the theme's, or Custom's settings.
    @MainActor static func current(dark: Bool, opacity: Double? = nil) -> MiniLook {
        let config = AppConfig.shared
        if let t = config.appTheme { return of(t, dark: dark, opacity: opacity) }
        let n = native(dark: dark)
        let tint = config.sidebarTint(dark: dark, opacity: opacity ?? config.savedSidebarOpacity)
        let toolbar = config.toolbarTint(dark: dark).map { Color(nsColor: $0) }
        let colors = config.agentColors
        return MiniLook(sidebar: tint.map { Color(nsColor: $0.color) } ?? n.sidebar, toolbar: toolbar ?? n.toolbar,
                        terminal: config.theme(isDark: dark), accent: colors.color, bubble: colors.bubble,
                        border: PaneLook.borderColor(dark: dark), sidebarAlpha: tint?.alpha ?? 1, dark: dark)
    }
}

/// What a drawing of the window shows.
struct MiniOptions {
    var layout = SidebarLayout.vertical
    var tabStatus = AppConfig.TabStatus.icon
    var spacious = false
    var dim = false
    var border = false
    var cursor = "block"
    /// The focused pane shows the agent view rather than the terminal.
    var agentView = false
    /// The other pane shows the agent view (theme cards: both kinds of colour).
    var agentViewSecond = false
    var showFinished = false
    var collapsedAgents = false
    /// Draw the desktop behind, so the sidebar's glass shows.
    var wallpaper = false

    /// Settings as they are now.
    @MainActor static var current: MiniOptions {
        let config = AppConfig.shared
        var o = MiniOptions()
        o.layout = .current
        o.tabStatus = config.tabStatus
        o.spacious = config.paneSpacing == .spacious
        o.dim = config.dimInactivePanes
        o.border = config.activePaneBorder
        o.cursor = config.cursorStyleName
        return o
    }
}

/// Hammock's window, small: sidebar, toolbar, and two panes side by side.
struct MiniWindow: View {
    var look: MiniLook
    var options: MiniOptions

    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            HStack(spacing: 0) {
                sidebar.frame(width: max(w * 0.3, 30))
                VStack(spacing: 0) {
                    // The whole tab, toolbar too, the sidebar's colour, compact or spacious.
                    look.sidebar.opacity(look.sidebarAlpha).frame(height: 9)
                    if options.layout == .horizontal { tabBar }
                    panes
                }
            }
        }
        .background {
            if options.wallpaper {
                LinearGradient(colors: [Color(red: 0.95, green: 0.55, blue: 0.35), Color(red: 0.45, green: 0.35, blue: 0.85)], startPoint: .topLeading, endPoint: .bottomTrailing)
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Color.primary.opacity(0.15), lineWidth: 0.5))
        .environment(\.colorScheme, look.dark ? .dark : .light)
    }

    private var ink: Color { look.dark ? .white : .black }

    // MARK: Sidebar

    private var sidebar: some View {
        let compact = options.layout == .compact
        let row: CGFloat = compact ? 5 : 7
        return VStack(alignment: .leading, spacing: compact ? 2 : 3) {
            group(open: true)
            if options.layout != .horizontal {
                tabRow(state: .working, selected: true, height: row)
                tabRow(state: .waiting, selected: false, height: row)
                tabRow(state: nil, selected: false, height: row)
            }
            group(open: false)
            if options.collapsedAgents && options.layout != .horizontal {
                tabRow(state: .working, selected: false, height: row)
            }
            if !options.showFinished { group(open: false) }
            Spacer(minLength: 0)
            if options.showFinished {
                Text("FINISHED").font(.system(size: 4, weight: .bold)).foregroundStyle(ink.opacity(0.4))
                tabRow(state: .done, selected: false, height: row)
            }
        }
        .padding(.horizontal, 4)
        .padding(.top, 8)
        .padding(.bottom, 4)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background {
            ZStack {
                if !options.wallpaper { (look.dark ? Color(white: 0.12) : Color(white: 0.86)) }
                look.sidebar.opacity(look.sidebarAlpha)
            }
        }
    }

    private func group(open: Bool) -> some View {
        HStack(spacing: 2) {
            Image(systemName: open ? "chevron.down" : "chevron.right").font(.system(size: 3.5, weight: .bold))
            Capsule().frame(width: open ? 18 : 14, height: 3)
        }
        .foregroundStyle(ink.opacity(0.45))
    }

    enum AgentState { case working, waiting, done }

    private func statusColor(_ s: AgentState) -> Color {
        switch s {
        case .working: look.accent
        case .waiting: .orange
        case .done: .green
        }
    }

    private func tabRow(state: AgentState?, selected: Bool, height: CGFloat) -> some View {
        HStack(spacing: 2) {
            if let state, options.tabStatus == .icon {
                Circle().fill(statusColor(state)).frame(width: height - 2, height: height - 2)
            } else if state != nil {
                // The agent's mark, its status at the end of the row.
                RoundedRectangle(cornerRadius: 1).fill(Color(red: 0.85, green: 0.47, blue: 0.34)).frame(width: height - 2, height: height - 2)
            } else {
                RoundedRectangle(cornerRadius: 1).strokeBorder(ink.opacity(0.4), lineWidth: 0.5).frame(width: height - 2, height: height - 2)
            }
            Capsule().fill(ink.opacity(selected ? 0.7 : 0.35)).frame(height: 2.5)
            if let state, options.tabStatus == .trailing {
                Circle().fill(statusColor(state)).frame(width: 3.5, height: 3.5)
            }
        }
        .padding(.leading, 4)
        .padding(.trailing, 2)
        .frame(height: height)
        .background(selected ? ink.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 2))
    }

    // MARK: Tab bar

    /// TabStrip: pills in a capsule, the first raised, a round + beside it.
    private var tabBar: some View {
        HStack(spacing: 2) {
            HStack(spacing: 0) {
                ForEach(0..<3) { i in
                    HStack(spacing: 2) {
                        Circle().fill(i == 0 ? look.accent : i == 1 ? .orange : ink.opacity(0.3)).frame(width: 3, height: 3)
                        Capsule().fill(ink.opacity(i == 0 ? 0.6 : 0.3)).frame(height: 2)
                    }
                    .padding(.horizontal, 3)
                    .frame(maxWidth: .infinity)
                    .frame(height: 6)
                    .background(i == 0 ? (look.dark ? Color.white.opacity(0.16) : .white) : .clear, in: Capsule())
                    .overlay(alignment: .trailing) {
                        if i == 1 { Rectangle().fill(ink.opacity(0.2)).frame(width: 0.5, height: 4) }
                    }
                }
            }
            .padding(1)
            .background(ink.opacity(look.dark ? 0.09 : 0.07), in: Capsule())
            Circle().fill(look.dark ? Color.white.opacity(0.16) : .white).frame(width: 8, height: 8)
                .overlay { Image(systemName: "plus").font(.system(size: 5, weight: .bold)).foregroundStyle(ink.opacity(0.5)) }
        }
        // In line with the panes below (spacious, they're inset as far),
        // and that far above them.
        .padding(.leading, options.spacious ? 3 : 0)
        .padding(.trailing, 3)
        .padding(.bottom, options.spacious ? 0 : 3)
        .background(look.sidebar.opacity(look.sidebarAlpha))
    }

    // MARK: Panes

    private var panes: some View {
        let gap: CGFloat = options.spacious ? 3 : 0.5
        let inset: CGFloat = options.spacious ? 3 : 0
        let radius: CGFloat = options.spacious ? 3 : 0
        return HStack(spacing: gap) {
            pane(focused: true, radius: radius)
            pane(focused: false, radius: radius)
        }
        .padding(inset)
        .background(options.spacious ? look.sidebar.opacity(look.sidebarAlpha) : ink.opacity(0.25))
    }

    private func pane(focused: Bool, radius: CGFloat) -> some View {
        let shape = RoundedRectangle(cornerRadius: radius, style: .continuous)
        return Group {
            if agent(focused) { agentContent } else { terminalContent(focused: focused) }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(agent(focused) ? (look.dark ? Color(white: 0.12) : .white) : look.terminal.background.color)
        .overlay { if !focused && options.dim { Color.black.opacity(look.dark ? 0.35 : 0.18) } }
        .clipShape(shape)
        .overlay { if focused && options.border { shape.strokeBorder(look.border, lineWidth: 1.25) } }
    }

    private func agent(_ focused: Bool) -> Bool { focused ? options.agentView : options.agentViewSecond }

    private func terminalContent(focused: Bool) -> some View {
        let t = look.terminal
        let lines: [[(Int, CGFloat)]] = focused
            ? [[(2, 6), (-1, 14)], [(-1, 20), (4, 8)], [(5, 10), (-1, 12), (6, 6)], [(-1, 16)], [(2, 6)]]
            : [[(3, 10), (-1, 8)], [(-1, 18)], [(1, 6), (-1, 10)], [(4, 14)]]
        return VStack(alignment: .leading, spacing: 2.5) {
            ForEach(lines.indices, id: \.self) { i in
                HStack(spacing: 2) {
                    ForEach(lines[i].indices, id: \.self) { j in
                        let (c, w) = lines[i][j]
                        Capsule().fill(c < 0 ? t.foreground.color.opacity(0.75) : t.ansi[c].color).frame(width: w, height: 2.5)
                    }
                    if focused && i == lines.count - 1 { cursor }
                }
            }
        }
        .padding(4)
    }

    @ViewBuilder private var cursor: some View {
        let c = look.terminal.cursor.color
        switch options.cursor {
        case "bar": Rectangle().fill(c).frame(width: 1, height: 5)
        case "bar_thin": Rectangle().fill(c).frame(width: 0.5, height: 5)
        case "underline": Rectangle().fill(c).frame(width: 3.5, height: 0.8).frame(height: 5, alignment: .bottom)
        case "block_hollow": Rectangle().strokeBorder(c, lineWidth: 0.6).frame(width: 3.5, height: 5)
        default: Rectangle().fill(c).frame(width: 3.5, height: 5)
        }
    }

    private var agentContent: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack { Spacer(); Capsule().fill(look.bubble).frame(width: 26, height: 6) }
            Capsule().fill(ink.opacity(0.5)).frame(width: 30, height: 2.5)
            Capsule().fill(ink.opacity(0.35)).frame(width: 22, height: 2.5)
            RoundedRectangle(cornerRadius: 1.5).fill(ink.opacity(0.08)).frame(height: 9)
            Spacer(minLength: 0)
            Capsule().strokeBorder(ink.opacity(0.25), lineWidth: 0.5).frame(height: 6)
        }
        .padding(4)
    }
}

/// The window as it is in this appearance, with one thing changed.
struct CurrentMiniWindow: View {
    /// Follows the sidebar slider as it moves (its own picture).
    var liveOpacity = false
    var change: (inout MiniOptions) -> Void = { _ in }
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        var o = MiniOptions.current
        change(&o)
        return MiniWindow(look: .current(dark: scheme == .dark, opacity: liveOpacity ? AppConfig.shared.sidebarOpacity : nil), options: o)
    }
}

// MARK: - Choosing

/// A picture to click, with its name under it, ringed when it's the choice.
struct PictureCard<Picture: View>: View {
    let title: String
    let selected: Bool
    var width: CGFloat = 132
    var height: CGFloat = 82
    let action: () -> Void
    @ViewBuilder let picture: () -> Picture

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                picture()
                    .frame(width: width, height: height)
                    .padding(3)
                    .overlay(RoundedRectangle(cornerRadius: 9, style: .continuous)
                        .strokeBorder(selected ? Color.themeAccent : .clear, lineWidth: 2.5))
                Text(title)
                    .font(.callout)
                    .foregroundStyle(selected ? .primary : .secondary)
                    .lineLimit(1)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(title)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// A setting chosen by picture: a row of cards under its label.
struct PicturePicker<Value: Hashable, Picture: View>: View {
    let label: String
    let options: [(Value, String)]
    @Binding var selection: Value
    var width: CGFloat = 132
    var height: CGFloat = 82
    @ViewBuilder let picture: (Value) -> Picture

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(label)
            HStack(alignment: .top, spacing: 14) {
                ForEach(options, id: \.0) { value, title in
                    PictureCard(title: title, selected: selection == value, width: width, height: height) {
                        withAnimation(.snappy) { selection = value }
                    } picture: { picture(value) }
                }
                Spacer(minLength: 0)
            }
        }
        .padding(.vertical, 2)
    }
}

/// A switch, its explanation, and a picture of what it does now.
struct PictureToggle<Picture: View>: View {
    let label: String
    var caption: String? = nil
    @Binding var isOn: Bool
    @ViewBuilder let picture: () -> Picture

    var body: some View {
        HStack(alignment: .center, spacing: 16) {
            VStack(alignment: .leading, spacing: 4) {
                Toggle(label, isOn: $isOn.animation(.snappy))
                if let caption {
                    Text(caption).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 0)
            picture().frame(width: 168, height: 84)
        }
    }
}

// MARK: - Themes

/// A theme to pick, drawn as the window it makes, in this appearance.
struct ThemeCard: View {
    let theme: AppTheme
    let selected: Bool
    var width: CGFloat = 112
    var height: CGFloat = 70
    let action: () -> Void
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        PictureCard(title: theme.name, selected: selected, width: width, height: height, action: action) {
            MiniWindow(look: .of(theme, dark: scheme == .dark), options: {
                var o = MiniOptions.current
                o.agentViewSecond = true
                o.border = true
                return o
            }())
        }
        .help(theme.ownTint ? theme.name : "\(theme.name): \(theme.lightTerminalName) and \(theme.darkTerminalName)")
    }
}

/// Custom…, last among the themes: each colour chosen on its own.
struct CustomThemeCard: View {
    let selected: Bool
    var width: CGFloat = 112
    var height: CGFloat = 70
    let action: () -> Void

    var body: some View {
        PictureCard(title: "Custom…", selected: selected, width: width, height: height, action: action) {
            ZStack {
                if selected {
                    CurrentMiniWindow { $0.agentViewSecond = true; $0.border = true }
                } else {
                    RoundedRectangle(cornerRadius: 6, style: .continuous)
                        .fill(Color.primary.opacity(0.05))
                        .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Color.primary.opacity(0.2), style: StrokeStyle(lineWidth: 1, dash: [3, 3])))
                    Image(systemName: "slider.horizontal.3").font(.title2).foregroundStyle(.secondary)
                }
            }
        }
        .help("Choose the terminal, window, agent and border colours each on their own")
    }
}

/// Every theme, then Custom….
struct ThemeGrid: View {
    var width: CGFloat = 112
    var height: CGFloat = 70
    /// Only these (onboarding's few); nil: all.
    var only: [String]? = nil
    var showCustom = true
    private var config: AppConfig { .shared }

    var body: some View {
        let themes = only.map { names in names.compactMap(AppTheme.named) } ?? AppTheme.all
        LazyVGrid(columns: [GridItem(.adaptive(minimum: width + 8), spacing: 10)], spacing: 14) {
            ForEach(themes) { t in
                ThemeCard(theme: t, selected: config.appThemeName == t.name, width: width, height: height) {
                    withAnimation(.snappy) { config.setAppTheme(t.name) }
                }
            }
            if showCustom {
                CustomThemeCard(selected: config.isCustomTheme, width: width, height: height) {
                    withAnimation(.snappy) { config.setAppTheme(AppTheme.custom) }
                }
            }
        }
        .padding(.vertical, 4)
    }
}

// MARK: - Cursor

/// A line of terminal text ending in the cursor, blinking if it does.
struct CursorPicture: View {
    let style: String
    let blink: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let t = AppConfig.shared.theme(isDark: scheme == .dark)
        if blink {
            TimelineView(.periodic(from: .now, by: 0.6)) { ctx in
                line(t, on: Int(ctx.date.timeIntervalSinceReferenceDate / 0.6) % 2 == 0)
            }
        } else {
            line(t, on: true)
        }
    }

    private func line(_ t: TerminalTheme, on: Bool) -> some View {
            HStack(spacing: 1) {
                Text("~ $ ls").font(.system(size: 13, design: .monospaced)).foregroundStyle(t.foreground.color)
                ZStack(alignment: .bottom) {
                    Color.clear.frame(width: 8, height: 16)
                    if on { shape(t.cursor.color) }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(t.background.color, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(Color.primary.opacity(0.15), lineWidth: 0.5))
    }

    @ViewBuilder private func shape(_ c: Color) -> some View {
        switch style {
        case "bar": Rectangle().fill(c).frame(width: 2, height: 16).frame(width: 8, alignment: .leading)
        case "bar_thin": Rectangle().fill(c).frame(width: 1, height: 16).frame(width: 8, alignment: .leading)
        case "underline": Rectangle().fill(c).frame(width: 8, height: 2)
        case "block_hollow": Rectangle().strokeBorder(c, lineWidth: 1).frame(width: 8, height: 16)
        default: Rectangle().fill(c).frame(width: 8, height: 16)
        }
    }
}

// MARK: - Close-ups

/// A close-up of one part of the window, readable, in the theme's colours.
private struct CloseUp<Content: View>: View {
    var background: Color
    @ViewBuilder let content: () -> Content

    var body: some View {
        content()
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(background)
            .clipShape(RoundedRectangle(cornerRadius: 7, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 7, style: .continuous).strokeBorder(Color.primary.opacity(0.15), lineWidth: 0.5))
    }
}

/// A sidebar row: status, name, what's in it.
private struct SidebarRow: View {
    let symbol: String
    let color: Color
    let name: String
    var detail: String = ""
    var indent: CGFloat = 10

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: symbol).font(.system(size: 9, weight: .semibold)).foregroundStyle(color).frame(width: 11)
            Text(name).font(.system(size: 10))
            Spacer(minLength: 2)
            Text(detail).font(.system(size: 9)).foregroundStyle(.secondary)
        }
        .padding(.leading, indent)
        .frame(height: 15)
    }
}

private func sidebarHeading(_ s: String) -> some View {
    Text(s).font(.system(size: 8, weight: .semibold)).foregroundStyle(.secondary).padding(.top, 3)
}

private func groupHeader(_ name: String, open: Bool) -> some View {
    HStack(spacing: 4) {
        Image(systemName: open ? "chevron.down" : "chevron.right").font(.system(size: 7, weight: .bold)).foregroundStyle(.secondary).frame(width: 8)
        Text(name).font(.system(size: 10, weight: .semibold))
    }
    .frame(height: 15)
}

/// The bottom of the sidebar: Needs You, then (if on) Finished.
struct FinishedPicture: View {
    let on: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let look = MiniLook.current(dark: scheme == .dark)
        CloseUp(background: look.sidebar) {
            VStack(alignment: .leading, spacing: 1) {
                sidebarHeading("NEEDS YOU")
                SidebarRow(symbol: "exclamationmark.circle.fill", color: .orange, name: "api", detail: "permission", indent: 0)
                VStack(alignment: .leading, spacing: 1) {
                    sidebarHeading("FINISHED")
                    SidebarRow(symbol: "checkmark.circle.fill", color: .green, name: "docs", detail: "2m", indent: 0)
                    SidebarRow(symbol: "checkmark.circle.fill", color: .green, name: "tests", detail: "9m", indent: 0)
                }
                .modifier(Ghosted(off: !on))
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
        }
    }
}

/// Two Groups and their tabs, the headings prominent (bigger than the tabs)
/// or subtle (smaller, dimmer), with or without the Groups' icons.
struct GroupHeadingsPicture: View {
    let subtle: Bool
    var icons = true
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let look = MiniLook.current(dark: scheme == .dark)
        CloseUp(background: look.sidebar) {
            VStack(alignment: .leading, spacing: 1) {
                heading("Hammock", icon: "hammer.fill", color: .orange)
                SidebarRow(symbol: "terminal", color: .secondary, name: "shell", indent: 0)
                SidebarRow(symbol: "terminal", color: .secondary, name: "server", indent: 0)
                heading("Website", icon: "globe", color: .blue)
                SidebarRow(symbol: "terminal", color: .secondary, name: "deploy", indent: 0)
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
        }
    }

    private func heading(_ name: String, icon: String, color: Color) -> some View {
        HStack(spacing: 4) {
            if icons {
                let side: CGFloat = subtle ? 10 : 13
                Image(systemName: icon).font(.system(size: subtle ? 6 : 8, weight: .semibold)).foregroundStyle(.white)
                    .frame(width: side, height: side).background(color, in: RoundedRectangle(cornerRadius: side * 0.27, style: .continuous))
                    .opacity(subtle ? 0.75 : 1)
            }
            Text(name).font(.system(size: subtle ? 8.5 : 11, weight: .semibold)).foregroundStyle(subtle ? .secondary : .primary)
            Image(systemName: "chevron.down").font(.system(size: 6, weight: .bold)).foregroundStyle(.secondary)
        }
        .frame(height: subtle ? 13 : 16)
        .padding(.top, 2)
    }
}

/// Sidebar tabs showing their programs in a style.
struct ProgramIconsPicture: View {
    let style: ProgramIconStyle
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let look = MiniLook.current(dark: scheme == .dark)
        CloseUp(background: look.sidebar) {
            VStack(alignment: .leading, spacing: 1) {
                groupHeader("hammock", open: true)
                ForEach([("editor", "nvim"), ("review", "claude"), ("server", "vite"), ("shell", "zsh")], id: \.0) { name, program in
                    HStack(spacing: 5) {
                        ProgramIcon(program: [program], size: 12, style: style)
                        Text(name).font(.system(size: 10))
                        Spacer(minLength: 2)
                    }
                    .padding(.leading, 10)
                    .frame(height: 15)
                }
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
        }
    }
}

/// Sidebar tabs, the pointer over one: (if on) a close button at its end
/// instead of its detail.
struct TabClosePicture: View {
    let on: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let look = MiniLook.current(dark: scheme == .dark)
        CloseUp(background: look.sidebar) {
            VStack(alignment: .leading, spacing: 1) {
                groupHeader("Hammock", open: true)
                SidebarRow(symbol: "terminal", color: .secondary, name: "shell")
                ZStack(alignment: .trailing) {
                    SidebarRow(symbol: "terminal", color: .secondary, name: "server", detail: on ? "" : "2")
                    if on {
                        Image(systemName: "xmark")
                            .font(.system(size: 7, weight: .bold))
                            .foregroundStyle(.secondary)
                            .frame(width: 12, height: 12)
                            .background(Circle().fill(Color.primary.opacity(0.1)))
                    }
                }
                .padding(.trailing, 2)
                .background(RoundedRectangle(cornerRadius: 4).fill(Color.primary.opacity(look.dark ? 0.08 : 0.06)).padding(.leading, 6))
                .overlay(alignment: .trailing) {
                    Image(systemName: "cursorarrow")
                        .font(.system(size: 11))
                        .foregroundStyle(.primary)
                        .shadow(color: .white.opacity(0.6), radius: 0.5)
                        .offset(x: on ? 2 : -14, y: 7)
                }
                SidebarRow(symbol: "terminal", color: .secondary, name: "tests")
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
        }
    }
}

/// A collapsed Group, still showing (if on) the tab whose agent is at work.
struct CollapsedAgentsPicture: View {
    let on: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let look = MiniLook.current(dark: scheme == .dark)
        CloseUp(background: look.sidebar) {
            VStack(alignment: .leading, spacing: 1) {
                groupHeader("Hammock", open: true)
                SidebarRow(symbol: "terminal", color: .secondary, name: "shell")
                groupHeader("Side project", open: false)
                SidebarRow(symbol: "circle.dotted", color: look.accent, name: "refactor", detail: "working")
                    .modifier(Ghosted(off: !on))
                groupHeader("Website", open: false)
            }
            .padding(.horizontal, 8).padding(.vertical, 5)
        }
    }
}

/// Two panes of a tab, you typing in the left: the other dimmed, or a
/// border around yours.
struct FocusPicture: View {
    let dim: Bool
    let border: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let look = MiniLook.current(dark: scheme == .dark)
        let t = look.terminal
        HStack(spacing: 4) {
            pane(t, lines: [("$ ", "make"), ("", "built in 2s")], cursor: true)
                .overlay { RoundedRectangle(cornerRadius: 5, style: .continuous).stroke(look.border, lineWidth: border ? 1.5 : 0) }
            pane(t, lines: [("$ ", "git log"), ("", "a1f3 fix")], cursor: false)
                .overlay { RoundedRectangle(cornerRadius: 5, style: .continuous).stroke(PaneLook.outlineColor(dark: look.dark), lineWidth: 1) }
                .overlay { RoundedRectangle(cornerRadius: 5, style: .continuous).fill(Color.black.opacity(dim ? (look.dark ? 0.45 : 0.3) : 0)) }
        }
        .padding(4)
        .background(look.toolbar, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 7, style: .continuous).strokeBorder(Color.primary.opacity(0.15), lineWidth: 0.5))
    }

    private func pane(_ t: TerminalTheme, lines: [(String, String)], cursor: Bool) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            ForEach(lines.indices, id: \.self) { i in
                HStack(spacing: 0) {
                    Text(lines[i].0).foregroundStyle(t.ansi[2].color)
                    Text(lines[i].1).foregroundStyle(t.foreground.color)
                }
            }
            if cursor { Rectangle().fill(t.cursor.color).frame(width: 5, height: 9) }
        }
        .font(.system(size: 8.5, design: .monospaced))
        .lineLimit(1)
        .padding(5)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(t.background.color, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
    }
}

/// What a setting adds, faded and outlined while it's off, so you can see
/// what turning it on brings.
private struct Ghosted: ViewModifier {
    let off: Bool
    func body(content: Content) -> some View {
        content
            .opacity(off ? 0.3 : 1)
            .padding(.horizontal, 3)
            .overlay {
                RoundedRectangle(cornerRadius: 4)
                    .strokeBorder(Color.primary.opacity(off ? 0.35 : 0), style: StrokeStyle(lineWidth: 0.8, dash: [3, 2]))
            }
            .padding(.horizontal, -3)
    }
}
