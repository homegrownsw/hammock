// Remote video of a simulator or browser pane: H.264 the host encodes
// for this viewer alone (ADR-0012). Over QUIC on a remote connection (the
// iPhone): pinned like the control link, with a ticket from OpenMedia, one
// stream per frame. In band on a local one (a guest's Mac, whose daemon
// relays the host's video). Decoded in hardware by an
// AVSampleBufferDisplayLayer.
//
// Network.framework quirks this relies on (docs/internals/remote-media.md): the
// group opens an empty stream of its own first; it never raises the peer's
// stream limit, so the initial one is huge; control-stream sends must pass
// isComplete: false or they finish the stream.

import AVFoundation
import ImageIO
import VideoToolbox

typealias MediaFrame = Hammock_V1_MediaFrame
typealias MediaTicket = Hammock_V1_MediaTicket
public typealias BrowserMenu = Hammock_V1_BrowserMenu
import CoreMedia
import CryptoKit
import Foundation
import Network
import os
import Security
import SwiftUI

/// A screen's pointer: where it is (fractions of the display), whether
/// it's on the display shown, and its shape (image, hot spot and size in
/// the Mac's points), once its image has come.
public struct ScreenPointer {
    public var position: CGPoint
    public var visible: Bool
    public var image: CGImage?
    public var hotSpot: CGPoint
    public var size: CGSize
}

public final class MediaStream: @unchecked Sendable {
    public enum State: Equatable {
        case connecting
        case playing
        case failed(String)
    }

    public let layer = AVSampleBufferDisplayLayer()
    /// Called on the main queue: the video's size in pixels, and its state.
    public var onSize: ((CGSize) -> Void)?
    public var onState: ((State) -> Void)?
    /// Set (before start) to show the pictures yourself: each one decoded
    /// here, handed over on the main queue with the part of the display it
    /// shows, so a view can place and show it in one screen update. The
    /// layer above then shows nothing. (It decodes on its own schedule,
    /// a frame or two after it's given a frame, too late to move it with.)
    public var onPicture: ((CVPixelBuffer, CGRect) -> Void)?
    /// A screen's pointer (it isn't in the picture), on the main queue.
    public var onCursor: ((ScreenPointer) -> Void)?
    /// Whether the screen's Mac is locked (its lock screen up), on main.
    public var onLocked: ((Bool) -> Void)?
    private var pointerShapes: [UInt32: CGImage] = [:]
    private var ownDecoder: VTDecompressionSession?
    private var ownFormat: CMVideoFormatDescription?
    /// Called on the stream's queue for each frame handed to the decoder
    /// (diagnostics): whether it was a keyframe.
    public var onDecoded: ((Bool) -> Void)?

    private let conn: DaemonConnection
    private let paneID: String
    /// A whole display instead of a pane (screen sharing, ADR-0014): its id
    /// (0: the main one) and the linked computer it's on ("" for the one
    /// this connection reaches).
    private let screen: (display: UInt32, computer: String)?
    private let maxPixels: UInt32
    private var maxFPS: UInt32 = 0
    private var background = false
    private let forceInBand: Bool
    private let queue = DispatchQueue(label: "hammock.media")
    private var sessionID = ""
    private var group: NWConnectionGroup?
    private var control: NWConnection?
    private var stopped = false
    private var ownsPictures = false  // onPicture was set before start: decoded here

    // Decoding, on `queue`.
    private var format: CMVideoFormatDescription?
    private var lastSeq: UInt32 = 0
    private var started = false
    /// Frames were thrown away here (not given up by the host): only a
    /// keyframe will do, since a refresh may reference one of them.
    private var needKey = false
    private var pending: [UInt32: Frame] = [:]
    // Diagnostics (log: subsystem com.homegrownsw.hammock, category video).
    private static let log = Logger(subsystem: "com.homegrownsw.hammock", category: "video")
    private var received = 0, decoded = 0
    private var lastFrameAt = Date()
    private var silentLogged = false
    fileprivate var watchdog: DispatchSourceTimer?
    private var waitingSince: Date?
    private var size = CGSize.zero
    private var lastKeyRequest = Date.distantPast

    struct Frame {
        var seq: UInt32
        var key: Bool
        var refresh = false // predicted from a frame we have (an LTR): frames before it can be skipped
        var width: Int
        var height: Int
        var hevc = false // HEVC (the sharp tier of a screen), else H.264
        var crop = CGRect(x: 0, y: 0, width: 1, height: 1) // the part of the display it shows
        var data: Data
    }

    /// A screen viewer zoomed in: the part of the display it shows
    /// (fractions; the whole display for none). The host sends that part at
    /// this stream's size.
    public func setView(_ r: CGRect) {
        queue.async {
            guard !self.sessionID.isEmpty, !self.stopped else { return }
            let sid = self.sessionID
            self.conn.send { $0.setMediaView = .with { $0.sessionID = sid; $0.x = Float(r.minX); $0.y = Float(r.minY); $0.w = Float(r.width); $0.h = Float(r.height) } }
        }
    }

    /// Whether this device decodes the sharp tier (HEVC 4:4:4): in hardware,
    /// and not found wanting in this run (a decoder failure turns it off).
    nonisolated(unsafe) static var decodesHEVC444 = VTIsHardwareDecodeSupported(kCMVideoCodecType_HEVC)

    /// inBand: frames on the control connection even when remote (a
    /// shared Group's pane, which your Mac relays from its host).
    public init(connection: DaemonConnection, paneID: String, maxPixels: UInt32 = 0, inBand: Bool = false) {
        self.conn = connection
        self.paneID = paneID
        self.screen = nil
        self.maxPixels = maxPixels
        self.forceInBand = inBand
        layer.videoGravity = .resizeAspect
        layer.preventsDisplaySleepDuringVideoPlayback = false
    }

    /// A display's video (screen sharing). In band when it comes through
    /// your own Mac (a linked computer's screen).
    public init(connection: DaemonConnection, display: UInt32, computerID: String = "", maxPixels: UInt32 = 0, maxFPS: UInt32 = 0,
                background: Bool = false, inBand: Bool = false) {
        self.conn = connection
        self.maxFPS = maxFPS
        self.background = background
        self.paneID = "screen:\(display)"
        self.screen = (display, computerID)
        self.maxPixels = maxPixels
        self.forceInBand = inBand || !computerID.isEmpty
        layer.videoGravity = .resizeAspect
        layer.preventsDisplaySleepDuringVideoPlayback = false
    }

    deinit {
        group?.cancel()
        if let d = ownDecoder { VTDecompressionSessionInvalidate(d) }
    }

    public func start() {
        ownsPictures = onPicture != nil  // read on the queue from now on; the callback stays main's
        state(.connecting)
        startWatchdog()
        let inBand = forceInBand || conn.remoteAddress == nil
        conn.call({
            $0.openMedia = .with {
                if let screen = self.screen {
                    $0.screen = true
                    $0.displayID = screen.display
                    $0.computerID = screen.computer
                    if Self.decodesHEVC444 { $0.codecs = ["hevc444"] }
                } else {
                    $0.paneID = self.paneID
                }
                $0.inBand = inBand
                $0.maxPixels = self.maxPixels
                $0.maxFps = self.maxFPS
                $0.background = self.background
                // Screens over QUIC take frames as datagrams (media
                // transport v2, ADR-0014); a host without it sends streams.
                $0.datagrams = !inBand && self.screen != nil
            }
        }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .failure(let e):
                self.state(.failed(e.localizedDescription))
            case .success(let msg):
                let t = msg.mediaTicket
                self.queue.async {
                    guard !self.stopped else {
                        self.conn.send { $0.closeMedia = .with { $0.sessionID = t.sessionID } }
                        return
                    }
                    self.sessionID = t.sessionID
                    self.listenForPointer(t.sessionID)
                    if inBand {
                        self.startInBand(t.sessionID)
                    } else {
                        self.startQUIC(t)
                    }
                }
            }
        }
    }

    public func stop() {
        queue.async {
            guard !self.stopped else { return }
            self.stopped = true
            self.watchdog?.cancel()
            self.watchdog = nil
            self.datagramTimer?.cancel()
            self.datagramTimer = nil
            self.group?.cancel()
            self.group = nil
            let sid = self.sessionID
            if !sid.isEmpty {
                self.conn.queue.async {
                    self.conn.mediaFrames[sid] = nil
                    self.conn.mediaEnded[sid] = nil
                    self.conn.screenCursors[sid] = nil
                    self.conn.screenStates[sid] = nil
                }
                self.conn.send { $0.closeMedia = .with { $0.sessionID = sid } }
            }
        }
    }

    private func state(_ s: State) {
        Self.log.notice("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): \(String(describing: s), privacy: .public)")
        DispatchQueue.main.async { self.onState?(s) }
    }

    // MARK: The pointer (screens)

    private func listenForPointer(_ sid: String) {
        guard screen != nil else { return }
        let conn = conn
        conn.queue.async { [weak self] in
            conn.screenCursors[sid] = { [weak self] c in
                guard let self else { return }
                if !c.png.isEmpty, let src = CGImageSourceCreateWithData(c.png as CFData, nil),
                   let img = CGImageSourceCreateImageAtIndex(src, 0, nil) {
                    self.pointerShapes[c.shapeID] = img
                }
                let p = ScreenPointer(position: CGPoint(x: CGFloat(c.x), y: CGFloat(c.y)), visible: c.visible,
                                      image: self.pointerShapes[c.shapeID], hotSpot: CGPoint(x: CGFloat(c.hotX), y: CGFloat(c.hotY)),
                                      size: CGSize(width: CGFloat(c.width), height: CGFloat(c.height)))
                DispatchQueue.main.async { self.onCursor?(p) }
            }
            conn.screenStates[sid] = { [weak self] st in
                let locked = st.locked
                DispatchQueue.main.async { self?.onLocked?(locked) }
            }
        }
    }

    // MARK: In band

    private func startInBand(_ sid: String) {
        let conn = conn
        conn.queue.async { [weak self] in
            conn.mediaFrames[sid] = { [weak self] f in
                guard let self else { return }
                var frame = Frame(seq: f.seq, key: f.keyframe, refresh: f.refresh, width: Int(f.width), height: Int(f.height), hevc: f.hevc, data: f.data)
                if f.cropW > 0 { frame.crop = CGRect(x: CGFloat(f.cropX), y: CGFloat(f.cropY), width: CGFloat(f.cropW), height: CGFloat(f.cropH)) }
                self.queue.async {
                    conn.send { $0.mediaAck = .with { $0.sessionID = sid; $0.seqs = [f.seq] } }
                    self.received(frame)
                }
            }
            conn.mediaEnded[sid] = { [weak self] reason in self?.state(.failed(reason)) }
        }
    }

    fileprivate func checkSilence() {
        guard !stopped, !silentLogged, Date().timeIntervalSince(lastFrameAt) > 5 else { return }
        silentLogged = true
        let status = layer.sampleBufferRenderer.status
        Self.log.notice("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): no frames for 5 s (received \(self.received), decoded \(self.decoded), waiting \(self.pending.count), last \(self.lastSeq), needKey \(self.needKey), renderer \(status.rawValue), flush \(self.layer.sampleBufferRenderer.requiresFlushToResumeDecoding))")
    }

    private func requestKeyframe(_ why: String) {
        guard Date().timeIntervalSince(lastKeyRequest) > 0.5 else { return }
        lastKeyRequest = Date()
        Self.log.notice("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): keyframe requested (\(why, privacy: .public))")
        if let control {
            control.send(content: Data([2]), contentContext: .defaultStream, isComplete: false, completion: .idempotent)
        } else if !sessionID.isEmpty {
            let sid = sessionID
            conn.send { $0.mediaAck = .with { $0.sessionID = sid; $0.keyframe = true } }
        }
    }

    // MARK: QUIC

    private func startQUIC(_ t: MediaTicket) {
        guard let address = conn.remoteAddress, let pin = conn.certSHA256,
              let raw = UInt16(exactly: t.quicPort), let port = NWEndpoint.Port(rawValue: raw)
        else {
            state(.failed("no address for the video"))
            return
        }
        let host = Self.host(of: address)
        // The host ending it (the pane closed, access was lost), or the
        // control link dropping, ends the QUIC video too.
        let sid = t.sessionID
        let conn = conn
        conn.queue.async { [weak self] in
            conn.mediaEnded[sid] = { [weak self] reason in self?.state(.failed(reason)) }
        }
        let quic = NWProtocolQUIC.Options(alpn: ["hmk-media/1"])
        quic.direction = .bidirectional
        quic.initialMaxStreamsUnidirectional = 1 << 40  // never raised later
        quic.initialMaxStreamDataUnidirectional = 8 << 20
        quic.initialMaxData = 32 << 20
        quic.idleTimeout = 20_000
        if screen != nil { quic.maxDatagramFrameSize = 65535 }
        sec_protocol_options_set_min_tls_protocol_version(quic.securityProtocolOptions, .TLSv13)
        sec_protocol_options_set_verify_block(quic.securityProtocolOptions, { _, trustRef, complete in
            let trust = sec_trust_copy_ref(trustRef).takeRetainedValue()
            guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], let leaf = chain.first else {
                return complete(false)
            }
            let hex = SHA256.hash(data: SecCertificateCopyData(leaf) as Data).map { String(format: "%02x", $0) }.joined()
            complete(hex == pin)
        }, queue)
        let g = NWConnectionGroup(with: NWMultiplexGroup(to: .hostPort(host: NWEndpoint.Host(host), port: port)), using: NWParameters(quic: quic))
        group = g
        g.newConnectionHandler = { [weak self] stream in
            stream.start(queue: self?.queue ?? .main)
            self?.readFrame(stream, Data())
        }
        let ticket = t.ticket
        g.stateUpdateHandler = { [weak self] st in
            guard let self else { return }
            switch st {
            case .ready:
                guard self.control == nil, let c = NWConnection(from: g) else { return }
                self.control = c
                c.stateUpdateHandler = { s in
                    if case .ready = s {
                        c.send(content: ticket, contentContext: .defaultStream, isComplete: false, completion: .idempotent)
                    }
                }
                c.start(queue: self.queue)
                if self.screen != nil { self.startDatagrams(g) }
            case .failed(let e):
                if !self.stopped { self.state(.failed(e.localizedDescription)) }
            case .cancelled:
                if !self.stopped { self.state(.failed("The video stopped.")) }
            default:
                break  // .waiting is normal before .ready
            }
        }
        g.start(queue: queue)
    }

    static func host(of address: String) -> String {
        // "host:port", "[v6]:port" or a bare host.
        if address.hasPrefix("["), let end = address.firstIndex(of: "]") {
            return String(address[address.index(after: address.startIndex)..<end])
        }
        if address.filter({ $0 == ":" }).count == 1, let colon = address.lastIndex(of: ":") {
            return String(address[..<colon])
        }
        return address
    }

    private func readFrame(_ stream: NWConnection, _ acc: Data) {
        stream.receive(minimumIncompleteLength: 1, maximumLength: 4 << 20) { [weak self] data, _, complete, error in
            guard let self else { return }
            var acc = acc
            if let data { acc.append(data) }
            if error != nil {  // given up by the host
                stream.cancel()
                return
            }
            if !complete { return self.readFrame(stream, acc) }
            stream.cancel()
            self.record(acc)
        }
    }

    /// A whole frame record (from a stream, or datagrams put back together):
    /// acknowledged, then on to ordering and decoding.
    private func record(_ acc: Data) {
            guard acc.count >= 17 else { return }
            let b = [UInt8](acc.prefix(17))
            let seq = UInt32(b[0]) << 24 | UInt32(b[1]) << 16 | UInt32(b[2]) << 8 | UInt32(b[3])
            var rec = Data([1])
            withUnsafeBytes(of: seq.bigEndian) { rec.append(contentsOf: $0) }
            self.control?.send(content: rec, contentContext: .defaultStream, isComplete: false, completion: .idempotent)
            let w = Int(b[13]) << 8 | Int(b[14]), h = Int(b[15]) << 8 | Int(b[16])
            var f = Frame(seq: seq, key: b[4] & 1 != 0, refresh: b[4] & 2 != 0, width: w, height: h, hevc: b[4] & 4 != 0, data: acc.dropFirst(17))
            if b[4] & 8 != 0, acc.count >= 25 {
                // The part of the display it shows: u16 x, y, w, h (fractions × 65535).
                let c = [UInt8](acc.dropFirst(17).prefix(8))
                let v = (0..<4).map { CGFloat(Int(c[2 * $0]) << 8 | Int(c[2 * $0 + 1])) / 65535 }
                f.crop = CGRect(x: v[0], y: v[1], width: v[2], height: v[3])
                f.data = acc.dropFirst(25)
            }
            self.received(f)
    }

    // MARK: Datagrams (media transport v2: datagram.go)

    /// A frame's shards as they come: data 0..<k, then p parity, each the
    /// XOR of the data shards i with i % p == j.
    private struct Assembly {
        var k: Int, p: Int, n: Int
        var shards: [Data?]
        var have = 0
        var lastAt = Date()
        var nacks = 0
        var nackedAt = Date.distantPast
    }
    private var assemblies: [UInt32: Assembly] = [:]
    private var assembled = Set<UInt32>()  // put together: later shards (parity, resends) are spare
    private var newestShard: UInt32 = 0
    private var datagramTimer: DispatchSourceTimer?
    private static let shardHeader = 15

    private func startDatagrams(_ g: NWConnectionGroup) {
        let o = NWProtocolQUIC.Options(alpn: ["hmk-media/1"])
        o.isDatagram = true
        o.maxDatagramFrameSize = 65535
        guard let flow = g.extract(connectionTo: nil, using: o) else {
            Self.log.notice("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): no datagram flow")
            return
        }
        flow.start(queue: queue)
        // Something on it, so the flow exists for the host too.
        flow.send(content: Data([0]), completion: .idempotent)
        func read() {
            flow.receiveMessage { [weak self] data, _, _, error in
                guard let self, !self.stopped else { return }
                if let data { self.shard(data) }
                if error == nil { read() }
            }
        }
        read()
        let t = DispatchSource.makeTimerSource(queue: queue)
        t.schedule(deadline: .now() + .milliseconds(20), repeating: .milliseconds(20))
        t.setEventHandler { [weak self] in self?.checkAssemblies() }
        t.resume()
        datagramTimer = t
    }

    private func shard(_ d: Data) {
        let b = [UInt8](d.prefix(Self.shardHeader))
        guard b.count == Self.shardHeader, b[0] == 1 else { return }
        func u16(_ i: Int) -> Int { Int(b[i]) << 8 | Int(b[i + 1]) }
        let seq = UInt32(b[1]) << 24 | UInt32(b[2]) << 16 | UInt32(b[3]) << 8 | UInt32(b[4])
        let i = u16(5), k = u16(7), p = u16(9)
        let n = Int(b[11]) << 24 | Int(b[12]) << 16 | Int(b[13]) << 8 | Int(b[14])
        guard k > 0, p > 0, i < k + p, n <= k * (d.count - Self.shardHeader), !assembled.contains(seq),
              !started || seq > lastSeq else { return }
        if seq > newestShard {
            // A later frame's shards: any earlier frame still short has lost
            // some (datagrams rarely overtake each other).
            newestShard = seq
            for (s, a) in assemblies where s < seq && a.nacks == 0 { nack(s) }
        }
        var a = assemblies[seq] ?? Assembly(k: k, p: p, n: n, shards: Array(repeating: nil, count: k + p))
        guard a.k == k, a.p == p, a.shards[i] == nil else { return }
        a.shards[i] = Data(d.dropFirst(Self.shardHeader))
        a.have += 1
        a.lastAt = Date()
        assemblies[seq] = a
        if a.have >= k { assemble(seq) }
    }

    /// Rebuilds what parity can; the record when every data shard's there.
    /// Returns the data shards still missing.
    @discardableResult private func assemble(_ seq: UInt32) -> [Int] {
        guard var a = assemblies[seq] else { return [] }
        for j in 0..<a.p {
            guard let par = a.shards[a.k + j] else { continue }
            let stripe = Array(stride(from: j, to: a.k, by: a.p))
            let lost = stripe.filter { a.shards[$0] == nil }
            guard lost.count == 1 else { continue }
            var x = [UInt8](par)
            for i in stripe where i != lost[0] {
                a.shards[i]!.withUnsafeBytes { src in
                    for o in 0..<min(x.count, src.count) { x[o] ^= src[o] }
                }
            }
            a.shards[lost[0]] = Data(x)
        }
        let missing = (0..<a.k).filter { a.shards[$0] == nil }
        assemblies[seq] = a
        guard missing.isEmpty else { return missing }
        var rec = Data(capacity: a.k * (a.shards[0]?.count ?? 0))
        for i in 0..<a.k { rec.append(a.shards[i]!) }
        assemblies[seq] = nil
        assembled.insert(seq)
        if assembled.count > 512 { assembled = Set(assembled.filter { $0 &+ 256 > seq }) }
        record(rec.prefix(a.n))
        return []
    }

    /// Asks the host again for the shards parity can't rebuild.
    private func nack(_ seq: UInt32) {
        let missing = assemble(seq)
        guard !missing.isEmpty, var a = assemblies[seq], let control else { return }
        a.nacks += 1
        a.nackedAt = Date()
        assemblies[seq] = a
        for chunk in stride(from: 0, to: missing.count, by: 255) {
            let part = missing[chunk..<min(chunk + 255, missing.count)]
            var rec = Data([3])
            withUnsafeBytes(of: seq.bigEndian) { rec.append(contentsOf: $0) }
            rec.append(UInt8(part.count))
            for i in part { withUnsafeBytes(of: UInt16(i).bigEndian) { rec.append(contentsOf: $0) } }
            control.send(content: rec, contentContext: .defaultStream, isComplete: false, completion: .idempotent)
        }
    }

    /// A frame gone quiet asks again (the last of a burst has no later frame
    /// to show it's short); one past saving is dropped, and ordering asks
    /// for the keyframe or refresh it needs.
    private func checkAssemblies() {
        let now = Date()
        for (s, a) in assemblies {
            if started && s <= lastSeq || now.timeIntervalSince(a.lastAt) > 0.5 {
                assemblies[s] = nil
            } else if a.nacks == 0 ? now.timeIntervalSince(a.lastAt) > 0.04 : (a.nacks < 4 && now.timeIntervalSince(a.nackedAt) > 0.08) {
                nack(s)
            }
        }
    }

    // MARK: Ordering and decoding

    /// Frames reach the decoder in order, each after the one it references.
    /// One that comes early waits; one whose predecessor was given up is
    /// dropped when the keyframe or refresh that follows arrives.
    private func received(_ f: Frame) {
        guard !stopped else { return }
        received += 1
        if silentLogged {
            Self.log.notice("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): frames again after \(Int(Date().timeIntervalSince(self.lastFrameAt))) s")
            silentLogged = false
        }
        lastFrameAt = Date()
        let restart = f.key && (!started || f.seq > lastSeq)
        let refresh = f.refresh && started && !needKey && f.seq > lastSeq
        if restart || refresh {
            pending = pending.filter { $0.key > f.seq }
            started = true
            if f.key { needKey = false }
            decode(f)
            drain()
            return
        }
        guard started, f.seq > lastSeq else { return }
        // Waiting for a keyframe: anything else references frames the
        // renderer no longer has.
        if needKey { return }
        if f.seq == lastSeq &+ 1 {
            decode(f)
            drain()
            return
        }
        pending[f.seq] = f
        if waitingSince == nil { waitingSince = Date() }
        if pending.count > 240 || Date().timeIntervalSince(waitingSince!) > 1 {
            let waiting = pending.count
            pending.removeAll()
            waitingSince = nil
            needKey = true
            requestKeyframe("frame \(lastSeq &+ 1) missing, \(waiting) waiting")
        }
    }

    private func drain() {
        while let next = pending.removeValue(forKey: lastSeq &+ 1) {
            decode(next)
        }
        waitingSince = pending.isEmpty ? nil : (waitingSince ?? Date())
    }

    private func decode(_ f: Frame) {
        lastSeq = f.seq
        let s = CGSize(width: f.width, height: f.height)
        if s != size {
            size = s
            DispatchQueue.main.async { self.onSize?(s) }
        }
        // Split the AVCC NALs: parameter sets make the format; the rest is the picture.
        var sps: Data?, pps: Data?, vps: Data?
        var picture = Data()
        var i = f.data.startIndex
        while i + 4 <= f.data.endIndex {
            let n = f.data[i..<i + 4].reduce(0) { $0 << 8 | Int($1) }
            let start = i + 4, end = start + n
            guard n > 0, end <= f.data.endIndex else { break }
            if f.hevc {
                switch (f.data[start] >> 1) & 0x3f {
                case 32: vps = f.data[start..<end]
                case 33: sps = f.data[start..<end]
                case 34: pps = f.data[start..<end]
                default: picture.append(f.data[i..<end])
                }
            } else {
                switch f.data[start] & 0x1f {
                case 7: sps = f.data[start..<end]
                case 8: pps = f.data[start..<end]
                default: picture.append(f.data[i..<end])
                }
            }
            i = end
        }
        if f.hevc, let vps, let sps, let pps {
            format = Self.hevcFormat([Data(vps), Data(sps), Data(pps)]) ?? format
        } else if !f.hevc, let sps, let pps {
            format = Self.formatDescription(sps: Data(sps), pps: Data(pps)) ?? format
        }
        guard let format, !picture.isEmpty, let sample = Self.sample(picture, format: format) else {
            if f.key {
                needKey = true
                requestKeyframe("unreadable keyframe")
            }
            return
        }
        if ownsPictures {
            decodeOwn(sample, format: format, f)
            return
        }
        let renderer = layer.sampleBufferRenderer
        if renderer.status == .failed, f.hevc || CMFormatDescriptionGetMediaSubType(format) == kCMVideoCodecType_HEVC {
            // This device can't decode the sharp tier after all: H.264 from
            // now on (the viewer tries again, and the host won't send HEVC).
            Self.log.error("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): HEVC decoder failed: \(String(describing: renderer.error), privacy: .public); H.264 from now on")
            Self.decodesHEVC444 = false
            renderer.flush()
            state(.failed("This device couldn’t show the sharp picture; trying again in standard quality."))
            return
        }
        if renderer.status == .failed {
            Self.log.error("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): decoder failed: \(String(describing: renderer.error), privacy: .public)")
            renderer.flush()
            needKey = true
            requestKeyframe("decoder failed")
            return
        }
        // After the app was in the background or the layer off screen, the
        // renderer drops every frame until flushed, and then needs a keyframe.
        if renderer.requiresFlushToResumeDecoding {
            Self.log.notice("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): renderer needed a flush")
            renderer.flush()
            if !f.key {
                needKey = true
                requestKeyframe("renderer flushed")
                return
            }
        }
        renderer.enqueue(sample)
        decoded += 1
        onDecoded?(f.key)
        if f.key { state(.playing) }
    }

    /// Decodes a frame here (onPicture), in order, on the stream's queue.
    private func decodeOwn(_ sample: CMSampleBuffer, format: CMVideoFormatDescription, _ f: Frame) {
        if ownDecoder == nil || ownFormat.map({ !CMFormatDescriptionEqual($0, otherFormatDescription: format) }) ?? true {
            if let d = ownDecoder { VTDecompressionSessionInvalidate(d) }
            ownDecoder = nil
            // GPU surfaces in BGRA: shown as a layer's contents as they are.
            let attrs: [CFString: Any] = [kCVPixelBufferPixelFormatTypeKey: kCVPixelFormatType_32BGRA,
                                          kCVPixelBufferIOSurfacePropertiesKey: [:] as CFDictionary]
            var d: VTDecompressionSession?
            let st = VTDecompressionSessionCreate(allocator: nil, formatDescription: format, decoderSpecification: nil,
                                                  imageBufferAttributes: attrs as CFDictionary, outputCallback: nil, decompressionSessionOut: &d)
            guard st == noErr, let d else {
                ownFailed(f, "couldn't make a decoder (\(st))")
                return
            }
            VTSessionSetProperty(d, key: kVTDecompressionPropertyKey_RealTime, value: kCFBooleanTrue)
            ownDecoder = d
            ownFormat = format
        }
        guard let d = ownDecoder else { return }
        var picture: CVPixelBuffer?
        let st = VTDecompressionSessionDecodeFrame(d, sampleBuffer: sample, flags: [], infoFlagsOut: nil) { status, _, image, _, _ in
            if status == noErr { picture = image }
        }
        guard st == noErr, let picture else {
            ownFailed(f, "decode \(st)")
            return
        }
        let crop = f.crop
        DispatchQueue.main.async { self.onPicture?(picture, crop) }
        decoded += 1
        onDecoded?(f.key)
        if f.key { state(.playing) }
    }

    private func ownFailed(_ f: Frame, _ why: String) {
        if f.hevc {
            // This device can't decode the sharp tier after all: H.264 from now on.
            Self.log.error("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): HEVC \(why, privacy: .public); H.264 from now on")
            Self.decodesHEVC444 = false
            state(.failed("This device couldn’t show the sharp picture; trying again in standard quality."))
            return
        }
        Self.log.error("\(self.paneID, privacy: .public) \(self.sessionID, privacy: .public): \(why, privacy: .public)")
        if let d = ownDecoder { VTDecompressionSessionInvalidate(d) }
        ownDecoder = nil
        needKey = true
        requestKeyframe(why)
    }

    static func formatDescription(sps: Data, pps: Data) -> CMVideoFormatDescription? {
        var fmt: CMVideoFormatDescription?
        let status = sps.withUnsafeBytes { s in
            pps.withUnsafeBytes { p in
                let ptrs = [s.bindMemory(to: UInt8.self).baseAddress!, p.bindMemory(to: UInt8.self).baseAddress!]
                let sizes = [sps.count, pps.count]
                return CMVideoFormatDescriptionCreateFromH264ParameterSets(
                    allocator: nil, parameterSetCount: 2, parameterSetPointers: ptrs, parameterSetSizes: sizes,
                    nalUnitHeaderLength: 4, formatDescriptionOut: &fmt)
            }
        }
        return status == noErr ? fmt : nil
    }

    static func hevcFormat(_ sets: [Data]) -> CMVideoFormatDescription? {
        var fmt: CMVideoFormatDescription?
        let bytes = sets.map { [UInt8]($0) }
        let status = bytes[0].withUnsafeBufferPointer { v in
            bytes[1].withUnsafeBufferPointer { s in
                bytes[2].withUnsafeBufferPointer { p in
                    let ptrs = [v.baseAddress!, s.baseAddress!, p.baseAddress!]
                    return CMVideoFormatDescriptionCreateFromHEVCParameterSets(
                        allocator: nil, parameterSetCount: 3, parameterSetPointers: ptrs, parameterSetSizes: bytes.map(\.count),
                        nalUnitHeaderLength: 4, extensions: nil, formatDescriptionOut: &fmt)
                }
            }
        }
        return status == noErr ? fmt : nil
    }

    static func sample(_ avcc: Data, format: CMVideoFormatDescription) -> CMSampleBuffer? {
        var block: CMBlockBuffer?
        guard CMBlockBufferCreateWithMemoryBlock(allocator: nil, memoryBlock: nil, blockLength: avcc.count, blockAllocator: nil,
                                                 customBlockSource: nil, offsetToData: 0, dataLength: avcc.count, flags: 0,
                                                 blockBufferOut: &block) == noErr, let block else { return nil }
        let ok = avcc.withUnsafeBytes { CMBlockBufferReplaceDataBytes(with: $0.baseAddress!, blockBuffer: block, offsetIntoDestination: 0, dataLength: avcc.count) }
        guard ok == noErr else { return nil }
        var sample: CMSampleBuffer?
        var sizes = [avcc.count]
        guard CMSampleBufferCreateReady(allocator: nil, dataBuffer: block, formatDescription: format, sampleCount: 1,
                                        sampleTimingEntryCount: 0, sampleTimingArray: nil, sampleSizeEntryCount: 1,
                                        sampleSizeArray: &sizes, sampleBufferOut: &sample) == noErr, let sample else { return nil }
        if let atts = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: true), CFArrayGetCount(atts) > 0 {
            let d = unsafeBitCast(CFArrayGetValueAtIndex(atts, 0), to: CFMutableDictionary.self)
            CFDictionarySetValue(d, Unmanaged.passUnretained(kCMSampleAttachmentKey_DisplayImmediately).toOpaque(),
                                 Unmanaged.passUnretained(kCFBooleanTrue).toOpaque())
        }
        return sample
    }
}

// MARK: View

#if os(iOS)
public struct MediaView: UIViewRepresentable {
    let stream: MediaStream
    public init(stream: MediaStream) { self.stream = stream }
    public func makeUIView(context: Context) -> LayerView {
        let v = LayerView()
        v.hosted = stream.layer
        return v
    }
    public func updateUIView(_ v: LayerView, context: Context) { v.hosted = stream.layer }

    public final class LayerView: UIView {
        public var hosted: CALayer? {
            didSet {
                guard hosted !== oldValue else { return }
                oldValue?.removeFromSuperlayer()
                if let hosted { layer.addSublayer(hosted) }
                setNeedsLayout()
            }
        }
        public override func layoutSubviews() {
            super.layoutSubviews()
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            hosted?.frame = bounds
            CATransaction.commit()
        }
    }
}
#else
public struct MediaView: NSViewRepresentable {
    let stream: MediaStream
    public init(stream: MediaStream) { self.stream = stream }
    public func makeNSView(context: Context) -> LayerView {
        let v = LayerView()
        v.hosted = stream.layer
        return v
    }
    public func updateNSView(_ v: LayerView, context: Context) { v.hosted = stream.layer }

    public final class LayerView: NSView {
        var hosted: CALayer? {
            didSet {
                guard hosted !== oldValue else { return }
                wantsLayer = true
                oldValue?.removeFromSuperlayer()
                if let hosted { layer?.addSublayer(hosted) }
                needsLayout = true
            }
        }
        public override func layout() {
            super.layout()
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            hosted?.frame = bounds
            CATransaction.commit()
        }
    }
}
#endif

extension MediaStream {
    /// Logs a silence once (a still page sends nothing, so it isn't an
    /// error, but a frozen picture shows up here with what was going on).
    fileprivate func startWatchdog() {
        queue.async { [weak self] in
            guard let self else { return }
            let t = DispatchSource.makeTimerSource(queue: self.queue)
            t.schedule(deadline: .now() + 5, repeating: 5)
            t.setEventHandler { [weak self] in self?.checkSilence() }
            t.resume()
            self.watchdog = t
        }
    }
}
