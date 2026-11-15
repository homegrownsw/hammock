import Foundation
import Testing
@testable import HammockKit

/// An editor's text kept for Exposé's tile: CodeMirror's ChangeSet JSON
/// applied in Swift.
@Suite struct EditorTextTests {
    @Test func keepsInsertsAndDeletes() {
        // "hello world": keep 6, replace "world" (5) with "there".
        #expect(EditorSession.applying(#"[6,[5,"there"]]"#, to: "hello world") == "hello there")
        // Insert at the start, delete at the end.
        #expect(EditorSession.applying(##"[[0,"# "],5,[6]]"##, to: "hello world") == "# hello")
    }

    @Test func insertsLinesJoinedByNewlines() {
        #expect(EditorSession.applying(#"[2,[0,"","x"],1]"#, to: "abc") == "ab\nxc")
    }

    @Test func countsUTF16Units() {
        // "é" is one unit, "😀" two.
        #expect(EditorSession.applying(#"[3,[0,"!"]]"#, to: "é😀") == "é😀!")
    }

    @Test func refusesWhatDoesNotFit() {
        #expect(EditorSession.applying(#"[20]"#, to: "short") == nil)
        #expect(EditorSession.applying(#"[2]"#, to: "short") == nil)
        #expect(EditorSession.applying("not json", to: "short") == nil)
    }
}
