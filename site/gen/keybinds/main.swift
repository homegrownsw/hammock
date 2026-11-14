// Prints the Mac app's keyboard actions (Keybinds.actions) as JSON, for the docs site's
// shortcuts page. Compiled with Keybinds.swift alone (it needs only Foundation): see `just docs-gen`.
import Foundation

struct Row: Encodable {
    let id, title, group: String
    let shortcut: String?
    let config: String?
}

let rows = Keybinds.actions.map {
    Row(id: $0.id, title: $0.title, group: $0.group, shortcut: $0.defaultChord?.label, config: $0.defaultChord?.configString)
}
let out = ["groups": Keybinds.groups.map { AnyEncodable($0) }, "actions": rows.map { AnyEncodable($0) }]

struct AnyEncodable: Encodable {
    let value: Encodable
    init(_ v: Encodable) { value = v }
    func encode(to e: Encoder) throws { try value.encode(to: e) }
}

let enc = JSONEncoder()
enc.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
FileHandle.standardOutput.write(try enc.encode(out))
