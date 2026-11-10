import Foundation
import Testing
import UniformTypeIdentifiers
@testable import HammockKit

@Suite struct FileOpeningTests {
    private func opens(_ ext: String, app: String?, executable: Bool = false, directory: Bool = false, package: Bool = false, alias: Bool = false) -> Bool {
        let type: UTType? = directory ? (package ? UTType(filenameExtension: ext, conformingTo: .package) : .folder) : UTType(filenameExtension: ext)
        return FileOpening.opensDirectly(.init(type: type, isDirectory: directory, isPackage: package, isExecutable: executable, isAlias: alias, defaultApp: app), pathExtension: ext)
    }

    @Test func documentsOpen() {
        #expect(opens("txt", app: "com.apple.TextEdit"))
        #expect(opens("pdf", app: "com.apple.Preview"))
        #expect(opens("png", app: "com.apple.Preview"))
        #expect(opens("md", app: "com.apple.dt.Xcode"))
        #expect(opens("json", app: "com.microsoft.VSCode"))
        #expect(opens("zip", app: "com.apple.archiveutility"))
        #expect(opens("", app: "com.apple.finder", directory: true))
    }

    @Test func scriptsOpenOnlyInAnEditor() {
        #expect(opens("py", app: "com.microsoft.VSCode"))
        #expect(opens("rb", app: "com.panic.Nova"))
        #expect(!opens("py", app: "org.python.PythonLauncher"))
        #expect(!opens("py", app: nil))
        // A type the system doesn't know yet (no Python installed).
        #expect(!opens("pyw", app: "com.microsoft.VSCode"))
        #expect(!opens("scpt", app: "com.apple.ScriptEditor2"))
        #expect(!opens("applescript", app: "com.apple.ScriptEditor2"))
        // Shell scripts and .command run in a terminal: never opened.
        #expect(!opens("sh", app: "com.panic.Nova"))
        #expect(!opens("command", app: "com.panic.Nova"))
        #expect(!opens("js", app: "com.panic.Nova")) // conforms to executable
    }

    @Test func whatRunsInstallsOrRedirectsIsShown() {
        #expect(!opens("app", app: nil, directory: true, package: true))
        #expect(!opens("pkg", app: "com.apple.installer"))
        #expect(!opens("jar", app: "com.apple.JavaLauncher"))
        #expect(!opens("webloc", app: "com.apple.Safari"))
        #expect(!opens("fileloc", app: nil))
        #expect(!opens("mobileconfig", app: "com.apple.mcx.ProfileHelper"))
        #expect(!opens("cer", app: "com.apple.keychainaccess"))
        #expect(!opens("shortcut", app: "com.apple.shortcuts"))
        #expect(!opens("terminal", app: "com.apple.Terminal"))
        #expect(!opens("dmg", app: "com.apple.DiskImageMounter"))
        #expect(!opens("dylib", app: nil))
        // Any document whose app is a terminal, and anything with an exec bit.
        #expect(!opens("txt", app: "com.mitchellh.ghostty"))
        #expect(!opens("txt", app: "com.apple.TextEdit", executable: true))
        // An alias that couldn't be resolved, and a file of no known type.
        #expect(!opens("txt", app: "com.apple.TextEdit", alias: true))
        #expect(!FileOpening.opensDirectly(.init(type: nil, defaultApp: "com.apple.TextEdit"), pathExtension: "txt"))
        #expect(!FileOpening.opensDirectly(.init(type: .aliasFile, defaultApp: "com.apple.finder"), pathExtension: ""))
    }

    @Test func controlCharactersRefuseThePath() {
        #expect(FileOpening.acceptable("/Users/me/notes.txt"))
        #expect(FileOpening.acceptable("/Users/me/Ünïcode file.txt"))
        #expect(!FileOpening.acceptable("/Users/me/notes.txt\u{0}.app"))
        #expect(!FileOpening.acceptable("/Users/me/a\nb"))
        #expect(!FileOpening.acceptable(""))
    }
}
