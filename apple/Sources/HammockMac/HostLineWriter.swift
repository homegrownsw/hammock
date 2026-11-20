import Foundation

/// Writes newline-delimited JSON to a helper host's socket (the browser host,
/// the simulator host) without ever blocking the main thread: the socket is
/// made non-blocking, and what the host can't take yet waits here until it
/// can (a write source), whole, so a line is never cut off. A failed write,
/// or a host that stops reading while more than `limit` bytes wait, calls
/// `onLost` once: the owner closes the connection as it does a lost host.
@MainActor
final class HostLineWriter {
    private let fd: Int32
    private let limit: Int
    private let onLost: () -> Void
    private var buf = Data()
    private var sent = 0 // of buf, already written
    private var source: DispatchSourceWrite?
    private var lost = false

    /// Bytes still waiting for the host.
    var pending: Int { buf.count - sent }

    init(fd: Int32, limit: Int = 16 << 20, onLost: @escaping () -> Void) {
        self.fd = fd
        self.limit = limit
        self.onLost = onLost
        _ = fcntl(fd, F_SETFL, fcntl(fd, F_GETFL) | O_NONBLOCK)
    }

    /// Queues one message as a line and writes what the host will take now.
    /// False when it wasn't sent: the connection's gone, or the message alone
    /// is bigger than `limit`.
    @discardableResult
    func send(_ m: [String: Any]) -> Bool {
        guard !lost, var d = try? JSONSerialization.data(withJSONObject: m) else { return false }
        d.append(0x0A)
        guard d.count <= limit else { return false }
        guard pending + d.count <= limit else { fail(); return false } // the host has stopped reading
        buf.append(d)
        flush()
        return !lost
    }

    /// Stops writing; the owner closes the socket.
    func cancel() {
        lost = true
        source?.cancel()
        source = nil
        buf = Data()
        sent = 0
    }

    private func flush() {
        while !lost, sent < buf.count {
            let n = buf.withUnsafeBytes { write(fd, $0.baseAddress! + sent, $0.count - sent) }
            if n < 0, errno == EINTR { continue }
            if n < 0, errno == EAGAIN { break }
            guard n > 0 else { fail(); return }
            sent += n
        }
        guard !lost else { return }
        if sent == buf.count {
            buf.removeAll(keepingCapacity: buf.count <= 1 << 16)
            sent = 0
            source?.cancel()
            source = nil
            return
        }
        if sent >= 1 << 20 { // don't let what's been written pile up
            buf.removeSubrange(buf.startIndex..<buf.startIndex + sent)
            sent = 0
        }
        if source == nil {
            let src = DispatchSource.makeWriteSource(fileDescriptor: fd, queue: .main)
            src.setEventHandler { [weak self] in MainActor.assumeIsolated { self?.flush() } }
            src.resume()
            source = src
        }
    }

    private func fail() {
        guard !lost else { return }
        cancel()
        onLost()
    }
}
