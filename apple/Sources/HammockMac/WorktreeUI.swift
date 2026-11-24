import AppKit
import HammockKit

/// Remove Worktree (a tab's menus, the File menu, the palette, its row):
/// asks, saying the command that runs and what's still running in its tabs,
/// then has the daemon remove the tab's worktree the repository's own way
/// (worktrees.go), which closes the tabs in it.
@MainActor
enum WorktreeUI {
    static func remove(_ model: AppModel, _ tab: WorkspaceTab) {
        guard model.canRemoveWorktree(tab) else { return }
        let q = model.removeWorktreeQuestion(tab, warnBusy: CloseGuard.warnBusy)
        let alert = NSAlert()
        alert.messageText = q.title
        alert.informativeText = tab.git.worktree + "\n\n" + q.message
        if q.canRemove {
            alert.addButton(withTitle: "Remove Worktree")
            alert.addButton(withTitle: "Cancel")
            alert.buttons[0].hasDestructiveAction = true
        } else {
            alert.addButton(withTitle: "OK")
        }
        let (id, force) = (tab.id, tab.git.dirty)
        let done: (NSApplication.ModalResponse) -> Void = { r in
            guard q.canRemove, r == .alertFirstButtonReturn else { return }
            Task { await model.removeWorktree(id, force: force) }
        }
        if let w = NSApp.keyWindow { alert.beginSheetModal(for: w, completionHandler: done) } else { done(alert.runModal()) }
    }
}
