import AppKit
import ScreenCaptureKit

/// Dev aid: HMK_DEBUG_RECORD=/path/movie.mov records the main window, as
/// it's really drawn (glass, the sidebar, live layers), to a movie, with the
/// app's own Screen Recording permission. It starts HMK_DEBUG_DELAY seconds
/// after launch (default 4), with the window's content HMK_DEBUG_RECORD_SIZE
/// points (default 1280x800), and stops once `<movie>.stop` exists, or after
/// HMK_DEBUG_RECORD_SECONDS (default 120); `<movie>.done` appears when the
/// movie is written. ScreenCaptureKit records the window even under others.
/// For the README's demo (`scripts/readme-demo`). The recorded window draws
/// as the active one throughout, even when another app takes the focus.
@MainActor enum DebugRecord {
    private static var recorder: Recorder?
    /// The window being recorded, which always draws as key and main.
    nonisolated(unsafe) fileprivate static var activeWindow: NSWindow?

    /// Makes `window` look active whatever else happens on the Mac: its
    /// key and main state, and the app's, read as on.
    private static func keepActive(_ window: NSWindow) {
        activeWindow = window
        let swaps: [(AnyClass, Selector, Selector)] = [
            (NSWindow.self, #selector(getter: NSWindow.isKeyWindow), #selector(getter: NSWindow.hmkRecordIsKeyWindow)),
            (NSWindow.self, #selector(getter: NSWindow.isMainWindow), #selector(getter: NSWindow.hmkRecordIsMainWindow)),
            (NSApplication.self, #selector(getter: NSApplication.isActive), #selector(getter: NSApplication.hmkRecordIsActive)),
        ]
        for (cls, original, replacement) in swaps {
            guard let a = class_getInstanceMethod(cls, original), let b = class_getInstanceMethod(cls, replacement) else { continue }
            method_exchangeImplementations(a, b)
        }
        let nc = NotificationCenter.default
        for name in [NSWindow.didResignKeyNotification, NSWindow.didResignMainNotification] {
            nc.addObserver(forName: name, object: window, queue: .main) { _ in
                MainActor.assumeIsolated { refresh(window) }
            }
        }
        nc.addObserver(forName: NSApplication.didResignActiveNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { refresh(window) }
        }
    }

    /// Tells the window's views it's key again, after AppKit said otherwise.
    private static func refresh(_ window: NSWindow) {
        DispatchQueue.main.async {
            let nc = NotificationCenter.default
            nc.post(name: NSApplication.didBecomeActiveNotification, object: NSApp)
            nc.post(name: NSWindow.didBecomeMainNotification, object: window)
            nc.post(name: NSWindow.didBecomeKeyNotification, object: window)
            window.contentView?.superview?.needsDisplay = true
            window.contentView?.setNeedsDisplay(window.contentView?.bounds ?? .zero)
        }
    }

    static func scheduleIfRequested() {
        let env = ProcessInfo.processInfo.environment
        guard let path = env["HMK_DEBUG_RECORD"], !path.isEmpty else { return }
        let delay = Double(env["HMK_DEBUG_DELAY"] ?? "") ?? 4
        let limit = Double(env["HMK_DEBUG_RECORD_SECONDS"] ?? "") ?? 120
        let size = (env["HMK_DEBUG_RECORD_SIZE"] ?? "1280x800").split(separator: "x").compactMap { Double($0) }
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
            guard let window = NSApp.windows.first(where: { $0.isVisible && $0.contentView != nil }) else {
                FileHandle.standardError.write(Data("HammockRecord: no window\n".utf8))
                return
            }
            if size.count == 2 { window.setContentSize(NSSize(width: size[0], height: size[1])) }
            keepActive(window)
            Task { @MainActor in
                let r = Recorder(url: URL(fileURLWithPath: path))
                recorder = r
                await r.record(window, limit: limit)
            }
        }
    }

    private final class Recorder: NSObject, SCRecordingOutputDelegate, SCStreamDelegate {
        let url: URL
        private var finished: CheckedContinuation<Void, Never>?

        init(url: URL) { self.url = url }

        @MainActor func record(_ window: NSWindow, limit: Double) async {
            let fm = FileManager.default
            let stop = url.path + ".stop", done = url.path + ".done"
            try? fm.removeItem(at: url)
            try? fm.removeItem(atPath: stop)
            try? fm.removeItem(atPath: done)
            let w: SCWindow
            do {
                let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
                guard let found = content.windows.first(where: { $0.windowID == CGWindowID(window.windowNumber) }) else {
                    FileHandle.standardError.write(Data("HammockRecord: window \(window.windowNumber) isn't among the \(content.windows.count) shareable\n".utf8))
                    return
                }
                w = found
            } catch {
                FileHandle.standardError.write(Data("HammockRecord: \(error) (Screen Recording permission?)\n".utf8))
                return
            }
            let config = SCStreamConfiguration()
            config.width = Int(w.frame.width * window.backingScaleFactor)
            config.height = Int(w.frame.height * window.backingScaleFactor)
            config.minimumFrameInterval = CMTime(value: 1, timescale: 30)
            config.showsCursor = false
            config.capturesAudio = false
            let output = SCRecordingOutputConfiguration()
            output.outputURL = url
            output.outputFileType = .mov
            output.videoCodecType = .hevc
            let stream = SCStream(filter: SCContentFilter(desktopIndependentWindow: w), configuration: config, delegate: self)
            do {
                try stream.addRecordingOutput(SCRecordingOutput(configuration: output, delegate: self))
                try await stream.startCapture()
            } catch {
                FileHandle.standardError.write(Data("HammockRecord: \(error)\n".utf8))
                return
            }
            FileHandle.standardError.write(Data("HammockRecord: recording \(url.path)\n".utf8))
            let start = Date()
            while !fm.fileExists(atPath: stop), Date().timeIntervalSince(start) < limit {
                try? await Task.sleep(for: .milliseconds(100))
            }
            await withCheckedContinuation { c in
                finished = c
                Task { try? await stream.stopCapture() }
            }
            fm.createFile(atPath: done, contents: Data("done\n".utf8))
            FileHandle.standardError.write(Data("HammockRecord: done\n".utf8))
        }

        nonisolated func recordingOutputDidFinishRecording(_ recordingOutput: SCRecordingOutput) {
            Task { @MainActor in self.finish() }
        }

        nonisolated func recordingOutput(_ recordingOutput: SCRecordingOutput, didFailWithError error: any Error) {
            FileHandle.standardError.write(Data("HammockRecord: \(error)\n".utf8))
            Task { @MainActor in self.finish() }
        }

        @MainActor private func finish() {
            finished?.resume()
            finished = nil
        }
    }
}

// DebugRecord.keepActive swaps these in for isKeyWindow, isMainWindow and
// isActive: after the swap, calling them calls the originals.
extension NSWindow {
    @objc fileprivate var hmkRecordIsKeyWindow: Bool { self === DebugRecord.activeWindow || hmkRecordIsKeyWindow }
    @objc fileprivate var hmkRecordIsMainWindow: Bool { self === DebugRecord.activeWindow || hmkRecordIsMainWindow }
}

extension NSApplication {
    @objc fileprivate var hmkRecordIsActive: Bool { DebugRecord.activeWindow != nil || hmkRecordIsActive }
}
