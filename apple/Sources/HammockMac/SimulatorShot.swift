import AppKit
import HammockKit

/// Screenshots of a simulator pane's screen, in the device's pixels: saved
/// where macOS saves screenshots, or copied.
@MainActor
enum SimulatorShot {
    /// Saves one where ⇧⌘3 would (the screencapture location, else the
    /// Desktop), named as Simulator.app names them.
    static func saveToFile(_ model: AppModel, _ paneID: String) {
        let device = model.panes[paneID]?.title ?? "Simulator"
        Task {
            guard let png = await shot(model, paneID) else { return }
            let folder = folder()
            let url = uniqueURL(in: folder, name: fileName(device: device))
            do {
                try png.write(to: url, options: .withoutOverwriting)
                model.showToast("Screenshot saved to \(FileManager.default.displayName(atPath: folder.path))")
            } catch {
                model.showToast("Couldn’t save the screenshot: \(error.localizedDescription)")
            }
        }
    }

    /// Copies one to the clipboard.
    static func copyToClipboard(_ model: AppModel, _ paneID: String) {
        Task {
            guard let png = await shot(model, paneID) else { return }
            let pb = NSPasteboard.general
            pb.clearContents()
            pb.setData(png, forType: .png)
            model.showToast("Screenshot copied")
        }
    }

    private static func shot(_ model: AppModel, _ paneID: String) async -> Data? {
        model.lastError = nil
        if let png = await model.simulatorScreenshot(paneID) { return png }
        model.showToast("Couldn’t take a screenshot" + (model.lastError.map { ": \($0)" } ?? ""))
        return nil
    }

    /// Where screenshots go: System Settings' choice (Screenshot.app ▸
    /// Options), else the Desktop.
    static func folder() -> URL {
        let fm = FileManager.default
        if let path = UserDefaults(suiteName: "com.apple.screencapture")?.string(forKey: "location") {
            let url = URL(fileURLWithPath: (path as NSString).expandingTildeInPath, isDirectory: true)
            var dir: ObjCBool = false
            if fm.fileExists(atPath: url.path, isDirectory: &dir), dir.boolValue { return url }
        }
        return fm.urls(for: .desktopDirectory, in: .userDomainMask).first ?? fm.homeDirectoryForCurrentUser
    }

    /// "Simulator Screenshot - iPhone 17 Pro - 2026-11-04 at 10.15.32.png"
    static func fileName(device: String, date: Date = .now) -> String {
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd 'at' HH.mm.ss"
        let name = device.replacingOccurrences(of: "/", with: "-").replacingOccurrences(of: ":", with: "-")
        return "Simulator Screenshot - \(name) - \(f.string(from: date)).png"
    }

    /// The name, or "… 2.png" and so on if it's taken (two in one second).
    private static func uniqueURL(in folder: URL, name: String) -> URL {
        let base = (name as NSString).deletingPathExtension
        var url = folder.appendingPathComponent(name)
        var n = 2
        while FileManager.default.fileExists(atPath: url.path) {
            url = folder.appendingPathComponent("\(base) \(n).png")
            n += 1
        }
        return url
    }
}
