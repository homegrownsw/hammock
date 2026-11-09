import Foundation
import Testing
@testable import HammockKit

@Suite struct SlashTokenTests {
    /// The token at a cursor marked "|" in s, and s without the mark.
    private func token(_ s: String) -> String? {
        let i = s.firstIndex(of: "|")!
        var t = s
        t.remove(at: i)
        let cursor = t.index(t.startIndex, offsetBy: s.distance(from: s.startIndex, to: i))
        return SlashToken.at(cursor, in: t).map { String(t[$0]) }
    }

    @Test func findsTheCommandAtTheCursor() {
        #expect(token("/|") == "/")
        #expect(token("/iss|") == "/iss")
        #expect(token("file it, use /issue-w|") == "/issue-w")
        #expect(token("use /iss| to build it") == "/iss")
        #expect(token("line one\n/rev|") == "/rev")
    }

    @Test func ignoresWhatIsntOne() {
        #expect(token("hello|") == nil)
        #expect(token("see src/foo|") == nil)
        #expect(token("/issue-worker |") == nil)
        #expect(token("/iss|ue") == nil)
        #expect(token("|/issue") == nil)
        #expect(token("|") == nil)
    }

    @Test func completesInPlace() {
        let t = "file it, use /iss to build it"
        let r = t.range(of: "/iss")!
        let done = SlashToken.complete(r, with: "/issue-worker", in: t)
        #expect(done.text == "file it, use /issue-worker to build it")
        #expect(done.text[done.text.startIndex..<done.cursor] == "file it, use /issue-worker ")

        let end = "now /iss"
        let done2 = SlashToken.complete(end.range(of: "/iss")!, with: "/issue-worker", in: end)
        #expect(done2.text == "now /issue-worker ")
        #expect(done2.cursor == done2.text.endIndex)

        // Before a line break: a space goes in, and the cursor stays on the line.
        let lines = "/iss\nmore"
        let done3 = SlashToken.complete(lines.range(of: "/iss")!, with: "/issue-worker", in: lines)
        #expect(done3.text == "/issue-worker \nmore")
        #expect(done3.text[done3.text.startIndex..<done3.cursor] == "/issue-worker ")
    }
}
