import Darwin
import Foundation

/// Which of a paired Mac's addresses a remote client tries, and in what
/// order (DaemonConnection's remote mode races them).
///
/// The Mac lists every address it listens on (Pairing, then each
/// HelloReply): Tailscale first, then a VPN, then local networks, then extra
/// addresses. A local network's address (one inside a `lanSubnets` entry,
/// "192.168.1.0/24") is only tried while this device has an address of its
/// own in that subnet: away from home, 192.168.1.20 is someone else, and
/// they shouldn't get a TLS hello from us. Anything else (Tailscale, a VPN,
/// a DNS name) is always tried.
public enum RemoteCandidates {
    /// The addresses to try now, in order: `lastWorked` first, then
    /// `addresses` as given, without repeats, leaving out local-network
    /// addresses this device isn't on (`localIPs`: its own interface
    /// addresses, as from `interfaceAddresses()`).
    public static func ordered(_ addresses: [String], lanSubnets: [String], lastWorked: String?, localIPs: [String]) -> [String] {
        var all: [String] = []
        if let lastWorked, !lastWorked.isEmpty { all.append(lastWorked) }
        all += addresses
        var seen = Set<String>()
        let subnets = lanSubnets.compactMap(Subnet.init)
        let mine = localIPs.compactMap(ipBytes)
        return all.filter { a in
            guard !a.isEmpty, seen.insert(a).inserted else { return false }
            return reachableFromHere(a, subnets: subnets, localIPs: mine)
        }
    }

    /// Whether `address` may be tried from a device with `localIPs`: not in
    /// any of `subnets`, or in one this device is on too.
    static func reachableFromHere(_ address: String, subnets: [Subnet], localIPs: [[UInt8]]) -> Bool {
        guard let ip = ipBytes(host(of: address)) else { return true } // a name: always
        let inside = subnets.filter { $0.contains(ip) }
        if inside.isEmpty { return true }
        return inside.contains { net in localIPs.contains { net.contains($0) } }
    }

    /// The host of "host:port" or "[v6]:port" (an address without a port as is).
    public static func host(of address: String) -> String {
        if address.hasPrefix("["), let end = address.firstIndex(of: "]") {
            return String(address[address.index(after: address.startIndex)..<end])
        }
        // One colon: host:port. More: a bare IPv6 address.
        if let colon = address.lastIndex(of: ":"), address.firstIndex(of: ":") == colon {
            return String(address[..<colon])
        }
        return address
    }

    /// "host:port", bracketing an IPv6 host.
    public static func address(host: String, port: UInt16) -> String {
        host.contains(":") ? "[\(host)]:\(port)" : "\(host):\(port)"
    }

    /// An IPv4 (4 bytes) or IPv6 (16) address's bytes; nil for anything else.
    static func ipBytes(_ s: String) -> [UInt8]? {
        // A zone ("fe80::1%en0") isn't part of the address.
        let s = s.split(separator: "%", maxSplits: 1).first.map(String.init) ?? s
        var v4 = in_addr()
        if inet_pton(AF_INET, s, &v4) == 1 {
            return withUnsafeBytes(of: &v4) { Array($0) }
        }
        var v6 = in6_addr()
        if inet_pton(AF_INET6, s, &v6) == 1 {
            return withUnsafeBytes(of: &v6) { Array($0) }
        }
        return nil
    }

    /// A subnet in CIDR notation ("192.168.1.0/24").
    struct Subnet: Equatable {
        let bytes: [UInt8]
        let prefix: Int

        init?(_ cidr: String) {
            let parts = cidr.split(separator: "/", maxSplits: 1)
            guard parts.count == 2, let b = RemoteCandidates.ipBytes(String(parts[0])), let p = Int(parts[1]),
                  p >= 0, p <= b.count * 8 else { return nil }
            bytes = b
            prefix = p
        }

        func contains(_ ip: [UInt8]) -> Bool {
            guard ip.count == bytes.count else { return false }
            var left = prefix
            for i in 0..<bytes.count where left > 0 {
                let bits = min(left, 8)
                let mask = UInt8(truncatingIfNeeded: 0xFF << (8 - bits))
                if ip[i] & mask != bytes[i] & mask { return false }
                left -= bits
            }
            return true
        }
    }

    /// This device's addresses now, on interfaces that are up (loopback and
    /// cellular left out: a carrier's 10.x address isn't the Mac's network).
    public static func interfaceAddresses() -> [String] {
        interfaces().filter { !$0.name.hasPrefix("pdp_ip") }.map(\.ip)
    }

    /// Whether this device is on a Wi‑Fi or Ethernet network now (an "en"
    /// interface with an IPv4 address that isn't link-local): where Bonjour
    /// can find the Mac.
    public static func onLocalNetwork() -> Bool {
        interfaces().contains { $0.name.hasPrefix("en") && !$0.ip.contains(":") && !$0.ip.hasPrefix("169.254.") }
    }

    private static func interfaces() -> [(name: String, ip: String)] {
        var head: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&head) == 0, let first = head else { return [] }
        defer { freeifaddrs(head) }
        var out: [(String, String)] = []
        for p in sequence(first: first, next: { $0.pointee.ifa_next }) {
            let ifa = p.pointee
            let flags = Int32(ifa.ifa_flags)
            guard flags & IFF_UP != 0, flags & IFF_LOOPBACK == 0, let sa = ifa.ifa_addr else { continue }
            let family = Int32(sa.pointee.sa_family)
            guard family == AF_INET || family == AF_INET6 else { continue }
            var buf = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            let len = socklen_t(family == AF_INET ? MemoryLayout<sockaddr_in>.size : MemoryLayout<sockaddr_in6>.size)
            guard getnameinfo(sa, len, &buf, socklen_t(buf.count), nil, 0, NI_NUMERICHOST) == 0 else { continue }
            let ip = String(decoding: buf.prefix { $0 != 0 }.map { UInt8(bitPattern: $0) }, as: UTF8.self)
            out.append((String(cString: ifa.ifa_name), ip))
        }
        return out
    }
}
