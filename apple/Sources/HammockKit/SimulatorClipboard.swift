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
    /// Puts this device's clipboard text on a simulator pane's device.
    public func sendClipboardToSimulator(_ paneID: String) {
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
        Task {
            lastError = nil
            guard let r = await simulatorCall(paneID, "get_clipboard") else {
                showToast("Couldn’t read the clipboard" + (lastError.map { ": \($0)" } ?? ""))
                return
            }
            guard let text = r.json["text"] as? String, !text.isEmpty else {
                showToast("\(deviceTitle(paneID))’s clipboard is empty")
                return
            }
            copy(text)
            showToast("Copied \(deviceTitle(paneID))’s clipboard")
        }
    }

    private func deviceTitle(_ paneID: String) -> String {
        let t = panes[paneID]?.title ?? ""
        return t.isEmpty ? "The device" : t
    }

    private static func clipboardText() -> String? {
        #if canImport(UIKit)
        UIPasteboard.general.string
        #else
        NSPasteboard.general.string(forType: .string)
        #endif
    }
}
