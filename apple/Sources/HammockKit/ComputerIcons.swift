import Foundation

// What the icon picker offers for a linked computer: SF Symbols that make
// sense for a computer (not all of them), and every emoji, each with words
// to search by. Suggested ones lead.

/// An icon in the picker: an SF Symbol's name or an emoji, and the words
/// it's found by.
public struct IconChoice: Hashable, Sendable {
    public let icon: String
    public let name: String
    public let keywords: String

    public init(_ icon: String, _ name: String, _ keywords: String = "") {
        self.icon = icon
        self.name = name
        self.keywords = keywords
    }

    /// Whether it matches a search (every word of it).
    public func matches(_ query: String) -> Bool {
        let hay = (name + " " + keywords + " " + icon).lowercased()
        return query.lowercased().split(separator: " ").allSatisfy { hay.contains($0) }
    }
}

/// A titled run of icons.
public struct IconSection: Identifiable, Sendable {
    public let title: String
    public let icons: [IconChoice]
    public var id: String { title }
}

public enum ComputerIcons {
    /// The SF Symbols offered: computers first.
    public static let symbols: [IconSection] = [
        IconSection(title: "Computers", icons: [
            IconChoice("laptopcomputer", "Laptop", "macbook notebook portable"),
            IconChoice("macbook", "MacBook", "laptop notebook portable"),
            IconChoice("desktopcomputer", "Desktop", "imac computer"),
            IconChoice("display", "Display", "monitor screen desktop"),
            IconChoice("display.2", "Two Displays", "monitors screens desktop dual"),
            IconChoice("macmini", "Mac mini", "mini desktop box small"),
            IconChoice("macstudio", "Mac Studio", "studio desktop"),
            IconChoice("macpro.gen3", "Mac Pro", "tower workstation desktop"),
            IconChoice("macpro.gen2", "Mac Pro (cylinder)", "trash can tower workstation"),
            IconChoice("macpro.gen1", "Mac Pro (classic)", "tower workstation cheese grater"),
            IconChoice("pc", "PC", "windows tower desktop computer"),
            IconChoice("server.rack", "Server", "rack linux nas homelab data centre center"),
            IconChoice("macpro.gen3.server", "Rack Server", "mac pro rack server homelab"),
            IconChoice("xserve", "Xserve", "rack server"),
            IconChoice("externaldrive", "Drive", "nas storage disk external"),
            IconChoice("internaldrive", "Internal Drive", "disk storage hard drive"),
            IconChoice("cpu", "Chip", "processor cpu silicon"),
            IconChoice("memorychip", "Board", "memory ram raspberry pi single board"),
            IconChoice("terminal", "Terminal", "shell prompt console"),
            IconChoice("apple.terminal", "Terminal Window", "shell console"),
        ]),
        IconSection(title: "Devices", icons: [
            IconChoice("keyboard", "Keyboard", "typing"),
            IconChoice("computermouse", "Mouse", "pointer"),
            IconChoice("ipad", "iPad", "tablet"),
            IconChoice("ipad.landscape", "iPad Landscape", "tablet"),
            IconChoice("iphone", "iPhone", "phone mobile"),
            IconChoice("tv", "TV", "television screen media"),
            IconChoice("appletv", "Apple TV", "box media streamer"),
            IconChoice("visionpro", "Vision Pro", "headset vr ar goggles"),
            IconChoice("gamecontroller", "Games", "gaming console controller"),
            IconChoice("headphones", "Headphones", "audio music"),
            IconChoice("hifispeaker", "Speaker", "audio music"),
            IconChoice("homepod", "HomePod", "speaker home"),
            IconChoice("printer", "Printer", "print"),
            IconChoice("wifi.router", "Router", "network wifi gateway"),
            IconChoice("airport.extreme.tower", "Base Station", "router airport network"),
            IconChoice("network", "Network", "nodes connected"),
            IconChoice("antenna.radiowaves.left.and.right", "Antenna", "radio wireless signal"),
            IconChoice("opticaldiscdrive", "Disc Drive", "cd dvd optical"),
            IconChoice("powerplug", "Plug", "power"),
        ]),
        IconSection(title: "Places", icons: [
            IconChoice("house", "Home", "house"),
            IconChoice("building.2", "Office", "work building company"),
            IconChoice("briefcase", "Work", "job office"),
            IconChoice("graduationcap", "School", "university study"),
            IconChoice("cloud", "Cloud", "vps hosted remote"),
            IconChoice("globe", "Globe", "world internet web"),
            IconChoice("airplane", "Travel", "plane flight trip"),
            IconChoice("car", "Car", "vehicle"),
            IconChoice("sailboat", "Boat", "sail"),
            IconChoice("tent", "Camp", "tent outdoors"),
            IconChoice("mountain.2", "Mountains", "outdoors"),
        ]),
        IconSection(title: "Things", icons: [
            IconChoice("cube", "Cube", "box block"),
            IconChoice("cube.transparent", "Glass Cube", "box"),
            IconChoice("shippingbox", "Box", "package container docker"),
            IconChoice("square.stack.3d.up", "Stack", "layers"),
            IconChoice("hammer", "Hammer", "build tools"),
            IconChoice("wrench.and.screwdriver", "Tools", "fix build"),
            IconChoice("gearshape", "Gear", "settings cog"),
            IconChoice("flask", "Lab", "experiment test science"),
            IconChoice("atom", "Atom", "science"),
            IconChoice("brain", "Brain", "ai think"),
            IconChoice("bolt", "Bolt", "fast power lightning"),
            IconChoice("flame", "Flame", "fire hot"),
            IconChoice("sparkles", "Sparkles", "ai new magic"),
            IconChoice("star", "Star", "favourite favorite"),
            IconChoice("heart", "Heart", "love"),
            IconChoice("music.note", "Music", "audio"),
            IconChoice("film", "Film", "movie video media"),
            IconChoice("camera", "Camera", "photo"),
            IconChoice("books.vertical", "Books", "library reading"),
        ]),
        IconSection(title: "Nature", icons: [
            IconChoice("leaf", "Leaf", "plant green"),
            IconChoice("sun.max", "Sun", "day bright"),
            IconChoice("moon.stars", "Night", "moon stars"),
            IconChoice("ladybug", "Ladybug", "bug debug"),
            IconChoice("ant", "Ant", "bug"),
            IconChoice("tortoise", "Tortoise", "slow turtle"),
            IconChoice("hare", "Hare", "fast rabbit"),
            IconChoice("bird", "Bird", "fly"),
            IconChoice("fish", "Fish", "sea"),
            IconChoice("cat", "Cat", "pet"),
            IconChoice("dog", "Dog", "pet"),
            IconChoice("pawprint", "Paw", "pet animal"),
        ]),
    ]

    /// Emoji suggested for a computer, ahead of all of them.
    public static let suggestedEmoji: [IconChoice] = [
        ("💻", "laptop"), ("🖥️", "desktop computer"), ("🗄️", "server file cabinet"), ("📦", "mac mini box package"),
        ("🖨️", "printer"), ("⌨️", "keyboard"), ("💾", "floppy disk"), ("💿", "optical disk"), ("📱", "mobile phone"),
        ("📺", "television"), ("🎮", "video game"), ("🕹️", "joystick"), ("🏠", "house home"), ("🏢", "office building work"),
        ("🏫", "school"), ("☁️", "cloud"), ("🌐", "globe network internet"), ("📡", "satellite antenna"), ("🛰️", "satellite"),
        ("🚀", "rocket"), ("🧪", "test tube lab"), ("🔬", "microscope"), ("🛠️", "hammer and wrench tools"), ("⚙️", "gear"),
        ("🐧", "penguin linux"), ("🍎", "apple"), ("⚡", "high voltage lightning"), ("🔥", "fire"), ("⭐", "star"), ("🌙", "moon night"),
    ].map { IconChoice($0.0, $0.1) }

    /// Every emoji (Unicode's list, EmojiData.swift), by group.
    public static let emoji: [IconSection] = {
        var out: [IconSection] = []
        var title = ""
        var icons: [IconChoice] = []
        for line in emojiData.split(separator: "\n") {
            if line.hasPrefix("@") {
                if !icons.isEmpty { out.append(IconSection(title: title, icons: icons)) }
                title = String(line.dropFirst())
                icons = []
            } else if let tab = line.firstIndex(of: "\t") {
                icons.append(IconChoice(String(line[..<tab]), String(line[line.index(after: tab)...])))
            }
        }
        if !icons.isEmpty { out.append(IconSection(title: title, icons: icons)) }
        return out
    }()

    /// The suggested symbols: the computers.
    public static var suggestedSymbols: [IconChoice] { symbols.first?.icons ?? [] }

    /// What the picker shows: for a search, the matches (suggested first,
    /// each once); else the suggested, then the rest by section.
    public static func sections(emoji: Bool, query: String) -> [IconSection] {
        let q = query.trimmingCharacters(in: .whitespaces)
        if emoji {
            if q.isEmpty { return [IconSection(title: "Suggested", icons: suggestedEmoji)] + Self.emoji }
            var seen = Set<String>()
            let found = (suggestedEmoji + Self.emoji.flatMap(\.icons)).filter { $0.matches(q) && seen.insert($0.icon).inserted }
            return found.isEmpty ? [] : [IconSection(title: "Results", icons: found)]
        }
        if q.isEmpty { return [IconSection(title: "Suggested", icons: suggestedSymbols)] + symbols.dropFirst() }
        let found = symbols.flatMap(\.icons).filter { $0.matches(q) }
        return found.isEmpty ? [] : [IconSection(title: "Results", icons: found)]
    }
}
