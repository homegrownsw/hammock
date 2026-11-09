import Foundation
import Network
import os
import SwiftProtobuf

public typealias ClientMessage = Hammock_V1_ClientMessage
public typealias ServerMessage = Hammock_V1_ServerMessage

public enum DaemonError: Error, LocalizedError {
    case notConnected
    case server(String)
    case closed

    public var errorDescription: String? {
        switch self {
        case .notConnected: return "Not connected to hmkd"
        case .server(let m): return m
        case .closed: return "Connection closed"
        }
    }
}

/// A connection to hmkd over its Unix socket (raw length-prefixed
/// frames; Network.framework can't run WebSocket over a Unix socket), speaking the
/// v1 protobuf protocol, or remotely to a paired Mac over WebSocket and
/// pinned TLS, racing its addresses (see "Remote" below). Replies are matched to requests by id; everything
/// else (state updates, pane snapshots/output) goes to `onEvent`.
public final class DaemonConnection: @unchecked Sendable {
    public let socketPath: String
    /// Remote mode: the paired Mac's addresses ("host:port"), raced over
    /// wss://address/v1 accepting only the pinned certificate (see
    /// `connectRemote`), and the local networks' subnets that scope them
    /// (RemoteCandidates).
    let remoteAddresses: [String]
    let lanSubnets: [String]
    let lastWorkedAddress: String?
    let certSHA256: String?
    var remoteConn: NWConnection?
    /// The remote connection being raced, until one address wins.
    var dial: RemoteDial?
    private let addressLock = NSLock()
    private var _connectedAddress: String?
    private var wire: WireZstd? // the remote link's decoder, one per connection
    private static let log = Logger(subsystem: "com.homegrownsw.hammock", category: "connection")
    /// Called on the connection's queue for unsolicited messages.
    public var onEvent: ((ServerMessage) -> Void)?
    /// In-band video and session ends by media session id (MediaStream),
    /// touched only on `queue`.
    var mediaFrames: [String: (MediaFrame) -> Void] = [:]
    var mediaEnded: [String: (String) -> Void] = [:]
    var screenCursors: [String: (Hammock_V1_ScreenCursor) -> Void] = [:]
    var screenStates: [String: (Hammock_V1_ScreenState) -> Void] = [:]
    /// Called on the connection's queue when the connection becomes ready or fails.
    public var onStateChange: ((Bool, Error?) -> Void)?

    let queue = DispatchQueue(label: "hammock.connection")
    private var connection: NWConnection?
    private var nextID: UInt64 = 0
    private var pending: [UInt64: (Result<ServerMessage, Error>) -> Void] = [:]

    public init(socketPath: String) {
        self.socketPath = socketPath
        self.remoteAddresses = []
        self.lanSubnets = []
        self.lastWorkedAddress = nil
        self.certSHA256 = nil
    }

    /// A remote connection to a paired daemon at one address.
    public convenience init(remoteAddress: String, certSHA256: String) {
        self.init(remoteAddresses: [remoteAddress], lanSubnets: [], lastWorked: nil, certSHA256: certSHA256)
    }

    /// A remote connection to a paired daemon, over whichever of its
    /// addresses answers first (Tailscale, a VPN, a local network this
    /// device is on, or one Bonjour finds here), `lastWorked` tried first.
    public init(remoteAddresses: [String], lanSubnets: [String], lastWorked: String?, certSHA256: String) {
        self.socketPath = ""
        self.remoteAddresses = remoteAddresses
        self.lanSubnets = lanSubnets
        self.lastWorkedAddress = lastWorked
        self.certSHA256 = certSHA256.lowercased()
    }

    /// Remote mode: the address connected to (before then, the first
    /// candidate); nil for a local connection.
    public var remoteAddress: String? {
        guard certSHA256 != nil else { return nil }
        addressLock.lock()
        defer { addressLock.unlock() }
        return _connectedAddress ?? lastWorkedAddress.flatMap { $0.isEmpty ? nil : $0 } ?? remoteAddresses.first ?? ""
    }

    /// The address the remote connection won the race on, once connected.
    public var connectedAddress: String? {
        addressLock.lock()
        defer { addressLock.unlock() }
        return _connectedAddress
    }

    fileprivate func setConnectedAddress(_ a: String) {
        addressLock.lock()
        _connectedAddress = a
        addressLock.unlock()
    }

    private var isOpen: Bool { connection != nil || remoteConn != nil }

    public static var defaultSocketPath: String {
        if let dir = ProcessInfo.processInfo.environment["HMK_DIR"] {
            return dir + "/run/hmkd.sock"
        }
        // Dev builds ("Hammock β") name their own state dir in Info.plist so
        // they run a separate daemon from the prod app.
        let name = Bundle.main.object(forInfoDictionaryKey: "HammockStateDir") as? String ?? "Hammock"
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return support.appendingPathComponent(name).appendingPathComponent("run/hmkd.sock").path
    }

    public func connect() {
        if let certSHA256 {
            queue.async { self.connectRemote(pin: certSHA256) }
            return
        }
        let conn = NWConnection(to: .unix(path: socketPath), using: .tcp)
        connection = conn
        conn.stateUpdateHandler = { [weak self] state in
            guard let self else { return }
            switch state {
            case .ready:
                // Raw-frame transport: magic, then 4-byte big-endian length + protobuf per message.
                conn.send(content: Data("PRC1".utf8), completion: .contentProcessed { _ in })
                self.onStateChange?(true, nil)
                self.receiveHeader()
            case .failed(let err), .waiting(let err):
                self.connection = nil // so calls fail fast instead of waiting forever
                self.failAll(err)
                self.onStateChange?(false, err)
                conn.cancel()
            case .cancelled:
                self.connection = nil
                self.failAll(DaemonError.closed)
                self.onStateChange?(false, nil)
            default:
                break
            }
        }
        conn.start(queue: queue)
    }

    public func disconnect() {
        queue.async {
            self.connection?.cancel()
            self.remoteConn?.cancel()
            self.dial?.stop()
            self.dial = nil
        }
    }

    private func failAll(_ err: Error) {
        let p = pending
        pending.removeAll()
        for (_, cb) in p { cb(.failure(err)) }
        // In-band video lives on this connection: a new one (or a restarted
        // daemon) won't know its sessions, so end them and let viewers retry.
        let ended = mediaEnded
        mediaEnded.removeAll()
        mediaFrames.removeAll()
        screenCursors.removeAll()
        screenStates.removeAll()
        for (_, cb) in ended { cb("The connection to Hammock closed.") }
    }

    private func receiveHeader() {
        connection?.receive(minimumIncompleteLength: 4, maximumLength: 4) { [weak self] data, _, isComplete, error in
            guard let self else { return }
            guard error == nil, let data, data.count == 4 else {
                return self.receiveFailed(error ?? DaemonError.closed)
            }
            let n = data.withUnsafeBytes { Int(UInt32(bigEndian: $0.loadUnaligned(as: UInt32.self))) }
            if n == 0 { return self.receiveHeader() }
            self.connection?.receive(minimumIncompleteLength: n, maximumLength: n) { [weak self] body, _, _, error in
                guard let self else { return }
                guard error == nil, let body, body.count == n else {
                    return self.receiveFailed(error ?? DaemonError.closed)
                }
                self.dispatch(body)
                self.receiveHeader()
            }
        }
    }

    func receiveFailed(_ error: Error) {
        failAll(error)
        onStateChange?(false, error)
        connection?.cancel()
        connection = nil
        remoteConn?.cancel()
        remoteConn = nil
    }

    func dispatch(_ data: Data) {
        guard let msg = try? ServerMessage(serializedBytes: data) else { return }
        switch msg.msg {
        case .mediaFrame(let f):
            mediaFrames[f.sessionID]?(f)
            return
        case .screenCursor(let c):
            screenCursors[c.sessionID]?(c)
            return
        case .screenState(let st):
            screenStates[st.sessionID]?(st)
            return
        case .mediaEnded(let e):
            mediaFrames[e.sessionID] = nil
            screenCursors[e.sessionID] = nil
            screenStates[e.sessionID] = nil
            mediaEnded.removeValue(forKey: e.sessionID)?(e.reason)
            return
        default:
            break
        }
        if msg.replyTo != 0, let cb = pending.removeValue(forKey: msg.replyTo) {
            if case .error(let e) = msg.msg {
                cb(.failure(DaemonError.server(e.message)))
            } else {
                cb(.success(msg))
            }
        } else {
            onEvent?(msg)
        }
    }

    private func write(_ msg: ClientMessage) {
        guard let body = try? msg.serializedData() else { return }
        if remoteConn != nil {
            sendWS(body)
            return
        }
        guard let connection else { return }
        var frame = Data(count: 4)
        frame.withUnsafeMutableBytes { $0.storeBytes(of: UInt32(body.count).bigEndian, as: UInt32.self) }
        frame.append(body)
        connection.send(content: frame, completion: .contentProcessed { _ in })
    }

    /// Sends a request and calls `completion` (on the connection queue) with the reply.
    public func call(_ build: (inout ClientMessage) -> Void, completion: @escaping (Result<ServerMessage, Error>) -> Void) {
        var msg = ClientMessage()
        build(&msg)
        queue.async {
            guard self.isOpen else { return completion(.failure(DaemonError.notConnected)) }
            self.nextID += 1
            msg.id = self.nextID
            self.pending[msg.id] = completion
            self.write(msg)
        }
    }

    /// Async variant of `call`.
    @discardableResult
    public func call(_ build: (inout ClientMessage) -> Void) async throws -> ServerMessage {
        var msg = ClientMessage()
        build(&msg)
        let m = msg
        return try await withCheckedThrowingContinuation { cont in
            self.call({ $0 = m }) { cont.resume(with: $0) }
        }
    }

    /// Sends a message without waiting for a reply.
    public func send(_ build: (inout ClientMessage) -> Void) {
        var msg = ClientMessage()
        build(&msg)
        msg.id = 0
        queue.async { self.write(msg) }
    }
}

// MARK: Remote (WebSocket + pinned TLS over Network.framework)
//
// Network.framework rather than URLSession: URLSession enforces App
// Transport Security, which rejects the daemon's self-signed certificate
// even when the delegate accepts it. Here trust is decided solely by the
// certificate pin from pairing.
//
// A Mac can be reached several ways (Tailscale, a VPN, a local network,
// extra addresses), and a local network's address changes (DHCP) and means
// nothing elsewhere. So every candidate is tried at once, happy-eyeballs
// style: the first starts now and each next 250 ms later, and the first to
// finish TLS with the pinned certificate and the WebSocket upgrade wins;
// the rest are cancelled. Candidates are the Mac's last-known addresses
// (RemoteCandidates.ordered: the one that worked last first, local-network
// ones only while this device is on that network) plus, on Wi‑Fi or
// Ethernet, whatever Bonjour finds advertising _hammock._tcp here (another
// Mac answering fails the pin).

import CryptoKit
import Security

/// One remote connection's race between its candidate addresses. Touched
/// only on the connection's queue.
final class RemoteDial {
    static let stagger: TimeInterval = 0.25
    /// How long Bonjour gets to find the Mac once every known address has failed.
    static let browseGrace: TimeInterval = 3
    /// The whole race's limit.
    static let deadline: TimeInterval = 12

    var tried = Set<String>()
    var attempts: [(address: String, conn: NWConnection)] = []
    var scheduled = 0        // starts not yet run
    var resolving: [NWConnection] = []
    var browser: NWBrowser?
    var browseStarted = Date()
    var seenServices = Set<NWEndpoint>()
    var lastError: Error?
    var done = false

    func stop() {
        done = true
        for a in attempts { a.conn.cancel() }
        attempts.removeAll()
        for r in resolving { r.cancel() }
        resolving.removeAll()
        browser?.cancel()
        browser = nil
    }
}

extension DaemonConnection {
    fileprivate func connectRemote(pin: String) {
        let dial = RemoteDial()
        self.dial = dial
        let candidates = RemoteCandidates.ordered(remoteAddresses, lanSubnets: lanSubnets, lastWorked: lastWorkedAddress,
                                                  localIPs: RemoteCandidates.interfaceAddresses())
        for (i, address) in candidates.enumerated() {
            dial.tried.insert(address)
            dial.scheduled += 1
            queue.asyncAfter(deadline: .now() + RemoteDial.stagger * Double(i)) { [weak self, weak dial] in
                guard let self, let dial, !dial.done else { return }
                dial.scheduled -= 1
                self.attempt(address, pin: pin, dial: dial)
            }
        }
        if RemoteCandidates.onLocalNetwork() {
            browse(pin: pin, dial: dial)
        }
        queue.asyncAfter(deadline: .now() + RemoteDial.deadline) { [weak self, weak dial] in
            guard let self, let dial, !dial.done else { return }
            self.dialFailed(dial, NWError.posix(.ETIMEDOUT))
        }
        if candidates.isEmpty && dial.browser == nil {
            dialFailed(dial, DaemonError.server("No address for this Mac can be reached from this network."))
        } else if dial.browser != nil {
            // Every known address may fail fast: give Bonjour its time, then decide.
            queue.asyncAfter(deadline: .now() + RemoteDial.browseGrace) { [weak self, weak dial] in
                guard let self, let dial else { return }
                self.checkExhausted(dial)
            }
        }
    }

    /// Starts one candidate: TLS pinned to the Mac's certificate, then the
    /// WebSocket upgrade.
    private func attempt(_ address: String, pin: String, dial: RemoteDial) {
        guard let url = URL(string: "wss://\(address)/v1") else { return checkExhausted(dial) }
        let tls = NWProtocolTLS.Options()
        sec_protocol_options_set_min_tls_protocol_version(tls.securityProtocolOptions, .TLSv13)
        sec_protocol_options_set_verify_block(tls.securityProtocolOptions, { _, trustRef, complete in
            let trust = sec_trust_copy_ref(trustRef).takeRetainedValue()
            guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], let leaf = chain.first else {
                return complete(false)
            }
            let der = SecCertificateCopyData(leaf) as Data
            let hex = SHA256.hash(data: der).map { String(format: "%02x", $0) }.joined()
            complete(hex == pin)
        }, queue)
        let ws = NWProtocolWebSocket.Options()
        ws.autoReplyPing = true
        ws.maximumMessageSize = 64 << 20
        ws.setSubprotocols(["hammock.v1+zstd", "hammock.v1"]) // compressed if the daemon can (WireZstd)
        // No Nagle: small messages (a pointer move, a key) go at once, not
        // held back for the last one's ack (tens of ms on the uplink).
        let tcp = NWProtocolTCP.Options()
        tcp.noDelay = true
        let params = NWParameters(tls: tls, tcp: tcp)
        params.defaultProtocolStack.applicationProtocols.insert(ws, at: 0)
        let conn = NWConnection(to: .url(url), using: params)
        dial.attempts.append((address, conn))
        conn.stateUpdateHandler = { [weak self, weak dial] state in
            guard let self else { return }
            if conn === self.remoteConn {
                // The winner: as any connection from here on.
                switch state {
                case .failed(let err), .waiting(let err):
                    self.receiveFailed(err)
                    conn.cancel()
                case .cancelled:
                    self.receiveFailed(DaemonError.closed)
                default:
                    break
                }
                return
            }
            guard let dial, !dial.done else { return }
            switch state {
            case .ready:
                self.won(conn, address: address, dial: dial)
            case .failed(let err), .waiting(let err):
                dial.lastError = err
                dial.attempts.removeAll { $0.conn === conn }
                conn.cancel()
                self.checkExhausted(dial)
            case .cancelled:
                dial.attempts.removeAll { $0.conn === conn }
                self.checkExhausted(dial)
            default:
                break
            }
        }
        conn.start(queue: queue)
    }

    private func won(_ conn: NWConnection, address: String, dial: RemoteDial) {
        dial.attempts.removeAll { $0.conn === conn }
        dial.stop()
        self.dial = nil
        remoteConn = conn
        wire = WireZstd()
        setConnectedAddress(address)
        onStateChange?(true, nil)
        receiveWS()
    }

    /// Fails the race once nothing is left to try: no attempt running or
    /// waiting to start, nothing being resolved, and Bonjour (if browsing)
    /// given its time.
    private func checkExhausted(_ dial: RemoteDial) {
        guard !dial.done, dial.attempts.isEmpty, dial.scheduled == 0, dial.resolving.isEmpty else { return }
        if dial.browser != nil, Date().timeIntervalSince(dial.browseStarted) < RemoteDial.browseGrace { return }
        dialFailed(dial, dial.lastError ?? DaemonError.closed)
    }

    private func dialFailed(_ dial: RemoteDial, _ err: Error) {
        dial.stop()
        if self.dial === dial { self.dial = nil }
        receiveFailed(err)
    }

    /// Looks for the Mac on this network with Bonjour, adding each
    /// computer found as a candidate.
    private func browse(pin: String, dial: RemoteDial) {
        let params = NWParameters()
        params.includePeerToPeer = false
        params.prohibitedInterfaceTypes = [.cellular, .loopback]
        let b = NWBrowser(for: .bonjour(type: "_hammock._tcp", domain: "local."), using: params)
        dial.browser = b
        dial.browseStarted = Date()
        b.browseResultsChangedHandler = { [weak self, weak dial] results, _ in
            guard let self, let dial, !dial.done else { return }
            for r in results where dial.seenServices.insert(r.endpoint).inserted {
                self.resolve(r.endpoint, pin: pin, dial: dial)
            }
        }
        b.stateUpdateHandler = { [weak self, weak dial] state in
            guard let self, let dial, !dial.done else { return }
            if case .failed = state {
                dial.browser?.cancel()
                dial.browser = nil
                self.checkExhausted(dial)
            }
        }
        b.start(queue: queue)
    }

    /// A Bonjour service's address: connect over plain TCP (IPv4, the
    /// listeners' family) and read where the connection went.
    private func resolve(_ service: NWEndpoint, pin: String, dial: RemoteDial) {
        let params = NWParameters.tcp
        params.prohibitedInterfaceTypes = [.cellular, .loopback]
        (params.defaultProtocolStack.internetProtocol as? NWProtocolIP.Options)?.version = .v4
        let conn = NWConnection(to: service, using: params)
        dial.resolving.append(conn)
        let finish: (String?) -> Void = { [weak self, weak dial] address in
            conn.cancel()
            guard let self, let dial, !dial.done else { return }
            guard dial.resolving.contains(where: { $0 === conn }) else { return }
            dial.resolving.removeAll { $0 === conn }
            if let address, dial.tried.insert(address).inserted {
                self.attempt(address, pin: pin, dial: dial)
            } else {
                self.checkExhausted(dial)
            }
        }
        conn.stateUpdateHandler = { state in
            switch state {
            case .ready:
                if case .hostPort(let host, let port)? = conn.currentPath?.remoteEndpoint {
                    finish(RemoteCandidates.address(host: Self.hostString(host), port: port.rawValue))
                } else {
                    finish(nil)
                }
            case .failed, .waiting, .cancelled:
                finish(nil)
            default:
                break
            }
        }
        conn.start(queue: queue)
    }

    private static func hostString(_ host: NWEndpoint.Host) -> String {
        switch host {
        case .ipv4(let a): return "\(a)"
        case .ipv6(let a): return "\(a)".split(separator: "%").first.map(String.init) ?? "\(a)"
        case .name(let n, _): return n
        @unknown default: return "\(host)"
        }
    }

    fileprivate func receiveWS() {
        remoteConn?.receiveMessage { [weak self] data, context, isComplete, error in
            guard let self else { return }
            if let error { return self.receiveFailed(error) }
            let meta = context?.protocolMetadata(definition: NWProtocolWebSocket.definition) as? NWProtocolWebSocket.Metadata
            if meta?.opcode == .close || (isComplete && data == nil) {
                return self.receiveFailed(DaemonError.closed)
            }
            if let data, !data.isEmpty, meta?.opcode == .binary {
                do {
                    self.dispatch(try self.wire?.decode(data) ?? data)
                } catch {
                    Self.log.error("remote message: \(String(describing: error), privacy: .public)")
                    self.remoteConn?.cancel() // out of step: reconnect
                    return
                }
            }
            self.receiveWS()
        }
    }

    func sendWS(_ body: Data) {
        let meta = NWProtocolWebSocket.Metadata(opcode: .binary)
        let ctx = NWConnection.ContentContext(identifier: "msg", metadata: [meta])
        remoteConn?.send(content: body, contentContext: ctx, isComplete: true, completion: .contentProcessed { _ in })
    }
}
