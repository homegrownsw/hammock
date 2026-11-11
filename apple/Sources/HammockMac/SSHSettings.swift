import AppKit
import HammockKit
import SwiftUI

/// Settings ▸ SSH: the Hammock Key (this Mac's SSH key in its Secure
/// Enclave, which signs only once you approve), who may approve it, how
/// your own SSH setup reaches Hammock's agent, and lending keys between your
/// computers.
struct SSHSettings: View {
    let model: AppModel
    /// Made-up state for HMK_DEBUG_SETTINGS=ssh snapshots (DebugSnapshot).
    var sample: Sample?
    @State private var setup: SSHSetup?
    @State private var busy = false
    @State private var error: String?
    @State private var confirmDelete = false
    @State private var copyHost = ""
    @State private var askHost = false
    @State private var note: String?

    private var key: HammockKeyStatus { sample?.key ?? model.hammockKey }

    struct Sample {
        var key = HammockKeyStatus()
        var setup = SSHSetup()

        init(_ kind: String) {
            setup.agentName = "1Password"
            setup.configPath = NSHomeDirectory() + "/.ssh/config"
            setup.change = "# Added by Hammock: in Hammock's panes, ssh uses Hammock's agent, which passes\n# requests on to 1Password and adds your Hammock Key. Delete these lines to undo.\nMatch exec \"test -n \\\"$HMK_PANE_ID\\\"\"\n  IdentityAgent SSH_AUTH_SOCK\n"
            setup.gitProgram = "/Applications/1Password.app/Contents/MacOS/op-ssh-sign"
            setup.jjPath = "/opt/homebrew/bin/jj"
            setup.jjProgram = setup.gitProgram
            setup.jjConfigPath = NSHomeDirectory() + "/.config/jj/conf.d/zz-hammock.toml"
            setup.jjChange = "# Added by Hammock: in Hammock's panes, jj signs through hmk-jj-ssh-sign, which\n# uses op-ssh-sign while you're at this Mac, and while you're away the\n# key of the computer you're at, or your Hammock Key. Delete this file to undo.\n--when.environments = [\"HMK_PANE_ID\"]\n\n[signing.backends.ssh]\nprogram = '\(NSHomeDirectory())/Library/Application Support/Hammock/run/hmk-jj-ssh-sign'\n"
            key.available = true
            guard kind == "key" else { return }
            key.exists = true
            key.publicKey = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFakeFakeFakeQm9yaW5nIGJ1dCByZWFsaXN0aWMga2V5IG1hdGVyaWFsIGZvciBzbmFwc2hvdHM= Hammock-Key-Studio"
            key.fingerprint = "SHA256:q2Xh0f3m3w2a9cJk1Vt8nZs4yLr6pE5uB7dG0iH1oKc"
            key.publicKeyPath = NSHomeDirectory() + "/Library/Application Support/Hammock/hmk-key.pub"
            key.approvers = [.with { $0.deviceID = "dv_1"; $0.name = "Sam's iPhone" }]
            key.canAdd = [.with { $0.deviceID = "dv_2"; $0.name = "iPad" }]
        }
    }

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .ssh)
            keySection
            if key.exists { approversSection }
            setupSection
            Section {
                Toggle("Use the SSH keys of the computer you're at", isOn: Binding(
                    get: { AppConfig.shared.sshAgentForwarding },
                    set: { AppConfig.shared.set("ssh-agent-forwarding", $0 ? nil : "false") }))
                    .settingAnchor("ssh.forwarding")
                Text("Like ssh -A: ssh and git in a pane on another of your computers can use this Mac's keys while you're here, and panes here use the keys of the one you're at. Agents ask you first. For panes opened from now on; off on either computer, nothing is lent.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            } header: {
                Text("Your Computers")
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
        .task { await refresh() }
        .confirmationDialog("Delete the Hammock Key?", isPresented: $confirmDelete) {
            Button("Delete", role: .destructive) { run { try await model.hammockKeyCommand(.delete) } }
        } message: {
            Text("Servers and GitHub that have it won't take it any more. A new one is a new key: you'd add it to them again.")
        }
        .sheet(isPresented: $askHost) { copySheet }
    }

    // MARK: The key

    @ViewBuilder private var keySection: some View {
        Section {
            if key.exists {
                Text(key.publicKey)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(3)
                LabeledContent("Fingerprint") {
                    Text(key.fingerprint).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                }
                HStack {
                    Button("Copy Public Key") { copyKey() }
                    Button("Add to GitHub…") { addToGitHub() }
                        .help("Copies the key and opens GitHub's SSH keys page: add it as an Authentication key and again as a Signing key.")
                    Button("Copy to a Server…") { askHost = true }
                    Spacer()
                    Button("Delete…", role: .destructive) { confirmDelete = true }
                }
                .disabled(busy)
                if let note {
                    Text(note).font(.callout).foregroundStyle(.secondary)
                }
                Button("Add to Git's Allowed Signers") { addAllowedSigner() }
                    .disabled(busy)
                    .help("So git log --show-signature trusts commits signed with it.")
            } else if !key.available && key.error.isEmpty {
                Text("This Mac has no Secure Enclave, so it can't have a Hammock Key.")
                    .foregroundStyle(.secondary)
            } else {
                Text("An SSH key that never leaves this Mac's Secure Enclave and signs only when you say so: Touch ID here, or Face ID on your iPhone when you're away. Your usual keys go on working at your desk; the Hammock Key covers the rest, for ssh, git push and signing commits.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                HStack {
                    Spacer()
                    Button("Create Hammock Key…") { run { try await model.hammockKeyCommand(.create) } }
                        .disabled(busy || !key.available)
                }
            }
            if !key.error.isEmpty {
                Label(key.error, systemImage: "exclamationmark.triangle.fill").font(.callout).foregroundStyle(.orange)
            }
            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill").font(.callout).foregroundStyle(.red)
            }
        } header: {
            Text("Hammock Key")
        } footer: {
            if key.exists {
                Text("Hammock asks with Touch ID here and on the phones below at once; the first answer wins. Allowing your own programs also allows them for 10 minutes in their pane; an agent can be allowed for 5 minutes if you choose to.")
            }
        }
    }

    @ViewBuilder private var approversSection: some View {
        Section {
            LabeledContent("This Mac") { Text("Touch ID").foregroundStyle(.secondary) }
            ForEach(key.approvers, id: \.deviceID) { a in
                LabeledContent(a.name) {
                    Button("Remove") { run { try await model.hammockKeyCommand(.removeApprover, deviceID: a.deviceID) } }
                        .disabled(busy)
                }
            }
            ForEach(key.canAdd, id: \.deviceID) { a in
                LabeledContent(a.name) {
                    Button("Allow…") { run { try await model.hammockKeyCommand(.addApprover, deviceID: a.deviceID) } }
                        .disabled(busy)
                        .help("With Touch ID: from then on it can approve the Hammock Key with Face ID.")
                }
            }
        } header: {
            Text("Who Can Approve")
        } footer: {
            Text("A paired iPhone shows up here once you've opened Hammock on it. Letting it approve needs Touch ID here, so nobody can add a phone while you're away.")
        }
    }

    // MARK: Your setup

    @ViewBuilder private var setupSection: some View {
        Section {
            if let setup = setup ?? sample?.setup {
                if !setup.agentName.isEmpty {
                    LabeledContent("Your SSH agent") { Text(setup.agentName).foregroundStyle(.secondary) }
                }
                if setup.routed {
                    Label("ssh in Hammock's panes goes through Hammock's agent.", systemImage: "checkmark.circle.fill")
                        .foregroundStyle(.green)
                } else {
                    Text("\(abbreviated(setup.configPath)) points ssh straight at \(setup.agentName), so ssh in Hammock's panes skips Hammock's agent and can't use the Hammock Key. Hammock can add this to it (only Hammock's panes are affected; the old file is kept):")
                        .font(.callout)
                    Text(setup.change.trimmingCharacters(in: .whitespacesAndNewlines))
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .padding(8)
                        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
                    HStack {
                        Spacer()
                        Button("Add to \(abbreviated(setup.configPath))") { applySetup() }.disabled(busy)
                    }
                }
                if !setup.gitProgram.isEmpty {
                    Text("Git signs commits with \(abbreviated(setup.gitProgram)). In Hammock's panes it signs with your key when it can and with the Hammock Key when it can't.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                if !setup.jjPath.isEmpty { jjSetup(setup) }
            } else {
                ProgressView().controlSize(.small)
            }
        } header: {
            Text("Your SSH Setup")
        }
    }

    /// jj signs with its own program, not git's (sshsetup_jj.go): shown only
    /// when jj is installed and signs with SSH.
    @ViewBuilder private func jjSetup(_ setup: SSHSetup) -> some View {
        let program = setup.jjProgram.isEmpty ? "ssh-keygen" : abbreviated(setup.jjProgram)
        if !setup.jjProblem.isEmpty {
            Label(setup.jjProblem, systemImage: "exclamationmark.triangle.fill")
                .font(.callout)
                .foregroundStyle(.orange)
        } else if setup.jjRouted {
            Label("jj in Hammock's panes signs with \(program) at this Mac; at another of your computers, with its key; otherwise, the Hammock Key.", systemImage: "checkmark.circle.fill")
                .foregroundStyle(.green)
        } else {
            Text("jj signs commits with \(program), not git's setting, so in Hammock's panes it can't use keys from your other computers or the Hammock Key: while you're at another computer, it waits on a prompt on this Mac. Hammock can add this file to jj's config (it applies only in Hammock's panes):")
                .font(.callout)
            Text(setup.jjChange.trimmingCharacters(in: .whitespacesAndNewlines))
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .padding(8)
                .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
            HStack {
                Spacer()
                Button("Add \(abbreviated(setup.jjConfigPath))") { run { self.setup = try await model.sshSetup(applyJJ: true) } }
                    .disabled(busy)
            }
        }
    }

    private var copySheet: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Copy the Hammock Key to a Server").font(.headline)
            Text("Opens a tab running ssh-copy-id, which adds it to the server's authorized_keys. Log in there as you usually do.")
                .font(.callout)
                .foregroundStyle(.secondary)
            TextField("user@host", text: $copyHost).onSubmit(copyToServer)
            HStack {
                Spacer()
                Button("Cancel") { askHost = false }.keyboardShortcut(.cancelAction)
                Button("Copy") { copyToServer() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(copyHost.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 380)
    }

    // MARK: Actions

    private func refresh() async {
        if let sample { setup = sample.setup; return }
        _ = try? await model.hammockKeyCommand(.status)
        setup = try? await model.sshSetup()
    }

    private func run(_ op: @escaping () async throws -> Void) {
        busy = true
        error = nil
        Task {
            do { try await op() } catch { self.error = error.localizedDescription }
            busy = false
        }
    }

    private func applySetup() {
        run { setup = try await model.sshSetup(apply: true) }
    }

    private func copyKey() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(key.publicKey, forType: .string)
        note = "Copied."
    }

    private func addToGitHub() {
        copyKey()
        note = "Copied. On GitHub, add it as an Authentication key, then again as a Signing key."
        if let url = URL(string: "https://github.com/settings/ssh/new") { NSWorkspace.shared.open(url) }
    }

    private func copyToServer() {
        let host = copyHost.trimmingCharacters(in: .whitespaces)
        guard !host.isEmpty, !key.publicKeyPath.isEmpty else { return }
        askHost = false
        let group = model.selectedTabID.flatMap { model.tabs[$0]?.groupID } ?? model.groups.first?.id
        guard let group else { error = "Open a Group first."; return }
        Task { await model.createTab(in: group, name: "ssh-copy-id", command: ["/usr/bin/ssh-copy-id", "-f", "-i", key.publicKeyPath, host]) }
    }

    /// Adds the key to gpg.ssh.allowedSignersFile, under user.email.
    private func addAllowedSigner() {
        let file = git("config", "--global", "--get", "gpg.ssh.allowedSignersFile")
        let email = git("config", "--global", "--get", "user.email")
        guard !file.isEmpty, !email.isEmpty else {
            note = "Git has no allowed signers file (gpg.ssh.allowedSignersFile) or no user.email."
            return
        }
        let path = (file as NSString).expandingTildeInPath
        let line = "\(email) namespaces=\"git\" \(key.publicKey)\n"
        let existing = (try? String(contentsOfFile: path, encoding: .utf8)) ?? ""
        let keyPart = key.publicKey.split(separator: " ").prefix(2).joined(separator: " ")
        if existing.contains(keyPart) {
            note = "It's in \(abbreviated(path)) already."
            return
        }
        let text = existing.isEmpty || existing.hasSuffix("\n") ? existing + line : existing + "\n" + line
        do {
            try text.write(toFile: path, atomically: true, encoding: .utf8)
            note = "Added to \(abbreviated(path))."
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func git(_ args: String...) -> String {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/git")
        p.arguments = args
        let out = Pipe()
        p.standardOutput = out
        p.standardError = Pipe()
        do { try p.run() } catch { return "" }
        p.waitUntilExit()
        let data = out.fileHandleForReading.readDataToEndOfFile()
        return String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    }

    private func abbreviated(_ path: String) -> String { (path as NSString).abbreviatingWithTildeInPath }
}
