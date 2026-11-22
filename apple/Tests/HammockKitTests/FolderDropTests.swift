import Foundation
import Testing
@testable import HammockKit

/// Folders dropped on the Mac's sidebar or Dock icon: which are taken,
/// where they land, and what each becomes.
@Suite struct FolderDropTests {
    private func group(_ id: String, folder: String = "", host: String = "", role: Role = .unspecified, offline: Bool = false) -> WorkspaceGroup {
        .with { $0.id = id; $0.folder = folder; $0.hostUserID = host; $0.myRole = role; $0.offline = offline }
    }

    // MARK: Folders, not files

    @Test func onlyFoldersAreTaken() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("folder-drop-\(UUID().uuidString)")
        let sub = dir.appendingPathComponent("app")
        let file = dir.appendingPathComponent("notes.txt")
        let package = dir.appendingPathComponent("Tool.app")
        try FileManager.default.createDirectory(at: sub, withIntermediateDirectories: true)
        try FileManager.default.createDirectory(at: package, withIntermediateDirectories: true)
        try Data("hi".utf8).write(to: file)
        defer { try? FileManager.default.removeItem(at: dir) }

        #expect(FolderDrop.folders([file]).isEmpty)
        #expect(FolderDrop.folders([package]).isEmpty)
        #expect(FolderDrop.folders([URL(string: "https://example.com")!]).isEmpty)
        // Mixed: the folders, in order, the files left out.
        #expect(FolderDrop.folders([file, sub, dir]) == [sub.standardizedFileURL.path, dir.standardizedFileURL.path])
    }

    @Test func aFolderIsTakenOnce() {
        let urls = [URL(fileURLWithPath: "/Users/me/Developer/app/"), URL(fileURLWithPath: "/Users/me/Developer/app"), URL(fileURLWithPath: "/Users/me/Developer/./app")]
        #expect(FolderDrop.folders(urls, isFolder: { _ in true }) == ["/Users/me/Developer/app"])
    }

    // MARK: The folder's Group

    @Test func aGroupOfThisMacsWithTheFolderIsFound() {
        let groups = [
            group("gp_none"),
            group("gp_shared", folder: "/Users/me/Developer/app", host: "us_sam", role: .driver),
            group("gp_app", folder: "/Users/me/Developer/app/"),
        ]
        #expect(FolderDrop.existingGroup(for: "/Users/me/Developer/app", in: groups)?.id == "gp_app")
        #expect(FolderDrop.existingGroup(for: "/Users/me/Developer/../Developer/app", in: groups)?.id == "gp_app")
        #expect(FolderDrop.existingGroup(for: "/Users/me/Developer", in: groups) == nil)
        // Another Mac's Group with the same path isn't this folder.
        #expect(FolderDrop.existingGroup(for: "/Users/me/Developer/app", in: [groups[1]]) == nil)
    }

    @Test func symlinksMatchTheirFolder() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("folder-drop-\(UUID().uuidString)")
        let real = dir.appendingPathComponent("real")
        let link = dir.appendingPathComponent("link")
        try FileManager.default.createDirectory(at: real, withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(at: link, withDestinationURL: real)
        defer { try? FileManager.default.removeItem(at: dir) }
        #expect(FolderDrop.existingGroup(for: link.path, in: [group("gp_real", folder: real.path)])?.id == "gp_real")
    }

    // MARK: Where it lands

    private let blocks = [
        FolderDrop.Block(id: "a", top: 0, bottom: 100, takesTabs: true),
        FolderDrop.Block(id: "b", top: 100, bottom: 130, takesTabs: false),
        FolderDrop.Block(id: "c", top: 130, bottom: 200, takesTabs: true),
    ]

    @Test func onAGroupsRows() {
        #expect(FolderDrop.target(y: 50, blocks: blocks) == .group("a"))
        #expect(FolderDrop.target(y: 170, blocks: blocks) == .group("c"))
    }

    @Test func betweenGroups() {
        #expect(FolderDrop.target(y: 2, blocks: blocks) == .insert(0))
        #expect(FolderDrop.target(y: 97, blocks: blocks) == .insert(1))
        #expect(FolderDrop.target(y: 133, blocks: blocks) == .insert(2))
        // Below the last, in the empty space: at the end.
        #expect(FolderDrop.target(y: 197, blocks: blocks) == .insert(3))
        #expect(FolderDrop.target(y: 600, blocks: blocks) == .insert(3))
        #expect(FolderDrop.target(y: 10, blocks: []) == .insert(0))
    }

    @Test func aGroupThatTakesNoTabsIsDroppedBeside() {
        #expect(FolderDrop.target(y: 110, blocks: blocks) == .insert(1))
        #expect(FolderDrop.target(y: 120, blocks: blocks) == .insert(2))
    }

    @Test func aGapBetweenRowsIsBetweenGroups() {
        let gapped = [FolderDrop.Block(id: "a", top: 0, bottom: 40, takesTabs: true), FolderDrop.Block(id: "b", top: 60, bottom: 100, takesTabs: true)]
        #expect(FolderDrop.target(y: 50, blocks: gapped) == .insert(1))
        #expect(FolderDrop.lineY(1, blocks: gapped) == 60)
        #expect(FolderDrop.lineY(2, blocks: gapped) == 100)
    }

    @Test func shownPlacesMapToAllGroups() {
        // Another computer's Group "x" isn't shown: the shown ones keep their places.
        let all = ["a", "x", "b"]
        #expect(FolderDrop.index(inserting: 0, shown: ["a", "b"], all: all) == 0)
        #expect(FolderDrop.index(inserting: 1, shown: ["a", "b"], all: all) == 2)
        #expect(FolderDrop.index(inserting: 2, shown: ["a", "b"], all: all) == nil)
        #expect(FolderDrop.index(inserting: 1, shown: ["a"], all: all) == 1)
    }

    // MARK: What it does

    @Test func betweenGroupsMakesGroupsThere() {
        let groups = [group("gp_a"), group("gp_b", folder: "/Users/me/Developer/b")]
        let actions = FolderDrop.actions(folders: ["/Users/me/one", "/Users/me/Developer/b", "/Users/me/two"], target: .insert(1), groups: groups, shown: groups)
        #expect(actions == [
            .newGroup(folder: "/Users/me/one", index: 1),
            .select(group: "gp_b"),
            .newGroup(folder: "/Users/me/two", index: 2),
        ])
        // At the end, they're left where they're made.
        #expect(FolderDrop.actions(folders: ["/Users/me/one"], target: .insert(2), groups: groups, shown: groups) == [.newGroup(folder: "/Users/me/one", index: nil)])
    }

    @Test func onAGroupOpensTabs() {
        let groups = [group("gp_a", folder: "/Users/me/Developer/a"), group("gp_b")]
        // One tab each, even for a folder that has a Group.
        #expect(FolderDrop.actions(folders: ["/Users/me/x", "/Users/me/Developer/a"], target: .group("gp_b"), groups: groups, shown: groups)
            == [.newTab(group: "gp_b", folder: "/Users/me/x"), .newTab(group: "gp_b", folder: "/Users/me/Developer/a")])
    }

    @Test func groupsThatCantTakeTabsMakeAGroupInstead() {
        let viewer = group("gp_view", host: "us_sam", role: .viewer)
        let driven = group("gp_drive", host: "us_sam", role: .driver)
        let offline = group("gp_linked", host: "us_me2", role: .owner, offline: true)
        #expect(!FolderDrop.takesTabs(viewer))
        #expect(!FolderDrop.takesTabs(driven)) // its folders are on the other Mac
        #expect(!FolderDrop.takesTabs(offline))
        #expect(FolderDrop.takesTabs(group("gp_mine")))
        let groups = [viewer, driven]
        #expect(FolderDrop.actions(folders: ["/Users/me/x"], target: .group("gp_view"), groups: groups, shown: groups) == [.newGroup(folder: "/Users/me/x", index: nil)])
    }
}
