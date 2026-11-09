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
}
