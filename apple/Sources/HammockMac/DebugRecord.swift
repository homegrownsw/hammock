import AppKit
import ScreenCaptureKit

/// Dev aid: HMK_DEBUG_RECORD=/path/movie.mov records the main window, as
/// it's really drawn (glass, the sidebar, live layers), to a movie, with the
/// app's own Screen Recording permission. It starts HMK_DEBUG_DELAY seconds
/// after launch (default 4), with the window's content HMK_DEBUG_RECORD_SIZE
/// points (default 1280x800), and stops once `<movie>.stop` exists, or after
/// HMK_DEBUG_RECORD_SECONDS (default 120); `<movie>.done` appears when the
/// movie is written. ScreenCaptureKit records the window even under others.
/// For the README's demo (`scripts/readme-demo`).
@MainActor enum DebugRecord {
    private static var recorder: Recorder?

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
