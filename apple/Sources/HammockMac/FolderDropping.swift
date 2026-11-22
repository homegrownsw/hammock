import AppKit
import HammockKit
import SwiftUI

/// Folders dropped on the sidebar or the Dock icon, or opened with the app
/// (`open -a Hammock ~/Developer/app`): each becomes a Group, or a tab in
/// the Group it was dropped on (FolderDrop decides which).
@MainActor @Observable
final class FolderDropUI {
    static let shared = FolderDropUI()

    /// The Group whose rows flash: a folder it already has was dropped.
    private(set) var flashing: String?
    /// One drop's work at a time, in the order they came.
    @ObservationIgnored private var last: Task<Void, Never>?

    /// The folders in the drag under way (none: it isn't taken), read from
    /// the drag's pasteboard, which macOS lets a destination read while
    /// the drag is still moving. Kept per drag.
    @ObservationIgnored private var dragged: (change: Int, folders: [String])?
    func draggedFolders() -> [String] {
        let pb = NSPasteboard(name: .drag)
        if let d = dragged, d.change == pb.changeCount { return d.folders }
        let urls = pb.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []
        let folders = FolderDrop.folders(urls)
        dragged = (pb.changeCount, folders)
        return folders
    }

    /// Folders the app was asked to open (the Dock icon, `open`): new
    /// Groups at the end, or the ones they have.
    func open(_ urls: [URL], model: AppModel) {
        let folders = FolderDrop.folders(urls)
        guard !folders.isEmpty else { return }
        NSApp.activate()
        enqueue(model) {
            // Opening the app with a folder: its Groups arrive a moment later.
            for _ in 0..<100 where !model.hasFullState {
                try? await Task.sleep(for: .milliseconds(100))
            }
            guard model.hasFullState else { return [] }
            return FolderDrop.actions(folders: folders, target: .insert(model.shownGroups.count), groups: model.groups, shown: model.shownGroups)
        }
    }

    /// Folders dropped on the sidebar at `target`.
    func drop(_ folders: [String], on target: FolderDrop.Target, shown: [WorkspaceGroup], model: AppModel) {
        let actions = FolderDrop.actions(folders: folders, target: target, groups: model.groups, shown: shown)
        enqueue(model) { actions }
    }

    private func enqueue(_ model: AppModel, _ plan: @escaping @MainActor () async -> [FolderDrop.Action]) {
        let before = last
        last = Task { @MainActor in
            await before?.value
            for action in await plan() {
                switch action {
                case .newGroup(let folder, let index):
                    await model.newGroup(inFolder: folder, at: index)
                case .select(let id):
                    model.selectGroup(id)
                    flash(id)
                case .newTab(let id, let folder):
                    await model.createTab(in: id, cwd: folder)
                }
            }
        }
    }

    private func flash(_ id: String) {
        withAnimation(.easeOut(duration: 0.15)) { flashing = id }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.9) { [weak self] in
            guard self?.flashing == id else { return }
            withAnimation(.easeOut(duration: 0.4)) { self?.flashing = nil }
        }
    }
}

/// The sidebar taking folders dragged from the Finder: only drags with a
/// folder in them (the sidebar's own Group and tab drags aren't drags
/// AppKit knows, so they never come here). `locate` turns the pointer,
/// in the List's space, into where it would land.
struct SidebarFolderDrop: DropDelegate {
    let locate: (CGPoint) -> FolderDrop.Target
    let show: (FolderDrop.Target?) -> Void
    let perform: ([String], FolderDrop.Target) -> Void

    func validateDrop(info: DropInfo) -> Bool {
        MainActor.assumeIsolated { !FolderDropUI.shared.draggedFolders().isEmpty }
    }

    func dropEntered(info: DropInfo) { show(locate(info.location)) }

    func dropUpdated(info: DropInfo) -> DropProposal? {
        show(locate(info.location))
        return DropProposal(operation: .copy)
    }

    func dropExited(info: DropInfo) { show(nil) }

    func performDrop(info: DropInfo) -> Bool {
        let folders = MainActor.assumeIsolated { FolderDropUI.shared.draggedFolders() }
        let target = locate(info.location)
        show(nil)
        guard !folders.isEmpty else { return false }
        perform(folders, target)
        return true
    }
}

/// Where dropped folders' new Groups will go: a line between Groups, as
/// the Finder draws one.
struct FolderDropLine: View {
    var body: some View {
        HStack(spacing: 0) {
            Circle()
                .strokeBorder(Color.themeAccent, lineWidth: 2)
                .frame(width: 8, height: 8)
            Rectangle()
                .fill(Color.themeAccent)
                .frame(height: 2)
        }
        .frame(height: 8)
        .padding(.horizontal, 8)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}
