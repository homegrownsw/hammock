import Foundation
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// A simulator's or emulator's clipboard, both ways, as it is: sending puts
/// this device's clipboard text there without pasting it, and copying brings
/// the device's back without pressing ⌘C on it. Errors and results are told
/// in a toast.
extension AppModel {
    /// Whether a simulator pane's device has a clipboard to use: not an
    /// Apple TV (tvOS apps can't paste).
    public func simulatorHasClipboard(_ paneID: String) -> Bool { !isAppleTV(paneID) }

    /// Puts this device's clipboard text on a simulator pane's device.
    public func sendClipboardToSimulator(_ paneID: String) {
        guard mayDriveClipboard(paneID) else { return }
        guard let text = Self.clipboardText(), !text.isEmpty else {
            showToast("Your clipboard has no text to send")
            return
        }
        Task {
            lastError = nil
            if await simulatorCall(paneID, "set_clipboard", ["text": text]) != nil {
                showToast("Sent to \(deviceTitle(paneID))’s clipboard")
            } else {
                showToast("Couldn’t send the clipboard" + (lastError.map { ": \($0)" } ?? ""))
            }
        }
    }

    /// Copies a simulator pane's device clipboard onto this device's.
    public func copySimulatorClipboard(_ paneID: String) {
        guard mayDriveClipboard(paneID) else { return }
        Task {
            lastError = nil
            guard let r = await simulatorCall(paneID, "get_clipboard") else {
                showToast("Couldn’t read the clipboard" + (lastError.map { ": \($0)" } ?? ""))
                return
            }
            guard let text = r.json["text"] as? String, !text.isEmpty else {
                showToast("The clipboard on \(deviceTitle(paneID)) is empty")
                return
            }
            copy(text)
            showToast("Copied \(deviceTitle(paneID))’s clipboard")
        }
    }

    /// In a View-only shared Group, the host would refuse it: say so.
    private func mayDriveClipboard(_ paneID: String) -> Bool {
        guard group(ofPane: paneID)?.canDrive != false else {
            showToast("You can only view this simulator")
            return false
        }
        return true
    }

    /// The device's name, mid-sentence.
    private func deviceTitle(_ paneID: String) -> String {
        let t = panes[paneID]?.title ?? ""
        return t.isEmpty ? "the device" : t
    }

    private static func clipboardText() -> String? {
        #if canImport(UIKit)
        UIPasteboard.general.string
        #else
        NSPasteboard.general.string(forType: .string)
        #endif
    }
}
