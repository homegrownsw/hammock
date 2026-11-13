import AVFoundation
import AVKit
import CryptoKit
import SwiftUI
#if os(macOS)
import AppKit
import Quartz
#else
import UIKit
#endif

// Attachments as the agent view shows them: the [#1] chip in the text (the
// composer's and your sent messages'), the tiles above a sent message with
// their number, a video's first frame, and on the Mac, QuickLook.

// MARK: - Chips

/// An attachment's chip: its thumbnail and number, as a token in text.
struct ChipLabel: View {
    let number: Int
    let thumbnail: PlatformImage?
    let isVideo: Bool
    /// In a sent message's bubble (white on its colour), not the composer.
    let onBubble: Bool
    let fontSize: CGFloat
    var accent: Color = .accentColor

    var body: some View {
        HStack(spacing: 3) {
            Group {
                if let thumbnail {
                    #if os(macOS)
                    Image(nsImage: thumbnail).resizable().scaledToFill()
                    #else
                    Image(uiImage: thumbnail).resizable().scaledToFill()
                    #endif
                } else {
                    Image(systemName: isVideo ? "film" : "photo")
                        .font(.system(size: fontSize * 0.7))
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .frame(width: fontSize * 1.05, height: fontSize * 1.05)
            .clipShape(RoundedRectangle(cornerRadius: 3, style: .continuous))
            .overlay(alignment: .center) {
                if isVideo, thumbnail != nil {
                    Image(systemName: "play.fill").font(.system(size: fontSize * 0.45)).foregroundStyle(.white).shadow(radius: 1)
                }
            }
            Text("#\(number)")
                .font(.system(size: fontSize * 0.82, weight: .semibold))
                .monospacedDigit()
        }
        .padding(.leading, 2).padding(.trailing, 5).padding(.vertical, 2)
        .background(RoundedRectangle(cornerRadius: 5, style: .continuous).fill(onBubble ? Color.white.opacity(0.24) : accent.opacity(0.18)))
        .foregroundStyle(onBubble ? Color.white : Color.primary)
        .fixedSize()
    }
}

enum ChipRenderer {
    private static var cache: [String: PlatformImage] = [:]

    /// The chip as a picture, to sit in text (an attachment in the composer's
    /// text view, an Image in a Text). Cached by everything it shows.
    @MainActor static func image(number: Int, thumbnail: PlatformImage?, thumbKey: String, isVideo: Bool, onBubble: Bool,
                                 fontSize: CGFloat, accent: Color, dark: Bool) -> PlatformImage? {
        let key = "\(number)|\(thumbKey)|\(thumbnail != nil)|\(isVideo)|\(onBubble)|\(fontSize)|\(accent.description)|\(dark)"
        if let i = cache[key] { return i }
        let r = ImageRenderer(content: ChipLabel(number: number, thumbnail: thumbnail, isVideo: isVideo, onBubble: onBubble, fontSize: fontSize, accent: accent)
            .environment(\.colorScheme, dark ? .dark : .light))
        #if os(macOS)
        r.scale = NSScreen.main?.backingScaleFactor ?? 2
        let img = r.nsImage
        #else
        r.scale = 3
        let img = r.uiImage
        #endif
        if cache.count > 400 { cache.removeAll() }
        cache[key] = img
        return img
    }
}

// MARK: - Video

enum VideoPoster {
    /// A video's first frame.
    static func image(data: Data, name: String, maxPixels: Int = 512) async -> PlatformImage? {
        guard let url = PreviewFiles.write(data, name: name) else { return nil }
        return await image(url: url, maxPixels: maxPixels)
    }

    static func image(url: URL, maxPixels: Int = 512) async -> PlatformImage? {
        let gen = AVAssetImageGenerator(asset: AVURLAsset(url: url))
        gen.appliesPreferredTrackTransform = true
        gen.maximumSize = CGSize(width: maxPixels, height: maxPixels)
        guard let (cg, _) = try? await gen.image(at: .zero) else { return nil }
        return platformImage(cg)
    }
}

/// Files written to show or play something (QuickLook, a video): in the
/// app's caches, named by their content, so the same thing is written once.
/// Swept a day after they were made.
enum PreviewFiles {
    static var dir: URL {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        return caches.appendingPathComponent(Bundle.main.bundleIdentifier ?? "Hammock", isDirectory: true)
            .appendingPathComponent("previews", isDirectory: true)
    }

    static func write(_ data: Data, name: String) -> URL? {
        let hash = SHA256.hash(data: data).prefix(12).map { String(format: "%02x", $0) }.joined()
        let ext = (name as NSString).pathExtension.isEmpty ? "bin" : (name as NSString).pathExtension
        let base = ((name as NSString).deletingPathExtension as String).filter { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" }
        let url = dir.appendingPathComponent("\(base.isEmpty ? "attachment" : base)-\(hash).\(ext)")
        if FileManager.default.fileExists(atPath: url.path) { return url }
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return (try? data.write(to: url, options: .atomic)) != nil ? url : nil
    }

    static func sweep() {
        let fm = FileManager.default
        for f in (try? fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: [.creationDateKey])) ?? [] {
            let made = (try? f.resourceValues(forKeys: [.creationDateKey]))?.creationDate ?? .distantPast
            if Date.now.timeIntervalSince(made) > 86400 { try? fm.removeItem(at: f) }
        }
    }

    /// A file name's extension for a media type.
    static func ext(_ mediaType: String) -> String {
        switch mediaType {
        case "image/png": "png"
        case "image/jpeg": "jpg"
        case "image/gif": "gif"
        case "image/webp": "webp"
        case "video/mp4": "mp4"
        case "video/quicktime": "mov"
        default: "bin"
        }
    }
}

// MARK: - Opening

/// Something a click on an attachment can open, with those beside it (a
/// message's other attachments, the tray's) to go through.
struct PreviewItem {
    let key: String
    /// A file name for it (its extension says what it is).
    let name: String
    let load: @MainActor () async -> Data?
}

private struct PreviewGroupKey: EnvironmentKey { static let defaultValue: [PreviewItem]? = nil }

extension EnvironmentValues {
    /// The attachments a click on one opens among (QuickLook's arrows).
    var previewGroup: [PreviewItem]? {
        get { self[PreviewGroupKey.self] }
        set { self[PreviewGroupKey.self] = newValue }
    }
}

#if os(macOS)
/// QuickLook's panel, on files written for it.
@MainActor
final class QuickLook: NSObject, QLPreviewPanelDataSource, QLPreviewPanelDelegate {
    static let shared = QuickLook()
    private var urls: [URL] = []

    /// Opens the item at index among items (each loaded and written first).
    static func open(_ items: [PreviewItem], at index: Int) async {
        var urls: [URL] = []
        var at = 0
        for (i, item) in items.enumerated() {
            guard let d = await item.load(), let url = PreviewFiles.write(d, name: item.name) else { continue }
            if i == index { at = urls.count }
            urls.append(url)
        }
        guard !urls.isEmpty else { return }
        shared.show(urls, at: at)
    }

    func show(_ urls: [URL], at index: Int) {
        self.urls = urls
        guard let panel = QLPreviewPanel.shared() else { return }
        panel.dataSource = self
        panel.delegate = self
        panel.reloadData()
        panel.currentPreviewItemIndex = index
        if panel.isVisible { panel.refreshCurrentPreviewItem() } else { panel.makeKeyAndOrderFront(nil) }
    }

    nonisolated func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int {
        MainActor.assumeIsolated { urls.count }
    }

    nonisolated func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> (any QLPreviewItem)! {
        MainActor.assumeIsolated { urls.indices.contains(index) ? urls[index] as NSURL : nil }
    }
}
#endif

/// Opens an attachment: QuickLook on the Mac (among its group), the
/// viewer on the iPhone.
@MainActor
func openAttachment(_ item: PreviewItem, group: [PreviewItem]?, image: PlatformImage?, video: Bool, open: ((ViewedImage) -> Void)?) {
    #if os(macOS)
    let items = group ?? [item]
    let i = items.firstIndex { $0.key == item.key } ?? 0
    Task { await QuickLook.open(items, at: i) }
    #else
    Task {
        if video {
            guard let d = await item.load(), let url = PreviewFiles.write(d, name: item.name) else { return }
            open?(ViewedImage(ref: item.key, image: image ?? PlatformImage(), video: url))
        } else if let image {
            open?(ViewedImage(ref: item.key, image: image))
        }
    }
    #endif
}

// MARK: - Sent messages

/// One of a sent message's attachments: from the transcript, or still on
/// its way (the composer's own data).
enum BubbleAttachment: Identifiable {
    case remote(Hammock_V1_AgentImage)
    case local(id: UUID, name: String, data: Data)

    var id: String {
        switch self {
        case .remote(let i): i.ref
        case .local(let id, _, _): id.uuidString
        }
    }

    var isVideo: Bool {
        switch self {
        case .remote(let i): i.mediaType.hasPrefix("video/")
        case .local(_, let name, _): isVideoName(name)
        }
    }

    var name: String {
        switch self {
        case .remote(let i):
            let last = (i.ref as NSString).lastPathComponent
            return (last as NSString).pathExtension.isEmpty ? "attachment." + PreviewFiles.ext(i.mediaType) : last
        case .local(_, let name, _): return name
        }
    }

    @MainActor func preview(_ session: AgentSession) -> PreviewItem {
        switch self {
        case .remote(let i): PreviewItem(key: i.ref, name: name) { await session.file(i.ref) }
        case .local(let id, let name, let data): PreviewItem(key: id.uuidString, name: name) { data }
        }
    }

    @MainActor func thumbnail(_ session: AgentSession) async -> PlatformImage? {
        switch self {
        case .remote(let i):
            if i.mediaType.hasPrefix("video/") {
                guard let d = await session.file(i.ref) else { return nil }
                return await VideoPoster.image(data: d, name: name, maxPixels: 128)
            }
            return await session.image(i.ref)
        case .local(_, let name, let data):
            return await ImageResize.thumbnail(data, name: name, maxPixels: 128)
        }
    }
}

/// A sent message's attachment above its bubble, with its number.
struct AttachmentTile: View {
    let attachment: BubbleAttachment
    let number: Int
    let session: AgentSession
    var maxHeight: CGFloat = 220

    var body: some View {
        Group {
            switch attachment {
            case .remote(let i):
                AgentImageView(ref: i.ref, session: session, maxHeight: maxHeight, mediaType: i.mediaType)
            case .local(let id, let name, let data):
                LocalAttachmentView(id: id, name: name, data: data, maxHeight: maxHeight)
            }
        }
        .overlay(alignment: .bottomLeading) { NumberBadge(number: number).padding(5).allowsHitTesting(false) }
    }
}

/// An attachment's number, small, over its picture.
struct NumberBadge: View {
    let number: Int
    var body: some View {
        Text("#\(number)")
            .font(.caption2.weight(.semibold))
            .monospacedDigit()
            .foregroundStyle(.white)
            .padding(.horizontal, 5).padding(.vertical, 2)
            .background(.black.opacity(0.42), in: Capsule())
    }
}

/// An attachment sent from here, shown from its own data until the
/// transcript has it.
struct LocalAttachmentView: View {
    let id: UUID
    let name: String
    let data: Data
    var maxHeight: CGFloat = 220
    @State private var image: PlatformImage?
    @Environment(\.openAgentImage) private var open
    @Environment(\.previewGroup) private var group

    var body: some View {
        Group {
            if let image {
                #if os(macOS)
                Image(nsImage: image).resizable().scaledToFit()
                #else
                Image(uiImage: image).resizable().scaledToFit()
                #endif
            } else {
                RoundedRectangle(cornerRadius: 10).fill(.quaternary).frame(width: 120, height: 80)
            }
        }
        .frame(maxHeight: maxHeight)
        .overlay { if isVideoName(name) { PlayBadge() } }
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .task(id: id) { image = await ImageResize.thumbnail(data, name: name, maxPixels: 800) }
        .contentShape(Rectangle())
        .onTapGesture {
            openAttachment(PreviewItem(key: id.uuidString, name: name) { data }, group: group, image: image, video: isVideoName(name), open: open)
        }
    }
}

/// A video's play button, over its first frame.
struct PlayBadge: View {
    var body: some View {
        Image(systemName: "play.fill")
            .font(.title3)
            .foregroundStyle(.white)
            .frame(width: 40, height: 40)
            .background(.black.opacity(0.45), in: Circle())
            .allowsHitTesting(false)
    }
}

/// A sent message's text with its attachments' chips where they were.
struct RichBubbleText: View {
    let rich: String
    let attachments: [BubbleAttachment]
    let session: AgentSession
    @Environment(\.agentFonts) private var fonts
    @Environment(\.colorScheme) private var scheme
    @State private var thumbs: [String: PlatformImage] = [:]

    var body: some View {
        text
            .task(id: attachments.map(\.id)) {
                for a in attachments where thumbs[a.id] == nil {
                    if let t = await a.thumbnail(session) { thumbs[a.id] = t }
                }
            }
    }

    private var text: Text {
        let parts = rich.split(separator: attachMark, omittingEmptySubsequences: false)
        var s = LocalizedStringKey.StringInterpolation(literalCapacity: 0, interpolationCount: parts.count * 2)
        for (k, part) in parts.enumerated() {
            if !part.isEmpty { s.appendInterpolation(Text(Markdown.inline(String(part)))) }
            guard k < parts.count - 1 else { continue }
            if k < attachments.count {
                let a = attachments[k]
                if let img = ChipRenderer.image(number: k + 1, thumbnail: thumbs[a.id], thumbKey: a.id, isVideo: a.isVideo, onBubble: true,
                                                fontSize: CGFloat(fonts.size), accent: .white, dark: scheme == .dark) {
                    #if os(macOS)
                    s.appendInterpolation(Text(Image(nsImage: img)).baselineOffset(-CGFloat(fonts.size) * 0.22))
                    #else
                    s.appendInterpolation(Text(Image(uiImage: img)).baselineOffset(-CGFloat(fonts.size) * 0.22))
                    #endif
                }
            }
        }
        return Text(LocalizedStringKey(stringInterpolation: s))
    }
}
