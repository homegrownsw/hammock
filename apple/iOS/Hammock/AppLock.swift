import HammockKit
import LocalAuthentication
import SwiftUI

// Settings ▸ Privacy ▸ Require Face ID (off by default): the whole app
// behind Face ID (or the passcode), from launch and each time it comes
// back from the background. While it's not in front, a cover hides it
// (the app switcher's picture too).

enum AppLock {
    static let key = "requireFaceID"

    /// What this phone unlocks with, for the setting's name.
    static var method: String {
        let ctx = LAContext()
        _ = ctx.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: nil)
        switch ctx.biometryType {
        case .faceID: return "Face ID"
        case .touchID: return "Touch ID"
        case .opticID: return "Optic ID"
        default: return "Passcode"
        }
    }

    /// Its symbol, for the button.
    static var symbol: String {
        switch method {
        case "Face ID": return "faceid"
        case "Touch ID": return "touchid"
        case "Optic ID": return "opticid"
        default: return "lock"
        }
    }

    /// Settings ▸ Privacy ▸ Lock After: seconds away before it locks (0: at once).
    static let afterKey = "lockAfter"
    static let afterChoices: [(Int, String)] = [(0, "Immediately"), (60, "1 Minute"), (300, "5 Minutes"), (900, "15 Minutes"), (3600, "1 Hour")]
    private static let awayKey = "lockAwayAt"

    /// The app's gone to the background: when, kept (iOS may quit it meanwhile).
    static func wentAway() { UserDefaults.standard.set(Date().timeIntervalSince1970, forKey: awayKey) }

    /// Away for longer than Lock After (or never seen leaving).
    static var due: Bool {
        let at = UserDefaults.standard.double(forKey: awayKey)
        guard at > 0 else { return true }
        return Date().timeIntervalSince1970 - at >= Double(UserDefaults.standard.integer(forKey: afterKey))
    }

    /// Locked as it opens: required, and away long enough.
    static var dueOnLaunch: Bool { UserDefaults.standard.bool(forKey: key) && due }

    /// Whether it can ask at all (a passcode is set).
    static var available: Bool { LAContext().canEvaluatePolicy(.deviceOwnerAuthentication, error: nil) }

    static func unlock(_ reason: String) async -> Bool {
        let ctx = LAContext()
        ctx.localizedFallbackTitle = "Use Passcode"
        guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: nil) else { return true }  // nothing to ask with
        return (try? await ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)) ?? false
    }
}

private struct AppLockCover: ViewModifier {
    @AppStorage(AppLock.key) private var required = false
    @Environment(\.scenePhase) private var phase
    @State private var locked = AppLock.dueOnLaunch
    @State private var asking = false
    @State private var opening = false  // unlocked: the lock opens, then the cover goes
    // Ask by itself once per return (launch, or back from the background):
    // the prompt makes the app inactive, so cancelling it comes back here.
    @State private var askOnReturn = true
    // Just unlocked: Face ID's checkmark keeps the app inactive for a
    // second more, and the cover needn't wait for it.
    @State private var justUnlocked = false

    func body(content: Content) -> some View {
        content
            .overlay {
                // Due as well as locked: coming back, the first frame is drawn
                // before onChange(of: phase) locks, and it mustn't show the app.
                if required && (locked || (phase != .active && !justUnlocked) || (askOnReturn && AppLock.due)) {
                    LockScreen(asking: asking, opening: opening, showButton: locked && phase == .active && !asking && !opening) { ask() }
                        .transition(.opacity.combined(with: .scale(scale: 1.04)))
                }
            }
            .animation(.smooth(duration: 0.25), value: locked)
            .onChange(of: phase, initial: true) { _, p in
                guard required else {
                    if p == .active { askOnReturn = false }  // off: nothing to ask on the next return either
                    return
                }
                switch p {
                case .background:
                    AppLock.wentAway()
                    askOnReturn = true
                    justUnlocked = false
                    opening = false  // the next time it covers, it's shut
                case .active:
                    justUnlocked = false  // in front: covered again whenever it isn't
                    if !locked { settle() }
                    if !locked, askOnReturn, AppLock.due { locked = true }
                    if locked, askOnReturn { ask() }
                    askOnReturn = false
                default: break
                }
            }
            .onChange(of: required) {
                // Turned on just now, with Face ID: that was the check, and
                // the prompt closing isn't a return. (Off: nothing's locked.)
                locked = false
                askOnReturn = false
                justUnlocked = required  // and no cover while its prompt closes
            }
            .onChange(of: locked) { _, now in if !now { settle() } }
    }

    /// Unlocked and in front: once the cover's faded, its lock is shut again
    /// for the next time it covers (the app switcher, a pull-down).
    private func settle() {
        guard opening else { return }
        Task {
            try? await Task.sleep(for: .milliseconds(450))
            if !locked { opening = false }
        }
    }

    private func ask() {
        guard !asking, !opening else { return }
        asking = true
        Task {
            let ok = await AppLock.unlock("Unlock Hammock")
            asking = false
            guard ok else { return }
            // The lock opens, then the cover goes (once Face ID's own sheet
            // has: till then the app isn't active and the cover stays, open).
            withAnimation(.spring(duration: 0.25)) { opening = true }
            try? await Task.sleep(for: .milliseconds(200))
            justUnlocked = true
            locked = false
        }
    }
}

/// The cover: the app's colour drifting behind a glass lock.
private struct LockScreen: View {
    let asking: Bool
    let opening: Bool
    let showButton: Bool
    let unlock: () -> Void

    var body: some View {
        ZStack {
            Backdrop().ignoresSafeArea()
            VStack(spacing: 0) {
                Spacer()
                Image(systemName: opening ? "lock.open.fill" : "lock.fill")
                    .font(.system(size: 46, weight: .semibold))
                    .foregroundStyle(.white)
                    .contentTransition(.symbolEffect(.replace))
                    .symbolEffect(.pulse, options: .repeating, isActive: asking)
                    .frame(width: 128, height: 128)
                    .glassEffect(.regular.tint(.white.opacity(0.06)), in: Circle())
                    .shadow(color: .black.opacity(0.25), radius: 24, y: 12)
                    .scaleEffect(opening ? 1.08 : 1)
                Text("Hammock")
                    .font(.system(size: 40, weight: .bold, design: .rounded))
                    .foregroundStyle(.white)
                    .padding(.top, 28)
                // Locked goes without saying; unlocking, a welcome (its room
                // kept, so nothing moves as it shows).
                Text("Welcome back")
                    .font(.title3.weight(.medium))
                    .foregroundStyle(.white.opacity(0.75))
                    .opacity(opening ? 1 : 0)
                    .padding(.top, 4)
                Spacer()
                Button(action: unlock) {
                    Label("Unlock with \(AppLock.method)", systemImage: AppLock.symbol)
                        .font(.headline)
                        .padding(.horizontal, 22)
                        .padding(.vertical, 14)
                }
                .buttonStyle(.glass)
                .foregroundStyle(.white)
                .opacity(showButton ? 1 : 0)
                .animation(.easeOut(duration: 0.2), value: showButton)
                .padding(.bottom, 40)
            }
            .padding(.horizontal, 24)
        }
        .preferredColorScheme(.dark)
    }
}

/// A slow mesh of the theme's colour (Settings ▸ Theme; Hammock's forest
/// greens by default), still under Reduce Motion.
private struct Backdrop: View {
    @Environment(\.accessibilityReduceMotion) private var still
    @Environment(\.phoneTheme) private var theme

    private static let hammock: [Color] = [
        Color(red: 0.04, green: 0.13, blue: 0.09), Color(red: 0.09, green: 0.33, blue: 0.23), Color(red: 0.05, green: 0.16, blue: 0.11),
        Color(red: 0.12, green: 0.43, blue: 0.3), Color(red: 0.3, green: 0.76, blue: 0.56), Color(red: 0.1, green: 0.39, blue: 0.27),
        Color(red: 0.03, green: 0.09, blue: 0.06), Color(red: 0.07, green: 0.27, blue: 0.19), Color(red: 0.04, green: 0.1, blue: 0.07),
    ]

    /// Hammock's mesh, shade for shade, in another colour: its brightest point
    /// is the colour's dark-mode shade, the rest that much darker as Hammock's are.
    private var colors: [Color] {
        guard !theme.isHammock else { return Self.hammock }
        var (r, g, b): (CGFloat, CGFloat, CGFloat) = (0, 0, 0)
        UIColor(theme.accent).resolvedColor(with: UITraitCollection(userInterfaceStyle: .dark)).getRed(&r, green: &g, blue: &b, alpha: nil)
        // Graphite's near-white would leave the white text unreadable: dimmed
        // to about as bright as the others (they're all under 0.7).
        let lightness = 0.2126 * r + 0.7152 * g + 0.0722 * b
        if lightness > 0.7 { (r, g, b) = (r * 0.5 / lightness, g * 0.5 / lightness, b * 0.5 / lightness) }
        return [0.19, 0.49, 0.23, 0.64, 1, 0.58, 0.12, 0.4, 0.14].map { k in
            Color(red: r * k, green: g * k, blue: b * k)
        }
    }

    var body: some View {
        TimelineView(.animation(minimumInterval: 1 / 30, paused: still)) { t in
            let s = still ? 0 : t.date.timeIntervalSinceReferenceDate
            let w = { (k: Double, a: Double) in Float(sin(s * k) * a) }
            MeshGradient(width: 3, height: 3, points: [
                [0, 0], [0.5 + w(0.21, 0.08), 0], [1, 0],
                [0, 0.5 + w(0.17, 0.07)], [0.5 + w(0.13, 0.12), 0.5 + w(0.19, 0.1)], [1, 0.5 + w(0.23, 0.07)],
                [0, 1], [0.5 + w(0.11, 0.08), 1], [1, 1],
            ], colors: colors)
        }
    }
}

extension View {
    /// The app behind Face ID when Settings ▸ Privacy says so.
    func appLock() -> some View { modifier(AppLockCover()) }
}

/// Settings' rows for it: the switch (turning it on asks first, so it's
/// known to work) and, when on, how long away before it locks.
struct AppLockSettings: View {
    @Environment(\.phoneTheme) private var theme
    @AppStorage(AppLock.key) private var required = false
    @AppStorage(AppLock.afterKey) private var after = 0

    var body: some View {
        Toggle("Require \(AppLock.method)", isOn: Binding(get: { required }, set: { on in
            guard on else { required = false; return }
            Task {
                if await AppLock.unlock("Require \(AppLock.method) to open Hammock") {
                    AppLock.wentAway()  // just unlocked: not due again until it's been away
                    required = true
                }
            }
        }))
        .disabled(!AppLock.available && !required)
        if required {
            Picker("Lock After", selection: $after) {
                ForEach(AppLock.afterChoices, id: \.0) { Text($0.1).tag($0.0) }
            }
            .tint(theme.accent)
            .id(theme.name) // as Settings' other menu picker
        }
    }
}
