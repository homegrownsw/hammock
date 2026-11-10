import Foundation
import UniformTypeIdentifiers

/// Whether a file a link points to (an agent's, or one a terminal printed)
/// may be opened in its default app straight away, or only shown in the
/// Finder for you to open yourself. Opening runs whatever the default app
/// does with it, so this is an allow-list: a plain document, whose app
/// isn't one that runs, installs or imports what it opens. Anything else
/// (apps, scripts their app would run, installers, aliases, internet
/// locations, packages, files with an exec bit, unknown types) is shown.
public enum FileOpening {
    /// What's known about the file, its aliases and symlinks resolved.
    public struct Facts: Equatable, Sendable {
        public var type: UTType?
        public var isDirectory = false
        public var isPackage = false
        public var isExecutable = false
        /// Still an alias (or symlink) after resolving: one that's broken.
        public var isAlias = false
        /// The bundle id of the app that would open it.
        public var defaultApp: String?

        public init(type: UTType?, isDirectory: Bool = false, isPackage: Bool = false, isExecutable: Bool = false, isAlias: Bool = false, defaultApp: String? = nil) {
            self.type = type
            self.isDirectory = isDirectory
            self.isPackage = isPackage
            self.isExecutable = isExecutable
            self.isAlias = isAlias
            self.defaultApp = defaultApp
        }
    }

    /// Apps that run, install or import what they open: terminals (a
    /// .command, a .terminal file's command), script runners and editors
    /// that run on open, installers, and what adds profiles, certificates,
    /// shortcuts or settings panes.
    public static let runners: Set<String> = [
        "com.apple.Terminal", "com.googlecode.iterm2", "com.mitchellh.ghostty", "dev.warp.Warp-Stable", "dev.warp.Warp",
        "net.kovidgoyal.kitty", "org.alacritty", "io.alacritty", "com.github.wez.wezterm", "co.zeit.hyper",
        "com.homegrownsw.hammock", "com.homegrownsw.hammock.beta",
        "org.python.PythonLauncher", "com.apple.ScriptEditor2", "com.apple.Automator", "com.apple.AutomatorInstaller",
        "com.apple.automator.runner", "com.apple.installer", "com.apple.JavaLauncher", "com.apple.JarLauncher",
        "com.apple.shortcuts", "com.apple.systempreferences", "com.apple.mcx.ProfileHelper", "com.apple.keychainaccess",
        "com.apple.DiskImageMounter", "com.apple.ScriptMenuApp", "com.apple.ArchiveUtility.installer",
    ]

    /// Extensions never opened directly, whatever app has them: they run,
    /// install, mount or redirect by what they are.
    static let runnable: Set<String> = [
        "app", "command", "tool", "terminal", "workflow", "action", "shortcut", "sh", "zsh", "bash", "csh", "ksh", "fish",
        "pkg", "mpkg", "jar", "prefpane", "saver", "qlgenerator", "mdimporter", "plugin", "bundle", "appex",
        "kext", "scpt", "scptd", "webloc", "inetloc", "fileloc", "url", "mobileconfig", "dmg", "dylib", "so", "pyc", "pyo",
    ]

    /// Scripts that open safely only in an editor, not their interpreter
    /// (a .py's default app may be Python Launcher, which runs it).
    static let scripts: Set<String> = ["py", "pyw", "rb", "pl", "pm", "php", "lua", "tcl", "js", "mjs", "cjs", "applescript", "ps1"]

    /// Types whose opening runs, installs or redirects.
    static let refusedTypes: [UTType] = [
        .executable, .application, .applicationBundle, .bundle, .package, .aliasFile, .symbolicLink,
        .internetLocation, .internetShortcut, .diskImage,
    ]

    /// The path a link gave may be used at all: no control characters (a
    /// NUL cuts a path short where the file system reads it; others hide
    /// what's shown).
    public static func acceptable(_ path: String) -> Bool {
        !path.isEmpty && !path.unicodeScalars.contains { $0.properties.generalCategory == .control }
    }

    /// Opened in its default app (true), or only shown in the Finder. A
    /// script opens only when its default app is a named one that isn't a
    /// runner (say a .py in VS Code), never as a type the system doesn't
    /// know (a .pyw before Python is installed).
    public static func opensDirectly(_ f: Facts, pathExtension ext: String) -> Bool {
        let e = ext.lowercased()
        if f.isAlias || f.isPackage || runnable.contains(e) { return false }
        if let app = f.defaultApp, runners.contains(app) { return false }
        guard let type = f.type, !refusedTypes.contains(where: { type.conforms(to: $0) }) else { return false }
        // A plain folder opens in the Finder.
        if f.isDirectory { return type.conforms(to: .directory) }
        if f.isExecutable { return false }
        guard type.conforms(to: .content) || type.conforms(to: .data) else { return false }
        if type.conforms(to: .script) || scripts.contains(e) {
            return f.defaultApp != nil && !type.isDynamic
        }
        return true
    }
}
