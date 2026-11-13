import Foundation
import ImageIO
import SwiftUI
import UniformTypeIdentifiers
#if os(macOS)
import AppKit
#else
import UIKit
#endif

// The composer's attachments: numbered, each with its chip in the text
// (deleting the chip greys it out; it stays in the tray, to put back), the
// size its picture is sent at, and kept with the draft until it's sent.

/// Where an attachment sits in the composer's text and a sent message's
/// (AgentBlock.richText): the object replacement character.
let attachMark: Character = "\u{FFFC}"

// MARK: - Sizes

/// How big a picture is sent: its long edge at most this, never made bigger.
/// Claude scales anything over 1568 down itself, so Large loses nothing.
public enum AttachmentSize: String, CaseIterable, Identifiable, Sendable {
    case original, large, medium, small

    public var id: String { rawValue }

    /// The long edge's limit, in pixels (nil: as it is).
    public var maxEdge: Int? {
        switch self {
        case .original: nil
        case .large: 1568
        case .medium: 1024
        case .small: 768
        }
    }

    public var label: String {
        switch self {
        case .original: "Original"
        case .large: "Large (1568 px)"
        case .medium: "Medium (1024 px)"
        case .small: "Small (768 px)"
        }
    }

    /// Its name, short: for a menu that shows the size it comes to beside.
    public var name: String {
        switch self {
        case .original: "Original"
        case .large: "Large"
        case .medium: "Medium"
        case .small: "Small"
        }
    }

    /// A picture this many pixels, sent at this size.
    func scaled(_ s: CGSize) -> CGSize {
        guard let m = maxEdge.map(CGFloat.init), max(s.width, s.height) > m else { return s }
        let k = m / max(s.width, s.height)
        return CGSize(width: (s.width * k).rounded(), height: (s.height * k).rounded())
    }
}

extension AgentViewPrefs {
    public static let imageSize = "agent.imageSize"
    public static let imageSizeRemote = "agent.imageSizeRemote"
}

extension Pref {
    /// The size pictures are sent at to agents on this Mac.
    public static let agentImageSize = Pref("agent-image-size", defaults: AgentViewPrefs.imageSize, .string, AttachmentSize.large.rawValue)
    /// …and over the network: to agents on a linked computer, and everything
    /// the iPhone sends.
    public static let agentImageSizeRemote = Pref("agent-image-size-remote", defaults: AgentViewPrefs.imageSizeRemote, .string, AttachmentSize.medium.rawValue)
}

// MARK: - An attachment

struct ComposerAttachment: Identifiable {
    let id: UUID
    /// Its number in the message: #1, #2…, kept while it's greyed out.
    var number: Int
    var name: String
    var data: Data
    /// Chosen for this one; nil: the default.
    var size: AttachmentSize?
    /// Its pixels, read once (nil: not a picture).
    let pixels: CGSize?

    init(id: UUID = UUID(), number: Int, name: String, data: Data, size: AttachmentSize? = nil) {
        self.id = id
        self.number = number
        self.name = name
        self.data = data
        self.size = size
        pixels = isVideoName(name) ? nil : ImageResize.pixelSize(data)
    }

    var isVideo: Bool { isVideoName(name) }
}

func isVideoName(_ name: String) -> Bool {
    let ext = (name as NSString).pathExtension.lowercased()
    return ["mp4", "mov", "m4v"].contains(ext)
}

/// The composer's text and the chips in it: the k-th mark in text is chips[k].
struct ComposerDoc: Equatable, Codable {
    var text = ""
    var chips: [UUID] = []

    var isBlank: Bool { text.allSatisfy { $0.isWhitespace || $0 == attachMark } && chips.isEmpty }

    /// The text without its chips (as the transcript will show it, and to
    /// match it there).
    var plain: String {
        text.split(separator: attachMark, omittingEmptySubsequences: false)
            .map { String($0) }.joined(separator: " ")
            .split(separator: " ", omittingEmptySubsequences: true).joined(separator: " ")
    }

    init(text: String = "", chips: [UUID] = []) {
        self.text = text
        self.chips = chips
    }

    /// Plain text: any marks in it (an old draft) dropped.
    init(plain: String) {
        text = plain.filter { $0 != attachMark }
    }
}

// MARK: - Resizing

enum ImageResize {
    /// A picture's size in pixels, from its header (oriented).
    static func pixelSize(_ data: Data) -> CGSize? {
        guard let src = CGImageSourceCreateWithData(data as CFData, nil),
              let p = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any],
              let w = p[kCGImagePropertyPixelWidth] as? Int, let h = p[kCGImagePropertyPixelHeight] as? Int else { return nil }
        let o = p[kCGImagePropertyOrientation] as? Int ?? 1
        return o >= 5 ? CGSize(width: h, height: w) : CGSize(width: w, height: h)
    }

    /// The picture with its long edge at most maxEdge: JPEG, or PNG while it
    /// has transparency. As it is when it's no bigger, or can't be read.
    static func resized(name: String, data: Data, maxEdge: Int?) -> (String, Data) {
        guard let maxEdge, let px = pixelSize(data), max(px.width, px.height) > CGFloat(maxEdge),
              let src = CGImageSourceCreateWithData(data as CFData, nil) else { return (name, data) }
        let opts: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxEdge,
        ]
        guard let img = CGImageSourceCreateThumbnailAtIndex(src, 0, opts as CFDictionary) else { return (name, data) }
        let alpha = ![.none, .noneSkipFirst, .noneSkipLast].contains(img.alphaInfo)
        let type = alpha ? UTType.png : UTType.jpeg
        let out = NSMutableData()
        guard let dst = CGImageDestinationCreateWithData(out, type.identifier as CFString, 1, nil) else { return (name, data) }
        CGImageDestinationAddImage(dst, img, [kCGImageDestinationLossyCompressionQuality: 0.85] as CFDictionary)
        guard CGImageDestinationFinalize(dst) else { return (name, data) }
        let base = (name as NSString).deletingPathExtension
        return (base + (alpha ? ".png" : ".jpg"), out as Data)
    }

    /// A small picture of an image or a video's first frame, for chips and
    /// the tray.
    static func thumbnail(_ data: Data, name: String, maxPixels: Int = 256) async -> PlatformImage? {
        if isVideoName(name) { return await VideoPoster.image(data: data, name: name, maxPixels: maxPixels) }
        return await Task.detached(priority: .userInitiated) {
            guard let src = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
            let opts: [CFString: Any] = [
                kCGImageSourceCreateThumbnailFromImageAlways: true,
                kCGImageSourceCreateThumbnailWithTransform: true,
                kCGImageSourceThumbnailMaxPixelSize: maxPixels,
            ]
            guard let cg = CGImageSourceCreateThumbnailAtIndex(src, 0, opts as CFDictionary) else { return nil }
            return platformImage(cg)
        }.value
    }
}

func platformImage(_ cg: CGImage) -> PlatformImage {
    #if os(macOS)
    NSImage(cgImage: cg, size: NSSize(width: cg.width, height: cg.height))
    #else
    UIImage(cgImage: cg)
    #endif
}

// MARK: - Drafts

/// A draft's attachments, kept with its text through leaving the pane and
/// the app quitting: each file and a manifest, in the app's caches, one
/// folder per pane. Gone once sent or taken off; a pane's folder is swept
/// when the pane's gone, and any after two weeks (pruneStale).
enum ComposerDrafts {
    struct Manifest: Codable {
        var doc: ComposerDoc
        var items: [Item]
        struct Item: Codable {
            var id: UUID
            var number: Int
            var name: String
            var size: String?
        }
    }

    static var root: URL {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        return caches.appendingPathComponent(Bundle.main.bundleIdentifier ?? "Hammock", isDirectory: true)
            .appendingPathComponent("composer-drafts", isDirectory: true)
    }

    static func dir(_ paneID: String) -> URL {
        let safe = paneID.map { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" ? $0 : "_" }
        return root.appendingPathComponent(String(safe), isDirectory: true)
    }

    private static func file(_ dir: URL, _ a: ComposerAttachment) -> URL {
        dir.appendingPathComponent(a.id.uuidString + "." + ((a.name as NSString).pathExtension.isEmpty ? "bin" : (a.name as NSString).pathExtension))
    }

    /// Keeps a draft's attachments (none: removes its folder). Files are
    /// written once; those no longer attached are deleted.
    static func save(_ paneID: String, doc: ComposerDoc, attachments: [ComposerAttachment]) {
        let dir = dir(paneID)
        let fm = FileManager.default
        guard !attachments.isEmpty else {
            if fm.fileExists(atPath: dir.path) { try? fm.removeItem(at: dir) }
            return
        }
        try? fm.createDirectory(at: dir, withIntermediateDirectories: true)
        var keep: Set<String> = ["manifest.json"]
        for a in attachments {
            let f = file(dir, a)
            keep.insert(f.lastPathComponent)
            if !fm.fileExists(atPath: f.path) { try? a.data.write(to: f, options: .atomic) }
        }
        for name in (try? fm.contentsOfDirectory(atPath: dir.path)) ?? [] where !keep.contains(name) {
            try? fm.removeItem(at: dir.appendingPathComponent(name))
        }
        let m = Manifest(doc: doc, items: attachments.map { .init(id: $0.id, number: $0.number, name: $0.name, size: $0.size?.rawValue) })
        if let d = try? JSONEncoder().encode(m) { try? d.write(to: dir.appendingPathComponent("manifest.json"), options: .atomic) }
    }

    /// A draft with attachments, as kept (nil: none kept).
    static func load(_ paneID: String) -> (ComposerDoc, [ComposerAttachment])? {
        let dir = dir(paneID)
        guard let d = try? Data(contentsOf: dir.appendingPathComponent("manifest.json")),
              let m = try? JSONDecoder().decode(Manifest.self, from: d) else { return nil }
        var out: [ComposerAttachment] = []
        for i in m.items {
            let a = ComposerAttachment(id: i.id, number: i.number, name: i.name, data: Data(), size: i.size.flatMap(AttachmentSize.init))
            guard let data = try? Data(contentsOf: file(dir, a)) else { continue }
            out.append(ComposerAttachment(id: i.id, number: i.number, name: i.name, data: data, size: a.size))
        }
        // A chip whose file went: its mark goes too.
        var doc = ComposerDoc()
        let have = Set(out.map(\.id))
        var k = 0
        for ch in m.doc.text {
            if ch == attachMark {
                if k < m.doc.chips.count, have.contains(m.doc.chips[k]) {
                    doc.text.append(ch)
                    doc.chips.append(m.doc.chips[k])
                }
                k += 1
            } else {
                doc.text.append(ch)
            }
        }
        return (doc, out)
    }

    static func remove(_ paneID: String) {
        try? FileManager.default.removeItem(at: dir(paneID))
    }

    private static var pruned = false

    /// Sweeps drafts' attachments nobody will come back to: any untouched
    /// for two weeks, and (once the panes are known) those of panes that
    /// are gone, after a day (a linked computer offline for an hour hides
    /// its panes, but they're back). Once a launch.
    @MainActor static func pruneStale(livePanes: Set<String>?) {
        guard !pruned else { return }
        if livePanes != nil { pruned = true }
        let fm = FileManager.default
        guard let dirs = try? fm.contentsOfDirectory(at: root, includingPropertiesForKeys: [.contentModificationDateKey]) else { return }
        let live = Set((livePanes ?? []).map { dir($0).lastPathComponent })
        for d in dirs {
            let m = (try? d.appendingPathComponent("manifest.json").resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate
                ?? (try? d.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
            let age = Date.now.timeIntervalSince(m)
            let gone = livePanes != nil && !live.contains(d.lastPathComponent)
            if age > 14 * 86400 || (gone && age > 86400) { try? fm.removeItem(at: d) }
        }
    }
}
