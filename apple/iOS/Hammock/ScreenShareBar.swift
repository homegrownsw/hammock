import CoreVideo
import HammockKit
import SwiftUI
import UIKit

// Screen sharing minimised: the Mac's screen stays connected as a bar on
// every screen of the app (above the tab bar on the lists, under the
// navigation bar in a pane, where the tab bar's hidden), with a live
// thumbnail at a frame or two a second. Tap it to have the screen back as it
// was, at once: the same streams, display, mode and zoom.

/// The screen being shared, open or minimised: one for the app, shown at its
/// root (CockpitView) so it outlives the screens it's opened from.
@MainActor
@Observable
final class ScreenShare {
    static let shared = ScreenShare()

    private(set) var state = ScreenShareState()
    /// The Mac's connection it was opened on: a different one (another
    /// computer shown) ends it.
    private(set) var model: AppModel?
    /// The latest whole picture of the display while minimised, for the bar.
    var thumbnail: CVPixelBuffer?
    /// Whether the stream's playing (else connecting, or stopped).
    var live = false
    /// Dev aid (HMK_DEBUG_SCREEN_BAR): a made-up minimised screen, its
    /// thumbnail drawn here, with no stream.
    private(set) var sample: UIImage?

    var isMinimised: Bool { state.isMinimised }
    /// Screens that hide the tab bar (a pane, on an iPhone), by the tab
    /// they're in: on the tab shown, the bar goes at their top, and not
    /// above the tab bar (whose accessory iOS would show on its own there).
    /// Counted for as long as each screen exists, not while it appears: a
    /// screen covered for a moment (Exposé) and shown again didn't always
    /// say it had appeared again, which left both bars up.
    private(set) var tabBarHiders: [String: Int] = [:]
    /// The tab the app's showing (CockpitView).
    var shownTab = ""
    var inAccessory: Bool { isMinimised && tabBarHiders[shownTab, default: 0] == 0 }

    func hidesTabBar(in tab: String, _ on: Bool) {
        tabBarHiders[tab, default: 0] += on ? 1 : -1
        if tabBarHiders[tab, default: 0] <= 0 { tabBarHiders[tab] = nil }
    }

    func open(_ request: ScreenRequest, model: AppModel) {
        if self.model !== model { state.end() }
        self.model = model
        sample = nil
        Self.dropKeyboard()
        let was = state.session
        state.open(computerID: request.computerID, name: request.name)
        if state.session != was {
            thumbnail = nil
            live = false
        }
    }

    func minimise() {
        guard state.phase == .open else { return }
        withAnimation(.snappy(duration: 0.3)) { state.minimise() }
        Haptics.screenShare()
    }

    func expand() {
        guard state.isMinimised, sample == nil else { return }
        Self.dropKeyboard()
        withAnimation(.snappy(duration: 0.3)) { state.expand() }
        Haptics.screenShare()
    }

    func end() {
        guard state.isShowing else { return }
        withAnimation(.snappy(duration: 0.3)) { state.end() }
        model = nil
        thumbnail = nil
        sample = nil
        live = false
    }

    /// The screen opening over the app: whatever under it had the keyboard
    /// (an agent's message box) lets go, or what's typed would go there,
    /// behind the screen, rather than to the Mac.
    private static func dropKeyboard() {
        UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
    }

    /// The computer shown changed: its screen goes with it.
    func check(model: AppModel?) {
        if state.isShowing, sample == nil, self.model !== model { end() }
    }

    #if DEBUG
    func showSample(name: String) {
        state.open(computerID: "sample", name: name)
        state.minimise()
        live = true
        sample = Self.drawSample()
    }

    /// A made-up desktop: a wallpaper, a menu bar, a window and a dock.
    private static func drawSample() -> UIImage {
        let size = CGSize(width: 320, height: 200)
        return UIGraphicsImageRenderer(size: size).image { ctx in
            let c = ctx.cgContext
            let colors = [UIColor.systemIndigo.cgColor, UIColor.systemTeal.cgColor] as CFArray
            if let g = CGGradient(colorsSpace: CGColorSpaceCreateDeviceRGB(), colors: colors, locations: [0, 1]) {
                c.drawLinearGradient(g, start: .zero, end: CGPoint(x: size.width, y: size.height), options: [])
            }
            UIColor.white.withAlphaComponent(0.75).setFill()
            c.fill(CGRect(x: 0, y: 0, width: size.width, height: 10))
            UIColor(white: 0.12, alpha: 1).setFill()
            UIBezierPath(roundedRect: CGRect(x: 40, y: 30, width: 200, height: 130), cornerRadius: 8).fill()
            UIColor(white: 0.22, alpha: 1).setFill()
            c.fill(CGRect(x: 40, y: 30, width: 200, height: 14))
            UIColor.systemGreen.setFill()
            for (i, w) in [120, 90, 150, 70].enumerated() {
                c.fill(CGRect(x: 52, y: 56 + i * 16, width: w, height: 6))
            }
            UIColor.white.withAlphaComponent(0.35).setFill()
            UIBezierPath(roundedRect: CGRect(x: 90, y: 178, width: 140, height: 18), cornerRadius: 6).fill()
        }
    }
    #endif
}

extension Haptics {
    /// Screen sharing minimised to its bar, or opened again from it.
    static func screenShare() {
        UIImpactFeedbackGenerator(style: .soft).impactOccurred()
    }
}

/// The minimised screen: its thumbnail, the Mac's name and whether it's
/// live, and End. Tapped, the screen comes back full screen. One element for
/// VoiceOver, with Open and End.
struct ScreenShareBar: View {
    enum Placement { case accessory, top }
    let placement: Placement
    @Environment(\.phoneTheme) private var theme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let share = ScreenShare.shared
        VStack(spacing: 0) {
            if share.isMinimised {
                switch placement {
                case .accessory:
                    row(share).padding(.horizontal, 12)
                case .top:
                    row(share)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 8)
                        .glassEffect(.regular.interactive(), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
                        .padding(.horizontal, 16)
                        .padding(.top, 2)
                        .padding(.bottom, 8)
                        .transition(reduceMotion ? .opacity : .move(edge: .top).combined(with: .opacity))
                }
            }
        }
        .frame(maxWidth: .infinity)
        .clipped()
    }

    private func row(_ share: ScreenShare) -> some View {
        let name = share.state.name
        return HStack(spacing: 12) {
            ScreenThumbnail(picture: share.thumbnail, sample: share.sample)
                .frame(width: 52, height: 32)
                .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(.primary.opacity(0.15)))
            VStack(alignment: .leading, spacing: 1) {
                Text(name)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                HStack(spacing: 5) {
                    Circle().fill(share.live ? Color.green : .orange).frame(width: 6, height: 6)
                    Text(share.live ? "Live" : "Connecting").font(.caption).foregroundStyle(.secondary)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Image(systemName: "chevron.up")
                .font(.footnote.weight(.semibold))
                .foregroundStyle(theme.accent)
            Button { share.end() } label: {
                Image(systemName: "xmark").font(.caption.weight(.bold)).frame(width: 14, height: 14)
            }
            .buttonStyle(.borderedProminent)
            .buttonBorderShape(.circle)
            .controlSize(.small)
            .tint(.red)
        }
        .contentShape(Rectangle())
        .onTapGesture { share.expand() }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Sharing \(name)’s screen")
        .accessibilityValue(share.live ? "Live" : "Connecting")
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { share.expand() }
        .accessibilityAction(named: "Open") { share.expand() }
        .accessibilityAction(named: "End") { share.end() }
    }
}

/// The display's latest picture, as the detail stream decoded it.
private struct ScreenThumbnail: UIViewRepresentable {
    let picture: CVPixelBuffer?
    let sample: UIImage?

    func makeUIView(context: Context) -> PictureView {
        let v = PictureView()
        v.backgroundColor = UIColor(white: 0.08, alpha: 1)
        v.layer.contentsGravity = .resizeAspectFill
        v.isAccessibilityElement = false
        return v
    }

    func updateUIView(_ v: PictureView, context: Context) {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        if let sample {
            v.picture = nil
            v.layer.contents = sample.cgImage
        } else if v.picture !== picture {
            // Kept here while shown, so the decoder doesn't reuse it.
            v.picture = picture
            v.layer.contents = picture.flatMap { CVPixelBufferGetIOSurface($0)?.takeUnretainedValue() }
        }
        CATransaction.commit()
    }

    final class PictureView: UIView {
        var picture: CVPixelBuffer?
    }
}

/// The viewer, over everything under it while it's open, and still there
/// (hidden, taking no touches) while it's minimised, so its streams go on.
struct ScreenShareViewerHost: View {
    let model: AppModel

    var body: some View {
        let share = ScreenShare.shared
        let minimised = share.isMinimised
        ZStack {
            if share.state.isShowing, let m = share.model, m === model {
                PhoneScreenViewer(model: m, request: ScreenRequest(computerID: share.state.computerID, name: share.state.name))
                    .id(share.state.session)
                    .opacity(minimised ? 0 : 1)
                    .offset(y: minimised ? 120 : 0)
                    .allowsHitTesting(!minimised)
                    .accessibilityHidden(minimised)
                    .transition(.move(edge: .bottom))
            }
        }
        .onChange(of: ObjectIdentifier(model), initial: true) { share.check(model: model) }
    }
}

extension View {
    /// The minimised screen above the tab bar, on every tab and the screens
    /// pushed that keep it. Once, on the TabView (CockpitView); screens that
    /// hide the tab bar show it at their top instead (TabBarHider).
    func screenShareAccessory() -> some View {
        modifier(ScreenShareAccessory())
    }
}

private struct ScreenShareAccessory: ViewModifier {
    func body(content: Content) -> some View {
        if #available(iOS 26.1, *) {
            content.tabViewBottomAccessory(isEnabled: ScreenShare.shared.inAccessory) {
                ScreenShareBar(placement: .accessory)
            }
        } else {
            content.safeAreaInset(edge: .bottom, spacing: 0) {
                if ScreenShare.shared.inAccessory { ScreenShareBar(placement: .top) }
            }
        }
    }
}
