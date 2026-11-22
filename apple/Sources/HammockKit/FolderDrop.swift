import CoreGraphics
import Foundation

/// Folders dropped on the Mac's sidebar (or its Dock icon): what each
/// becomes. Between Groups, or below them, a new Group for each folder
/// there; on a Group of this Mac's, a new tab in it for each. A folder a
/// Group of this Mac's already has selects that Group instead of making
/// another. Files aren't taken.
public enum FolderDrop {
    /// Where a drop lands in the sidebar.
    public enum Target: Equatable, Sendable {
        /// On a Group's header or one of its tabs: new tabs there.
        case group(String)
        /// Between the shown Groups, before the one at this index (the
        /// count: after the last): new Groups there.
        case insert(Int)
    }

    /// What one dropped folder does.
    public enum Action: Equatable, Sendable {
        /// A new Group for the folder, moved to `index` among all your
        /// Groups (nil: left at the end, where it's made).
        case newGroup(folder: String, index: Int?)
        /// The folder's Group already exists: select it (and flash its row).
        case select(group: String)
        /// A new terminal tab in a Group, opening in the folder.
        case newTab(group: String, folder: String)
    }

    /// One Group's rows in the sidebar, top to bottom: its header and the
    /// tabs shown under it. `takesTabs`: a drop on it can open tabs there.
    public struct Block: Equatable, Sendable {
        public var id: String
        public var top: CGFloat
        public var bottom: CGFloat
        public var takesTabs: Bool
        public init(id: String, top: CGFloat, bottom: CGFloat, takesTabs: Bool) {
            self.id = id
            self.top = top
            self.bottom = bottom
            self.takesTabs = takesTabs
        }
    }

    /// The folders among dropped URLs, in order, each once: file URLs of
    /// directories that aren't packages (an app, a bundle). Files are left out.
    public static func folders(_ urls: [URL], isFolder: (URL) -> Bool = isFolder) -> [String] {
        var seen = Set<String>()
        var out: [String] = []
        for url in urls where url.isFileURL && isFolder(url) {
            let path = url.standardizedFileURL.path
            let trimmed = path.count > 1 && path.hasSuffix("/") ? String(path.dropLast()) : path
            if seen.insert(key(trimmed)).inserted { out.append(trimmed) }
        }
        return out
    }

    /// Whether a URL is a folder, as the Finder shows one.
    public static func isFolder(_ url: URL) -> Bool {
        let v = try? url.resourceValues(forKeys: [.isDirectoryKey, .isPackageKey])
        return v?.isDirectory == true && v?.isPackage != true
    }

    /// A path to compare by: standardised, symlinks resolved, without a
    /// trailing slash.
    public static func key(_ path: String) -> String {
        guard !path.isEmpty else { return "" }
        let p = URL(fileURLWithPath: (path as NSString).expandingTildeInPath).standardizedFileURL.resolvingSymlinksInPath().path
        return p.count > 1 && p.hasSuffix("/") ? String(p.dropLast()) : p
    }

    /// Whether a Group can take tabs dropped from this Mac's Finder: one of
    /// this Mac's (a folder here means nothing on another computer).
    public static func takesTabs(_ g: WorkspaceGroup) -> Bool { !g.isRemote && g.canAddTabs }

    /// The Group of this Mac's whose folder is `folder`, if there is one.
    public static func existingGroup(for folder: String, in groups: [WorkspaceGroup]) -> WorkspaceGroup? {
        let k = key(folder)
        return groups.first { !$0.isRemote && !$0.folder.isEmpty && key($0.folder) == k }
    }

    /// Where a drop at `y` lands, given the shown Groups' rows: within
    /// `edge` of where two Groups meet, above the first or below the last,
    /// between them; on a Group that takes tabs, that Group; on one that
    /// doesn't, between it and the neighbour nearer the pointer.
    public static func target(y: CGFloat, blocks: [Block], edge: CGFloat = 6) -> Target {
        guard let first = blocks.first, let last = blocks.last else { return .insert(0) }
        if y < first.top + edge { return .insert(0) }
        if y > last.bottom - edge { return .insert(blocks.count) }
        for (i, b) in blocks.enumerated() where i > 0 && abs(y - b.top) <= edge { return .insert(i) }
        for (i, b) in blocks.enumerated() where y >= b.top && y <= b.bottom {
            if b.takesTabs { return .group(b.id) }
            return .insert(y < (b.top + b.bottom) / 2 ? i : i + 1)
        }
        // In a gap between two Groups' rows.
        return .insert(blocks.firstIndex { $0.top > y } ?? blocks.count)
    }

    /// Where the line for `.insert(i)` is drawn: the top of the Group it
    /// goes before, or the bottom of the last.
    public static func lineY(_ i: Int, blocks: [Block]) -> CGFloat? {
        if blocks.indices.contains(i) { return blocks[i].top }
        return blocks.last?.bottom
    }

    /// The index among all your Groups that `.insert(i)` among the shown
    /// ones means (nil: the end).
    public static func index(inserting i: Int, shown: [String], all: [String]) -> Int? {
        if shown.indices.contains(i) { return all.firstIndex(of: shown[i]) }
        guard let last = shown.last, let j = all.firstIndex(of: last), j + 1 < all.count else { return nil }
        return j + 1
    }

    /// What each folder does, in order, dropped on `target` (a Dock icon or
    /// `open`: `.insert` past the last). New Groups at one place keep the
    /// folders' order.
    public static func actions(folders: [String], target: Target, groups: [WorkspaceGroup], shown: [WorkspaceGroup]) -> [Action] {
        switch target {
        case .group(let id):
            guard let g = groups.first(where: { $0.id == id }), takesTabs(g) else {
                return actions(folders: folders, target: .insert(shown.count), groups: groups, shown: shown)
            }
            return folders.map { .newTab(group: id, folder: $0) }
        case .insert(let i):
            var next = index(inserting: i, shown: shown.map(\.id), all: groups.map(\.id))
            return folders.map { f in
                if let g = existingGroup(for: f, in: groups) { return .select(group: g.id) }
                defer { next = next.map { $0 + 1 } }
                return .newGroup(folder: f, index: next)
            }
        }
    }
}
