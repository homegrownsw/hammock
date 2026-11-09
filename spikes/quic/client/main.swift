// Media transport spike: client side, on Network.framework (same API on iOS).
//
//   swiftc -O main.swift -o quicclient && ./quicclient HOST PORT FINGERPRINT [tcp]
//
// QUIC: a connection with the pinned self-signed certificate (ALPN
// hmk-media/1), a bidirectional control stream carrying a 32-byte ticket
// and then acks, and the server's unidirectional streams, one per frame.
// TCP: the same over one TLS connection, frames back to back.
//
// Findings baked in:
//   - Network.framework opens an empty stream of its own before ours; the
//     server takes the first stream that carries a ticket.
//   - It never raises the peer's stream limit (MAX_STREAMS), so the initial
//     limit has to be huge.
//   - send(content:completion:) defaults to isComplete: true, which closes
//     the stream; acks go with isComplete: false.
import CryptoKit
import Foundation
import Network
import Security

setvbuf(stdout, nil, _IOLBF, 0)
let args = CommandLine.arguments
guard args.count >= 4, let port = NWEndpoint.Port(args[2]) else {
    print("usage: quicclient HOST PORT FINGERPRINT [tcp]")
    exit(2)
}
let host = args[1], pin = args[3], useTCP = args.count > 4 && args[4] == "tcp"
let queue = DispatchQueue(label: "media")
let start = Date()
var frames = 0, bytes = 0

func pinned(_ sec: sec_protocol_options_t) {
    sec_protocol_options_set_min_tls_protocol_version(sec, .TLSv13)
    sec_protocol_options_add_tls_application_protocol(sec, "hmk-media/1")
    sec_protocol_options_set_verify_block(sec, { _, trustRef, complete in
        let trust = sec_trust_copy_ref(trustRef).takeRetainedValue()
        guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], let leaf = chain.first else {
            return complete(false)
        }
        let hex = SHA256.hash(data: SecCertificateCopyData(leaf) as Data).map { String(format: "%02x", $0) }.joined()
        complete(hex == pin)
    }, queue)
}

func be32(_ d: Data, _ at: Int) -> UInt32 {
    d.dropFirst(at).prefix(4).reduce(UInt32(0)) { $0 << 8 | UInt32($1) }
}

func ackData(_ seq: UInt32) -> Data {
    var be = seq.bigEndian
    return Data(bytes: &be, count: 4)
}

func finish() {
    let el = Date().timeIntervalSince(start)
    print(String(format: "client: %d frames, %.1f Mbit/s", frames, Double(bytes * 8) / el / 1e6))
    exit(0)
}

// MARK: QUIC

var control: NWConnection?

func readFrame(_ conn: NWConnection, _ acc: Data) {
    conn.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { data, _, complete, error in
        var acc = acc
        if let data { acc.append(data) }
        if error != nil { conn.cancel(); return } // reset by the server: an abandoned frame
        if !complete { return readFrame(conn, acc) }
        conn.cancel()
        guard acc.count >= 13, acc.count == 13 + Int(be32(acc, 9)) else { return }
        frames += 1
        bytes += acc.count
        control?.send(content: ackData(be32(acc, 0)), contentContext: .defaultStream, isComplete: false, completion: .idempotent)
    }
}

func runQUIC() {
    let quic = NWProtocolQUIC.Options(alpn: ["hmk-media/1"])
    quic.direction = .bidirectional
    quic.initialMaxStreamsUnidirectional = 1 << 40 // never raised later
    quic.initialMaxStreamDataUnidirectional = 4 << 20
    quic.initialMaxData = 32 << 20
    quic.idleTimeout = 15_000
    pinned(quic.securityProtocolOptions)
    let group = NWConnectionGroup(with: NWMultiplexGroup(to: .hostPort(host: .init(host), port: port)), using: NWParameters(quic: quic))
    group.newConnectionHandler = { conn in
        conn.start(queue: queue)
        readFrame(conn, Data())
    }
    group.stateUpdateHandler = { state in
        switch state {
        case .ready:
            let c = NWConnection(from: group)!
            control = c
            c.stateUpdateHandler = { s in
                if case .ready = s {
                    c.send(content: Data(repeating: 7, count: 32), contentContext: .defaultStream, isComplete: false, completion: .idempotent)
                }
            }
            c.start(queue: queue)
        case .failed, .cancelled:
            finish()
        default:
            break
        }
    }
    group.start(queue: queue)
    queue.asyncAfter(deadline: .now() + 60) { group.cancel() }
}

// MARK: TCP

func runTCP() {
    let tls = NWProtocolTLS.Options()
    pinned(tls.securityProtocolOptions)
    let tcp = NWProtocolTCP.Options()
    tcp.noDelay = true
    let conn = NWConnection(host: .init(host), port: port, using: NWParameters(tls: tls, tcp: tcp))
    var buf = Data()
    func read() {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 1 << 20) { data, _, complete, error in
            if let data { buf.append(data) }
            while buf.count >= 13 {
                let len = Int(be32(buf, 9))
                guard buf.count >= 13 + len else { break }
                frames += 1
                bytes += 13 + len
                conn.send(content: ackData(be32(buf, 0)), completion: .idempotent)
                buf.removeFirst(13 + len)
            }
            if complete || error != nil { return finish() }
            read()
        }
    }
    conn.stateUpdateHandler = { s in
        switch s {
        case .ready:
            conn.send(content: Data(repeating: 7, count: 32), completion: .idempotent)
            read()
        case .failed, .cancelled:
            finish()
        default:
            break
        }
    }
    conn.start(queue: queue)
    queue.asyncAfter(deadline: .now() + 60) { conn.cancel() }
}

if useTCP { runTCP() } else { runQUIC() }
dispatchMain()
