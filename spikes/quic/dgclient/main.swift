// Datagram spike, client: Network.framework's QUIC datagram flow.
//   swiftc -O main.swift -o dgclient && ./dgclient HOST PORT FINGERPRINT
import CryptoKit
import Foundation
import Network
import Security

setvbuf(stdout, nil, _IOLBF, 0)
let a = CommandLine.arguments
let host = a[1], port = NWEndpoint.Port(a[2])!, pin = a[3]
let q = DispatchQueue(label: "dg")
var got = 0, latSum = 0.0, maxSeq = -1

let quic = NWProtocolQUIC.Options(alpn: ["hmk-media/1"])
quic.direction = .bidirectional
quic.isDatagram = false
quic.maxDatagramFrameSize = 65535
quic.idleTimeout = 15_000
sec_protocol_options_set_verify_block(quic.securityProtocolOptions, { _, trustRef, complete in
    let trust = sec_trust_copy_ref(trustRef).takeRetainedValue()
    let leaf = (SecTrustCopyCertificateChain(trust) as! [SecCertificate])[0]
    complete(SHA256.hash(data: SecCertificateCopyData(leaf) as Data).map { String(format: "%02x", $0) }.joined() == pin)
}, q)
let group = NWConnectionGroup(with: NWMultiplexGroup(to: .hostPort(host: .init(host), port: port)), using: NWParameters(quic: quic))
group.stateUpdateHandler = { st in
    print("group", st)
    guard case .ready = st else { return }
    // The datagram flow.
    let dg = NWProtocolQUIC.Options(alpn: ["hmk-media/1"])
    dg.isDatagram = true
    dg.maxDatagramFrameSize = 65535
    guard let flow = group.extract(connectionTo: nil, using: dg) else { print("no datagram flow"); exit(1) }
    flow.stateUpdateHandler = { print("flow", $0) }
    func read() {
        flow.receiveMessage { data, _, _, err in
            if let data, data.count >= 12 {
                let seq = data.prefix(4).reduce(0) { $0 << 8 | Int($1) }
                let sent = data.dropFirst(4).prefix(8).reduce(UInt64(0)) { $0 << 8 | UInt64($1) }
                latSum += Date().timeIntervalSince1970 * 1e6 - Double(sent)
                got += 1
                maxSeq = max(maxSeq, seq)
            }
            if err != nil { print("flow error", err!); return }
            read()
        }
    }
    flow.start(queue: q)
    read()
    // Something on the flow so the server sees it.
    flow.send(content: Data([0]), completion: .idempotent)
}
group.newConnectionHandler = { conn in
    print("server opened a stream")
    conn.start(queue: q)
}
group.start(queue: q)
print("group starting")
q.asyncAfter(deadline: .now() + 5) {
    print(String(format: "got %d of 2000 datagrams (highest %d), %.2f ms average one way", got, maxSeq, got > 0 ? latSum / Double(got) / 1000 : 0))
    exit(0)
}
dispatchMain()
