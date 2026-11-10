import HammockKit
import SwiftUI

/// Settings ▸ Acknowledgements: the third-party software in the app, each
/// opening its licence (Acknowledgements.json, which the build bundles).
struct AcknowledgementsScreen: View {
    private let items = Acknowledgements.load()
    #if DEBUG
    // HMK_DEBUG_ACKNOWLEDGEMENTS=<name> opens that one's licence (=1: the list).
    @State private var debugItem: Acknowledgement? = ProcessInfo.processInfo.environment["HMK_DEBUG_ACKNOWLEDGEMENTS"].flatMap { name in
        Acknowledgements.load().first { $0.name.localizedCaseInsensitiveContains(name) }
    }
    #endif

    var body: some View {
        List {
            Section {
                ForEach(items) { item in
                    NavigationLink {
                        AcknowledgementText(item)
                            .navigationTitle(item.name)
                            .navigationBarTitleDisplayMode(.inline)
                    } label: {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(item.name)
                            Text([item.licenseName, item.detail].compactMap { $0 }.joined(separator: " · "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            } footer: {
                Text("Hammock is open source, under the MIT licence, and is built with this software. Each one's licence is above.")
            }
        }
        .navigationTitle("Acknowledgements")
        #if DEBUG
        .navigationDestination(item: $debugItem) { AcknowledgementText($0).navigationTitle($0.name).navigationBarTitleDisplayMode(.inline) }
        #endif
    }
}
