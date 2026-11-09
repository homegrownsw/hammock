import CryptoKit
import HammockKit
import LocalAuthentication
import Security
import SwiftUI

/// This phone's approval key: a key in its Secure Enclave that only
/// signs with Face ID (or the passcode), which a Mac's Hammock Key helper
/// trusts once you've let this phone approve, with Touch ID on that Mac.
/// Approving a request to sign with the Hammock Key is this key's signature
/// over the request, so nothing but you, here, can approve: not the Mac's
/// daemon, not whoever holds this phone's connection.
enum ApprovalKey {
    private static let service = "com.homegrownsw.hammock.approval-key"

    /// The public key (x9.63) the Mac keeps: made the first time it's asked
    /// for. Making it needs no Face ID; signing with it does.
    static func publicKey() -> Data? {
        if let k = try? load() { return k.publicKey }
        guard let k = try? create() else { return nil }
        return k.publicKey
    }

    /// Signs an approval: Face ID (or the passcode) first, with reason.
    static func sign(_ message: Data, reason: String) async throws -> Data {
        let ctx = LAContext()
        ctx.localizedReason = reason
        // Ask now, so the prompt says why; the key then signs without asking again.
        try await ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)
        guard let k = try load(context: ctx) else { throw ApprovalError.noKey }
        return try k.sign(message)
    }

    enum ApprovalError: LocalizedError {
        case noKey
        var errorDescription: String? { "This iPhone has no approval key yet: reopen Hammock." }
    }

    /// The key, whichever kind: the Secure Enclave's, or (the simulator,
    /// which has none) one in software.
    private struct Key {
        let publicKey: Data
        let sign: (Data) throws -> Data
    }

    private static func create() throws -> Key {
        #if targetEnvironment(simulator)
        let k = P256.Signing.PrivateKey()
        try save(k.rawRepresentation)
        return Key(publicKey: k.publicKey.x963Representation) { try k.signature(for: $0).derRepresentation }
        #else
        var err: Unmanaged<CFError>?
        guard let access = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
                                                           [.privateKeyUsage, .userPresence], &err) else {
            throw err!.takeRetainedValue() as Error
        }
        let k = try SecureEnclave.P256.Signing.PrivateKey(accessControl: access)
        try save(k.dataRepresentation)
        return Key(publicKey: k.publicKey.x963Representation) { try k.signature(for: $0).derRepresentation }
        #endif
    }

    private static func load(context: LAContext? = nil) throws -> Key? {
        var out: CFTypeRef?
        let st = SecItemCopyMatching([
            kSecClass: kSecClassGenericPassword, kSecAttrService: service, kSecAttrAccount: "key", kSecReturnData: true,
        ] as CFDictionary, &out)
        guard st == errSecSuccess, let data = out as? Data else { return nil }
        #if targetEnvironment(simulator)
        let k = try P256.Signing.PrivateKey(rawRepresentation: data)
        return Key(publicKey: k.publicKey.x963Representation) { try k.signature(for: $0).derRepresentation }
        #else
        let k = try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: data, authenticationContext: context)
        return Key(publicKey: k.publicKey.x963Representation) { try k.signature(for: $0).derRepresentation }
        #endif
    }

    private static func save(_ data: Data) throws {
        let q: [CFString: Any] = [kSecClass: kSecClassGenericPassword, kSecAttrService: service, kSecAttrAccount: "key"]
        SecItemDelete(q as CFDictionary)
        var add = q
        add[kSecValueData] = data
        add[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let st = SecItemAdd(add as CFDictionary, nil)
        guard st == errSecSuccess else { throw NSError(domain: NSOSStatusErrorDomain, code: Int(st)) }
    }
}

/// A Mac asks to sign with its Hammock Key: approve once, for a while in that
/// pane (your own programs only), or say no. Shown while the request waits,
/// on a phone that may approve.
struct HammockKeyApprovalSheet: View {
    let model: AppModel
    let request: HammockKeyRequest
    let hostName: String
    /// Approve once straight away (the notification's Approve).
    var approveNow = false
    @Environment(\.dismiss) private var dismiss
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            List {
                // What's asked, as the sheet's headline.
                Section {
                    VStack(spacing: 12) {
                        Image(systemName: "key.fill")
                            .font(.title2)
                            .foregroundStyle(.tint)
                            .frame(width: 52, height: 52)
                            .background(.tint.opacity(0.15), in: Circle())
                        Text(request.what)
                            .font(.title3.weight(.semibold))
                            .multilineTextAlignment(.center)
                        if !request.command.isEmpty {
                            Text(request.command)
                                .font(.system(.footnote, design: .monospaced))
                                .foregroundStyle(.secondary)
                                .lineLimit(3)
                                .multilineTextAlignment(.center)
                                .padding(.horizontal, 10)
                                .padding(.vertical, 6)
                                .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 8))
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets(top: 4, leading: 16, bottom: 4, trailing: 16))
                }
                Section {
                    if !request.paneLabel.isEmpty {
                        LabeledContent("Where", value: request.paneLabel)
                    }
                    LabeledContent("Asked by", value: request.agent ? "An agent" : "A program you ran")
                    LabeledContent("Mac", value: hostName)
                } footer: {
                    if request.agent {
                        Text("Agents are asked every time.")
                    }
                }
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.red)
                }
            }
            .contentMargins(.top, 0, for: .scrollContent)
            // The answers stay in reach at the sheet's half height, over the
            // list as the system's bottom bar.
            .safeAreaBar(edge: .bottom) {
                VStack(spacing: 8) {
                    Button { approve(window: false) } label: {
                        Label("Approve", systemImage: "faceid").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.glassProminent)
                    if request.windowMs > 0, !request.agent {
                        Button { approve(window: true) } label: {
                            Text("Approve for \(request.windowMs / 60000) Minutes").frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.glass)
                    }
                    Button("Don't Allow", role: .destructive) {
                        model.denyHammockKey(request)
                        dismiss()
                    }
                    .buttonStyle(.borderless)
                    .padding(.top, 4)
                }
                .controlSize(.large)
                .disabled(busy)
                .padding(.horizontal, 20)
                .padding(.bottom, 8)
            }
            .navigationTitle("Hammock Key")
            .navigationBarTitleDisplayMode(.inline)
        }
        .presentationDetents([.medium, .large])
        .task { if approveNow { approve(window: false) } }
    }

    private func approve(window: Bool) {
        busy = true
        error = nil
        Task {
            do {
                let expires = HammockKeyApproval.expiry()
                let msg = HammockKeyApproval.message(request, window: window, expires: expires)
                let sig = try await ApprovalKey.sign(msg, reason: "Approve: \(request.what)")
                try await model.approveHammockKey(request, window: window, signature: sig, expires: expires)
                dismiss()
            } catch {
                self.error = error.localizedDescription
            }
            busy = false
        }
    }
}
