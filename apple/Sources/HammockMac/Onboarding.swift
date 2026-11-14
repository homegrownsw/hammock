import AppKit
import HammockKit
import SwiftUI

/// First-run setup, shown in place of the workspace in the main window:
/// Welcome → Look & feel → Keep it running → Full Disk Access → Agents →
/// Finish. Every step can be skipped ("Set up later" jumps to the finish).
/// It shows while `onboarding.completed` is unset, and again from Help ▸
/// Welcome to Hammock…. Quit on the Full Disk Access step (macOS asks to
/// Quit & Reopen once it's granted), it opens on that step again.
/// `HMK_ONBOARDING=1` forces it; `HMK_ONBOARDING_STEP=agents` opens on a
/// step (for screenshots).
@MainActor
@Observable
final class Onboarding {
    static let shared = Onboarding()
    static let completedKey = "onboarding.completed"
    /// The step to open on next launch: only Full Disk Access, which macOS
    /// may relaunch Hammock from.
    static let resumeKey = "onboarding.resume"

    enum Step: Int, CaseIterable {
        case welcome, look, daemon, access, agents, finish
    }

    var isShowing: Bool
    var step: Step = .welcome

    // What got set up, for the finish screen.
    var daemonOn = false
    var diskAccess = false
    var cliPath: String?
    var connected: [String] = []
    /// Where claude was found (from the agents step), so the finish can offer it.
    var claudeFound = false

    private init() {
        let env = ProcessInfo.processInfo.environment
        // Also when it was reopened from Help and macOS relaunched Hammock
        // from the Full Disk Access step.
        let resume = UserDefaults.standard.string(forKey: Self.resumeKey) == "\(Step.access)"
        isShowing = env["HMK_ONBOARDING"] == "1" || !UserDefaults.standard.bool(forKey: Self.completedKey) || resume
        if let s = env["HMK_ONBOARDING_STEP"], let step = Step.allCases.first(where: { "\($0)" == s }) {
            self.step = step
        } else if resume {
            step = .access
            // What the steps before set up, for the finish screen.
            daemonOn = DaemonService.isEnabled
            cliPath = CommandLineTool.installedPath
        }
    }

    func show() {
        step = .welcome
        withAnimation(.smooth) { isShowing = true }
    }

    func go(_ s: Step) {
        if s == .access {
            UserDefaults.standard.set("\(Step.access)", forKey: Self.resumeKey)
        } else {
            UserDefaults.standard.removeObject(forKey: Self.resumeKey)
        }
        withAnimation(.smooth(duration: 0.35)) { step = s }
    }

    func next() {
        if let n = Step(rawValue: step.rawValue + 1) { go(n) }
    }

    func back() {
        if let p = Step(rawValue: step.rawValue - 1) { go(p) }
    }

    func finish() {
        UserDefaults.standard.removeObject(forKey: Self.resumeKey)
        UserDefaults.standard.set(true, forKey: Self.completedKey)
        withAnimation(.smooth) { isShowing = false }
    }
}

/// The colours of Hammock's icon: its forest greens (the six colours of its
/// mark are HammockMark.colors).
enum Brand {
    static let forest = Color(red: 0x0F / 255, green: 0x36 / 255, blue: 0x27 / 255)
    static let pine = Color(red: 0x07 / 255, green: 0x1A / 255, blue: 0x12 / 255)
    static let mist = Color(red: 0xCD / 255, green: 0xEF / 255, blue: 0xDD / 255)
    static let fern = Color(red: 0x2E / 255, green: 0x8A / 255, blue: 0x62 / 255)
    static let needsYou = Color(red: 0xE8 / 255, green: 0x74 / 255, blue: 0x0C / 255)
    static let done = Color(red: 0x1F / 255, green: 0x8A / 255, blue: 0x3A / 255)
}

extension RGB {
    var color: Color { Color(red: Double(r) / 255, green: Double(g) / 255, blue: Double(b) / 255) }
}

struct OnboardingView: View {
    let model: AppModel
    @State private var flow = Onboarding.shared

    var body: some View {
        VStack(spacing: 0) {
            StepDots(step: flow.step)
                .frame(height: 52)
            Group {
                switch flow.step {
                case .welcome: WelcomeStep()
                case .look: LookStep()
                case .daemon: DaemonStep()
                case .access: AccessStep()
                case .agents: AgentsStep()
                case .finish: FinishStep(model: model)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .transition(.asymmetric(insertion: .opacity.combined(with: .offset(x: 24)), removal: .opacity.combined(with: .offset(x: -24))))
            .id(flow.step)
        }
        .background {
            ZStack {
                Color(nsColor: .windowBackgroundColor)
                if flow.step == .welcome || flow.step == .finish { BrandGlow(pulse: flow.step == .finish) }
            }
            .ignoresSafeArea()
        }
        .ignoresSafeArea()
        .toolbar(removing: .title)
        .toolbarBackgroundVisibility(.hidden, for: .windowToolbar)
        .onAppear { Self.makeRoom() }
    }

    /// The steps are laid out for about 1100×720; grow a smaller window.
    static func makeRoom() {
        DispatchQueue.main.async {
            guard let window = WorkspaceWindow.window ?? NSApp.windows.first(where: { $0.isVisible && $0.canBecomeMain }),
                  let screen = window.screen?.visibleFrame else { return }
            var f = window.frame
            let w = min(max(f.width, 1100), screen.width), h = min(max(f.height, 740), screen.height)
            guard w != f.width || h != f.height else { return }
            f.origin.x = max(screen.minX, min(f.midX - w / 2, screen.maxX - w))
            f.origin.y = max(screen.minY, min(f.maxY - h, screen.maxY - h))
            f.size = NSSize(width: w, height: h)
            window.setFrame(f, display: true, animate: true)
        }
    }
}

// MARK: - Chrome

private struct StepDots: View {
    let step: Onboarding.Step

    var body: some View {
        HStack(spacing: 6) {
            ForEach(Onboarding.Step.allCases, id: \.self) { s in
                Capsule()
                    .fill(s == step ? Brand.fern : s.rawValue < step.rawValue ? Brand.fern.opacity(0.45) : Color.primary.opacity(0.18))
                    .frame(width: s == step ? 20 : 6, height: 6)
            }
        }
        .animation(.smooth, value: step)
        .accessibilityElement()
        .accessibilityLabel("Step \(step.rawValue + 1) of \(Onboarding.Step.allCases.count)")
    }
}

private struct BrandGlow: View {
    /// Breathe slowly (the finish screen).
    var pulse = false
    @Environment(\.colorScheme) private var scheme
    @State private var bright = false

    var body: some View {
        GeometryReader { geo in
            RadialGradient(
                colors: [Brand.mist.opacity(scheme == .dark ? 0.16 : 0.55), Color(red: 0.24, green: 0.85, blue: 0.6).opacity(scheme == .dark ? 0.06 : 0.18), .clear],
                center: UnitPoint(x: 0.5, y: 0.05), startRadius: 0, endRadius: max(geo.size.width, geo.size.height) * 0.55)
                .opacity(pulse && !bright ? 0.75 : 1)
                .scaleEffect(pulse && bright ? 1.06 : 1, anchor: .top)
        }
        .allowsHitTesting(false)
        .onAppear {
            guard pulse else { return }
            withAnimation(.easeInOut(duration: 2).repeatForever()) { bright = true }
        }
    }
}

/// The bar along the bottom of each step.
struct StepFooter<Primary: View>: View {
    var showBack = true
    var showLater = true
    @ViewBuilder var primary: Primary

    var body: some View {
        HStack(spacing: 10) {
            if showLater {
                Button("Set up later") { Onboarding.shared.go(.finish) }
                    .buttonStyle(.plain)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if showBack {
                Button("Back") { Onboarding.shared.back() }
                    .controlSize(.large)
            }
            primary
        }
        .padding(.horizontal, 28)
        .frame(height: 76)
    }
}

/// A rounded white card.
struct OnboardingCard<Content: View>: View {
    var padding: CGFloat = 20
    var highlighted = false
    @ViewBuilder var content: Content

    var body: some View {
        // Fills its row's height, so cards side by side match (as in a grid).
        content
            .padding(padding)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(.background.opacity(0.85), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .strokeBorder(highlighted ? Color.themeAccent : Color.primary.opacity(0.08), lineWidth: highlighted ? 2 : 1)
            }
            .shadow(color: Brand.pine.opacity(0.06), radius: 12, y: 6)
    }
}

struct StepHeading: View {
    let title: String
    let subtitle: String
    var centered = false

    var body: some View {
        VStack(alignment: centered ? .center : .leading, spacing: 6) {
            Text(title)
                .font(.system(size: 30, weight: .bold))
                .kerning(-0.4)
            Text(subtitle)
                .font(.system(size: 14))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(centered ? .center : .leading)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

struct DoneCheck: View {
    var size: CGFloat = 14
    var body: some View {
        Image(systemName: "checkmark.circle.fill")
            .font(.system(size: size))
            .foregroundStyle(.white, Brand.done)
    }
}

// MARK: - 1 · Welcome

private struct WelcomeStep: View {
    @State private var shown = false

    var body: some View {
        VStack(spacing: 0) {
            VStack(spacing: 0) {
                Image(nsImage: NSApp.applicationIconImage)
                    .resizable()
                    .frame(width: 132, height: 132)
                    .shadow(color: Brand.forest.opacity(0.3), radius: 18, y: 14)
                    .padding(.bottom, 2)
                    .scaleEffect(shown ? 1 : 0.9)
                    .opacity(shown ? 1 : 0)
                Text("Welcome to Hammock")
                    .font(.system(size: 40, weight: .bold))
                    .kerning(-0.6)
                Text("Your terminals and agents in one place that keeps running, and taps you on the shoulder when something needs you.")
                    .font(.system(size: 17))
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 560)
                    .padding(.top, 10)
            }
            .padding(.top, 8)

            HStack(alignment: .top, spacing: 20) {
                Pillar(title: "A Group for each piece of work",
                       text: "Groups hold tabs, tabs hold panes: shells, agents, browsers, simulators. Switch tasks without losing your place.") { GroupsArt() }
                Pillar(title: "Nothing stops when you quit",
                       text: "A small background service holds every pane. Quit the app, update it, even reboot, and your sessions are right where you left them.") { RestoreArt() }
                Pillar(title: "Agents tell you when they need you",
                       text: "Claude Code, Codex and other agents show up in Needs You when they're waiting, finished or stuck. Answer them right there.") { NeedsYouArt() }
            }
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: 980)
            .padding(.top, 40)
            .padding(.horizontal, 48)
            .offset(y: shown ? 0 : 16)
            .opacity(shown ? 1 : 0)
            .padding(.bottom, 24)
            Spacer(minLength: 0)
            StepFooter(showBack: false) {
                Button("Get Started") { Onboarding.shared.next() }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .onAppear { withAnimation(.smooth(duration: 0.6).delay(0.05)) { shown = true } }
    }
}

private struct Pillar<Art: View>: View {
    let title: String
    let text: String
    @ViewBuilder var art: Art

    var body: some View {
        OnboardingCard {
            VStack(alignment: .leading, spacing: 14) {
                art
                    .frame(maxWidth: .infinity)
                    .frame(height: 116)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 4) {
                    Text(title).font(.system(size: 15, weight: .semibold))
                    Text(text)
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxHeight: .infinity, alignment: .top)
        }
    }
}

private struct ArtBackground: View {
    var body: some View { Brand.mist.opacity(0.22) }
}

private struct GroupsArt: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Label { Text("Hammock").fontWeight(.semibold) } icon: { Image(systemName: "chevron.down").font(.system(size: 8, weight: .bold)) }
            HStack(spacing: 8) {
                StatusIcon(state: .working, size: 12)
                Text("daemon")
                Spacer()
                Text("claude · 4m").foregroundStyle(.secondary)
            }
            .padding(.horizontal, 8).padding(.vertical, 4)
            .background(.background, in: RoundedRectangle(cornerRadius: 6))
            .padding(.leading, 12)
            HStack(spacing: 8) {
                StatusIcon(state: .idle, size: 12)
                Text("mac app")
                Spacer()
                Text("3 panes").foregroundStyle(.secondary)
            }
            .padding(.horizontal, 8)
            .padding(.leading, 12)
            Label { Text("Side project").fontWeight(.semibold) } icon: { Image(systemName: "chevron.right").font(.system(size: 8, weight: .bold)) }
        }
        .font(.system(size: 12))
        .padding(.horizontal, 14)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
        .background(ArtBackground())
    }
}

private struct RestoreArt: View {
    var body: some View {
        ZStack(alignment: .bottomTrailing) {
            VStack(alignment: .leading, spacing: 5) {
                (Text("~/dev/hammock ").foregroundColor(Color(red: 0.61, green: 0.81, blue: 0.54)) + Text("just test"))
                Text("ok  internal/daemon  4.2s").foregroundStyle(Color(white: 0.55))
                Text("ok  internal/holder  1.9s").foregroundStyle(Color(white: 0.55))
                Text("▍").foregroundStyle(Color(white: 0.55))
            }
            .font(.system(size: 11, design: .monospaced))
            .foregroundStyle(Color(white: 0.85))
            .padding(.horizontal, 14)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .leading)
            Label("Back after restart", systemImage: "arrow.clockwise")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(Brand.pine)
                .padding(.horizontal, 9).padding(.vertical, 4)
                .background(Brand.mist, in: Capsule())
                .padding(10)
        }
        .background(Color(red: 0.12, green: 0.114, blue: 0.106))
    }
}

private struct NeedsYouArt: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                StatusIcon(state: .needsInput, size: 14)
                Text("claude").fontWeight(.semibold)
                Text("· daemon").foregroundStyle(.secondary)
            }
            (Text("Run ") + Text("git push").font(.system(size: 12, design: .monospaced)) + Text("?"))
            HStack(spacing: 6) {
                Spacer()
                Text("Deny").padding(.horizontal, 10).padding(.vertical, 3).background(Color.primary.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
                Text("Reply…").padding(.horizontal, 10).padding(.vertical, 3).background(Color.primary.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
                Text("Allow").fontWeight(.semibold).foregroundStyle(.white).padding(.horizontal, 10).padding(.vertical, 3).background(Color.themeAccent, in: RoundedRectangle(cornerRadius: 6))
            }
        }
        .font(.system(size: 12))
        .padding(.horizontal, 12).padding(.vertical, 10)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Brand.needsYou.opacity(0.4)))
        .padding(12)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(ArtBackground())
    }
}

// MARK: - 2 · Look & feel

private struct LookStep: View {
    private var config: AppConfig { .shared }
    @Environment(\.colorScheme) private var scheme
    private var layout: SidebarLayout { .current }

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .top, spacing: 40) {
                VStack(alignment: .leading, spacing: 22) {
                    StepHeading(title: "Make it yours", subtitle: "You can change any of this later in Settings.")
                    section("Appearance") {
                        HStack(spacing: 12) {
                            ForEach(AppConfig.Appearance.allCases) { a in
                                AppearanceTile(appearance: a, selected: config.appearance == a) { config.setAppearance(a) }
                            }
                        }
                    }
                    section("Tabs") {
                        HStack(spacing: 12) {
                            LayoutCard(top: false, title: "Tabs in the sidebar", text: "Tabs nest under their Group. Best when you juggle many Groups.",
                                       selected: layout != .horizontal) { SidebarLayout.set(.vertical) }
                            LayoutCard(top: true, title: "Tabs along the top", text: "Groups in the sidebar, the Group's tabs in a tab bar.",
                                       selected: layout == .horizontal) { SidebarLayout.set(.horizontal) }
                        }
                        .fixedSize(horizontal: false, vertical: true)
                    }
                    section("Theme") {
                        ThemeGrid(width: 64, height: 40, only: ["Hammock", "Native", "Ocean", "Catppuccin", "Tokyo Night"], showCustom: false)
                        HStack(spacing: 6) {
                            Image(systemName: "paintpalette").foregroundStyle(Brand.fern)
                            Text("More themes, and each colour your own, in Settings.")
                            SettingsLink { Text("Settings…") }
                                .buttonStyle(.link)
                                .simultaneousGesture(TapGesture().onEnded { SettingsTab.select(.appearance) })
                        }
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                        HStack(spacing: 6) {
                            Image(systemName: "textformat").foregroundStyle(Brand.fern)
                            Text(fontLine)
                            SettingsLink { Text("Change…") }
                                .buttonStyle(.link)
                        }
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                        .padding(.top, 4)
                    }
                }
                .frame(width: 430)
                .frame(maxHeight: .infinity, alignment: .top)

                VStack(spacing: 12) {
                    MiniWorkspace(top: layout == .horizontal)
                    Text(config.appearance == .auto ? "Automatic follows macOS. Showing \(scheme == .dark ? "Dark" : "Light") right now." : "Live preview")
                        .font(.system(size: 12))
                        .foregroundStyle(.secondary)
                }
                .padding(.bottom, 20)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .padding(.leading, 56)
            .padding(.trailing, 40)
            .padding(.top, 8)
            .frame(maxHeight: .infinity)

            StepFooter {
                Button("Continue") { Onboarding.shared.next() }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .keyboardShortcut(.defaultAction)
            }
        }
    }

    private var fontLine: AttributedString {
        let name = config.fontFamily ?? "SF Mono"
        let size = config.fontSize.map { " \(Int($0))" } ?? ""
        var s = AttributedString(config.fontFromGhostty ? "Using your Ghostty font, " : "Terminal font: ")
        var font = AttributedString(name + size)
        font.foregroundColor = .primary
        font.font = .system(size: 12, weight: .semibold)
        s += font
        return s
    }

    private func section<C: View>(_ title: String, @ViewBuilder _ content: () -> C) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title.uppercased())
                .font(.system(size: 11, weight: .semibold))
                .kerning(0.4)
                .foregroundStyle(.secondary)
            content()
        }
    }
}

private struct SelectRing: ViewModifier {
    let selected: Bool
    let radius: CGFloat
    func body(content: Content) -> some View {
        content.overlay {
            RoundedRectangle(cornerRadius: radius, style: .continuous)
                .strokeBorder(selected ? Color.themeAccent : Color.primary.opacity(0.12), lineWidth: selected ? 3 : 1)
        }
    }
}

private struct AppearanceTile: View {
    let appearance: AppConfig.Appearance
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 6) {
                ZStack {
                    switch appearance {
                    case .light: thumb(dark: false)
                    case .dark: thumb(dark: true)
                    case .auto:
                        thumb(dark: false)
                        thumb(dark: true).mask(DiagonalHalf())
                    }
                }
                .frame(width: 124, height: 72)
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                .modifier(SelectRing(selected: selected, radius: 10))
                Text(appearance == .auto ? "Automatic" : appearance == .light ? "Light" : "Dark")
                    .font(.system(size: 13))
            }
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private func thumb(dark: Bool) -> some View {
        HStack(spacing: 0) {
            Rectangle().fill(dark ? Color(white: 0.23) : Color(red: 0.925, green: 0.906, blue: 0.875)).frame(width: 26)
            VStack(alignment: .leading, spacing: 4) {
                ForEach([0.7, 0.45, 0.6], id: \.self) { w in
                    Capsule().fill(dark ? Color(white: 0.36) : Color(white: 0.78)).frame(width: 80 * w, height: 4)
                }
            }
            .padding(8)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(dark ? Color(white: 0.16) : .white)
        }
    }
}

private struct DiagonalHalf: Shape {
    func path(in r: CGRect) -> Path {
        Path { p in
            p.move(to: CGPoint(x: r.maxX, y: r.minY))
            p.addLine(to: CGPoint(x: r.maxX, y: r.maxY))
            p.addLine(to: CGPoint(x: r.minX, y: r.maxY))
            p.closeSubpath()
        }
    }
}

private struct LayoutCard: View {
    let top: Bool
    let title: String
    let text: String
    let selected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 0) {
                    VStack(alignment: .leading, spacing: 3) {
                        Capsule().fill(Brand.fern.opacity(0.6)).frame(width: 30, height: 4)
                        if !top {
                            Capsule().fill(Color.themeAccent).frame(width: 26, height: 4).padding(.leading, 6)
                            Capsule().fill(Color.primary.opacity(0.2)).frame(width: 22, height: 4).padding(.leading, 6)
                        }
                        Capsule().fill(Color.primary.opacity(0.2)).frame(width: 30, height: 4)
                    }
                    .padding(6)
                    .frame(width: 52, alignment: .topLeading)
                    .frame(maxHeight: .infinity, alignment: .top)
                    .background(Brand.mist.opacity(0.35))
                    VStack(spacing: 0) {
                        if top {
                            HStack(spacing: 3) {
                                UnevenRoundedRectangle(topLeadingRadius: 3, topTrailingRadius: 3).fill(.background).frame(width: 28)
                                UnevenRoundedRectangle(topLeadingRadius: 3, topTrailingRadius: 3).fill(Color.primary.opacity(0.1)).frame(width: 28)
                                Spacer()
                            }
                            .padding(.horizontal, 4).padding(.top, 3)
                            .frame(height: 12)
                            .background(Brand.mist.opacity(0.35))
                        }
                        Rectangle().fill(.background)
                    }
                }
                .frame(height: 58)
                .clipShape(RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(Color.primary.opacity(0.08)))
                Text(title).font(.system(size: 13, weight: .semibold))
                Text(text)
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(10)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(.background, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .modifier(SelectRing(selected: selected, radius: 12))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}


/// A small drawing of the workspace in the chosen appearance, layout and
/// terminal theme.
private struct MiniWorkspace: View {
    let top: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let dark = scheme == .dark
        let t = AppConfig.shared.theme(isDark: dark)
        let look = MiniLook.current(dark: dark)
        HStack(spacing: 6) {
            sidebar(look)
            VStack(alignment: .leading, spacing: 0) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text("daemon").font(.system(size: 12, weight: .semibold))
                    Text("hammock · ~/dev/hammock").font(.system(size: 10)).foregroundStyle(.secondary)
                }
                .padding(.horizontal, 10).padding(.top, 8).padding(.bottom, 4)
                if top { tabBar.padding(.top, 2) }
                terminal(t)
                    .padding(.top, top ? 0 : 6)
            }
        }
        .padding(6)
        .frame(width: 540, height: 400)
        .background(look.toolbar, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(Color.primary.opacity(0.1)))
        .shadow(color: Brand.pine.opacity(0.22), radius: 30, y: 24)
        .animation(.smooth, value: top)
        .accessibilityHidden(true)
    }

    /// The sidebar as SidebarView draws it, a little smaller: Groups (icon,
    /// name, then the chevron), their tabs indented under them, Needs You at
    /// the bottom; in the theme's tint over the glass.
    private func sidebar(_ look: MiniLook) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 5) {
                Circle().fill(Color(red: 1, green: 0.37, blue: 0.34)).frame(width: 8)
                Circle().fill(Color(red: 1, green: 0.74, blue: 0.18)).frame(width: 8)
                Circle().fill(Color(red: 0.16, green: 0.78, blue: 0.25)).frame(width: 8)
            }
            .frame(height: 8)
            .padding(.leading, 6)
            .padding(.bottom, 14)
            if top {
                groupRow("🌿", "hammock", open: true, selected: true, chevron: false)
                groupRow("🧪", "side project", open: false, status: .idle, chevron: false)
            } else {
                groupRow("🌿", "hammock", open: true)
                tabRow(.working, "daemon", detail: "4m", selected: true)
                tabRow(.idle, "mac app", detail: "3")
                tabRow(.needsInput, "browser", detail: "1m")
                groupRow("🧪", "side project", open: false, status: .idle)
            }
            Spacer()
            HStack(spacing: 6) {
                StatusIcon(state: .needsInput, size: 12)
                Text("claude needs you").font(.system(size: 10, weight: .semibold))
            }
            .padding(.horizontal, 8).padding(.vertical, 7)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Brand.needsYou.opacity(0.14), in: RoundedRectangle(cornerRadius: 8))
        }
        .padding(.horizontal, 6).padding(.vertical, 8)
        .frame(width: 160)
        .frame(maxHeight: .infinity)
        .background {
            ZStack {
                RoundedRectangle(cornerRadius: 9, style: .continuous).fill(.regularMaterial)
                RoundedRectangle(cornerRadius: 9, style: .continuous).fill(look.sidebar.opacity(look.sidebarAlpha))
            }
        }
        .overlay(RoundedRectangle(cornerRadius: 9, style: .continuous).strokeBorder(Color.primary.opacity(0.08)))
    }

    private func groupRow(_ icon: String, _ name: String, open: Bool, selected: Bool = false, status: AgentState? = nil, chevron: Bool = true) -> some View {
        HStack(spacing: 5) {
            Text(icon).font(.system(size: 12)).frame(width: 17)
            Text(name).font(.system(size: 12, weight: .semibold)).lineLimit(1)
            if chevron {
                Image(systemName: "chevron.right")
                    .font(.system(size: 8, weight: .semibold))
                    .foregroundStyle(.secondary.opacity(0.7))
                    .rotationEffect(.degrees(open ? 90 : 0))
            }
            Spacer(minLength: 2)
            if let status { StatusIcon(state: status, size: 12) }
        }
        .padding(.horizontal, 6)
        .frame(height: 24)
        .background(selected ? Color.primary.opacity(0.09) : .clear, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
    }

    private func tabRow(_ state: AgentState, _ name: String, detail: String, selected: Bool = false) -> some View {
        HStack(spacing: 6) {
            StatusIcon(state: state, size: 13)
            Text(name).font(.system(size: 11.5)).lineLimit(1)
            Spacer(minLength: 2)
            Text(detail).font(.system(size: 10)).foregroundStyle(.secondary)
        }
        .padding(.leading, 11)
        .padding(.trailing, 6)
        .frame(height: 22)
        .background(selected ? Color.primary.opacity(0.09) : .clear, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
    }

    /// TabStrip, a little smaller: equal tabs, the selected one filled,
    /// hairlines between the rest, + at the end.
    private var tabBar: some View {
        let tabs: [(AgentState, String)] = [(.working, "daemon"), (.idle, "mac app"), (.needsInput, "browser")]
        return HStack(spacing: 0) {
            ForEach(tabs.indices, id: \.self) { i in
                let on = i == 0
                HStack(spacing: 5) {
                    StatusIcon(state: tabs[i].0, size: 11)
                    Text(tabs[i].1).font(.system(size: 10, weight: on ? .medium : .regular)).lineLimit(1)
                }
                .foregroundStyle(on ? .primary : .secondary)
                .frame(width: 104, height: 24)
                .background(on ? Color(nsColor: .windowBackgroundColor) : .clear)
                .overlay(alignment: .trailing) {
                    if i < tabs.count - 1 { Rectangle().fill(Color(nsColor: .separatorColor)).frame(width: 1).padding(.vertical, on ? 0 : 5) }
                }
            }
            Image(systemName: "plus").font(.system(size: 10, weight: .medium)).foregroundStyle(.secondary).frame(width: 24, height: 24)
            Spacer(minLength: 0)
        }
        .frame(height: 24)
        .background(scheme == .dark ? Color.black.opacity(0.28) : Color.black.opacity(0.07))
        .overlay(alignment: .bottom) { Divider() }
    }

    private func terminal(_ t: TerminalTheme) -> some View {
        let g = t.ansi[2].color, b = t.ansi[4].color, m = t.ansi[5].color
        let font = Font.custom(AppConfig.shared.fontFamily ?? "SF Mono", size: 10.5)
        return VStack(alignment: .leading, spacing: 3) {
            Text("\(Text("~/dev/hammock").foregroundColor(g)) \(Text("❯").foregroundColor(b)) claude")
            Text("● Reading internal/daemon/daemon.go").foregroundStyle(m)
            Text("  Found the reconnect path; holders re-").opacity(0.7)
            Text("  attach in order, snapshot then stream.").opacity(0.7)
            Text("● Edit internal/daemon/snapshot.go").foregroundStyle(m)
            Text("\(Text("  + ").foregroundColor(g))if c.offset < ring.start {")
            Text("\(Text("  + ").foregroundColor(g))    return c.resnapshot()")
            Text("\(Text("  + ").foregroundColor(g))}")
            Text("✻ Running tests…").foregroundStyle(b)
        }
        .font(font)
        .foregroundStyle(t.foreground.color)
        .lineLimit(1)
        .padding(.horizontal, 12).padding(.vertical, 10)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(t.background.color, in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(Color.primary.opacity(0.08)))
    }
}

/// Onboarding's workspace drawing, both layouts, for HMK_DEBUG_SETTINGS=workspace.
struct MiniWorkspaceDebug: View {
    var body: some View {
        HStack(spacing: 30) {
            MiniWorkspace(top: false)
            MiniWorkspace(top: true)
        }
        .padding(30)
    }
}
