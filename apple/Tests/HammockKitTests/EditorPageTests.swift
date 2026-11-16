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

    @Test func previewImagesAreLocalImagesOnly() {
        #expect(LocalImageHandler.file(for: URL(string: "hammock-file://local/Users/me/app/docs/shot.png"))?.path == "/Users/me/app/docs/shot.png")
        #expect(LocalImageHandler.file(for: URL(string: "hammock-file://local/Users/me/app/My%20Shot.JPG"))?.type == "image/jpeg")
        for s in ["hammock-file://local/Users/me/.ssh/id_ed25519", "hammock-file://local/Users/me/app/../notes.txt",
                  "hammock-file://elsewhere/Users/me/a.png", "file:///Users/me/a.png", "https://example.com/a.png"] {
            #expect(LocalImageHandler.file(for: URL(string: s)) == nil, "\(s)")
        }
    }
}
