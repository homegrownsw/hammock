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
                PairingScreen(session: session)
            }
            .navigationTitle("Hammock")
            .toolbar(.hidden, for: .navigationBar)
        }
    }
}
