import Foundation
import libzstd

/// Reads a compressed remote link's messages (internal/wirezstd): the daemon
/// sends one zstd stream for the whole connection, flushed after each
/// message. A message starting 0 is sent as is, 1 is the uncompressed length
/// (uvarint) then the stream's next piece, and anything else is a plain
/// protobuf message (a field tag is never 0 or 1). One per connection, used
/// on its queue.
final class WireZstd {
    private let stream: OpaquePointer
    private let maxSize: Int

    init?(maxSize: Int = 64 << 20) {
        guard let s = ZSTD_createDStream() else { return nil }
        stream = s
        self.maxSize = maxSize
        ZSTD_initDStream(s)
        ZSTD_DCtx_setParameter(s, ZSTD_d_windowLogMax, 22) // the daemon's window is 1 MB
    }

    deinit { ZSTD_freeDStream(stream) }

    enum Failure: Error { case malformed, tooLarge, decompress(String) }

    /// The protobuf bytes of message m. After a failure the stream is out of
    /// step: drop the connection.
    func decode(_ m: Data) throws -> Data {
        guard let kind = m.first, kind <= 1 else { return m }
        let body = m.dropFirst()
        if kind == 0 { return Data(body) }
        // The length.
        var n: UInt64 = 0, shift: UInt64 = 0, i = body.startIndex
        while true {
            guard i < body.endIndex, shift < 64 else { throw Failure.malformed }
            let b = body[i]
            i += 1
            n |= UInt64(b & 0x7f) << shift
            if b < 0x80 { break }
            shift += 7
        }
        guard n <= UInt64(maxSize) else { throw Failure.tooLarge }
        let input = body[i...]
        var out = Data(count: Int(n))
        if n == 0 && input.isEmpty { return out }
        let ok: Bool = try input.withUnsafeBytes { src in
            try out.withUnsafeMutableBytes { dst in
                var inBuf = ZSTD_inBuffer(src: src.baseAddress, size: src.count, pos: 0)
                var outBuf = ZSTD_outBuffer(dst: dst.baseAddress, size: dst.count, pos: 0)
                // Until this message's piece is used up (the daemon flushed
                // it, so it decodes whole); it must fill exactly the length
                // given, no more.
                while inBuf.pos < inBuf.size {
                    let before = (inBuf.pos, outBuf.pos)
                    let r = ZSTD_decompressStream(stream, &outBuf, &inBuf)
                    if ZSTD_isError(r) != 0 {
                        throw Failure.decompress(String(cString: ZSTD_getErrorName(r)))
                    }
                    if (inBuf.pos, outBuf.pos) == before { return false } // output full, input left
                }
                return outBuf.pos == outBuf.size
            }
        }
        guard ok else { throw Failure.malformed }
        return out
    }
}
