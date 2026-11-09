import Foundation
import Testing
@testable import HammockKit

@Suite struct MarkdownFenceTests {
    @Test func longerFenceKeepsInnerFences() {
        let src = """
        ````markdown
        # Plan

        ```swift
        let x = 1
        ```

        Done.
        ````
        After.
        """
        let blocks = Markdown.parse(src)
        #expect(blocks.count == 2)
        #expect(blocks.first == .code(language: "markdown", text: "# Plan\n\n```swift\nlet x = 1\n```\n\nDone."))
        #expect(blocks.last == .paragraph("After."))
    }

    @Test func tildeFenceIgnoresBackticks() {
        let blocks = Markdown.parse("~~~md\n```\ncode\n```\n~~~")
        #expect(blocks == [.code(language: "md", text: "```\ncode\n```")])
    }

    @Test func fenceWithLanguageDoesNotClose() {
        let blocks = Markdown.parse("```\nfirst\n```swift\nsecond\n```")
        #expect(blocks == [.code(language: "", text: "first\n```swift\nsecond")])
    }

    @Test func plainFenceStillWorks() {
        let blocks = Markdown.parse("Hi\n\n```go\nfmt.Println()\n```\nBye")
        #expect(blocks == [.paragraph("Hi"), .code(language: "go", text: "fmt.Println()"), .paragraph("Bye")])
    }

    @Test func inlineTripleBackticksAreNotAFence() {
        #expect(Markdown.parse("```a` b```") == [.paragraph("```a` b```")])
    }
}
