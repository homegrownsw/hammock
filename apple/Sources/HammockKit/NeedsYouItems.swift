import Foundation

extension AppModel {
    /// What waits on you on this Mac, as the iPhone's Needs You widget
    /// lists it (NeedsYouStore): each agent waiting for your input, named as
    /// its notification is (its tab, its Group, what it asks).
    public func needsYouItems(host: String) -> [NeedsYouItem] {
        attention.filter { $0.state == .needsInput }.map { a in
            let pane = panes[a.paneID]
            let tab = pane.flatMap { tab(of: $0) }
            let who = pane.map { $0.displayName.capitalized } ?? "Agent"
            let group = tab.flatMap { t in groups.first { $0.id == t.groupID } }
            let title = [tab?.name ?? "", tab?.autoName ?? "", pane?.title ?? ""].first { !$0.trimmingCharacters(in: .whitespaces).isEmpty } ?? who
            let place = group.map { "\($0.icon) \($0.name)".trimmingCharacters(in: .whitespaces) } ?? ""
            return NeedsYouItem(id: a.id, host: host, pane: a.paneID, title: title, place: place, ask: a.noticeBody(who: who),
                                at: Double(a.createdUnixMs) / 1000)
        }
    }
}
