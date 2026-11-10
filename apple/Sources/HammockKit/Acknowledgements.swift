import SwiftUI

/// One piece of third-party software the app ships, with its licence: a
/// row of Acknowledgements.json (internal/acknowledgements, made by `just
/// acknowledgements`), which both apps bundle.
public struct Acknowledgement: Decodable, Identifiable, Hashable, Sendable {
    public struct Package: Decodable, Hashable, Sendable {
        public let name: String
        public let version: String
    }

    public let name: String
    public let version: String?
    public let url: String?
    /// SPDX ids, joined with " AND " when it has several.
    public let license: String
    /// The apps it ships in: mac, ios, linux.
    public let apps: [String]
    /// A family's members (CodeMirror's packages, say).
    public let packages: [Package]?
    public let note: String?
    /// "chromium": Chromium's credits, a page in the Mac app's browser host.
    public let credits: String?
    /// Its licence files' full text.
    public let text: String

    public var id: String { name }

    /// The text with its paragraphs' hard line breaks taken out, for a
    /// screen narrower than the 80 columns it was written for. Short lines
    /// (titles, copyright lines) and indented or listed ones keep theirs.
    public var reflowedText: String {
        var out: [String] = []
        var paragraph = ""
        func flush() { if !paragraph.isEmpty { out.append(paragraph); paragraph = "" } }
        for line in text.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.isEmpty { flush(); out.append(""); continue }
            let listed = line.first?.isWhitespace == true || trimmed.hasPrefix("*") || trimmed.hasPrefix("-") || trimmed.hasPrefix("•")
                || trimmed.first?.isNumber == true && trimmed.prefix(4).contains(where: { $0 == "." || $0 == ")" })
            if paragraph.isEmpty || listed { flush(); paragraph = trimmed.isEmpty ? "" : (listed ? line : trimmed) }
            else { paragraph += " " + trimmed }
            // A short line ends its paragraph: a title, a name, an address.
            if trimmed.count < 50 { flush() }
        }
        flush()
        return out.joined(separator: "\n")
    }

    /// Its version, or how many packages it is.
    public var detail: String? {
        if let version, !version.isEmpty {
            // A commit: its first 12 characters.
            if version.count == 40, version.allSatisfy(\.isHexDigit) { return String(version.prefix(12)) }
            return version
        }
        if let packages, packages.count > 1 { return "\(packages.count) packages" }
        return nil
    }

    /// The licence, readably: "MIT and Apache 2.0".
    public var licenseName: String {
        license.components(separatedBy: " AND ").map { id in
            switch id {
            case "Apache-2.0": "Apache 2.0"
            case "BSD-3-Clause": "BSD 3-Clause"
            case "BSD-2-Clause": "BSD 2-Clause"
            case "CC0-1.0": "CC0 1.0"
            case "Unicode-3.0": "Unicode License v3"
            case "Public-Domain": "Public domain"
            default: id
            }
        }.joined(separator: ", ")
    }
}

public enum Acknowledgements {
    /// This app's name in Acknowledgements.json.
    public static var thisApp: String {
        #if os(macOS)
        "mac"
        #else
        "ios"
        #endif
    }

    /// The software in this app, by name; empty if the file's missing.
    public static func load(bundle: Bundle = .main) -> [Acknowledgement] {
        struct File: Decodable { let components: [Acknowledgement] }
        guard let url = bundle.url(forResource: "Acknowledgements", withExtension: "json"),
              let data = try? Data(contentsOf: url),
              let file = try? JSONDecoder().decode(File.self, from: data) else { return [] }
        return file.components.filter { $0.apps.contains(thisApp) }
    }
}

/// One component's licence: what it is, where it's from, and the text.
public struct AcknowledgementText: View {
    let item: Acknowledgement

    public init(_ item: Acknowledgement) { self.item = item }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(item.name).font(.title2.bold())
                    HStack(spacing: 6) {
                        Text(item.licenseName)
                        if let v = item.version, !v.isEmpty, let shown = item.detail {
                            Text("·")
                            Text(shown).textSelection(.enabled).help(v)
                        }
                    }
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    if let s = item.url, let url = URL(string: s) {
                        Link(s.replacingOccurrences(of: "https://", with: ""), destination: url)
                            .font(.callout)
                    }
                }
                if let note = item.note, !note.isEmpty {
                    Text(note).font(.callout)
                }
                if let packages = item.packages, packages.count > 1 {
                    Text(packages.map { "\($0.name) \($0.version)" }.joined(separator: "\n"))
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
                Divider()
                licenceText
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding()
        }
    }

    private var licenceText: Text {
        #if os(macOS)
        Text(item.text).font(.system(.caption, design: .monospaced))
        #else
        // Too narrow for its 80 columns.
        Text(item.reflowedText).font(.footnote)
        #endif
    }
}
