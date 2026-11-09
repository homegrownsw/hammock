import SwiftUI

/// Groups' pictures: from .hammock/icon.png in a Group's folder, served
/// small by the daemon and fetched once per change (the Group has its hash).
extension AppModel {
    /// The Group's picture (PNG), if it has one and it's here yet.
    public func groupPicture(_ g: WorkspaceGroup) -> Data? {
        g.iconImage.isEmpty ? nil : groupPictures[g.iconImage]
    }

    /// The Group's picture decoded, the same image each time it's asked
    /// for: a new one per render looks like a change to SwiftUI, which
    /// crossfades it in any animation (collapsing a Group, say).
    func groupImage(_ g: WorkspaceGroup) -> PlatformImage? {
        guard let data = groupPicture(g) else { return nil }
        if let img = decodedPictures[g.iconImage] { return img }
        let img = PlatformImage(data: data)
        decodedPictures[g.iconImage] = img
        return img
    }

    /// The Group's icon for a menu item, as the sidebar shows it: its emoji,
    /// else its picture (small, rounded, in its own colours: menus draw images
    /// as one-colour templates unless told not to), else the default symbol.
    /// Every Group has one, so their names line up.
    public func groupMenuIcon(_ g: WorkspaceGroup) -> Image {
        if !g.icon.isEmpty, let e = emojiMenuImage(g.icon) { return Self.image(e) }
        if let p = groupMenuImage(g) { return Self.image(p) }
        return Image(systemName: "square.stack.3d.up")
    }

    private static func image(_ i: PlatformImage) -> Image {
        #if os(macOS)
        Image(nsImage: i)
        #else
        Image(uiImage: i)
        #endif
    }

    /// An emoji drawn at a menu icon's size.
    private func emojiMenuImage(_ emoji: String) -> PlatformImage? {
        let key = "menu-emoji|" + emoji
        if let icon = decodedPictures[key] { return icon }
        #if os(macOS)
        let side: CGFloat = 16
        let text = NSAttributedString(string: emoji, attributes: [.font: NSFont.systemFont(ofSize: side * 0.85)])
        let icon = NSImage(size: NSSize(width: side, height: side), flipped: false) { r in
            let s = text.size()
            text.draw(at: NSPoint(x: (r.width - s.width) / 2, y: (r.height - s.height) / 2))
            return true
        }
        icon.isTemplate = false
        #else
        let side: CGFloat = 22
        let text = NSAttributedString(string: emoji, attributes: [.font: UIFont.systemFont(ofSize: side * 0.82)])
        let icon = UIGraphicsImageRenderer(size: CGSize(width: side, height: side)).image { _ in
            let s = text.size()
            text.draw(at: CGPoint(x: (side - s.width) / 2, y: (side - s.height) / 2))
        }.withRenderingMode(.alwaysOriginal)
        #endif
        decodedPictures[key] = icon
        return icon
    }

    private func groupMenuImage(_ g: WorkspaceGroup) -> PlatformImage? {
        guard let img = groupImage(g) else {
            // Not fetched yet (nothing on screen showed this Group): fetch it,
            // for the next time the menu opens.
            if !g.iconImage.isEmpty { Task { await loadGroupPicture(g) } }
            return nil
        }
        let key = "menu|" + g.iconImage
        if let icon = decodedPictures[key] { return icon }
        #if os(macOS)
        let side: CGFloat = 16
        let icon = NSImage(size: NSSize(width: side, height: side), flipped: false) { r in
            NSBezierPath(roundedRect: r, xRadius: side * 0.22, yRadius: side * 0.22).addClip()
            img.draw(in: r)
            return true
        }
        icon.isTemplate = false
        #else
        let side: CGFloat = 22
        let icon = UIGraphicsImageRenderer(size: CGSize(width: side, height: side)).image { _ in
            let r = CGRect(x: 0, y: 0, width: side, height: side)
            UIBezierPath(roundedRect: r, cornerRadius: side * 0.22).addClip()
            img.draw(in: r)
        }.withRenderingMode(.alwaysOriginal)
        #endif
        decodedPictures[key] = icon
        return icon
    }

    /// Fetches the Group's picture if it has one not fetched yet. The fetch
    /// is the model's, not the asking view's: views come and go (a row
    /// redrawn), and one going mid-fetch cancelled it while the others,
    /// seeing it under way, had stopped asking, so the picture never came.
    /// Every view showing it waits for the one fetch.
    public func loadGroupPicture(_ g: WorkspaceGroup) async {
        let hash = g.iconImage, id = g.id
        guard !hash.isEmpty, groupPictures[hash] == nil else { return }
        if let t = pictureLoads[hash] {
            await t.value
            return
        }
        let t = Task { [weak self] in
            // Tries for a while: a fetch made as the daemon restarts (the app
            // replacing an older one at launch) fails.
            for attempt in 0..<8 {
                if attempt > 0 { try? await Task.sleep(for: .seconds(Double(min(attempt, 5)))) }
                guard let self else { return }
                if let r = try? await self.connection?.call({ $0.readGroupIcon = .with { $0.groupID = id } }), !r.groupIcon.png.isEmpty {
                    self.groupPictures[hash] = r.groupIcon.png
                    if r.groupIcon.hash != hash { self.groupPictures[r.groupIcon.hash] = r.groupIcon.png }
                    return
                }
            }
        }
        pictureLoads[hash] = t
        await t.value
        // Done (or given up on: a view showing it asks again on reconnecting).
        pictureLoads[hash] = nil
    }

    /// Sets the Group's picture (PNG or JPEG), saved in its folder as
    /// .hammock/icon.png; nil removes it. Says why if it couldn't.
    @discardableResult
    public func setGroupPicture(_ id: String, _ data: Data?) async -> Bool {
        let ok = await run { $0.setGroupIconImage = .with { $0.groupID = id; $0.image = data ?? Data() } } != nil
        if !ok { showToast("Couldn't set the picture: \(lastError ?? "unknown error")") }
        return ok
    }
}

/// A Group's icon: its emoji if it has one, else its folder's picture, else
/// the default symbol (or what fallback draws).
public struct GroupIconView<Fallback: View>: View {
    let model: AppModel
    let group: WorkspaceGroup
    let size: CGFloat
    let fallback: Fallback

    public init(model: AppModel, group: WorkspaceGroup, size: CGFloat, @ViewBuilder fallback: () -> Fallback) {
        self.model = model
        self.group = group
        self.size = size
        self.fallback = fallback()
    }

    public var body: some View {
        Group {
            if !group.icon.isEmpty {
                Text(group.icon)
            } else if let img = model.groupImage(group) {
                #if os(macOS)
                Image(nsImage: img).resizable().interpolation(.high).scaledToFit()
                    .frame(width: size, height: size)
                    .clipShape(RoundedRectangle(cornerRadius: size * 0.22, style: .continuous))
                #else
                Image(uiImage: img).resizable().interpolation(.high).scaledToFit()
                    .frame(width: size, height: size)
                    .clipShape(RoundedRectangle(cornerRadius: size * 0.22, style: .continuous))
                #endif
            } else if !group.iconImage.isEmpty {
                // Coming: hold its place (and give the fetch below a view to
                // run on, which an EmptyView fallback wouldn't be).
                Color.clear.frame(width: size, height: size)
            } else {
                fallback
            }
        }
        // Again on reconnecting, for any that didn't come.
        .task(id: group.iconImage + (model.connected ? "+" : "-")) { await model.loadGroupPicture(group) }
    }
}

extension GroupIconView where Fallback == AnyView {
    public init(model: AppModel, group: WorkspaceGroup, size: CGFloat) {
        self.init(model: model, group: group, size: size) {
            AnyView(Image(systemName: "square.stack.3d.up").foregroundStyle(.tint))
        }
    }
}
