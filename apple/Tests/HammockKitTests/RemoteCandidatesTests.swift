import Testing
@testable import HammockKit

@Suite struct RemoteCandidatesTests {
    let home = ["192.168.1.0/24"]

    @Test func localNetworkAddressesOnlyFromThatNetwork() {
        let addrs = ["100.64.1.2:7766", "192.168.1.20:7766", "mac.example.com:7766"]
        // At home: everything.
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: home, lastWorked: nil, localIPs: ["192.168.1.33"]) == addrs)
        // Elsewhere (a café's 10.0.0.x, or another 192.168.x): not the home address.
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: home, lastWorked: nil, localIPs: ["10.0.0.5", "192.168.2.9"])
                == ["100.64.1.2:7766", "mac.example.com:7766"])
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: home, lastWorked: nil, localIPs: [])
                == ["100.64.1.2:7766", "mac.example.com:7766"])
        // Having the Mac's very address is being on its network (another Hammock on that computer).
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: home, lastWorked: nil, localIPs: ["192.168.1.20"]) == addrs)
        // No subnets given (an older Mac): nothing is scoped.
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: [], lastWorked: nil, localIPs: []) == addrs)
    }

    @Test func lastWorkedFirstWithoutRepeats() {
        let addrs = ["100.64.1.2:7766", "10.8.0.2:7766", "192.168.1.20:7766"]
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: home, lastWorked: "192.168.1.20:7766", localIPs: ["192.168.1.33"])
                == ["192.168.1.20:7766", "100.64.1.2:7766", "10.8.0.2:7766"])
        // A last-worked address the Mac no longer lists is still tried first...
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: home, lastWorked: "192.168.1.40:7766", localIPs: ["192.168.1.33"])
                == ["192.168.1.40:7766", "100.64.1.2:7766", "10.8.0.2:7766", "192.168.1.20:7766"])
        // ...but scoped like the rest.
        #expect(RemoteCandidates.ordered(addrs, lanSubnets: home, lastWorked: "192.168.1.40:7766", localIPs: ["10.0.0.5"])
                == ["100.64.1.2:7766", "10.8.0.2:7766"])
        #expect(RemoteCandidates.ordered(["a:1", "a:1", ""], lanSubnets: [], lastWorked: "", localIPs: []) == ["a:1"])
    }

    @Test func subnetsAndHosts() {
        let s = RemoteCandidates.Subnet("10.1.0.0/16")!
        #expect(s.contains(RemoteCandidates.ipBytes("10.1.200.3")!))
        #expect(!s.contains(RemoteCandidates.ipBytes("10.2.0.1")!))
        #expect(!s.contains(RemoteCandidates.ipBytes("fd00::1")!))
        #expect(RemoteCandidates.Subnet("192.168.1.0/22")!.contains(RemoteCandidates.ipBytes("192.168.3.7")!))
        #expect(RemoteCandidates.Subnet("fd7a:115c::/48")!.contains(RemoteCandidates.ipBytes("fd7a:115c:0:1::2")!))
        #expect(RemoteCandidates.Subnet("192.168.1.0") == nil)
        #expect(RemoteCandidates.Subnet("192.168.1.0/33") == nil)

        #expect(RemoteCandidates.host(of: "192.168.1.2:7766") == "192.168.1.2")
        #expect(RemoteCandidates.host(of: "[fd00::2]:7766") == "fd00::2")
        #expect(RemoteCandidates.host(of: "mac.local:7766") == "mac.local")
        #expect(RemoteCandidates.address(host: "fd00::2", port: 7766) == "[fd00::2]:7766")
        #expect(RemoteCandidates.address(host: "10.0.0.1", port: 7766) == "10.0.0.1:7766")
    }
}
