import AppKit
import HammockKit
import SwiftUI

/// What sharing sheet is up, from wherever it was asked for (the toolbar, a
/// Group's menu, an opened invite link).
@MainActor
@Observable
final class SharingUI {
    static let shared = SharingUI()
    var shareGroup: String?
    var joinLink: String?
    /// The pairing code is up (File ▸ Pair a Device…, `hmk pair`).
    var pairing = false
}

/// Sharing a Group from this Mac: who's in it, the invite link, and stopping.
struct ShareSheet: View {
    let model: AppModel
    let groupID: String
    @Environment(\.dismiss) private var dismiss
    @State private var link = ""
    @State private var copied = false
    @State private var error: String?
    @State private var confirmStop = false
    @State private var gate = NetworkGate()

    private var group: WorkspaceGroup? { model.groups.first { $0.id == groupID } }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Share “\(group.map { model.title(of: $0) } ?? "Group")”").font(.title3.weight(.semibold))
            if let group {
                people(group)
                Divider()
                invite(group)
                Text("People you let drive can have their agents drive too.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange).font(.callout)
            }
            HStack {
                if group?.isShared == true || group?.inviteActive == true {
                    Button("Stop Sharing", role: .destructive) { confirmStop = true }
                }
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 460)
        .networkGate(gate, model: model)
        .confirmationDialog("Stop sharing this Group?", isPresented: $confirmStop) {
            Button("Stop Sharing", role: .destructive) {
                Task { await model.stopSharing(groupID); dismiss() }
            }
        } message: {
            Text("Everyone in it is disconnected and the invite link stops working.")
        }
        .task(id: group?.inviteActive) {
            if group?.inviteActive == true, link.isEmpty { await makeLink(regenerate: false) }
        }
    }

    @ViewBuilder
    private func people(_ group: WorkspaceGroup) -> some View {
        let guests = model.members(of: group).filter { $0.0.role != .host }
        VStack(alignment: .leading, spacing: 8) {
            Text("People with access").font(.headline)
            if guests.isEmpty {
                Text("No one yet. Send the invite link below; you'll approve each person.")
                    .foregroundStyle(.secondary).font(.callout)
            }
            ForEach(guests, id: \.0.userID) { m, p in
                HStack(spacing: 10) {
                    AvatarView(person: p, size: 26, typing: m.typing)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(p.name)
                        Text(p.deviceName).font(.caption).foregroundStyle(.secondary)
                    }
                    if m.online {
                        Circle().fill(.green).frame(width: 7, height: 7).help("Here now")
                    }
                    Spacer()
                    Menu(m.role.label) {
                        Button("Can view") { Task { await model.setRole(.viewer, of: p.userID, in: groupID) } }
                        Button("Can drive") { Task { await model.setRole(.driver, of: p.userID, in: groupID) } }
                            .help(Role.driverMeans)
                        Divider()
                        Button("Remove", role: .destructive) { Task { await model.setRole(nil, of: p.userID, in: groupID) } }
                    }
                    .fixedSize()
                }
            }
        }
    }

    @ViewBuilder
    private func invite(_ group: WorkspaceGroup) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Invite link").font(.headline)
            if !model.remoteEnabled {
                Text("Guests reach this Mac through remote access, so it needs to be on.")
                    .font(.callout).foregroundStyle(.secondary)
                Button("Turn On Remote Access") {
                    Task {
                        // The first time: where to listen.
                        guard await gate.ensure(model) else { return }
                        do { try await model.setRemoteAccess(true); await makeLink(regenerate: false) } catch { self.error = error.localizedDescription }
                    }
                }
            } else if !link.isEmpty {
                HStack {
                    Text(link)
                        .font(.system(.caption, design: .monospaced))
                        .lineLimit(1).truncationMode(.middle)
                        .textSelection(.enabled)
                    Spacer()
                    Button(copied ? "Copied" : "Copy Link") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(link, forType: .string)
                        copied = true
                        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { copied = false }
                    }
                }
                HStack(spacing: 12) {
                    Button("New Link") { Task { await makeLink(regenerate: true) } }
                        .help("Replace the link: the old one stops working")
                    Button("Turn Off Link") { Task { await model.revokeInvite(groupID); link = "" } }
                }
                .buttonStyle(.link)
                Text("Anyone with the link can ask to join; you choose who's let in, and whether they can view or drive. They reach this Mac on the networks remote access listens on (Settings ▸ Devices): over Tailscale, if they're on another tailnet, share this Mac with them in Tailscale first.")
                    .font(.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Button("Create Invite Link") { Task { await makeLink(regenerate: false) } }
            }
        }
    }

    private func makeLink(regenerate: Bool) async {
        do {
            link = try await model.inviteLink(groupID, regenerate: regenerate)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// Joining a Group someone shared: confirm, then wait for them to let you in.
struct JoinSheet: View {
    let model: AppModel
    @State var link: String
    @Environment(\.dismiss) private var dismiss
    @State private var waiting = false
    @State private var error: String?

    var body: some View {
        let preview = InvitePreview(link: link)
        VStack(alignment: .leading, spacing: 14) {
            if let preview {
                Text("Join “\(preview.groupName)” from \(preview.hostName)?").font(.title3.weight(.semibold))
                Text("Everything in it runs on \(preview.hostName). Once you're let in, it appears in your sidebar, with what you may do (view, or type and drive).")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } else {
                Text("Join a Shared Group").font(.title3.weight(.semibold))
                TextField("Paste the invite link (hammock://join/…)", text: $link)
                    .textFieldStyle(.roundedBorder)
            }
            if waiting {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Waiting for \(preview?.hostName ?? "the host") to let you in…")
                }
                if !model.myUserID.isEmpty {
                    Text("They’ll see your key as \(keyFingerprint(userID: model.myUserID)).")
                        .font(.callout).foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }
            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
            }
            HStack {
                Spacer()
                Button(waiting ? "Close" : "Cancel") { dismiss() }
                if !waiting {
                    Button("Ask to Join") { join() }
                        .keyboardShortcut(.defaultAction)
                        .disabled(preview == nil)
                }
            }
        }
        .padding(20)
        .frame(width: 440)
    }

    private func join() {
        waiting = true
        error = nil
        Task {
            do {
                let groupID = try await model.join(link: link)
                model.selectGroup(groupID) // its first tab, or the Group if it has none
                dismiss()
            } catch {
                waiting = false
                self.error = error.localizedDescription
            }
        }
    }
}

/// Settings ▸ Sharing: how others see you.
struct SharingSettings: View {
    let model: AppModel
    @State private var name = ""
    @State private var deviceName = ""
    @State private var confirmStop = false

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .sharing)
            Section("You") {
                HStack(spacing: 14) {
                    if let me = model.me { AvatarView(person: me, size: 48) }
                    VStack(alignment: .leading, spacing: 6) {
                        TextField("Your name", text: $name)
                            .onSubmit { save(avatar: model.me?.avatar) }
                        HStack {
                            Button("Use Account Picture") { save(avatar: AccountPicture.png()) }
                            Button("Choose…") { choose() }
                            Button("Remove") { save(avatar: nil) }
                        }
                        .buttonStyle(.link)
                    }
                }
                Text("Shown to people you share Groups with, and in Groups they share with you.")
                    .font(.caption).foregroundStyle(.secondary)
                TextField("This Mac's name", text: $deviceName, prompt: Text(model.me?.deviceName ?? Host.current().localizedName ?? ""))
                    .onSubmit { saveDeviceName() }
                Text("How your phone and the people you share with see this Mac. Empty: its own name, from System Settings.")
                    .font(.caption).foregroundStyle(.secondary)
                if !model.myUserID.isEmpty {
                    LabeledContent("Your key", value: keyFingerprint(userID: model.myUserID))
                        .font(.body.monospaced())
                        .textSelection(.enabled)
                    Text("When you ask to join, the host sees this. Read it out to them if they ask.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            Section("Sharing") {
                let s = model.sharing
                LabeledContent("Shared Groups", value: "\(s.groups)")
                LabeledContent("People here now", value: "\(s.online)")
                Button("Stop All Sharing", role: .destructive) { confirmStop = true }
                    .disabled(s.groups == 0 && !model.groups.contains(where: \.inviteActive))
            }
        }
        .formStyle(.grouped)
        .onAppear {
            name = model.me?.name ?? ""
            deviceName = model.me?.deviceName ?? ""
        }
        .onDisappear { if deviceName != (model.me?.deviceName ?? "") { saveDeviceName() } }
        .confirmationDialog("Stop sharing everything?", isPresented: $confirmStop) {
            Button("Stop All Sharing", role: .destructive) { Task { await model.stopSharing(nil) } }
        } message: {
            Text("Everyone is disconnected and every invite link stops working.")
        }
    }

    private func saveDeviceName() {
        let d = deviceName.trimmingCharacters(in: .whitespaces)
        Task { await model.setProfile(name: model.me?.name ?? NSFullUserName(), avatar: model.me?.avatar, deviceName: d) }
    }

    private func save(avatar: Data?) {
        let n = name.trimmingCharacters(in: .whitespaces)
        Task { await model.setProfile(name: n.isEmpty ? (model.me?.name ?? NSFullUserName()) : n, avatar: avatar) }
    }

    private func choose() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.image]
        guard panel.runModal() == .OK, let url = panel.url, let image = NSImage(contentsOf: url) else { return }
        save(avatar: AccountPicture.png(image))
    }
}

/// This Mac user's account picture, as a small PNG.
enum AccountPicture {
    static func png(_ image: NSImage? = nil) -> Data? {
        guard let image = image ?? accountImage() else { return nil }
        let side = 128
        guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: side, pixelsHigh: side, bitsPerSample: 8, samplesPerPixel: 4,
                                         hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0) else { return nil }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
        // Fill the square, cropping the longer side.
        let s = image.size, scale = CGFloat(side) / min(s.width, s.height)
        let w = s.width * scale, h = s.height * scale
        image.draw(in: NSRect(x: (CGFloat(side) - w) / 2, y: (CGFloat(side) - h) / 2, width: w, height: h))
        NSGraphicsContext.restoreGraphicsState()
        return rep.representation(using: .png, properties: [:])
    }

    /// The login picture, from Directory Services (no permission needed).
    private static func accountImage() -> NSImage? {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/dscl")
        p.arguments = [".", "-read", "/Users/\(NSUserName())", "JPEGPhoto"]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = Pipe()
        guard (try? p.run()) != nil else { return nil }
        let raw = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        // "JPEGPhoto:\n ffd8ffe0 …" — hex, after the attribute name.
        guard let text = String(data: raw, encoding: .utf8), let colon = text.firstIndex(of: ":") else { return nil }
        let hex = text[text.index(after: colon)...].filter(\.isHexDigit)
        var bytes = Data(capacity: hex.count / 2)
        var i = hex.startIndex
        while i < hex.endIndex, let j = hex.index(i, offsetBy: 2, limitedBy: hex.endIndex) {
            if let b = UInt8(hex[i..<j], radix: 16) { bytes.append(b) }
            i = j
        }
        return NSImage(data: bytes)
    }
}
