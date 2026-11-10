import AppKit
import HammockKit
import SwiftUI

// Linked computers (ADR-0013) on the Mac: linking one (File ▸ Link a
// Computer…), the code check on both, the footer's switcher, and Settings
// ▸ Devices ▸ Your Computers.

/// What linking sheet is up, from wherever it was asked for.
@MainActor
@Observable
final class LinkUI {
    static let shared = LinkUI()
    /// Link a Computer… is up.
    var showing = false
    /// What linking starts with: an address or hammock://link/ link.
    var link: String?
    /// The link came from outside the app (a URL opened from a web page or a
    /// message): it's asked about before anything is sent.
    var linkFromOutside = false
    /// A computer to unlink, once you confirm, and where it was asked (its
    /// window, or Settings: the confirmation shows there).
    var unlinking: Computer?
    var unlinkingIn: UnlinkPlace = .window
    /// The computer whose icon and colour are being picked (its user id).
    var customising: String?

    func customise(_ c: Computer) {
        customising = c.userID
        NSApp.activate()
    }

    func askUnlink(_ c: Computer, in place: UnlinkPlace = .window) {
        unlinkingIn = place
        unlinking = c
    }

    /// Link a Computer…, starting with `link` (an address, or a hammock://link/
    /// link) when given. `fromOutside` only for a URL opened from elsewhere
    /// (onOpenURL): those ask first; the app's own (Link Again…) start at once.
    func open(link: String? = nil, fromOutside: Bool = false) {
        self.link = link
        linkFromOutside = link != nil && fromOutside
        showing = true
        NSApp.activate()
    }
}

/// Linking another computer of yours: those on your tailnet (your Tailscale
/// user's) and those Bonjour finds on the local networks remote access
/// listens on, or one typed by its address or link; then the code, which the
/// other computer shows too. The first time, where to listen.
struct LinkComputerSheet: View {
    let model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var found: [Hammock_V1_DiscoveredComputer] = []
    @State private var searching = true
    @State private var discoveryError: String?
    @State private var linking: String?        // the computer being linked (its name)
    @State private var linked: String?
    @State private var error: String?
    @State private var showLink = false
    @State private var myLink = ""
    @State private var theirLink = ""
    /// Set when remote access has no network to listen on yet: where to listen, first.
    @State private var choosing: Hammock_V1_Devices?
    /// A hammock://link/ link opened from elsewhere (a web page, a message),
    /// waiting for you to say to link with it: nothing is sent until you do.
    @State private var asking: String?
    /// The link you said to link with.
    @State private var confirmedLink: String?
    /// The found list's own height, so it's only as tall as it needs to be,
    /// up to listMax, then scrolls (a tailnet can have dozens of devices).
    @State private var listHeight: CGFloat = 0
    private let listMax: CGFloat = 360

    /// The code this side shows once both have exchanged nonces.
    private var request: LinkRequest? { model.linkRequests.first { !$0.incoming } }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            if let asking {
                confirmLink(asking)
            } else if let choosing {
                NetworkChooser(model: model, status: choosing) { ok in
                    if ok {
                        self.choosing = nil
                        Task { await begin() }
                    } else {
                        dismiss()
                    }
                }
            } else if let request {
                CodeCheck(model: model, request: request)
            } else if let linked {
                Label("Linked with \(linked)", systemImage: "checkmark.circle.fill")
                    .font(.title3.weight(.semibold))
                    .foregroundStyle(.green)
                Text("Its Groups show among yours, with its badge, and this Mac's show there. Use the footer to see one computer at a time.")
                    .foregroundStyle(.secondary)
                HStack { Spacer(); Button("Done") { dismiss() }.keyboardShortcut(.defaultAction) }
            } else if showLink {
                linkByHand
            } else {
                discovery
            }
        }
        .padding(22)
        .frame(width: 480)
        .task {
            // A link opened from elsewhere: asked about first, before anything
            // (remote access, connecting to it) happens. The app's own (Link
            // Again…, an address) start as they always have.
            let how = LinkStart(link: LinkUI.shared.link, fromOutside: LinkUI.shared.linkFromOutside)
            LinkUI.shared.link = nil
            LinkUI.shared.linkFromOutside = false
            switch how {
            case .ask(let link): asking = link
            case .start(let link): confirmedLink = link; await prepare()
            case .discover: await prepare()
            }
        }
    }

    private func prepare() async {
        // Linking turns remote access on: somewhere to listen, first.
        if let s = try? await model.deviceStatus(), s.needsNetworkChoice, !s.networks.isEmpty {
            choosing = s
        } else {
            await begin()
        }
    }

    /// "Link with …?" for a link opened from elsewhere. Its name is only what
    /// the link says, so the addresses it would connect to are what's shown.
    @ViewBuilder
    private func confirmLink(_ link: String) -> some View {
        let preview = LinkInvitePreview(link: link)
        VStack(alignment: .leading, spacing: 14) {
            if let preview {
                Text("Link with \(preview.addresses.first.map(hostOnly) ?? "this computer")?").font(.title3.weight(.bold))
                Text("A link asked Hammock to link this Mac with another computer. Link only if you opened it yourself, from a computer of yours: a linked computer sees and drives this Mac's Groups. Nothing is sent until you choose Link.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(preview.addresses, id: \.self) { Text($0).font(.callout.monospaced()) }
                    if !preview.name.isEmpty {
                        Text("It calls itself “\(preview.name)”.").font(.callout)
                    }
                }
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
            } else {
                Text("Link a Computer").font(.title3.weight(.bold))
                Text("That link is damaged; make a new one.").foregroundStyle(.secondary)
            }
            HStack {
                Spacer()
                // Escape cancels; Return does nothing (it mustn't link, and
                // a Cancel that took Return would lose Escape).
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                if preview != nil {
                    Button("Link") {
                        confirmedLink = link
                        asking = nil
                        Task { await prepare() }
                    }
                }
            }
        }
    }

    private func begin() async {
        if let link = confirmedLink {
            confirmedLink = nil
            start(link, name: "the other computer")
        } else {
            await discover()
        }
    }

    private var discovery: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Link a Computer").font(.title3.weight(.bold))
                Text("Your computers running Hammock, on your tailnet and on the local networks this Mac listens on. A linked computer's Groups show here, and this Mac's show there.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            GroupBox {
                if searching {
                    HStack { ProgressView().controlSize(.small); Text("Looking for your computers…").foregroundStyle(.secondary) }
                        .frame(maxWidth: .infinity, minHeight: 60)
                } else if found.isEmpty {
                    VStack(spacing: 6) {
                        Text("No computers found on your tailnet or the local networks this Mac listens on.")
                        if let discoveryError { Text(discoveryError).font(.callout) }
                        Button("On a VPN? Type its address…") { showLink = true }.buttonStyle(.link)
                    }
                    .multilineTextAlignment(.center)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, minHeight: 60)
                } else {
                    ScrollView {
                        VStack(spacing: 0) {
                            ForEach(Array(found.enumerated()), id: \.offset) { i, d in
                                if i > 0 { Divider() }
                                row(d)
                            }
                        }
                        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { listHeight = $0 }
                    }
                    .scrollBounceBehavior(.basedOnSize)
                    .frame(height: min(max(listHeight, 1), listMax))
                }
            }
            Label("Linking turns on remote access on both computers, on the networks each listens on (Settings ▸ Devices). Your other linked computers link with it too.", systemImage: "info.circle")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if let error {
                Text(error).foregroundStyle(.red).font(.callout).fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Button("Not listed? Type its address or use a link…") { showLink = true }
                    .buttonStyle(.link)
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
            }
        }
    }

    private func row(_ d: Hammock_V1_DiscoveredComputer) -> some View {
        HStack(spacing: 12) {
            let linkedTo = model.computer(d.linkedUserID)
            Group {
                if let linkedTo {
                    ComputerIcon(linkedTo, size: 20, weight: .regular)
                } else {
                    Image(systemName: d.os.lowercased() == "linux" ? "server.rack" : "desktopcomputer")
                        .font(.system(size: 20))
                        .foregroundStyle(d.answering ? .primary : .secondary)
                }
            }
            .frame(width: 28)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    Text(d.name).fontWeight(.semibold)
                    viaBadge(d)
                }
                Text(detail(d)).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            Spacer()
            if linkedTo != nil {
                Label("Linked", systemImage: "checkmark").foregroundStyle(.green).font(.callout.weight(.semibold))
            } else if d.answering {
                if linking == d.name {
                    ProgressView().controlSize(.small)
                } else {
                    Button("Link") { start(d.addresses.first ?? "", name: d.name) }
                        .disabled(linking != nil)
                }
            }
        }
        .padding(.vertical, 6)
    }

    /// Where it was found: Bonjour on a local network, or Tailscale.
    private func viaBadge(_ d: Hammock_V1_DiscoveredComputer) -> some View {
        let local = d.via == "local"
        return Label(local ? "Local network" : "Tailscale", systemImage: local ? "wifi" : "point.3.connected.trianglepath.dotted")
            .labelStyle(.titleAndIcon)
            .font(.caption)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 6)
            .padding(.vertical, 1)
            .background(Color.secondary.opacity(0.12), in: Capsule())
    }

    private func detail(_ d: Hammock_V1_DiscoveredComputer) -> String {
        let addr = d.addresses.first.map(hostOnly) ?? ""
        switch true {
        case !d.online: return "\(d.os) · offline"
        case !d.answering: return "\(d.via == "local" ? "On your local network" : "On your tailnet"); Hammock isn't answering. Open Hammock there, or turn on remote access."
        default: return "\(d.os) · \(addr)"
        }
    }

    private var linkByHand: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Link by Address or Link").font(.title3.weight(.bold))
            Text("Type the other computer's address: its name or IP, with :port if Hammock there doesn't use its usual one (on a VPN like WireGuard, its address on the VPN). Or open Link a Computer… on the other computer, choose this, and paste its link here (or run hmk link --invite there), or give it this computer's link.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            TextField("Address or link", text: $theirLink, prompt: Text("studio.local, 10.0.0.2:7433 or hammock://link/…"))
                .textFieldStyle(.roundedBorder)
                .onSubmit { if typedValid { startTyped() } }
            HStack {
                Text("This computer's link:").foregroundStyle(.secondary)
                Text(myLink.isEmpty ? "…" : myLink).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                Button("Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(myLink, forType: .string)
                }
                .disabled(myLink.isEmpty)
            }
            .font(.callout)
            if let error {
                Text(error).foregroundStyle(.red).font(.callout).fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Button("Back") { showLink = false; error = nil }
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Link") { startTyped() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!typedValid || linking != nil)
            }
        }
        .task {
            do { myLink = try await model.linkInvite() } catch { self.error = error.localizedDescription }
        }
    }

    private var typed: String { theirLink.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// A hammock://link/ link, or an address (host or host:port).
    private var typedValid: Bool { !typed.isEmpty && !typed.contains(where: \.isWhitespace) && (typed.hasPrefix("hammock://link/") || !typed.contains("://")) }

    private func startTyped() {
        start(typed, name: typed.hasPrefix("hammock://") ? "the other computer" : hostOnly(typed))
    }

    private func discover() async {
        searching = true
        defer { searching = false }
        do {
            let r = try await model.discoverComputers()
            // The ones Hammock answers on (linkable, or linked) first, then the
            // rest online, then offline; otherwise in the daemon's order.
            func rank(_ d: Hammock_V1_DiscoveredComputer) -> Int { d.answering ? 0 : d.online ? 1 : 2 }
            found = r.list.enumerated()
                .sorted { (rank($0.element), $0.offset) < (rank($1.element), $1.offset) }
                .map(\.element)
            discoveryError = r.error.isEmpty ? nil : r.error
        } catch {
            discoveryError = error.localizedDescription
        }
    }

    private func start(_ address: String, name: String) {
        guard !address.isEmpty else { return }
        linking = name
        error = nil
        Task {
            do {
                try await model.linkComputer(address)
                linked = name
            } catch {
                self.error = error.localizedDescription
            }
            linking = nil
        }
    }
}

/// The code a link shows on both computers, and the person's answer.
struct CodeCheck: View {
    let model: AppModel
    let request: LinkRequest

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text(request.incoming ? "\(request.name) wants to link" : "Check the code on \(request.name)")
                    .font(.title3.weight(.bold))
                Text(request.incoming
                     ? "Link only if \(request.name) shows the same code. Its Groups will show here, and this Mac's there."
                     : "\(request.name) is showing a code too. Link only if they're the same.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Text(request.code)
                .font(.system(size: 40, weight: .semibold, design: .monospaced))
                .tracking(6)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 8)
            if let w = request.unlinkedWarning {
                Label(w, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
            VStack(spacing: 2) {
                if let origin = request.origin {
                    // Who Tailscale says it is (not what it calls itself), or where it came from.
                    Text(origin).font(.callout)
                }
                Text("\(request.name)'s key: \(request.fingerprint)").font(.callout.monospaced())
            }
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity)
            HStack {
                if request.acceptedHere {
                    ProgressView().controlSize(.small)
                    Text("Waiting for \(request.name)…").foregroundStyle(.secondary)
                } else if request.acceptedThere {
                    Text("\(request.name) says they match.").foregroundStyle(.secondary)
                }
                Spacer()
                // Neither is the default: Return mustn't say they match
                // without you reading the code.
                Button("They're Different") { model.respondLink(request.id, accept: false) }
                    .keyboardShortcut(.cancelAction)
                    .disabled(request.acceptedHere)
                Button("They Match") { model.respondLink(request.id, accept: true) }
                    .disabled(request.acceptedHere)
            }
        }
    }
}

/// Another computer asking to link, on this one (Needs You).
struct LinkRequestCard: View {
    let model: AppModel
    let request: LinkRequest
    @State private var exit = CardExit.fade

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: "link").foregroundStyle(.blue)
                VStack(alignment: .leading, spacing: 1) {
                    Text("\(request.name) wants to link").fontWeight(.semibold).lineLimit(1)
                    Text("Code \(request.code)").font(.callout.monospaced()).foregroundStyle(.secondary)
                    if let origin = request.origin {
                        Text(origin).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                    if let w = request.unlinkedWarning {
                        Text(w).font(.caption).foregroundStyle(.orange).lineLimit(2)
                    }
                }
                Spacer(minLength: 4)
            }
            HStack(spacing: 6) {
                if request.acceptedHere {
                    Text("Waiting for \(request.name)…").font(.callout).foregroundStyle(.secondary)
                }
                Spacer()
                // They Match keeps the card (waiting for the other side), so
                // only Different marks how it leaves.
                Button("Different") { CardExit.leave(.denied, $exit) { model.respondLink(request.id, accept: false) } }
                Button("They Match") { model.respondLink(request.id, accept: true) }
                    .buttonStyle(.borderedProminent)
            }
            .controlSize(.small)
            .disabled(request.acceptedHere)
        }
        .padding(10)
        .background(.background, in: RoundedRectangle(cornerRadius: 11))
        .shadow(color: .black.opacity(0.06), radius: 1.5, y: 1)
        .cardMotion(exit, cornerRadius: 11)
    }
}

/// The sidebar's footer once computers are linked: All, then each computer
/// (its badge and whether it's reachable). Clicking one shows only its
/// Groups (and again, or All, shows them all); the chosen one shows its
/// name. Its status, key and sharing are in its help and right-click menu.
struct ComputerSwitcher: View {
    @Bindable var model: AppModel
    @State private var confirmStop = false
    var body: some View {
        HStack(spacing: 2) {
            chip(nil)
            ForEach(model.computers, id: \.userID) { c in chip(c) }
            Spacer(minLength: 0)
            // Only when something's wrong: connected is the normal state, and a
            // green dot beside the computers read as one of theirs.
            if !model.connected {
                Circle()
                    .fill(Color.orange)
                    .frame(width: 7, height: 7)
                    .accessibilityLabel("Disconnected")
                    .help("Connecting to hmkd…")
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .confirmationDialog("Stop sharing everything?", isPresented: $confirmStop) {
            Button("Stop All Sharing", role: .destructive) { Task { await model.stopSharing(nil) } }
        } message: {
            Text("Everyone is disconnected and every invite link stops working.")
        }
    }

    private func chip(_ c: Computer?) -> some View {
        let id = c?.userID ?? ""
        let on = model.computerFilter == id || (c?.thisComputer == true && model.computerFilter == model.myUserID)
        return Button {
            withAnimation(.snappy(duration: 0.25)) {
                // Again shows them all.
                model.computerFilter = (on && c != nil) ? "" : (c?.thisComputer == true ? model.myUserID : id)
            }
        } label: {
            HStack(spacing: 5) {
                if let c {
                    ComputerIcon(c, size: 15)
                        .opacity(c.online ? 1 : 0.5)
                        .overlay(alignment: .bottomTrailing) {
                            Circle()
                                .fill(c.online ? Color.green : Color.clear)
                                .strokeBorder(c.online ? Color.clear : Color.secondary, lineWidth: 1.2)
                                .frame(width: 6, height: 6)
                                .offset(x: 3, y: 2)
                        }
                    if on { Text(c.thisComputer ? "This Mac" : c.name).font(.callout.weight(.semibold)).lineLimit(1) }
                } else {
                    Text("All").font(.callout.weight(.semibold))
                }
            }
            .padding(.horizontal, 8)
            .frame(minWidth: 34, minHeight: 28)
            .background(on ? Color.primary.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 7))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(help(c))
        .accessibilityLabel(c.map { "Show only \($0.thisComputer ? "this Mac" : $0.name)" } ?? "Show all computers")
        .accessibilityAddTraits(on ? .isSelected : [])
        .contextMenu { if let c { menu(c) } else { Button("Link a Computer…") { LinkUI.shared.open() } } }
    }

    private func help(_ c: Computer?) -> String {
        guard let c else { return "All your computers' Groups" }
        let groups = model.groups.filter { c.thisComputer ? !$0.isOnLinkedComputer : $0.hostUserID == c.userID && $0.isOnLinkedComputer }.count
        var lines = [c.thisComputer ? "\(c.name) (this Mac)" : c.name, c.status, groups == 1 ? "1 Group" : "\(groups) Groups"]
        if c.thisComputer, model.sharing.groups > 0 {
            lines.append(model.sharing.groups == 1 ? "Sharing 1 Group" : "Sharing \(model.sharing.groups) Groups")
        }
        lines.append("Key \(c.fingerprint)")
        return lines.joined(separator: "\n")
    }

    @ViewBuilder
    private func menu(_ c: Computer) -> some View {
        Button(c.thisComputer ? "New Group on This Mac" : "New Group on \(c.name)") { Task { await model.newGroup(on: c.thisComputer ? model.myUserID : c.userID) } }
            .disabled(!c.online)
        Button(c.thisComputer ? "New Agent Tab on This Mac…" : "New Agent Tab on \(c.name)…") { AgentLauncher.shared.open(model, computerID: c.thisComputer ? "" : c.userID) }
            .disabled(!c.online)
        Button(c.thisComputer ? "Find Session on This Mac…" : "Find Session on \(c.name)…") { PaletteState.shared.open(.sessions, computer: c.thisComputer ? "" : c.userID) }
            .disabled(!c.online)
        if model.screenComputers.contains(where: { $0.userID == c.userID }) {
            Button(c.thisComputer ? "Show Screen of This Mac" : "Show Screen of \(c.name)") {
                ScreenViewerUI.shared.open(model, computerID: c.userID, name: c.thisComputer ? "This Mac" : c.name)
            }
        }
        Divider()
        Button("Icon…") { LinkUI.shared.customise(c) }
            .disabled(!c.online)
        ColourMenu(model: model, computer: c)
            .disabled(!c.online)
        if c.thisComputer, model.sharing.groups > 0 {
            Button("Stop All Sharing…") { confirmStop = true }
        }
        if c.needsAttention, let addr = c.addresses.first {
            Button("Link \(c.name) Again…") { LinkUI.shared.open(link: addr) }
        }
        if c.needsLinking {
            Button("Remove \(c.name) from This Mac") { Task { await model.unlinkComputer(c.userID, forget: true) } }
            Button("Unlink \(c.name) from All Your Computers…") { LinkUI.shared.askUnlink(c) }
        } else {
            Button(c.thisComputer ? "Unlink This Mac…" : "Unlink \(c.name)…") { LinkUI.shared.askUnlink(c) }
        }
        Divider()
        Button("Link a Computer…") { LinkUI.shared.open() }
    }
}

enum UnlinkPlace { case window, settings }

extension View {
    /// Asks before unlinking LinkUI's computer, when it was asked here
    /// (place): the one confirmation, from the footer, palette or Settings.
    func unlinkConfirmation(_ model: AppModel, in place: UnlinkPlace = .window) -> some View {
        let ui = LinkUI.shared
        return confirmationDialog(ui.unlinking.map { $0.thisComputer ? "Unlink this Mac from your other computers?" : "Unlink \($0.name)?" } ?? "",
                                  isPresented: Binding(get: { ui.unlinking != nil && ui.unlinkingIn == place }, set: { if !$0 { ui.unlinking = nil } })) {
            Button("Unlink", role: .destructive) {
                if let c = ui.unlinking { Task { await model.unlinkComputer(c.userID) } }
            }
        } message: {
            Text(ui.unlinking?.thisComputer == true
                 ? "Your other computers' Groups stop showing here, and this Mac's there."
                 : ui.unlinking?.needsLinking == true
                 ? "For a lost or stolen computer: every computer of yours refuses it for good. To forget it on this Mac only, use Remove from This Mac."
                 : "It's unlinked from all your computers: its Groups stop showing on them, and theirs on it.")
        }
    }
}

/// A computer's icon in Settings ▸ Devices: click it to pick its icon and
/// colour (in a popover).
struct ComputerLookButton: View {
    let model: AppModel
    let computer: Computer
    /// A titled button ("Icon…"), rather than the icon itself.
    var titled = false
    @State private var picking = false

    var body: some View {
        Group {
            if titled {
                Button("Icon…") { picking = true }
            } else {
                Button { picking = true } label: {
                    ComputerIcon(computer, size: 20, weight: .regular)
                        .opacity(computer.online ? 1 : 0.6)
                        .frame(width: 32, height: 32)
                        .background(Color.primary.opacity(picking ? 0.1 : 0), in: RoundedRectangle(cornerRadius: 7))
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
        }
        .disabled(!computer.online)
        .help(computer.online ? "Its icon, in its colour, on all your computers" : "\(computer.name) is offline: its icon is set there")
        .accessibilityLabel("Icon of \(computer.name)")
        .popover(isPresented: $picking, arrowEdge: .trailing) {
            ComputerLookPicker(model: model, userID: computer.userID)
        }
    }
}

/// A computer's colour, as a menu: each in its colour, the one it has
/// ticked, and those other computers have shown but not offered (each has
/// its own).
struct ColourMenu: View {
    let model: AppModel
    let computer: Computer
    var title = "Colour"

    var body: some View {
        Menu(title) {
            ForEach(computerTints, id: \.self) { t in
                let owner = model.tintOwner(t, except: computer.userID)
                Toggle(isOn: Binding(get: { t.caseInsensitiveCompare(computer.tint) == .orderedSame || (t == neutralTint && computer.tint.isEmpty) },
                                     set: { if $0 { Task { await model.setTint(t, of: computer.userID) } } })) {
                    Label {
                        Text(owner.map { "\(tintName(t)) (\($0.thisComputer ? "this Mac" : $0.name))" } ?? tintName(t))
                    } icon: {
                        Image(nsImage: swatchImage(t))
                    }
                }
                .disabled(owner != nil)
            }
            Divider()
            Button("Other…") { LinkUI.shared.customise(computer) }
        }
    }
}

/// A colour's name in the menu.
func tintName(_ hex: String) -> String {
    if hex == neutralTint { return "Default" }
    return ["#2563EB": "Blue", "#7C3AED": "Purple", "#15803D": "Green", "#C2410C": "Orange", "#DB2777": "Pink",
     "#0891B2": "Teal", "#A16207": "Amber", "#4F46E5": "Indigo"][hex.uppercased()] ?? hex
}

/// Settings ▸ Devices ▸ Your Computers.
struct YourComputersSection: View {
    @Bindable var model: AppModel

    var body: some View {
        Section {
            if !model.computersNote.isEmpty {
                Label(model.computersNote, systemImage: "info.circle")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            if !model.hasLinkedComputers && !model.computers.contains(where: { $0.needsAttention }) {
                Text("Link another computer of yours (a Mac, or a Linux box running hmkd) and each shows the other's Groups, badged by computer.")
                    .foregroundStyle(.secondary)
            }
            Group {
                // This Mac always (its icon can be picked before anything's
                // linked); the others once linked.
                ForEach(model.computers.filter { $0.thisComputer || model.hasLinkedComputers || $0.needsAttention }, id: \.userID) { c in
                    HStack(spacing: 12) {
                        ComputerLookButton(model: model, computer: c)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(c.name).fontWeight(.semibold)
                            Text(detail(c)).font(.callout).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Text(c.fingerprint).font(.caption.monospaced()).foregroundStyle(.secondary)
                        ComputerLookButton(model: model, computer: c, titled: true)
                        if c.thisComputer {
                            Text("This Mac").foregroundStyle(.secondary).frame(width: 110, alignment: .trailing)
                        } else {
                            if c.needsAttention, let addr = c.addresses.first {
                                // Link again: the code check, on both (it confirms
                                // a changed certificate too).
                                Button("Link Again…") { LinkUI.shared.open(link: addr) }
                            }
                            if c.needsLinking {
                                // Forget it here only; or, for a lost or stolen
                                // one, unlink it from all of them (asked first).
                                Menu("Remove") {
                                    Button("Remove from This Mac") { Task { await model.unlinkComputer(c.userID, forget: true) } }
                                    Button("Unlink from All Your Computers…") { LinkUI.shared.askUnlink(c, in: .settings) }
                                }
                                .fixedSize()
                                .help(c.suspect
                                      ? "Removed, it isn't linked again by an introduction: only Link Again (its code checked) brings it back."
                                      : "Remove forgets it on this Mac only.")
                            } else {
                                Button("Unlink…") { LinkUI.shared.askUnlink(c, in: .settings) }
                            }
                        }
                    }
                }
            }
            HStack {
                Spacer()
                if model.hasLinkedComputers {
                    Button("Unlink This Mac…") { if let me = model.thisComputer { LinkUI.shared.askUnlink(me, in: .settings) } }
                }
                Button("Link a Computer…") { LinkUI.shared.open() }
            }
        } header: {
            Text("Your Computers")
        } footer: {
            Text("Each one shows every other's Groups. Unlinking one here unlinks it everywhere.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .unlinkConfirmation(model, in: .settings)
    }

    private func detail(_ c: Computer) -> String {
        var s = c.status
        if !c.thisComputer, c.linkedUnixMs > 0 {
            s += " · linked " + Date(timeIntervalSince1970: Double(c.linkedUnixMs) / 1000).formatted(date: .abbreviated, time: .omitted)
        }
        return s
    }
}

extension LinkRequest {
    /// "studio · alex@example.com": the Tailscale host and login it came
    /// from (checked to be your own), nil if not known.
    /// "You unlinked it on 3 Nov 2026": a computer you unlinked (lost,
    /// stolen?) asking to be linked again. nil if it never was.
    var unlinkedWarning: String? {
        guard unlinkedUnixMs > 0 else { return nil }
        let on = Date(timeIntervalSince1970: Double(unlinkedUnixMs) / 1000).formatted(date: .abbreviated, time: .omitted)
        let by = unlinkedBy.isEmpty ? "" : ", as \(unlinkedBy) asked"
        return "You unlinked \(name) on \(on)\(by). Link it again only if it's back with you."
    }

    var tailscaleSource: String? {
        guard !tailscaleHost.isEmpty else { return nil }
        return tailscaleLogin.isEmpty ? tailscaleHost : "\(tailscaleHost) · \(tailscaleLogin)"
    }

    /// "On Tailscale: studio · alex@example.com", or, from another network,
    /// "From 192.168.1.30 on Wi-Fi". nil if neither is known.
    var origin: String? {
        if let ts = tailscaleSource { return "On Tailscale: \(ts)" }
        return source.isEmpty ? nil : "From \(source)"
    }
}

/// "studio.local" from "studio.local:7433", "fd00::1" from "[fd00::1]:7433".
func hostOnly(_ address: String) -> String {
    if address.hasPrefix("["), let end = address.firstIndex(of: "]") {
        return String(address[address.index(after: address.startIndex)..<end])
    }
    let parts = address.split(separator: ":", omittingEmptySubsequences: false)
    return parts.count == 2 ? String(parts[0]) : address
}
