// hmk-probe: a tiny dev tool that connects to hmkd with
// HammockKit, prints the state tree and streams one pane's output.
//   hmk-probe [SOCKET] [PANE MATCH]
// With PANE and MATCH, feeds the pane through TerminalEngine and prints the
// cells of every row containing MATCH (renderer debugging).
import CryptoKit
import Foundation
import HammockKit

//   hmk-probe video SOCKET PANE [ADDR] [SECS]
// watches a simulator pane's remote video through MediaStream: in band on
// the local socket, or with ADDR (the daemon's remote address) pairing like
// a phone and taking it over QUIC. Prints frames decoded.
if CommandLine.arguments.count > 3, CommandLine.arguments[1] == "video" {
    videoProbe(Array(CommandLine.arguments.dropFirst(2)))
}

func videoProbe(_ a: [String]) -> Never {
    let local = DaemonConnection(socketPath: a[0])
    let pane = a[1]
    let addr = a.count > 2 && a[2] != "-" ? a[2] : nil
    let secs = a.count > 3 ? Double(a[3]) ?? 5 : 5
    let ready = DispatchSemaphore(value: 0)
    local.onStateChange = { ok, _ in if ok { ready.signal() } }
    local.connect()
    ready.wait()
    var conn = local
    if let addr {
        let p = try! awaitSync { try await local.call { $0.createPairing = .init() } }.pairing
        let remote = DaemonConnection(remoteAddress: addr, certSHA256: p.certSha256)
        let up = DispatchSemaphore(value: 0)
        remote.onStateChange = { ok, err in if ok { up.signal() } else { print("remote: \(String(describing: err))") } }
        remote.connect()
        up.wait()
        let key = Curve25519.Signing.PrivateKey()
        _ = try! awaitSync {
            try await remote.call { $0.pair = .with { $0.secret = p.secret; $0.deviceName = "hmk-probe"; $0.platform = "ios"; $0.publicKey = key.publicKey.rawRepresentation } }
        }
        conn = remote
    }
    let stream = MediaStream(connection: conn, paneID: pane)
    let lock = NSLock()
    var frames = 0, keys = 0
    stream.onDecoded = { key in lock.lock(); frames += 1; if key { keys += 1 }; lock.unlock() }
    stream.onState = { print("state: \($0)") }
    stream.onSize = { print("size: \(Int($0.width))x\(Int($0.height))") }
    stream.start()
    // Progress every 5 s, so a stall shows when it starts.
    var waited = 0.0
    while waited < secs {
        let step = min(5, secs - waited)
        Thread.sleep(forTimeInterval: step)
        waited += step
        lock.lock()
        print("  \(Int(waited)) s: \(frames) frames")
        lock.unlock()
    }
    lock.lock()
    print("\(addr == nil ? "in band" : "quic"): \(frames) frames decoded (\(keys) keyframes), renderer \(stream.layer.sampleBufferRenderer.status.rawValue) error \(String(describing: stream.layer.sampleBufferRenderer.error))")
    lock.unlock()
    stream.stop()
    Thread.sleep(forTimeInterval: 0.3)
    exit(0)
}

func awaitSync<T>(_ f: @escaping () async throws -> T) throws -> T {
    let sem = DispatchSemaphore(value: 0)
    nonisolated(unsafe) var result: Result<T, Error>!
    Task {
        do { result = .success(try await f()) } catch { result = .failure(error) }
        sem.signal()
    }
    sem.wait()
    return try result.get()
}

let args = CommandLine.arguments
let conn = DaemonConnection(socketPath: args.count > 1 ? args[1] : DaemonConnection.defaultSocketPath)
let done = DispatchSemaphore(value: 0)
let dumpPane = args.count > 3 ? args[2] : nil
let dumpMatch = args.count > 3 ? args[3] : nil
var engine: TerminalEngine?

conn.onEvent = { msg in
    switch msg.msg {
    case .paneSnapshot(let s):
        print("snapshot \(s.paneID): \(s.data.count) bytes, \(s.cols)x\(s.rows)")
        if s.paneID == dumpPane {
            engine = TerminalEngine(cols: Int(s.cols), rows: Int(s.rows))
            engine?.write(s.data)
        }
    case .paneOutput(let o):
        print("output \(o.paneID) @\(o.offset): \(o.data.count) bytes")
        if o.paneID == dumpPane { engine?.write(o.data) }
    case .state(let u): print("state v\(u.version): \(u.panes.count) panes changed")
    default: break
    }
}

func hex(_ c: RGB?) -> String { c.map { String(format: "%02x%02x%02x", $0.r, $0.g, $0.b) } ?? "-" }

func dumpRows() {
    guard let engine, let dumpMatch else { return }
    for (y, row) in engine.frame().cells.enumerated() {
        let line = row.map { $0.text.isEmpty ? " " : $0.text }.joined()
        guard line.contains(dumpMatch) else { continue }
        print("row \(y): \(line.trimmingCharacters(in: .whitespaces))")
        for (x, c) in row.enumerated() where !(c.text.isEmpty || c.text == " ") || c.bg != nil {
            let cps = c.text.unicodeScalars.map { String(format: "U+%04X", $0.value) }.joined(separator: ",")
            var flags = ""
            if c.wide { flags += " wide" }
            if c.spacer { flags += " spacer" }
            if c.inverse { flags += " inverse" }
            if c.bold { flags += " bold" }
            print(String(format: "  %3d %-14@ fg=%@ bg=%@%@", x, cps as NSString, hex(c.fg), hex(c.bg), flags))
        }
    }
}

conn.onStateChange = { ready, err in
    if !ready {
        print("disconnected: \(String(describing: err))")
        done.signal()
        return
    }
    Task {
        do {
            let hello = try await conn.call { $0.hello = .with { $0.protocolVersion = 1; $0.clientKind = "probe"; $0.clientName = "hmk-probe" } }
            print("hello: daemon \(hello.helloReply.daemonVersion) on \(hello.helloReply.hostName), vt \(hello.helloReply.vtEngine)")
            let state = try await conn.call { $0.subscribe = .init() }.state
            for group in state.groups {
                print("group \(group.id) \(group.name) tabs=\(group.tabIds)")
            }
            if let pane = dumpPane ?? state.panes.first?.id {
                _ = try await conn.call { $0.attachPane = .with { $0.paneID = pane } }
                try await Task.sleep(for: .seconds(1))
                dumpRows()
            }
        } catch {
            print("error: \(error)")
        }
        done.signal()
    }
}
conn.connect()
done.wait()
