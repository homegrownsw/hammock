import Foundation
import Testing
@testable import HammockKit

@Suite struct AgentLinkTests {
    @Test func webAndMailOpenDirectly() {
        for s in ["https://example.com", "http://x", "mailto:a@b.c", "HTTPS://EXAMPLE.COM"] {
            #expect(AgentLink.opensDirectly(URL(string: s)!), "\(s)")
        }
        for s in ["file:///tmp/x", "vscode://file/x", "x-apple.systempreferences:foo", "javascript:alert(1)"] {
            #expect(!AgentLink.opensDirectly(URL(string: s)!), "\(s)")
        }
    }

    @Test func terminalLinksOnlyOpenWebAndMailDirectly() {
        #expect(AgentLink.opening(URL(string: "https://example.com/docs")!) == .web)
        #expect(AgentLink.opening(URL(string: "HTTP://example.com")!) == .web)
        #expect(AgentLink.opening(URL(string: "mailto:a@b.c")!) == .direct)
        #expect(AgentLink.opening(URL(string: "file:///tmp/evil.command")!) == .file("/tmp/evil.command"))
        #expect(AgentLink.opening(URL(string: "file://localhost/tmp/a%20b.txt")!) == .file("/tmp/a b.txt"))
        for s in ["smb://attacker.example/share", "vscode://file/x",
                  "x-apple.systempreferences:foo", "afp://host/vol", "javascript:alert(1)"] {
            #expect(AgentLink.opening(URL(string: s)!) == .ask, "\(s)")
        }
        // A file on another computer is never opened, nor asked about.
        #expect(AgentLink.opening(URL(string: "file://attacker.example/share/x.command")!, localNames: ["Studio.local"]) == .elsewhere)
    }

    /// `ls --hyperlink`, `eza`, `rg` and `fd` put this machine's hostname in
    /// their links, with or without .local or a domain.
    @Test func fileLinksNamingThisMacAreLocal() {
        let names = ["Studio.local", "studio-2.local"]
        for s in ["file://Studio.local/tmp/a.txt", "file://studio/tmp/a.txt", "file://STUDIO.LOCAL/tmp/a.txt",
                  "file://studio.example.com/tmp/a.txt", "file://studio-2/tmp/a.txt", "file://localhost/tmp/a.txt",
                  "file:///tmp/a.txt"] {
            #expect(AgentLink.opening(URL(string: s)!, localNames: names) == .file("/tmp/a.txt"), "\(s)")
        }
        for s in ["file://studio2.local/tmp/a.txt", "file://other-mac/tmp/a.txt", "file://100.64.0.2/tmp/a.txt",
                  "file://studio.local"] {
            #expect(AgentLink.opening(URL(string: s)!, localNames: names) == .elsewhere, "\(s)")
        }
        // No names known (the iPhone): only empty and localhost.
        #expect(AgentLink.opening(URL(string: "file://studio/tmp/a.txt")!, localNames: []) == .elsewhere)
        #expect(AgentLink.opening(URL(string: "file://localhost/tmp/a.txt")!, localNames: []) == .file("/tmp/a.txt"))
    }

    /// In a Group on another computer, a file link with no host, or naming
    /// that computer, is its file; one naming any other (this Mac too) isn't.
    @Test func fileLinksInAGroupOnAnotherComputerAreItsFiles() {
        let host = ["Sam’s MacBook Pro", "Build Box"]
        for s in ["file:///tmp/a.txt", "file://localhost/tmp/a.txt", "file://Sams-MacBook-Pro.local/tmp/a.txt",
                  "file://sams-macbook-pro/tmp/a.txt", "file://build-box.example.com/tmp/a.txt"] {
            #expect(AgentLink.opening(URL(string: s)!, onHostNamed: host) == .file("/tmp/a.txt"), "\(s)")
        }
        for s in ["file://other-mac/tmp/a.txt", "file://100.64.0.2/tmp/a.txt", "file://sams/tmp/a.txt"] {
            #expect(AgentLink.opening(URL(string: s)!, onHostNamed: host) == .elsewhere, "\(s)")
        }
        // This Mac's own name isn't the host's.
        for name in AgentLink.localHostNames() {
            guard let url = URL(string: "file://\(name)/tmp/a.txt") else { continue }
            #expect(AgentLink.opening(url, onHostNamed: host) == .elsewhere, "\(name)")
        }
        // Not file links: as anywhere.
        #expect(AgentLink.opening(URL(string: "https://example.com")!, onHostNamed: host) == .web)
        #expect(AgentLink.opening(URL(string: "vscode://file/x")!, onHostNamed: host) == .ask)
    }

    @Test func hostNameForms() {
        #expect(Set(AgentLink.hostNameForms("Sam's MacBook Pro")) == ["Sam's MacBook Pro", "Sams-MacBook-Pro"])
        #expect(AgentLink.hostNameForms("Studio") == ["Studio"])
        #expect(AgentLink.hostNameForms("  ").isEmpty)
    }

    #if os(macOS)
    /// This Mac's own hostname, as `ls --hyperlink` writes it, is local.
    @Test func thisMacsHostnameIsLocal() {
        let names = AgentLink.localHostNames()
        #expect(!names.isEmpty)
        for name in names {
            guard let url = URL(string: "file://\(name)/tmp/a.txt") else { continue }
            #expect(AgentLink.opening(url) == .file("/tmp/a.txt"), "\(name)")
        }
    }
    #endif

    @Test func destinationsLeaveOutLinksThatShowThemselves() {
        let text = Markdown.inline("See [the docs](https://example.com/docs), /tmp/notes.md and [this](vscode://open)")
        #expect(AgentLink.destinations(text) == "https://example.com/docs\nvscode://open")
    }

    @Test func linkedTextTagsOnlyTextWithLinks() {
        #expect(LinkedText.tagged(Markdown.inline("See [the docs](https://example.com) and /tmp/notes.md")) != nil)
        #expect(LinkedText.tagged(Markdown.inline("No links, just `code`")) == nil)
    }
}

@Test func modelDisplayNames() {
    #expect(AgentStatusBar.displayName("claude-opus-5-5") == "Opus 5.5")
    #expect(AgentStatusBar.displayName("claude-sonnet-4-5-20250929") == "Sonnet 4.5")
    #expect(AgentStatusBar.displayName("gpt-6-luna") == "GPT-6 Luna")
    #expect(AgentStatusBar.displayName("gpt-5.2-codex") == "GPT-5.2 Codex")
    #expect(AgentStatusBar.displayName("claude-haiku-5-5[1m]") == "Haiku 5.5")
    #expect(AgentStatusBar.displayName("gpt-5") == "GPT-5")
    #expect(AgentStatusBar.displayName("Opus 5.5 (1M context)") == "Opus 5.5 (1M context)")
}

@Test func effortNames() {
    #expect(AgentStatusBar.effortName("xhigh") == "Extra high")
    #expect(AgentStatusBar.effortName("max") == "Max")
    #expect(AgentStatusBar.effortName("turbo") == "Turbo")
}
