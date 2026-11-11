import AppKit
import HammockKit
import HammockUpdates
import SwiftUI

/// Settings ▸ About (the Hammock card at the top of the sidebar, and
/// Hammock ▸ About Hammock): the version, updates, quitting with the server,
/// acknowledgements (AcknowledgementsWindow.swift) and uninstalling.
struct AboutSettings: View {
    let model: AppModel
    @State private var uninstalling = false
    @State private var error: String?

    private var appName: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleName") as? String ?? "Hammock" }

    var body: some View {
        Form {
            Section {
                VStack(spacing: 6) {
                    Image(nsImage: NSApp.applicationIconImage)
                        .resizable()
                        .frame(width: 112, height: 112)
                    Text(appName).font(.largeTitle.bold())
                    Text("Version \(Updater.shared.version)")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 12)
            }
            .listRowBackground(Color.clear)

            // Always shown; a build without an updater (β, one built here)
            // says why it can't check.
            Section("Updates") {
                Toggle("Check for updates daily", isOn: Binding(get: { Updater.shared.checksAutomatically }, set: { Updater.shared.checksAutomatically = $0 }))
                    .disabled(!Updater.shared.isAvailable)
                    .settingAnchor("about.updates")
                LabeledContent("Check for updates now") {
                    Button("Check for Updates…") { Updater.shared.checkForUpdates() }
                        .disabled(!Updater.shared.canCheck)
                }
                .settingAnchor("about.checknow")
                if !Updater.shared.isAvailable {
                    Text("This build doesn't update itself: it's a development build. Build a newer one to update it.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
            }

            Section {
                LabeledContent("Server") {
                    Text(model.connected ? "Running" : "Not running").foregroundStyle(.secondary)
                }
                LabeledContent("Quit Hammock") {
                    HStack {
                        Button("Quit") { NSApp.terminate(nil) }
                        Button("Quit and Stop Server…") { quitAndStop() }
                    }
                }
                .settingAnchor("about.quit")
                Text("Quitting leaves the server running, so panes, agents and paired devices carry on.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            } header: {
                Text("Quitting")
            }

            Section {
                LabeledContent("Third-party software and its licences") {
                    Button("Acknowledgements…") { AcknowledgementsUI.shared.show() }
                }
                .settingAnchor("about.acknowledgements")
                Text("Hammock is open source (MIT) and built on Ghostty, Chromium, CodeMirror, Sparkle and more.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            } header: {
                Text("Acknowledgements")
            }

            Section {
                LabeledContent("Uninstall Hammock") {
                    Button("Uninstall…", role: .destructive) { uninstall() }
                        .disabled(uninstalling)
                }
                .settingAnchor("about.uninstall")
                Text("Stops everything Hammock runs, removes its hooks and hmk command, and moves it to the Bin. You can also remove your settings and data.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                if let error {
                    Text(error).font(.callout).foregroundStyle(.red)
                }
            } header: {
                Text("Uninstall")
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }

    // MARK: Quitting

    /// Quit and Stop Server, asked first as the menu bar item asks.
    private func quitAndStop() {
        let a = NSAlert()
        a.messageText = "Quit \(appName) and stop the server?"
        a.informativeText = "Unless you end them, your panes keep running and come back when you next open \(appName). Until then, your iPhone and other Macs can't reach this Mac, and agents don't notify you."
        a.addButton(withTitle: "Quit and Stop")
        a.addButton(withTitle: "Cancel")
        a.buttons.first?.hasDestructiveAction = true
        // Off each time: ending them can't be undone.
        a.showsSuppressionButton = true
        a.suppressionButton?.title = "Also end what's running in your panes"
        a.suppressionButton?.state = .off
        guard a.runModal() == .alertFirstButtonReturn else { return }
        let endPanes = a.suppressionButton?.state == .on
        model.shutdownDaemon(endPanes: endPanes)
        quitMenuBarItem()
        // Once the shutdown's been written to the socket.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { NSApp.terminate(nil) }
    }

    /// The menu bar item goes too: with the server stopped it has nothing to show.
    private func quitMenuBarItem() {
        NSRunningApplication.runningApplications(withBundleIdentifier: MenuHelper.bundleID).forEach { $0.terminate() }
    }

    // MARK: Uninstalling

    /// Where this copy keeps its state: the server's folder, its config
    /// (a β's is in its state folder), caches and preferences.
    private var dataFolders: [URL] {
        let fm = FileManager.default
        let state = URL(fileURLWithPath: DaemonConnection.defaultSocketPath).deletingLastPathComponent().deletingLastPathComponent()
        var folders = [state, AppConfig.shared.dir]
        if let id = Bundle.main.bundleIdentifier, let caches = fm.urls(for: .cachesDirectory, in: .userDomainMask).first {
            folders.append(caches.appendingPathComponent(id))
        }
        var seen = Set<String>()
        return folders.filter { fm.fileExists(atPath: $0.path) && seen.insert($0.standardizedFileURL.path).inserted }
    }

    private func uninstall() {
        let folders = dataFolders
        let a = NSAlert()
        a.messageText = "Uninstall \(appName)?"
        a.informativeText = "The server stops, and with it everything running in your panes. \(appName)'s login item, the hmk command and agents' Hammock hooks and tools are removed, and \(appName) moves to the Bin.\n\nYour Hammock Key stays: delete it in Settings ▸ SSH first if you want it gone."
        a.addButton(withTitle: "Uninstall")
        a.addButton(withTitle: "Cancel")
        a.buttons.first?.hasDestructiveAction = true
        a.showsSuppressionButton = true
        a.suppressionButton?.title = "Also move your settings and data to the Bin"
        a.suppressionButton?.state = .off
        if !folders.isEmpty {
            let list = NSTextField(wrappingLabelWithString: folders.map { ($0.path as NSString).abbreviatingWithTildeInPath }.joined(separator: "\n"))
            list.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            list.textColor = .secondaryLabelColor
            list.frame.size.width = 300
            list.sizeToFit()
            a.accessoryView = list
        }
        guard a.runModal() == .alertFirstButtonReturn else { return }
        let removeData = a.suppressionButton?.state == .on
        uninstalling = true
        Task { await runUninstall(removeData: removeData, folders: folders) }
    }

    private func runUninstall(removeData: Bool, folders: [URL]) async {
        // Agents' hooks and tools, with the bundled hmk, before the app goes.
        let hmk = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/hmk")
        if FileManager.default.isExecutableFile(atPath: hmk.path) {
            await Task.detached {
                let p = Process()
                p.executableURL = hmk
                p.arguments = ["hooks", "install", "--uninstall"]
                p.standardOutput = FileHandle.nullDevice
                p.standardError = FileHandle.nullDevice
                try? p.run()
                p.waitUntilExit()
            }.value
        }
        CommandLineTool.uninstall()
        try? DaemonService.setEnabled(false)
        model.shutdownDaemon(endPanes: true)
        quitMenuBarItem()
        // The server gone before its folder is.
        try? await Task.sleep(for: .seconds(1.5))

        var failed: [String] = []
        if removeData {
            for folder in folders {
                do { try FileManager.default.trashItem(at: folder, resultingItemURL: nil) } catch { failed.append(folder.path) }
            }
            if let id = Bundle.main.bundleIdentifier { UserDefaults.standard.removePersistentDomain(forName: id) }
        }
        do {
            try FileManager.default.trashItem(at: Bundle.main.bundleURL, resultingItemURL: nil)
        } catch {
            failed.append(Bundle.main.bundlePath)
        }
        if !failed.isEmpty {
            error = "Couldn't move to the Bin: " + failed.map { ($0 as NSString).abbreviatingWithTildeInPath }.joined(separator: ", ") + ". Move them yourself to finish."
            uninstalling = false
            return
        }
        NSApp.terminate(nil)
    }
}
