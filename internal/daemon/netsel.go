package daemon

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexjsp/hammock/internal/hammockconfig"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Remote access on any network (ADR-0015). Remote access listens on the
// networks a person chose: Tailscale, a local network (Wi-Fi, Ethernet) or
// a VPN (WireGuard and the like). Each is remembered by its subnet
// ("192.168.1.0/24"), so a VPN whose utun is numbered afresh is still the
// same network, and a café's Wi-Fi on another subnet isn't the one chosen at
// home. Tailscale is one network ("tailscale"), whatever its addresses.
//
// Config (~/.config/hammock/config, written by SetRemoteNetworks):
//
//	remote-network = tailscale         (repeatable; unset: Tailscale only, as before)
//	remote-network = 192.168.1.0/24
//	remote-address = mac.example.net   (repeatable: extra host[:port] given out)

const (
	netTailscale = "tailscale"
	kindTS       = "tailscale"
	kindLAN      = "lan"
	kindVPN      = "vpn"
)

// hostNet is a network this computer is on now.
type hostNet struct {
	id, label, kind, iface string
	ips                    []net.IP
	public                 bool
}

// chosenNetworks is the networks remote access listens on (config's
// remote-network: unset is Tailscale only, as before networks could be
// chosen; set to nothing is none).
func chosenNetworks() []string {
	vals, set := hammockconfig.Repeated("remote-network")
	if !set {
		return []string{netTailscale}
	}
	var out []string
	for _, v := range vals {
		if id := normalizeNetID(v); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// normalizeNetID: "tailscale", or a subnet as its network address
// ("192.168.1.20/24" → "192.168.1.0/24"); "" if it's neither.
func normalizeNetID(s string) string {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, netTailscale) {
		return netTailscale
	}
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		return ""
	}
	return n.String()
}

// extraAddresses is config's remote-address, as host:port (the port this
// computer listens on when not given).
func extraAddresses() []string {
	vals, _ := hammockconfig.Repeated("remote-address")
	var out []string
	for _, v := range vals {
		if a := normalizeAddress(v); a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// normalizeAddress makes host[:port] host:port; "" if it isn't one (it's
// written to the config file: nothing that could start another line or
// key there).
func normalizeAddress(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "wss://")
	s = strings.TrimSuffix(s, "/")
	if s == "" || len(s) > 255 || strings.ContainsAny(s, " /=#\"'\\") || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return ""
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		if n, err := strconv.Atoi(p); err != nil || n <= 0 || n > 65535 || h == "" {
			return ""
		}
		return s
	}
	return net.JoinHostPort(strings.Trim(s, "[]"), strconv.Itoa(remotePort()))
}

// vpnInterface: an interface name that's a tunnel (WireGuard, OpenVPN,
// IPsec, a utun some VPN app made), not Wi-Fi or Ethernet.
func vpnInterface(name string) bool {
	for _, p := range []string{"utun", "tun", "tap", "wg", "ppp", "ipsec", "zt", "tailscale", "gif", "stf"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// bridgeInterface: a container's or VM's bridge or virtual link (Docker's
// docker0 and br-<12 hex>, libvirt's virbr*, veth*, CNI's, Podman's): never
// a network anyone reaches this computer over, so not offered.
func bridgeInterface(name string) bool {
	if name == "docker0" {
		return true
	}
	if id, ok := strings.CutPrefix(name, "br-"); ok && len(id) == 12 && strings.Trim(id, "0123456789abcdef") == "" {
		return true
	}
	for _, p := range []string{"virbr", "veth", "cni", "podman"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// hostNetworks is the networks this computer is on now: Tailscale (its own
// addresses), then each other interface's IPv4 subnet. Loopback and
// link-local addresses aren't networks; IPv6 is Tailscale's only (a local
// network's IPv6 addresses change, its subnets are the internet's).
func hostNetworks(addrs []ifAddr, ts tsNet, labels map[string]string) []hostNet {
	var out []hostNet
	if len(ts.own) > 0 {
		out = append(out, hostNet{id: netTailscale, label: "Tailscale", kind: kindTS, ips: slices.Clone(ts.own)})
	}
	byID := map[string]int{}
	for _, a := range addrs {
		ip := a.ip.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		if slices.ContainsFunc(ts.own, ip.Equal) {
			continue
		}
		mask := a.mask
		if len(mask) == net.IPv6len {
			mask = mask[12:]
		}
		if len(mask) != net.IPv4len {
			mask = net.CIDRMask(32, 32)
		}
		n := &net.IPNet{IP: ip.Mask(mask), Mask: mask}
		id := n.String()
		if i, ok := byID[id]; ok {
			out[i].ips = append(out[i].ips, ip)
			continue
		}
		kind := kindLAN
		if vpnInterface(a.name) || isTailscaleIP(ip) { // (another VPN in Tailscale's range: Cloudflare WARP)
			kind = kindVPN
		}
		label := labels[a.name]
		if label == "" {
			label = a.name
		}
		byID[id] = len(out)
		out = append(out, hostNet{id: id, label: label, kind: kind, iface: a.name, ips: []net.IP{ip},
			public: !ip.IsPrivate() && !isTailscaleIP(ip)})
	}
	return out
}

// portLabels is each interface's name as people know it ("Wi-Fi",
// "Ethernet", "Thunderbolt Bridge"): macOS's hardware ports. Asked once.
var portLabels = sync.OnceValue(func() map[string]string {
	out := map[string]string{}
	if runtime.GOOS != "darwin" {
		return out
	}
	raw, err := runOutput(context.Background(), "/usr/sbin/networksetup", "-listallhardwareports")
	if err != nil {
		return out
	}
	port := ""
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "Hardware Port: "); ok {
			port = v
		} else if v, ok := strings.CutPrefix(line, "Device: "); ok && port != "" {
			out[v] = port
		}
	}
	return out
})

// netSnapshot is what remote access knows of this computer's networks:
// those it's on, and which are chosen. Taken off the lock (Tailscale may
// be asked).
type netSnapshot struct {
	nets   []hostNet
	chosen []string
	extra  []string
	ifaces map[string]string // this computer's addresses → their interface
}

func takeNetSnapshot() netSnapshot { return snapshotWith(tailscaleNet()) }

func snapshotWith(ts tsNet) netSnapshot {
	addrs := interfaceAddrs()
	ifaces := map[string]string{}
	for _, a := range addrs {
		ifaces[a.ip.String()] = a.name
	}
	return netSnapshot{
		nets:   hostNetworks(addrs, ts, portLabels()),
		chosen: chosenNetworks(),
		extra:  extraAddresses(),
		ifaces: ifaces,
	}
}

// ifaceFor is the interface of a listen address's host ("" if unknown).
func (s netSnapshot) ifaceFor(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return s.ifaces[ip.String()]
	}
	return ""
}

// lanOf is the chosen local network ip is this computer's address on, if
// it's one.
func (s netSnapshot) lanOf(ip net.IP) *net.IPNet {
	for _, n := range s.listening() {
		if n.kind != kindLAN || !slices.ContainsFunc(n.ips, ip.Equal) {
			continue
		}
		if _, sub, err := net.ParseCIDR(n.id); err == nil {
			return sub
		}
	}
	return nil
}

// cachedNetSnapshot is takeNetSnapshot without asking Tailscale (its
// addresses as last looked up): fine under d.mu.
func cachedNetSnapshot() netSnapshot {
	tsCache.Lock()
	ts := tsCache.net
	tsCache.Unlock()
	return snapshotWith(ts)
}

// listening is the chosen networks this computer is on now.
func (s netSnapshot) listening() []hostNet {
	var out []hostNet
	for _, n := range s.nets {
		if slices.Contains(s.chosen, n.id) {
			out = append(out, n)
		}
	}
	return out
}

// listenAddrs is where remote access listens: each chosen network's
// addresses here, on port.
func (s netSnapshot) listenAddrs(port int) []string {
	var out []string
	for _, n := range s.listening() {
		for _, ip := range n.ips {
			out = append(out, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		}
	}
	return out
}

// advertised is where others reach this computer: Tailscale's addresses
// first (they work from anywhere), then VPNs', then local networks' (tried
// only from that network: lanSubnets), then the extra addresses, at most 8.
func (s netSnapshot) advertised(port int) (addrs, lanSubnets []string) {
	order := map[string]int{kindTS: 0, kindVPN: 1, kindLAN: 2}
	nets := s.listening()
	slices.SortStableFunc(nets, func(a, b hostNet) int { return order[a.kind] - order[b.kind] })
	for _, n := range nets {
		if n.kind == kindLAN {
			lanSubnets = append(lanSubnets, n.id)
		}
		for _, ip := range n.ips {
			addrs = append(addrs, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		}
	}
	for _, a := range s.extra {
		if !slices.Contains(addrs, a) {
			addrs = append(addrs, a)
		}
	}
	return addrs[:min(len(addrs), 8)], lanSubnets
}

// networks is the list Settings shows: the networks this computer is on
// (not containers' and VMs' bridges, unless chosen), then those chosen but
// not here now.
func (s netSnapshot) networks() []*pb.RemoteNetwork {
	var out []*pb.RemoteNetwork
	seen := map[string]bool{}
	for _, n := range s.nets {
		if bridgeInterface(n.iface) && !slices.Contains(s.chosen, n.id) {
			continue
		}
		seen[n.id] = true
		r := &pb.RemoteNetwork{Id: n.id, Label: n.label, Kind: n.kind, Enabled: slices.Contains(s.chosen, n.id), Connected: true, Public: n.public}
		for _, ip := range n.ips {
			r.Addresses = append(r.Addresses, ip.String())
		}
		out = append(out, r)
	}
	if !seen[netTailscale] {
		out = append(out, &pb.RemoteNetwork{Id: netTailscale, Label: "Tailscale", Kind: kindTS, Enabled: slices.Contains(s.chosen, netTailscale)})
		seen[netTailscale] = true
	}
	for _, id := range s.chosen {
		if !seen[id] {
			out = append(out, &pb.RemoteNetwork{Id: id, Label: id, Kind: kindLAN, Enabled: true})
		}
	}
	return out
}

// sourceLabel says where a connection came from, for a link's code check
// ("192.168.1.30 on Wi-Fi").
func (s netSnapshot) sourceLabel(ip string) string {
	x := net.ParseIP(ip)
	if x == nil {
		return ip
	}
	for _, n := range s.nets {
		if n.id == netTailscale {
			continue
		}
		if _, sub, err := net.ParseCIDR(n.id); err == nil && sub.Contains(x) {
			return ip + " on " + n.label
		}
	}
	return ip
}

// ---------- listening ----------

// remoteSourceOK: a connection from src that arrived at local may be
// served. Each listener is bound to a chosen network's address and its
// interface, so arriving there is being on that network; at a Tailscale
// address it must come from Tailscale too, and at a local network's from
// that subnet (unless extra addresses are given out: a router's forwarded
// port brings others). Listening on addresses given explicitly (tests, a
// second host on one Mac), Tailscale and loopback.
func (d *Daemon) remoteSourceOK(src, local net.IP) bool {
	if src == nil {
		return false
	}
	d.mu.Lock()
	explicit := len(d.cfg.RemoteListen) > 0
	snap := d.remote.snap
	d.mu.Unlock()
	if !explicit && local != nil && len(snap.extra) == 0 {
		if sub := snap.lanOf(local); sub != nil && !sub.Contains(src) {
			return false
		}
	}
	if explicit {
		return isTailscaleIP(src) || src.IsLoopback()
	}
	if local == nil || local.IsUnspecified() {
		return isTailscaleIP(src)
	}
	if isTailscaleIP(local) && !isTailscaleIP(src) {
		return false
	}
	return true
}

// errNoNetwork: remote access is on, but no network is chosen to listen on.
var errNoNetwork = errors.New("choose a network for remote access first (Settings ▸ Devices ▸ Remote Access, or hmk remote networks)")

// syncListeners listens on want and stops listening elsewhere, keeping the
// connections on addresses still wanted. Callers hold d.mu.
func (d *Daemon) syncListeners(want []string) {
	keep := d.remote.servers[:0]
	var keepAddrs, gone []string
	for i, s := range d.remote.servers {
		a := d.remote.addrs[i]
		if slices.Contains(want, a) {
			keep, keepAddrs = append(keep, s), append(keepAddrs, a)
			continue
		}
		_ = s.Close()
		gone = append(gone, a)
		d.log.Info("remote access stopped listening", "addr", a)
	}
	d.remote.servers, d.remote.addrs = keep, keepAddrs
	// A server's Close leaves its WebSockets (hijacked) open: whoever came
	// in on a network no longer chosen goes now too.
	for c := range d.conns {
		if c.remote && c.localAt != "" && slices.Contains(gone, c.localAt) {
			c.cancel()
		}
	}
	var added []string
	for _, a := range want {
		if slices.Contains(d.remote.addrs, a) {
			continue
		}
		if d.listenOn(a, d.remote.snap.ifaceFor(a)) {
			added = append(added, d.remote.addrs[len(d.remote.addrs)-1])
		}
	}
	if len(gone) > 0 {
		d.media.stopAddrs(gone) // (now: the same address may be listened on again at once)
	}
	if len(added) > 0 {
		d.media.listen(added, d.remote.cert, d.remote.snap.ifaceFor)
	}
}

// watchNetworks follows this computer's networks while remote access is
// on: joining a chosen one listens there, leaving it stops; this
// computer's record (its addresses) follows, and so does Bonjour.
func (d *Daemon) watchNetworks(stop <-chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var lastIfs string
	ticks := 0
	for {
		select {
		case <-stop:
			return
		case <-d.done:
			return
		case <-t.C:
		}
		// Interfaces are cheap to look at; Tailscale's CLI isn't: asked
		// again when they change, else every minute.
		var b strings.Builder
		for _, a := range interfaceAddrs() {
			b.WriteString(a.name + a.ip.String() + ";")
		}
		if ticks++; b.String() != lastIfs || ticks%12 == 0 {
			refreshTailscale()
			lastIfs = b.String()
		}
		d.applyNetworks(cachedNetSnapshot())
	}
}

// applyNetworks listens on snap's chosen networks (if remote access is on
// and its addresses weren't given explicitly). Off the lock.
func (d *Daemon) applyNetworks(snap netSnapshot) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.remote.on || len(d.cfg.RemoteListen) > 0 {
		return
	}
	want := snap.listenAddrs(remotePort())
	before := slices.Clone(d.remote.addrs)
	d.remote.snap = snap
	d.syncListeners(want)
	if !slices.Equal(before, d.remote.addrs) || !slices.Equal(d.remote.advertised, d.advertisedLocked()) {
		d.remote.advertised = d.advertisedLocked()
		if d.comp.self != nil {
			d.refreshLinked()
		}
		d.bonjour.update(d.log, d.bonjourTargets())
	}
}

// advertisedLocked is where this computer is reached at now (addresses,
// then lan subnets folded in for change detection). Callers hold d.mu.
func (d *Daemon) advertisedLocked() []string {
	a, l := d.reachAt()
	return append(a, l...)
}

// reachAt is where this computer is reached now, and which of those
// addresses are a local network's (as advertised). Listening on addresses
// given explicitly, those (a wildcard as Tailscale's addresses). Callers
// hold d.mu.
func (d *Daemon) reachAt() (addrs, lanSubnets []string) {
	if len(d.cfg.RemoteListen) > 0 {
		return d.explicitAddrs(), nil
	}
	snap := d.remote.snap
	if !d.remote.on {
		snap = cachedNetSnapshot() // (where it would be: a pairing code made before it's on)
	}
	return snap.advertised(remotePort())
}

// explicitAddrs is reachAt for addresses given explicitly: never a
// wildcard (":7433" listens on every address: it's given out as this
// computer's Tailscale addresses, as last looked up, with that port).
// Callers hold d.mu.
func (d *Daemon) explicitAddrs() []string {
	var out []string
	for _, a := range d.remote.addrs {
		host, port, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsUnspecified() {
			out = append(out, a)
			continue
		}
		for _, ip := range tailscaleAddrsCached() {
			if t := net.JoinHostPort(ip.String(), port); !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out[:min(len(out), 8)]
}

// setRemoteNetworks: SetRemoteNetworks. Writes the config, then listens on
// the new set. Off the lock.
func (d *Daemon) setRemoteNetworks(m *pb.SetRemoteNetworks) error {
	var nets, extra []string
	for _, n := range m.Networks {
		id := normalizeNetID(n)
		if id == "" {
			return fmt.Errorf("%q isn't a network: use tailscale or a subnet, like 192.168.1.0/24", n)
		}
		if !slices.Contains(nets, id) {
			nets = append(nets, id)
		}
	}
	var seen []string
	for _, a := range m.ExtraAddresses {
		x := normalizeAddress(a)
		if x == "" {
			return fmt.Errorf("%q isn't an address: use host or host:port", a)
		}
		if !slices.Contains(seen, x) {
			seen = append(seen, x)
			extra = append(extra, strings.TrimSpace(a)) // (as given: checked above, so one line, one value)
		}
	}
	if len(extra) > 8 {
		return errors.New("at most 8 extra addresses")
	}
	d.mu.Lock()
	if len(nets) == 0 && d.remote.on && d.linkedCount() > 0 {
		d.mu.Unlock()
		return errors.New("your linked computers reach this one through remote access: keep a network chosen, or unlink them first")
	}
	d.mu.Unlock()
	if err := hammockconfig.SetRepeated("remote-network", nets); err != nil {
		return err
	}
	d.cfgw.noteWritten("remote-network", nets, true) // asked for here: no warning (configwatch.go)
	setExtra := func() error { return hammockconfig.SetRepeated("remote-address", extra) }
	if len(extra) == 0 {
		setExtra = func() error { return hammockconfig.Unset("remote-address") }
	}
	if err := setExtra(); err != nil {
		return err
	}
	d.mu.Lock()
	d.audit("user", "remote-networks", strings.Join(nets, ","), strings.Join(extra, ","))
	d.mu.Unlock()
	d.applyNetworks(takeNetSnapshot())
	return nil
}
