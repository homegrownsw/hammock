import Foundation
import SwiftUI

/// A block of Markdown, for the agent view. Block structure is parsed here;
/// inline formatting (emphasis, code spans, links) by AttributedString.
public enum MDBlock: Hashable {
    case heading(Int, String)
    case paragraph(String)
    case code(language: String, text: String)
    case list([MDListItem])
    case quote(String)
    case table(header: [String], rows: [[String]])
    case rule
}

public struct MDListItem: Hashable {
    public var marker: String   // "•", "1.", or "" for a checkbox
    public var checked: Bool?   // a task list item
    public var indent: Int
    public var text: String
}

public enum Markdown {
    public static func parse(_ src: String) -> [MDBlock] {
        var out: [MDBlock] = []
        var para: [String] = []
        var items: [MDListItem] = []
        var quote: [String] = []
        let lines = src.replacingOccurrences(of: "\r\n", with: "\n").components(separatedBy: "\n")
        func flushPara() {
            if !para.isEmpty { out.append(.paragraph(para.joined(separator: "\n"))); para = [] }
        }
        func flushList() {
            if !items.isEmpty { out.append(.list(items)); items = [] }
        }
        func flushQuote() {
            if !quote.isEmpty { out.append(.quote(quote.joined(separator: "\n"))); quote = [] }
        }
        func flushAll() { flushPara(); flushList(); flushQuote() }

        var i = 0
        while i < lines.count {
            let line = lines[i]
            let t = line.trimmingCharacters(in: .whitespaces)
            // Fenced code.
            if t.hasPrefix("```") || t.hasPrefix("~~~") {
                flushAll()
                let fence = String(t.prefix(3))
                let lang = String(t.dropFirst(3)).trimmingCharacters(in: .whitespaces)
                var code: [String] = []
                i += 1
                while i < lines.count, !lines[i].trimmingCharacters(in: .whitespaces).hasPrefix(fence) {
                    code.append(lines[i])
                    i += 1
                }
                out.append(.code(language: lang, text: code.joined(separator: "\n")))
                i += 1
                continue
            }
            if t.isEmpty {
                flushAll()
                i += 1
                continue
            }
            if let h = heading(t) {
                flushAll()
                out.append(.heading(h.0, h.1))
                i += 1
                continue
            }
            if t == "---" || t == "***" || t == "___" {
                flushAll()
                out.append(.rule)
                i += 1
                continue
            }
            // Tables: a header row, a |---| row, then rows.
            if t.hasPrefix("|"), i + 1 < lines.count, isTableRule(lines[i + 1]) {
                flushAll()
                let header = cells(t)
                var rows: [[String]] = []
                i += 2
                while i < lines.count, lines[i].trimmingCharacters(in: .whitespaces).hasPrefix("|") {
                    rows.append(cells(lines[i].trimmingCharacters(in: .whitespaces)))
                    i += 1
                }
                out.append(.table(header: header, rows: rows))
                continue
            }
            if t.hasPrefix(">") {
                flushPara(); flushList()
                quote.append(String(t.dropFirst()).trimmingCharacters(in: .whitespaces))
                i += 1
                continue
            }
            if let item = listItem(line) {
                flushPara(); flushQuote()
                items.append(item)
                i += 1
                continue
            }
            // A continuation of a list item.
            if !items.isEmpty, line.hasPrefix("  ") {
                items[items.count - 1].text += " " + t
                i += 1
                continue
            }
            flushList(); flushQuote()
            para.append(line)
            i += 1
        }
        flushAll()
        return out
    }

    private static func heading(_ t: String) -> (Int, String)? {
        var level = 0
        for c in t {
            if c == "#" { level += 1 } else { break }
        }
        guard (1...6).contains(level), t.dropFirst(level).first == " " else { return nil }
        return (level, String(t.dropFirst(level + 1)))
    }

    private static func isTableRule(_ l: String) -> Bool {
        let t = l.trimmingCharacters(in: .whitespaces)
        return t.hasPrefix("|") && t.allSatisfy { "|-: ".contains($0) } && t.contains("-")
    }

    private static func cells(_ row: String) -> [String] {
        var r = row
        if r.hasPrefix("|") { r.removeFirst() }
        if r.hasSuffix("|") { r.removeLast() }
        return r.components(separatedBy: "|").map { $0.trimmingCharacters(in: .whitespaces) }
    }

    private static let bullet = try! NSRegularExpression(pattern: #"^(\s*)([-*+]|\d+[.)])\s+(\[[ xX~]\]\s+)?(.*)$"#)

    private static func listItem(_ line: String) -> MDListItem? {
        let ns = line as NSString
        guard let m = bullet.firstMatch(in: line, range: NSRange(location: 0, length: ns.length)) else { return nil }
        let indent = ns.substring(with: m.range(at: 1)).count / 2
        var marker = ns.substring(with: m.range(at: 2))
        if "-*+".contains(marker) { marker = "•" }
        var checked: Bool?
        if m.range(at: 3).location != NSNotFound {
            let box = ns.substring(with: m.range(at: 3))
            checked = box.contains("x") || box.contains("X")
            marker = ""
        }
        return MDListItem(marker: marker, checked: checked, indent: indent, text: ns.substring(with: m.range(at: 4)))
    }

    private static let barePath = try! NSRegularExpression(pattern: #"(?<![\w`(\[/])(/(?:Users|tmp|private|var|opt|Volumes)/[^\s`)\]"'<>]+[^\s`)\]"'<>.,:;])"#)

    /// Inline Markdown as an AttributedString, with bare absolute paths made
    /// into file links.
    public static func inline(_ s: String) -> AttributedString {
        RenderCache.get("in|" + s) { parseInline(s) }
    }

    /// Inline Markdown without links: for a row you tap as a whole (a
    /// session, a tool call), where a path or URL mustn't take the tap.
    public static func inlineText(_ s: String) -> AttributedString {
        RenderCache.get("it|" + s) {
            var a = parseInline(s)
            for run in a.runs where run.link != nil { a[run.range].link = nil }
            return a
        }
    }

    private static func parseInline(_ s: String) -> AttributedString {
        let ns = s as NSString
        var linked = ""
        var last = 0
        for m in barePath.matches(in: s, range: NSRange(location: 0, length: ns.length)) {
            linked += ns.substring(with: NSRange(location: last, length: m.range.location - last))
            // Inside a code span (an odd number of backticks before it): leave it.
            let inCode = ns.substring(to: m.range.location).filter { $0 == "`" }.count % 2 == 1
            let path = ns.substring(with: m.range(at: 1))
            linked += inCode ? path : "[\(path)](file://\(path.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? path))"
            last = m.range.location + m.range.length
        }
        linked += ns.substring(from: last)
        let opts = AttributedString.MarkdownParsingOptions(interpretedSyntax: .inlineOnlyPreservingWhitespace, failurePolicy: .returnPartiallyParsedIfPossible)
        return (try? AttributedString(markdown: linked, options: opts)) ?? AttributedString(s)
    }
}

/// A tiny syntax highlighter: comments, strings, numbers and common keywords.
public enum CodeHighlight {
    private static let keywords: Set<String> = [
        "func", "let", "var", "if", "else", "for", "while", "return", "import", "package", "type", "struct", "class", "enum", "case", "switch",
        "const", "def", "fn", "pub", "use", "go", "defer", "interface", "public", "private", "static", "final", "async", "await", "try", "catch",
        "throw", "throws", "guard", "in", "of", "new", "this", "self", "nil", "null", "true", "false", "none", "None", "True", "False", "export",
        "from", "default", "break", "continue", "extension", "protocol", "range", "map", "select", "chan", "echo", "then", "fi", "do", "done",
    ]
    private static let token = try! NSRegularExpression(pattern: #"(//[^\n]*|#[^\n]*|--[^\n]*)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|`[^`]*`)|\b(\d[\d_.xXa-fA-F]*)\b|\b([A-Za-z_][A-Za-z0-9_]*)\b"#)

    public static func attributed(_ code: String, language: String) -> AttributedString {
        var out = AttributedString(code)
        let ns = code as NSString
        let plain = ["", "text", "txt", "plain", "output", "log"].contains(language.lowercased())
        guard !plain, ns.length < 40_000 else { return out }
        let hashComments = ["sh", "bash", "zsh", "shell", "python", "py", "ruby", "rb", "yaml", "yml", "toml", "make", "makefile", "just", "conf", "dockerfile"].contains(language.lowercased())
        for m in token.matches(in: code, range: NSRange(location: 0, length: ns.length)) {
            guard let r = Range(m.range, in: code), let ar = Range(r, in: out) else { continue }
            if m.range(at: 1).location != NSNotFound {
                let t = ns.substring(with: m.range(at: 1))
                if t.hasPrefix("#"), !hashComments { continue }
                if t.hasPrefix("--"), !["sql", "lua", "haskell", "hs"].contains(language.lowercased()) { continue }
                out[ar].foregroundColor = .secondary
            } else if m.range(at: 2).location != NSNotFound {
                out[ar].foregroundColor = Color(red: 0.77, green: 0.33, blue: 0.24)
            } else if m.range(at: 3).location != NSNotFound {
                out[ar].foregroundColor = Color(red: 0.11, green: 0.52, blue: 0.68)
            } else if keywords.contains(ns.substring(with: m.range(at: 4))) {
                out[ar].foregroundColor = Color(red: 0.62, green: 0.25, blue: 0.70)
            }
        }
        return out
    }
}
