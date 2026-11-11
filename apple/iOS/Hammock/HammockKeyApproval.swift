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
/// pane (10 minutes for your own programs, 5 for an agent's), or say no. Shown while the request waits,
/// on a phone that may approve. Drawn like the Mac's own prompt
/// (helpers/key/panel.swift): what's signed, then which tab in which Group
/// is asking, and by what.
struct HammockKeyApprovalSheet: View {
    let model: AppModel
    let request: HammockKeyRequest
    let hostName: String
    /// Approve once straight away (the notification's Approve).
    var approveNow = false
    @Environment(\.dismiss) private var dismiss
    @State private var busy = false
    @State private var error: String?
    private var unlockSymbol: String { AppLock.symbol }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    header
                    source
                    if request.windowMs > 0, request.agent {
                        let name = Place(model: model, request: request).agent
                        let agent = name == "An agent" ? "the agent" : name
                        note("Approving for \(minutes) minutes also lets \(agent) in this pane sign without asking again.",
                             systemImage: "clock")
                    } else if request.windowMs > 0 {
                        note("Approving for \(minutes) minutes also lets programs in this pane sign without asking again.",
                             systemImage: "clock")
                    }
                    if let error {
                        Label(error, systemImage: "exclamationmark.triangle.fill")
                            .font(.callout)
                            .foregroundStyle(.red)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.top, 8)
                .padding(.bottom, 16)
            }
            .background(Color(.systemGroupedBackground))
            // The answers stay in reach, over the content as the system's
            // bottom bar.
            .safeAreaBar(edge: .bottom) {
                VStack(spacing: 8) {
                    Button { approve(window: false) } label: {
                        // Touch ID, Face ID or Optic ID: however this phone unlocks.
                        Label("Approve", systemImage: unlockSymbol).frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.glassProminent)
                    if request.windowMs > 0 {
                        Button { approve(window: true) } label: {
                            Text("Approve for \(minutes) Minutes").frame(maxWidth: .infinity)
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
        // All of it at once: at half height the details went under the buttons.
        .presentationDetents([.large])
        .task { if approveNow { approve(window: false) } }
    }

    private var minutes: Int64 { request.windowMs / 60000 }

    /// What's asked, on which Mac, as the sheet's headline.
    private var header: some View {
        HStack(alignment: .center, spacing: 14) {
            Image(systemName: "key.fill")
                .font(.system(size: 24, weight: .semibold))
                .foregroundStyle(.white)
                .frame(width: 56, height: 56)
                .background(RoundedRectangle(cornerRadius: 14, style: .continuous).fill(Color.orange.gradient))
            VStack(alignment: .leading, spacing: 2) {
                Text(hostName)
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Text(sentence(request.what))
                    .font(.title3.weight(.semibold))
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
    }

    /// Where it's from: the Group and tab, then the command.
    private var source: some View {
        let place = Place(model: model, request: request)
        // No pane (a program outside Hammock): who asked, not a made-up place.
        let inPane = !place.groupName.isEmpty || !place.tabName.isEmpty
        return VStack(alignment: .leading, spacing: 12) {
            Text(!inPane ? "Asked by" : request.agent ? "\(place.agent) is asking from" : "Asked from")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .textCase(.uppercase)
            HStack(spacing: 12) {
                avatar(place)
                VStack(alignment: .leading, spacing: 3) {
                    if !inPane {
                        Text(request.agent ? "\(place.agent) outside Hammock" : "A program outside Hammock")
                            .font(.headline)
                            .lineLimit(1)
                    }
                    if !place.groupName.isEmpty {
                        Text(place.groupName)
                            .font(.headline)
                            .lineLimit(1)
                    }
                    if !place.tabName.isEmpty {
                        Label(place.tabName, systemImage: "rectangle.topthird.inset.filled")
                            .font(place.groupName.isEmpty ? .headline : .body.weight(.medium))
                            .foregroundStyle(place.groupName.isEmpty ? .primary : .secondary)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
            }
            if !request.command.isEmpty {
                Text(request.command)
                    .font(.system(.callout, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(4)
                    .truncationMode(.middle)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 7)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 8, style: .continuous).fill(Color.primary.opacity(0.05)))
            }
        }
        .padding(14)
        .background(RoundedRectangle(cornerRadius: 16, style: .continuous).fill(Color(.secondarySystemGroupedBackground)))
    }

    /// The Group's icon or picture, as the Mac draws it.
    @ViewBuilder private func avatar(_ place: Place) -> some View {
        let shape = RoundedRectangle(cornerRadius: 10, style: .continuous)
        Group {
            if let group = place.group {
                GroupIconView(model: model, group: group, size: 44) {
                    Image(systemName: "square.stack.3d.up.fill")
                        .font(.system(size: 19, weight: .medium))
                        .foregroundStyle(.tint)
                }
                .font(.system(size: 26))
            } else {
                Image(systemName: place.groupName.isEmpty ? "terminal" : "square.stack.3d.up.fill")
                    .font(.system(size: 19, weight: .medium))
                    .foregroundStyle(.secondary)
            }
        }
        .frame(width: 44, height: 44)
        .background(shape.fill(Color.primary.opacity(0.07)))
        .clipShape(shape)
    }

    private func note(_ text: String, systemImage: String) -> some View {
        Label(text, systemImage: systemImage)
            .font(.callout)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.horizontal, 4)
    }

    /// The first letter up: the daemon's words start a sentence here.
    private func sentence(_ s: String) -> String { s.prefix(1).uppercased() + s.dropFirst() }

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

extension HammockKeyApprovalSheet {
    /// Which tab in which Group asked: from the Groups this phone has, by
    /// the request's pane, else from its label ("Group ▸ tab").
    struct Place {
        var group: WorkspaceGroup?
        var groupName = ""
        var tabName = ""
        var agent = "An agent"

        @MainActor init(model: AppModel, request: HammockKeyRequest) {
            if let pane = model.panes[request.paneID] {
                if !pane.agent.harness.isEmpty { agent = AgentCatalog.shortName(of: pane.agent.harness) }
                if let tab = model.tab(of: pane) {
                    tabName = model.title(of: tab)
                    if let g = model.group(of: tab) {
                        group = g
                        groupName = model.title(of: g)
                    }
                    return
                }
            }
            (groupName, tabName) = Self.split(request.paneLabel)
        }

        /// "Group ▸ tab" as its parts; a label with no Group is the tab's.
        static func split(_ label: String) -> (group: String, tab: String) {
            guard let r = label.range(of: " ▸ ") else { return ("", label) }
            return (String(label[..<r.lowerBound]), String(label[r.upperBound...]))
        }
    }
}
