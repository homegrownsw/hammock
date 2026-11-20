import HammockKit
import SwiftUI

/// First run: pair a computer (a Mac, or a server running hmk).
struct PairingView: View {
    let session: Session

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                VStack(spacing: 10) {
                    HammockMarkView()
                        .frame(width: 76, height: 40)
                    Text("Connect to your computer").font(.title2.bold())
                }
                .padding(.top, 24)
                PairingScreen(session: session, offersDemo: true)
            }
            .navigationTitle("Hammock")
            .toolbar(.hidden, for: .navigationBar)
        }
    }
}

/// The Mac shown removed this iPhone (AppModel.unpaired): it stopped trying
/// to connect. Pair it again, or forget that Mac.
struct RemovedView: View {
    let session: Session

    var body: some View {
        let name = session.host?.displayName ?? ""
        let shown = name.isEmpty ? "Your computer" : name
        NavigationStack {
            VStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("\(shown) removed this iPhone").font(.title2.bold())
                    Text("Pair it again with a new pairing code to connect.").foregroundStyle(.secondary)
                    if let h = session.host {
                        Button("Forget \(name.isEmpty ? "This Computer" : name)", role: .destructive) { session.forget(h) }
                            .padding(.top, 4)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 20)
                .padding(.top, 24)
                PairingScreen(session: session)
            }
            .navigationTitle("Hammock")
            .toolbar(.hidden, for: .navigationBar)
        }
    }
}

/// Above the demo's tab bar: what it is, and the way out to pairing.
struct DemoBar: View {
    let session: Session

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "sparkles")
            Text("Demo").font(.subheadline.weight(.semibold))
            Spacer(minLength: 8)
            Button("Pair Your Mac") { session.endDemo() }
                .font(.subheadline.weight(.semibold))
                .buttonStyle(.borderedProminent)
                .buttonBorderShape(.capsule)
                .controlSize(.small)
        }
        .padding(.leading, 16)
        .padding(.trailing, 6)
    }
}

extension View {
    /// The demo's bar, above the tab bar on every tab (only in the demo).
    @ViewBuilder
    func demoBar(_ session: Session) -> some View {
        if session.isDemo, !DemoMac.screenshotMode {
            tabViewBottomAccessory { DemoBar(session: session) }
        } else {
            self
        }
    }
}
