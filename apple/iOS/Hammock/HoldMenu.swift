import HammockKit
import SwiftUI
import UIKit

// MARK: Menu items

/// A menu's items, said once for SwiftUI's menus (a pill's, an Exposé
/// tile's, a pane's … menu: MenuEntries) and UIKit's (a row's touch and
/// hold: HoldMenu), so the two can't drift apart.
struct MenuEntry {
    enum Icon {
        case symbol(String)
        case picture(UIImage)

        var image: UIImage? {
            switch self {
            case let .symbol(name): UIImage(systemName: name)
            case let .picture(image): image
            }
        }
    }

    enum Kind {
        case action(() -> Void)
        /// Its items, between separators.
        case section([MenuEntry])
        case submenu([MenuEntry])
        case divider
    }

    var title = ""
    var icon: Icon?
    var kind: Kind
    var destructive = false
    /// Off, and everything in it.
    var isDisabled = false

    static func button(_ title: String, systemImage: String, destructive: Bool = false, _ run: @escaping () -> Void) -> MenuEntry {
        MenuEntry(title: title, icon: .symbol(systemImage), kind: .action(run), destructive: destructive)
    }

    static func section(_ items: [MenuEntry]) -> MenuEntry { MenuEntry(kind: .section(items)) }

    static func submenu(_ title: String, systemImage: String, _ items: [MenuEntry]) -> MenuEntry {
        MenuEntry(title: title, icon: .symbol(systemImage), kind: .submenu(items))
    }

    static let divider = MenuEntry(kind: .divider)

    func disabled(_ off: Bool) -> MenuEntry {
        var e = self
        e.isDisabled = e.isDisabled || off
        return e
    }

    /// As UIKit's menu elements: sections and dividers as inline menus.
    static func uiElements(_ entries: [MenuEntry], disabled: Bool = false) -> [UIMenuElement] {
        var groups: [[UIMenuElement]] = [[]]
        for e in entries {
            let off = disabled || e.isDisabled
            switch e.kind {
            case .divider:
                groups.append([])
            case let .action(run):
                let a = UIAction(title: e.title, image: e.icon?.image) { _ in run() }
                if e.destructive { a.attributes.insert(.destructive) }
                if off { a.attributes.insert(.disabled) }
                groups[groups.count - 1].append(a)
            case let .section(items):
                let children = uiElements(items, disabled: off)
                if !children.isEmpty { groups[groups.count - 1].append(UIMenu(options: .displayInline, children: children)) }
            case let .submenu(items):
                groups[groups.count - 1].append(UIMenu(title: e.title, image: e.icon?.image, children: uiElements(items, disabled: off)))
            }
        }
        let shown = groups.filter { !$0.isEmpty }
        return shown.count <= 1 ? shown.first ?? [] : shown.map { UIMenu(options: .displayInline, children: $0) }
    }

    /// Its actions that can be done now, submenus' too, for VoiceOver.
    static func enabledActions(_ entries: [MenuEntry]) -> [(title: String, run: () -> Void)] {
        entries.flatMap { e -> [(title: String, run: () -> Void)] in
            guard !e.isDisabled else { return [] }
            switch e.kind {
            case let .action(run): return [(e.title, run)]
            case let .section(items), let .submenu(items): return enabledActions(items)
            case .divider: return []
            }
        }
    }
}

/// Menu items in a SwiftUI menu.
struct MenuEntries: View {
    let entries: [MenuEntry]

    var body: some View {
        ForEach(Array(entries.enumerated()), id: \.offset) { _, e in MenuEntryView(entry: e) }
    }
}

private struct MenuEntryView: View {
    let entry: MenuEntry

    var body: some View {
        switch entry.kind {
        case let .action(run):
            Button(role: entry.destructive ? .destructive : nil, action: run) { label }
                .disabled(entry.isDisabled)
        case let .section(items):
            Section { MenuEntries(entries: items) }
                .disabled(entry.isDisabled)
        case let .submenu(items):
            Menu { MenuEntries(entries: items) } label: { label }
                .disabled(entry.isDisabled)
        case .divider:
            Divider()
        }
    }

    @ViewBuilder private var label: some View {
        switch entry.icon {
        case let .symbol(name): Label(entry.title, systemImage: name)
        case let .picture(image): Label { Text(entry.title) } icon: { Image(uiImage: image) }
        case nil: Text(entry.title)
        }
    }
}

// MARK: Touch and hold

/// A row's touch and hold (Sessions, Needs You's cards, Groups' tabs): its
/// pane's preview, large and live, over its menu; tap the preview to open
/// it, as Mail and Messages do. Without a pane to show, the menu alone.
///
/// UIKit's context menu rather than SwiftUI's, whose preview can't be
/// tapped: a UIContextMenuInteraction on the row's List cell, put there by
/// a view behind the row that takes no touches, so the List's tap and
/// swipes and the row's own buttons work as they did.
struct HoldMenu: ViewModifier {
    let model: AppModel
    let paneID: String?
    let open: () -> Void
    /// Its items, made as it opens.
    let menu: () -> [MenuEntry]
    @Environment(\.phoneTheme) private var theme

    func body(content: Content) -> some View {
        content
            .background {
                HoldMenuAnchor(model: model, paneID: paneID, theme: theme, open: open, menu: menu)
                    .allowsHitTesting(false)
                    .accessibilityHidden(true)
            }
            // VoiceOver's way to the menu, as SwiftUI's context menu had.
            .accessibilityActions {
                ForEach(Array(MenuEntry.enabledActions(menu()).enumerated()), id: \.offset) { _, a in
                    Button(a.title, action: a.run)
                }
            }
    }
}

private struct HoldMenuAnchor: UIViewRepresentable {
    let model: AppModel
    let paneID: String?
    let theme: PhoneTheme
    let open: () -> Void
    let menu: () -> [MenuEntry]

    func makeCoordinator() -> HoldMenuCoordinator { HoldMenuCoordinator() }

    func makeUIView(context: Context) -> HoldMenuAnchorView {
        let v = HoldMenuAnchorView()
        v.isUserInteractionEnabled = false
        v.coordinator = context.coordinator
        update(context.coordinator)
        return v
    }

    func updateUIView(_ v: HoldMenuAnchorView, context: Context) {
        update(context.coordinator)
        if v.window != nil { context.coordinator.attach(near: v) }
    }

    static func dismantleUIView(_ v: HoldMenuAnchorView, coordinator: HoldMenuCoordinator) {
        coordinator.detach()
    }

    private func update(_ c: HoldMenuCoordinator) {
        c.model = model
        c.paneID = paneID
        c.theme = theme
        c.open = open
        c.menu = menu
    }
}

final class HoldMenuAnchorView: UIView {
    weak var coordinator: HoldMenuCoordinator?

    override func didMoveToWindow() {
        super.didMoveToWindow()
        if window != nil { coordinator?.attach(near: self) }
    }
}

/// The interaction itself, on the row's cell (or, outside a List, the row).
final class HoldMenuCoordinator: NSObject, UIContextMenuInteractionDelegate {
    var model: AppModel?
    var paneID: String?
    var theme = PhoneTheme.hammock
    var open: () -> Void = {}
    var menu: () -> [MenuEntry] = { [] }
    private lazy var interaction = UIContextMenuInteraction(delegate: self)
    private weak var host: UIView?
    /// The preview up now, holding its agent's watch until it goes.
    private var preview: HoldPreviewController?

    /// Puts the interaction on the cell the anchor's in: one per cell, the
    /// latest row's (a reused cell's earlier row may not have let go yet).
    func attach(near anchor: UIView) {
        var v = anchor.superview
        while let s = v, !(s is UICollectionViewCell) { v = s.superview }
        guard let target = v ?? anchor.superview, target !== host || interaction.view !== target else { return }
        detach()
        for i in target.interactions where (i as? UIContextMenuInteraction)?.delegate is HoldMenuCoordinator {
            target.removeInteraction(i)
        }
        target.addInteraction(interaction)
        host = target
    }

    func detach() {
        if let host, interaction.view === host { host.removeInteraction(interaction) }
        host = nil
    }

    func contextMenuInteraction(_ interaction: UIContextMenuInteraction, configurationForMenuAtLocation location: CGPoint) -> UIContextMenuConfiguration? {
        guard let model else { return nil }
        let shown = paneID.flatMap { model.panes[$0] != nil ? $0 : nil }
        let style = host?.traitCollection.userInterfaceStyle ?? .unspecified
        let theme = theme
        let preview: UIContextMenuContentPreviewProvider? = shown.map { id in
            { [weak self] in
                let c = HoldPreviewController(model: model, paneID: id, theme: theme)
                c.overrideUserInterfaceStyle = style
                self?.preview = c
                return c
            }
        }
        return UIContextMenuConfiguration(identifier: nil, previewProvider: preview) { [weak self] _ in
            UIMenu(children: MenuEntry.uiElements(self?.menu() ?? []))
        }
    }

    /// Tapping the preview opens its pane, once the menu's gone.
    func contextMenuInteraction(_ interaction: UIContextMenuInteraction, willPerformPreviewActionForMenuWith configuration: UIContextMenuConfiguration,
                                animator: UIContextMenuInteractionCommitAnimating) {
        guard preview != nil else { return }
        animator.preferredCommitStyle = .dismiss
        let open = open
        animator.addCompletion { open() }
    }

    func contextMenuInteraction(_ interaction: UIContextMenuInteraction, willEndFor configuration: UIContextMenuConfiguration,
                                animator: UIContextMenuInteractionAnimating?) {
        let done = { [weak self] in
            self?.preview?.letGo()
            self?.preview = nil
        }
        if let animator { animator.addCompletion(done) } else { done() }
    }
}

/// A touch and hold's preview (HoldMenu): the pane's tile, phone-wide and
/// shaped like the pane (HoldPreview), drawn and never driven: its terminal
/// sends no size or keys (TerminalRepresentable's preview). An agent's pane
/// that the phone shows as its conversation shows its latest instead,
/// watching the agent (or sharing a watch already held) until the preview
/// goes.
final class HoldPreviewController: UIHostingController<AnyView> {
    private var lease: AgentLease?

    init(model: AppModel, paneID: String, theme: PhoneTheme) {
        let p = model.panes[paneID]
        let lease = p.flatMap { model.showsAgentTile($0) ? AgentLease(model, paneID) : nil }
        let size = Self.size(of: p)
        super.init(rootView: AnyView(
            LiveTile(model: model, paneID: paneID, current: false, roomy: true, agent: lease?.session)
                .frame(width: size.width, height: size.height)
                .phoneTheme(theme)
        ))
        self.lease = lease
        preferredContentSize = size
        view.backgroundColor = .secondarySystemGroupedBackground
    }

    @available(*, unavailable)
    @MainActor required dynamic init?(coder: NSCoder) { fatalError() }

    /// The preview's gone: its agent's watch goes too (kept a while by the
    /// model, as any other).
    func letGo() { lease = nil }

    static func size(of p: Pane?) -> CGSize {
        let grid = p.map { ![.browser, .simulator, .editor, .diff].contains($0.kind) } ?? false
        let screen = UIApplication.shared.activeScreen?.bounds.size ?? CGSize(width: 390, height: 844)
        return HoldPreview.size(room: CGSize(width: screen.width - 32, height: screen.height),
                                cols: grid ? Int(p?.cols ?? 0) : 0, rows: grid ? Int(p?.rows ?? 0) : 0)
    }
}

#if DEBUG
/// Dev aid: HMK_DEBUG_HOLD_PREVIEW=<pane id> shows that pane's touch-and-hold
/// preview over Sessions, as the menu would, to look at without a hold.
struct HoldPreviewDebug: View {
    let model: AppModel
    let paneID: String
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        let size = HoldPreviewController.size(of: model.panes[paneID])
        ZStack {
            Rectangle().fill(.ultraThinMaterial).ignoresSafeArea()
            Controller(model: model, paneID: paneID, theme: theme)
                .frame(width: size.width, height: size.height)
                .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
        }
    }

    private struct Controller: UIViewControllerRepresentable {
        let model: AppModel
        let paneID: String
        let theme: PhoneTheme

        func makeUIViewController(context: Context) -> HoldPreviewController {
            HoldPreviewController(model: model, paneID: paneID, theme: theme)
        }

        func updateUIViewController(_ c: HoldPreviewController, context: Context) {}
    }
}
#endif
