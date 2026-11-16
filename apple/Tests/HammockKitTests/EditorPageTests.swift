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

    @Test func previewImagePaths() {
        #expect(PreviewImageHandler.path(for: URL(string: "hammock-file://local/Users/me/app/docs/shot.png")) == "/Users/me/app/docs/shot.png")
        #expect(PreviewImageHandler.path(for: URL(string: "hammock-file://local/Users/me/app/My%20Shot.JPG")) == "/Users/me/app/My Shot.JPG")
        for s in ["hammock-file://local/Users/me/app/../notes.png", "hammock-file://elsewhere/Users/me/a.png",
                  "file:///Users/me/a.png", "https://example.com/a.png"] {
            #expect(PreviewImageHandler.path(for: URL(string: s)) == nil, "\(s)")
        }
    }
}
