import AppKit
import Foundation
import HammockKit

// e in a diff pane (and a file's open button): the file at the cursor, in
// the editor the diff-editor setting names; once you're done with it (its
// pane closes, its window's file is closed), the diff refreshes, keeping
// your place, and has the keys again. As revdiff's e.
//
// The setting: "" a Hammock editor pane; "$EDITOR" the shell's editor
// ($VISUAL, else $EDITOR), in a terminal pane beside the diff; an editor's
// name (code, zed, nvim…) or a command with {file} and {line}. Editors with
// windows of their own run without a pane, waited on.

@MainActor
enum DiffEditing {
    static func open(_ model: AppModel, diffPane: String, path: String, line: Int) {
        guard let p = model.panes[diffPane], p.isDiff else { return }
        let full = (p.diff.repo as NSString).appendingPathComponent(path)
        let setting = Prefs.shared.string(.diffEditor).trimmingCharacters(in: .whitespaces)
        // A shared Group's files are on its host: there, it's Hammock's own editor.
        let remote = model.group(ofPane: diffPane)?.isRemote == true
        if setting.isEmpty || remote {
            guard HammockTool.editor.on else {
                // Turned off in Settings ▸ Tools: the file's own app, as a
                // terminal's file links open (not waited on).
                if !remote { FileLink.open(full) }
                return
            }
            Task {
                do {
                    let id = try await model.openEditor(full, groupID: nil, splitPane: diffPane, direction: .horizontal, line: line)
                    if let id { await whenGone(model, id) { done(model, diffPane) } }
                } catch {
                    model.diffSession(diffPane)?.error = error.localizedDescription
                }
            }
            return
        }
        Task {
            let command = await resolve(setting)
            guard !command.isEmpty else {
                model.diffSession(diffPane)?.error = "No editor set: $VISUAL and $EDITOR are both empty in your shell"
                return
            }
            run(model, diffPane: diffPane, command: command, file: full, line: line, cwd: p.diff.repo)
        }
    }

    /// The setting as a command: $EDITOR is the shell's ($VISUAL first).
    static func resolve(_ setting: String) async -> String {
        guard setting == "$EDITOR" || setting == "$VISUAL" else { return setting }
        return await withCheckedContinuation { done in
            let p = Process()
            p.executableURL = URL(fileURLWithPath: loginShell)
            p.arguments = ["-lc", "printf %s \"${VISUAL:-$EDITOR}\""]
            let out = Pipe()
            p.standardOutput = out
            p.standardError = FileHandle.nullDevice
            p.terminationHandler = { _ in
                let s = String(data: out.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
                done.resume(returning: s.trimmingCharacters(in: .whitespacesAndNewlines))
            }
            do { try p.run() } catch { done.resume(returning: "") }
        }
    }

    static func run(_ model: AppModel, diffPane: String, command: String, file: String, line: Int, cwd: String) {
        let (cmd, isWindowed) = DiffEditorCommand.commandLine(command, file: file, line: line)
        if isWindowed {
            // Its own window: waited on in the background, until the file's closed.
            let p = Process()
            p.executableURL = URL(fileURLWithPath: loginShell)
            p.arguments = ["-lc", cmd]
            p.currentDirectoryURL = URL(fileURLWithPath: cwd)
            p.standardOutput = FileHandle.nullDevice
            p.standardError = FileHandle.nullDevice
            p.terminationHandler = { proc in
                let status = proc.terminationStatus
                Task { @MainActor in
                    if status == 127 {
                        model.diffSession(diffPane)?.error = "Couldn't find the editor: \(command)"
                    } else if status != 0 {
                        model.diffSession(diffPane)?.error = "The editor ended with an error (\(status))"
                    }
                    done(model, diffPane)
                }
            }
            do { try p.run() } catch {
                model.diffSession(diffPane)?.error = "Couldn't start the editor: \(error.localizedDescription)"
            }
            return
        }
        // In a terminal pane beside the diff, closing when you quit it.
        Task {
            guard let id = await model.split(diffPane, running: [loginShell, "-lc", cmd], cwd: cwd) else { return }
            await whenGone(model, id) { done(model, diffPane) }
        }
    }

    /// Waits until pane id has closed, or its command has ended.
    static func whenGone(_ model: AppModel, _ id: String, then: () -> Void) async {
        while let p = model.panes[id], !p.exited {
            try? await Task.sleep(for: .milliseconds(300))
        }
        then()
    }

    /// Done editing: the diff afresh, where you were, with the keys again.
    static func done(_ model: AppModel, _ diffPane: String) {
        guard model.panes[diffPane]?.isDiff == true else { return }
        model.diffSession(diffPane)?.load(refresh: true)
        model.focus(diffPane)
    }

    static var loginShell: String {
        if let pw = getpwuid(getuid()), let sh = pw.pointee.pw_shell { return String(cString: sh) }
        return "/bin/zsh"
    }

    /// The setting's name for the editor, for the page's tooltips.
    static var editorName: String {
        let s = Prefs.shared.string(.diffEditor).trimmingCharacters(in: .whitespaces)
        if s.isEmpty { return "" }
        let names = ["code": "VS Code", "cursor": "Cursor", "windsurf": "Windsurf", "zed": "Zed", "subl": "Sublime Text",
                     "mate": "TextMate", "bbedit": "BBEdit", "$EDITOR": "Your Editor", "$VISUAL": "Your Editor"]
        let first = (String(s.split(separator: " ").first ?? "") as NSString).lastPathComponent
        return names[s] ?? names[first] ?? "Your Editor"
    }
}
