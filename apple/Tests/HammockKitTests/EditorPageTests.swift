import Foundation
import Testing
@testable import HammockKit

@Suite struct EditorPageTests {
    private let page = URL(fileURLWithPath: "/Applications/Hammock.app/Contents/Resources/editor.html")

    @Test func onlyTheBundledPageCounts() {
        #expect(EditorBridge.isPage(page, page))
        #expect(EditorBridge.isPage(URL(string: "file:///Applications/Hammock.app/Contents/Resources/editor.html#x"), page))
        #expect(EditorBridge.isPage(URL(string: "file:///Applications/Hammock.app/Contents/Resources/./editor.html"), page))
        for s in ["file:///Applications/Hammock.app/Contents/Resources/other.html", "file:///tmp/editor.html",
                  "https://example.com/editor.html", "about:blank", "data:text/html,hi"] {
            #expect(!EditorBridge.isPage(URL(string: s), page), "\(s)")
        }
        #expect(!EditorBridge.isPage(nil, page))
    }
}
