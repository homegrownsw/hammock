package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func ifa(name, cidr string) ifAddr {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return ifAddr{name, ip, n.Mask, 0}
}

func TestHostNetworks(t *testing.T) {
	ts := tsNet{own: []net.IP{net.ParseIP("100.86.15.86"), net.ParseIP("fd7a:115c:a1e0::1")}}
	nets := hostNetworks([]ifAddr{
		ifa("lo0", "127.0.0.1/8"),
		ifa("en0", "192.168.1.20/24"),
		ifa("en0", "fe80::1/64"),
		ifa("en0", "2a02:c7c::5/64"), // a local network's IPv6: not a network here
		ifa("en7", "10.0.0.5/24"),
		ifa("utun3", "100.86.15.86/32"),
		ifa("utun3", "fd7a:115c:a1e0::1/128"),
		ifa("utun4", "10.8.0.2/24"),   // WireGuard
		ifa("utun5", "100.96.0.2/32"), // Cloudflare WARP, in Tailscale's range
		ifa("en1", "169.254.3.3/16"),
		ifa("en8", "203.0.113.7/24"),
	}, ts, map[string]string{"en0": "Wi-Fi", "en7": "Ethernet"})
	got := map[string]hostNet{}
	var ids []string
	for _, n := range nets {
		got[n.id] = n
		ids = append(ids, n.id)
	}
	want := []string{"tailscale", "192.168.1.0/24", "10.0.0.0/24", "10.8.0.0/24", "100.96.0.2/32", "203.0.113.0/24"}
	if !slices.Equal(ids, want) {
		t.Fatalf("networks %v, want %v", ids, want)
	}
	if n := got["tailscale"]; n.kind != kindTS || len(n.ips) != 2 {
		t.Errorf("tailscale: %+v", n)
	}
	if n := got["192.168.1.0/24"]; n.kind != kindLAN || n.label != "Wi-Fi" || n.iface != "en0" || n.public {
		t.Errorf("wi-fi: %+v", n)
	}
	if n := got["10.8.0.0/24"]; n.kind != kindVPN || n.label != "utun4" {
		t.Errorf("wireguard: %+v", n)
	}
	if n := got["100.96.0.2/32"]; n.kind != kindVPN {
		t.Errorf("warp: %+v", n)
	}
	if !got["203.0.113.0/24"].public {
		t.Error("a public range isn't marked so")
	}
}

func TestAdvertisedAndListening(t *testing.T) {
	nets := hostNetworks([]ifAddr{
		ifa("en0", "192.168.1.20/24"),
		ifa("utun4", "10.8.0.2/24"),
		ifa("en7", "10.0.0.5/24"),
	}, tsNet{own: []net.IP{net.ParseIP("100.86.15.86")}}, nil)
	s := netSnapshot{nets: nets, chosen: []string{"192.168.1.0/24", "tailscale", "10.8.0.0/24", "172.16.0.0/16"}, extra: []string{"mac.example.net:7433"}}
	if got := s.listenAddrs(7433); !slices.Equal(got, []string{"100.86.15.86:7433", "192.168.1.20:7433", "10.8.0.2:7433"}) {
		t.Errorf("listening on %v", got)
	}
	addrs, lan := s.advertised(7433)
	if !slices.Equal(addrs, []string{"100.86.15.86:7433", "10.8.0.2:7433", "192.168.1.20:7433", "mac.example.net:7433"}) {
		t.Errorf("advertised %v", addrs)
	}
	if !slices.Equal(lan, []string{"192.168.1.0/24"}) {
		t.Errorf("lan subnets %v", lan)
	}
	// Settings' list: those here, then those chosen but not here.
	var ids []string
	for _, n := range s.networks() {
		ids = append(ids, n.Id)
		if n.Id == "172.16.0.0/16" && (n.Connected || !n.Enabled) {
			t.Errorf("away network: %v", n)
		}
		if n.Id == "10.0.0.0/24" && (n.Enabled || !n.Connected) {
			t.Errorf("unchosen network: %v", n)
		}
	}
	if !slices.Equal(ids, []string{"tailscale", "192.168.1.0/24", "10.8.0.0/24", "10.0.0.0/24", "172.16.0.0/16"}) {
		t.Errorf("networks %v", ids)
	}
	if got := s.sourceLabel("192.168.1.30"); got != "192.168.1.30 on en0" {
		t.Errorf("source %q", got)
	}
}

func TestBridgesHidden(t *testing.T) {
	nets := hostNetworks([]ifAddr{
		ifa("eth0", "192.168.1.5/24"),
		ifa("docker0", "172.17.0.1/16"),
		ifa("br-3f2a9c8d1e0b", "172.18.0.1/16"),
		ifa("br-lan", "10.1.0.1/24"), // a bridge someone made: a real network
		ifa("virbr0", "192.168.122.1/24"),
		ifa("veth12ab", "169.254.9.9/16"),
		ifa("cni0", "10.88.0.1/16"),
		ifa("podman1", "10.89.0.1/24"),
	}, tsNet{}, nil)
	s := netSnapshot{nets: nets, chosen: []string{"172.18.0.0/16"}}
	var ids []string
	for _, n := range s.networks() {
		ids = append(ids, n.Id)
	}
	if want := []string{"192.168.1.0/24", "172.18.0.0/16", "10.1.0.0/24", "tailscale"}; !slices.Equal(ids, want) {
		t.Errorf("networks %v, want %v", ids, want)
	}
	if !bridgeInterface("docker0") || bridgeInterface("br-xyz") || bridgeInterface("br-3F2A9C8D1E0B") || bridgeInterface("eth0") {
		t.Error("bridgeInterface")
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"Tailscale":       "tailscale",
		"192.168.1.20/24": "192.168.1.0/24",
		" 10.8.0.0/24 ":   "10.8.0.0/24",
		"192.168.1.20":    "",
		"wifi":            "",
	} {
		if got := normalizeNetID(in); got != want {
			t.Errorf("normalizeNetID(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"mac.example.net":        "mac.example.net:7433",
		"mac.example.net:9000":   "mac.example.net:9000",
		"10.8.0.2":               "10.8.0.2:7433",
		"[fd00::1]":              "[fd00::1]:7433",
		"wss://mac.example.net":  "mac.example.net:7433",
		"a b":                    "",
		"host:0":                 "",
		"":                       "",
		"x\nscreen-sharing=true": "",
		"x=y":                    "",
		"x#y":                    "",
		"\"x\"":                  "",
	} {
		if got := normalizeAddress(in); got != want {
			t.Errorf("normalizeAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChosenNetworksConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	if got := chosenNetworks(); !slices.Equal(got, []string{"tailscale"}) {
		t.Fatalf("unset: %v", got)
	}
	os.WriteFile(filepath.Join(dir, "config"), []byte("# mine\nfont-size = 15\n"), 0o644)
	if err := hammockconfig.SetRepeated("remote-network", []string{"tailscale", "192.168.1.0/24"}); err != nil {
		t.Fatal(err)
	}
	if got := chosenNetworks(); !slices.Equal(got, []string{"tailscale", "192.168.1.0/24"}) {
		t.Fatalf("set: %v", got)
	}
	if err := hammockconfig.SetRepeated("remote-network", nil); err != nil {
		t.Fatal(err)
	}
	if got := chosenNetworks(); len(got) != 0 {
		t.Fatalf("set to none: %v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config"))
	if s := string(raw); !strings.Contains(s, "# mine\nfont-size = 15\nremote-network =\n") || strings.Count(s, "remote-network") != 1 {
		t.Fatalf("file:\n%s", s)
	}
	hammockconfig.SetRepeated("remote-address", []string{"mac.example.net", "bad host"})
	if got := extraAddresses(); !slices.Equal(got, []string{"mac.example.net:" + "7433"}) && remotePort() == 7433 {
		t.Fatalf("extra: %v", got)
	}
}

func TestWorthTrying(t *testing.T) {
	old := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = old })
	interfaceAddrs = func() []ifAddr { return []ifAddr{ifa("en0", "10.0.0.5/24"), ifa("utun3", "100.86.1.1/32")} }
	addrs := []string{"100.86.15.86:7433", "192.168.1.20:7433", "10.0.0.9:7433", "10.8.0.2:7433", "mac.example.net:7433"}
	lan := []string{"192.168.1.0/24", "10.0.0.0/24"}
	// At a café on 10.0.0.0/24 too? Same subnet: tried (its certificate
	// still decides). Home's 192.168.1.0/24: not here, not tried.
	got := worthTrying(addrs, lan)
	if !slices.Equal(got, []string{"100.86.15.86:7433", "10.0.0.9:7433", "10.8.0.2:7433", "mac.example.net:7433"}) {
		t.Errorf("worth trying: %v", got)
	}
	if got := worthTrying(addrs, nil); !slices.Equal(got, addrs) {
		t.Errorf("no subnets: %v", got)
	}
}

type fakeConn struct{ closed *atomic.Int32 }

func (f fakeConn) Close() error { f.closed.Add(1); return nil }

func TestRaceDial(t *testing.T) {
	old := dialStagger
	dialStagger = 20 * time.Millisecond
	t.Cleanup(func() { dialStagger = old })
	var closed atomic.Int32
	// The first hangs; the second connects: it wins without waiting out the first.
	start := time.Now()
	v, addr, err, _ := raceDial(context.Background(), []string{"slow", "fast", "fail"}, 5*time.Second, func(ctx context.Context, a string) (fakeConn, error) {
		switch a {
		case "slow":
			<-ctx.Done()
			return fakeConn{}, ctx.Err()
		case "fail":
			return fakeConn{}, errors.New("refused")
		}
		return fakeConn{&closed}, nil
	})
	if err != nil || addr != "fast" || v.closed == nil || time.Since(start) > time.Second {
		t.Fatalf("raced: %v %q %v", err, addr, time.Since(start))
	}
	// None connect: the first real error.
	_, _, err, errs := raceDial(context.Background(), []string{"a", "b"}, time.Second, func(ctx context.Context, a string) (fakeConn, error) {
		return fakeConn{}, errors.New("no " + a)
	})
	if err == nil || len(errs) != 2 {
		t.Fatalf("none: %v %v", err, errs)
	}
	// None connect, each saying it was cancelled (not by this call): still an error, never a zero conn
	// taken for a connection (a peer link sent on that nil client and the daemon crashed).
	v, _, err, _ = raceDial(context.Background(), []string{"a", "b"}, time.Second, func(ctx context.Context, a string) (fakeConn, error) {
		return fakeConn{}, fmt.Errorf("sign in: %w", context.Canceled)
	})
	if err == nil || v.closed != nil {
		t.Fatalf("all cancelled: %v %v", err, v)
	}
	// Two connect (both under way when the first does): the later one is closed.
	dialStagger = 0
	_, _, err, _ = raceDial(context.Background(), []string{"a", "b"}, time.Second, func(ctx context.Context, a string) (fakeConn, error) {
		time.Sleep(50 * time.Millisecond)
		return fakeConn{&closed}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); closed.Load() < 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the loser wasn't closed")
		}
	}
}

// An mDNS answer as mDNSResponder sends one: PTR, then SRV and A alongside.
func TestMDNSAnswer(t *testing.T) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, Authoritative: true})
	b.EnableCompression()
	svc := dnsmessage.MustNewName("_hammock._tcp.local.")
	inst := dnsmessage.MustNewName(`Alex\226\128\153s Studio._hammock._tcp.local.`)
	host := dnsmessage.MustNewName("Studio.local.")
	b.StartAnswers()
	b.PTRResource(dnsmessage.ResourceHeader{Name: svc, Class: dnsmessage.ClassINET, TTL: 10}, dnsmessage.PTRResource{PTR: inst})
	b.StartAdditionals()
	b.TXTResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 10}, dnsmessage.TXTResource{TXT: []string{""}})
	b.SRVResource(dnsmessage.ResourceHeader{Name: inst, Class: dnsmessage.ClassINET, TTL: 10}, dnsmessage.SRVResource{Port: 7433, Target: host})
	b.AResource(dnsmessage.ResourceHeader{Name: host, Class: dnsmessage.ClassINET, TTL: 10}, dnsmessage.AResource{A: [4]byte{192, 168, 1, 21}})
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	found := parseMDNS([][]byte{msg})
	if len(found) != 1 || found[0].name != "Alex’s Studio" || !slices.Equal(found[0].addrs, []string{"192.168.1.21:7433"}) {
		t.Fatalf("found %+v", found)
	}
}

func TestMDNSQuestion(t *testing.T) {
	q, err := mdnsQuestion()
	if err != nil {
		t.Fatal(err)
	}
	var p dnsmessage.Parser
	if _, err := p.Start(q); err != nil {
		t.Fatal(err)
	}
	qs, err := p.AllQuestions()
	if err != nil || len(qs) != 1 || qs[0].Name.String() != "_hammock._tcp.local." || qs[0].Type != dnsmessage.TypePTR {
		t.Fatalf("question %v %v", qs, err)
	}
}

// At a chosen local network's address, only from that subnet, unless extra
// addresses are given out (a forwarded port brings others).
func TestRemoteSourceOKLocalNetwork(t *testing.T) {
	d := &Daemon{}
	d.remote.snap = netSnapshot{
		nets:   hostNetworks([]ifAddr{ifa("en0", "192.168.1.20/24"), ifa("utun4", "10.8.0.2/24")}, tsNet{}, nil),
		chosen: []string{"192.168.1.0/24", "10.8.0.0/24"},
	}
	local := net.ParseIP("192.168.1.20")
	if !d.remoteSourceOK(net.ParseIP("192.168.1.30"), local) {
		t.Error("from the subnet: refused")
	}
	if d.remoteSourceOK(net.ParseIP("10.0.0.9"), local) {
		t.Error("from elsewhere, at the local network's address: let in")
	}
	if !d.remoteSourceOK(net.ParseIP("10.8.0.7"), net.ParseIP("10.8.0.2")) {
		t.Error("over the VPN: refused")
	}
	d.remote.snap.extra = []string{"mac.example.net:7433"}
	if !d.remoteSourceOK(net.ParseIP("203.0.113.9"), local) {
		t.Error("a forwarded port, with extra addresses given out: refused")
	}
}

// A listener's interface created again (tailscaled restarted: the same
// addresses, a new interface number) is listened on afresh: the old
// listener is bound to an interface that's gone, and takes nothing.
func TestListenAgainWhenInterfaceIsCreatedAgain(t *testing.T) {
	lo := ""
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			lo = i.Name
			break
		}
	}
	if lo == "" {
		t.Skip("no loopback interface")
	}
	tc := startComputer(t)
	d, addr := tc.d, tc.addr
	listen := func(index int) *http.Server {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.remote.snap = netSnapshot{ifaces: map[string]string{"127.0.0.1": lo}, index: map[string]int{lo: index}}
		// Tried again as watchNetworks' tick would: the port, free for a
		// moment, may be taken meanwhile by another test's connection.
		for range 20 {
			d.syncListeners([]string{addr})
			if slices.Equal(d.remote.addrs, []string{addr}) && len(d.remote.servers) == 1 {
				return d.remote.servers[0]
			}
			d.mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			d.mu.Lock()
		}
		t.Fatalf("index %d: listening on %v", index, d.remote.addrs)
		return nil
	}
	first := listen(1)
	if again := listen(1); again != first {
		t.Fatal("the same interface: listened on again")
	}
	if again := listen(2); again == first {
		t.Fatal("the interface created again: the old listener kept")
	}
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("after listening again: %v", err)
	}
	c.Close()
	// Its media listener (UDP, the same address) is opened again too.
	d.media.mu.Lock()
	var media []string
	for _, tr := range d.media.trs {
		media = append(media, tr.Conn.LocalAddr().String())
	}
	d.media.mu.Unlock()
	if !slices.Equal(media, []string{addr}) {
		t.Fatalf("media listening on %v, want %v", media, addr)
	}
}

func TestBindingFor(t *testing.T) {
	s := netSnapshot{ifaces: map[string]string{"100.64.0.2": "tailscale0"}, index: map[string]int{"tailscale0": 8}}
	if got := s.bindingFor("100.64.0.2:7433"); got != "tailscale0#8" {
		t.Errorf("binding: %q", got)
	}
	if got := s.bindingFor("192.168.1.20:7433"); got != "" {
		t.Errorf("unknown address: %q", got)
	}
}

// hmk remote restart listens again with the same settings, from a terminal
// without asking (it opens nothing that wasn't open), while a computer is
// linked too (which turning it off refuses): the link comes back. Agents
// are refused it; with remote access off there's nothing to restart.
func TestRestartRemoteAccess(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	buid := b.uid
	online := func() (bool, time.Time) {
		a.d.mu.Lock()
		defer a.d.mu.Unlock()
		if l := a.d.linkOf(buid); l != nil {
			return l.online, l.onlineAt
		}
		return false, time.Time{}
	}
	waitFor(t, "the link up", func() bool { up, _ := online(); return up })
	_, since := online()
	b.d.mu.Lock()
	first := b.d.remote.servers[0]
	b.d.media.mu.Lock()
	firstMedia := b.d.media.trs[0]
	b.d.media.mu.Unlock()
	b.d.mu.Unlock()

	asPerson(t, nobody)
	asPlain(t, func(cl *caller, _ int) bool { return cl == nil })
	restart := &pb.ClientMessage{Msg: &pb.ClientMessage_RestartRemoteAccess{RestartRemoteAccess: &pb.RestartRemoteAccess{}}}
	term := dialAs(t, b.dir, nil, "cli")
	if _, err := try(term, restart); err != nil {
		t.Fatalf("from a terminal: %v", err)
	}
	b.d.mu.Lock()
	addrs, again := slices.Clone(b.d.remote.addrs), b.d.remote.servers[0]
	var media []string
	newMedia := false
	b.d.media.mu.Lock()
	for _, tr := range b.d.media.trs {
		media = append(media, tr.Conn.LocalAddr().String())
		newMedia = tr != firstMedia
	}
	b.d.media.mu.Unlock()
	b.d.mu.Unlock()
	if again == first || !newMedia || !slices.Equal(addrs, []string{b.addr}) || !slices.Equal(media, []string{b.addr}) {
		t.Fatalf("after restarting: listening on %v, media on %v (a new server: %v, new media: %v)", addrs, media, again != first, newMedia)
	}
	waitFor(t, "the link up again", func() bool { up, at := online(); return up && at.After(since) })

	agent := dialAs(t, b.dir, &caller{pane: "pn_x", agent: true}, "cli")
	if _, err := try(agent, restart); err == nil {
		t.Fatal("an agent restarted remote access")
	}
	// Nowhere it could listen (an address that isn't this computer's): said,
	// not a quiet success; and the next restart, with it fixed, listens again.
	b.d.mu.Lock()
	given := b.d.cfg.RemoteListen
	b.d.cfg.RemoteListen = []string{"192.0.2.1:0"}
	b.d.mu.Unlock()
	if _, err := try(term, restart); err == nil || !strings.Contains(err.Error(), "couldn't listen on 192.0.2.1:0") {
		t.Fatalf("restarting where it can't listen: %v", err)
	}
	b.d.mu.Lock()
	b.d.cfg.RemoteListen = given
	b.d.mu.Unlock()
	if _, err := try(term, restart); err != nil {
		t.Fatalf("restarting again: %v", err)
	}
	b.d.mu.Lock()
	b.d.stopRemote()
	b.d.mu.Unlock()
	if _, err := try(term, restart); err == nil || !strings.Contains(err.Error(), "remote access is off") {
		t.Fatalf("restarting while off: %v", err)
	}
}
