import Foundation
import HammockKit

/// Starts the bundled hmkd when the app can't reach one. The daemon
/// runs in its own session so it outlives the app. (Onboarding will register
/// it as a login item via SMAppService instead.)
enum DaemonLauncher {
    private static var lastAttempt = Date.distantPast
    /// When launchd was last asked to start the login item. Its kickstart
    /// succeeds even when the job can't spawn (after an update, until
    /// macOS takes the new build's signature), so still unreachable after
    /// one, start the daemon directly.
    private static var kickstartedAt = Date.distantPast
    /// The login item was registered again this run (see launchIfNeeded).
    private static var repaired = false

    static func daemonPath() -> String? {
        let env = ProcessInfo.processInfo.environment
        if let p = env["HMK_DAEMON"], FileManager.default.isExecutableFile(atPath: p) { return p }
        let candidates = [
            Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/hmkd").path,
            Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("hmkd").path,
        ].compactMap { $0 }
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) }
    }

    static func launchIfNeeded() {
        guard Date().timeIntervalSince(lastAttempt) > 10, let path = daemonPath() else { return }
        lastAttempt = Date()
        // Asked launchd a moment ago and still no daemon: the login item may
        // point at a copy of the app that's gone (a moved or deleted build,
        // launchd saying "Could not find … Contents/MacOS/hmkd" forever).
        // Register it again from this copy, once per run; it starts at load.
        // Not from a copy that's run from outside Applications (AppMover).
        if DaemonService.isEnabled, !repaired, !AppMover.provisional, Date().timeIntervalSince(kickstartedAt) < 60 {
            repaired = true
            NSLog("Hammock: the daemon's login item didn't start it; registering it again from %@", Bundle.main.bundlePath)
            try? DaemonService.service.unregister()
            if (try? DaemonService.service.register()) != nil {
                kickstartedAt = Date()
                return
            }
        }
        // The login item only restarts after a crash, not a clean exit (hammock
        // stop, an update): start it through launchd so launchd keeps it.
        if DaemonService.isEnabled, Date().timeIntervalSince(kickstartedAt) > 60,
           let label = Bundle.main.object(forInfoDictionaryKey: "HammockAgentLabel") as? String {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/bin/launchctl")
            p.arguments = ["kickstart", "gui/\(getuid())/\(label)"]
            p.standardOutput = FileHandle.nullDevice
            p.standardError = FileHandle.nullDevice
            if (try? p.run()) != nil {
                p.waitUntilExit()
                if p.terminationStatus == 0 {
                    kickstartedAt = Date()
                    return
                }
            }
        }
        let dir = ((DaemonConnection.defaultSocketPath as NSString).deletingLastPathComponent as NSString).deletingLastPathComponent
        try? FileManager.default.createDirectory(atPath: dir + "/logs", withIntermediateDirectories: true)

        var attr: posix_spawnattr_t?
        posix_spawnattr_init(&attr)
        posix_spawnattr_setflags(&attr, Int16(POSIX_SPAWN_SETSID))
        var actions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&actions)
        let log = dir + "/logs/hmkd.log"
        posix_spawn_file_actions_addopen(&actions, 0, "/dev/null", O_RDONLY, 0)
        posix_spawn_file_actions_addopen(&actions, 1, log, O_WRONLY | O_CREAT | O_APPEND, 0o600)
        posix_spawn_file_actions_adddup2(&actions, 1, 2)

        let args = [path, "--dir", dir]
        var cargs = args.map { strdup($0) } + [nil]
        var pid: pid_t = 0
        let spawned = posix_spawn(&pid, path, &actions, &attr, &cargs, environ)
        cargs.forEach { free($0) }
        posix_spawn_file_actions_destroy(&actions)
        posix_spawnattr_destroy(&attr)
        // Reap it when it exits: unreaped, it stays a zombie child of the
        // app, which kill(pid, 0) still finds, so `hmk stop` and
        // DaemonUpdater would think it's still running.
        if spawned == 0, pid > 0 {
            Thread.detachNewThread {
                var status: Int32 = 0
                while waitpid(pid, &status, 0) == -1 && errno == EINTR {}
            }
        }
    }
}

/// Keeps the running daemon the one this app bundles: after an app update the
/// old daemon is still running, so it's asked to exit and the bundled one is
/// started by DaemonLauncher (through launchd for the login item). Panes survive.
@MainActor
enum DaemonUpdater {
    private static var attempted: String?

    static func check(running: String, model: AppModel) {
        guard let bundled = bundledVersion, !bundled.isEmpty, running != bundled, attempted != bundled else { return }
        attempted = bundled // once per bundled version, so a mismatch can't loop
        NSLog("Hammock: daemon %@ differs from bundled %@; restarting it", running, bundled)
        let old = daemonPID()
        model.shutdownDaemon()
        // Daemons older than ShutdownDaemon ignore it: stop that same process
        // the ordinary way if it's still there (never a replacement).
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
            if let old, kill(old, 0) == 0 { kill(old, SIGTERM) }
        }
    }

    /// The running daemon's pid: written to its lock file by current daemons,
    /// else whoever holds the lock file open.
    private static func daemonPID() -> pid_t? {
        let lock = ((DaemonConnection.defaultSocketPath as NSString).deletingLastPathComponent as NSString).appendingPathComponent("daemon.lock")
        if let s = try? String(contentsOfFile: lock, encoding: .utf8), let pid = pid_t(s.trimmingCharacters(in: .whitespacesAndNewlines)) {
            return pid
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/sbin/lsof")
        p.arguments = ["-t", lock]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return nil }
        p.waitUntilExit()
        return String(decoding: out.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            .split(separator: "\n").first.flatMap { pid_t($0) }
    }

    /// `hmkd --version` from the app bundle.
    static let bundledVersion: String? = {
        guard let path = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("hmkd").path,
              FileManager.default.isExecutableFile(atPath: path) else { return nil }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = ["--version"]
        let out = Pipe()
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return nil }
        p.waitUntilExit()
        return String(decoding: out.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }()
}
