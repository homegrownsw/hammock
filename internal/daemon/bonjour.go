package daemon

import (
	"context"
	"log/slog"
	"net"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// Bonjour (ADR-0015): while remote access is on, this computer advertises
// _hammock._tcp on each chosen local network it's on (never on Tailscale or a
// VPN, which don't carry multicast), so Link a Computer lists it on the
// others there and a paired phone finds it when its address has changed.
// The advertisement carries only its name and port: whoever answers there
// is checked by its certificate (and, linking, by the code) as anywhere.

const bonjourService = "_hammock._tcp"

// bonjourTarget is one advertisement: on interface iface, as name.
type bonjourTarget struct {
	iface string
	name  string
	port  int
}

// bonjourTargets is what to advertise now: this computer's name on each
// chosen local network it's on. Callers hold d.mu.
func (d *Daemon) bonjourTargets() []bonjourTarget {
	if !d.remote.on || len(d.cfg.RemoteListen) > 0 {
		return nil
	}
	var out []bonjourTarget
	for _, n := range d.remote.snap.listening() {
		if n.kind == kindLAN && n.iface != "" {
			out = append(out, bonjourTarget{iface: n.iface, name: d.macName(), port: remotePort()})
		}
	}
	if runtime.GOOS != "darwin" && len(out) > 1 {
		out = out[:1] // (Avahi advertises on every interface it manages: once is all)
	}
	return out
}

// bonjourAds keeps the advertisements up: update is called under d.mu, so
// it only hands the wanted set to a goroutine that registers and
// unregisters (bonjourRegister, per platform).
type bonjourAds struct {
	mu      sync.Mutex
	want    []bonjourTarget
	kick    chan struct{}
	once    sync.Once
	running map[bonjourTarget]func()
	log     *slog.Logger
}

func (b *bonjourAds) update(log *slog.Logger, want []bonjourTarget) {
	b.once.Do(func() {
		b.log = log
		b.kick = make(chan struct{}, 1)
		b.running = map[bonjourTarget]func(){}
		go b.loop()
	})
	b.mu.Lock()
	b.want = slices.Clone(want)
	b.mu.Unlock()
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

func (b *bonjourAds) loop() {
	for range b.kick {
		b.mu.Lock()
		want := slices.Clone(b.want)
		b.mu.Unlock()
		for t, stop := range b.running {
			if !slices.Contains(want, t) {
				stop()
				delete(b.running, t)
			}
		}
		for _, t := range want {
			if _, ok := b.running[t]; ok {
				continue
			}
			stop, err := bonjourRegister(t)
			if err != nil {
				if b.log != nil {
					b.log.Warn("bonjour", "iface", t.iface, "err", err)
				}
				continue
			}
			if b.log != nil {
				b.log.Info("bonjour advertising", "iface", t.iface, "name", t.name, "port", t.port)
			}
			b.running[t] = stop
		}
	}
}

// bonjourFound is a computer Bonjour found: its name and where it listens.
type bonjourFound struct {
	name  string
	addrs []string
}

// browseBonjour asks each chosen local network this computer is on who
// offers _hammock._tcp (one mDNS question, answers taken until ctx ends),
// leaving out this computer's own addresses.
func browseBonjour(ctx context.Context, nets []hostNet) []bonjourFound {
	var mine []net.IP
	for _, n := range nets {
		mine = append(mine, n.ips...)
	}
	var mu sync.Mutex
	var out []bonjourFound
	var wg sync.WaitGroup
	for _, n := range nets {
		if n.kind != kindLAN || n.iface == "" {
			continue
		}
		_, sub, err := net.ParseCIDR(n.id)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			wait := time.Until(deadlineOr(ctx, time.Now().Add(1500*time.Millisecond))) - 300*time.Millisecond // (leaving time to resolve)
			for _, f := range bonjourBrowse(ctx, n, max(wait, 500*time.Millisecond)) {
				// Each as found: two advertising one name aren't merged
				// (discovery merges by certificate), and only addresses on
				// the network it was found on (an advertiser can't send
				// this computer probing elsewhere).
				if strings.ContainsFunc(f.name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
					continue
				}
				e := bonjourFound{name: f.name}
				for _, a := range f.addrs {
					host, _, _ := net.SplitHostPort(a)
					ip := net.ParseIP(host)
					if ip == nil || !sub.Contains(ip) || slices.ContainsFunc(mine, ip.Equal) {
						continue
					}
					if !slices.Contains(e.addrs, a) {
						e.addrs = append(e.addrs, a)
					}
				}
				if len(e.addrs) > 0 {
					mu.Lock()
					out = append(out, e)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return out
}

// mdnsQuery asks n's interface for _hammock._tcp.local's instances from a port
// of our own (a "legacy unicast" query, RFC 6762 §6.7: responders answer
// it straight back, with the service's SRV and addresses alongside), and
// reads answers until ctx ends.
func mdnsQuery(ctx context.Context, n hostNet) []bonjourFound {
	ifi, err := net.InterfaceByName(n.iface)
	if err != nil || len(n.ips) == 0 {
		return nil
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: n.ips[0]})
	if err != nil {
		return nil
	}
	defer conn.Close()
	pc := ipv4.NewPacketConn(conn)
	_ = pc.SetMulticastInterface(ifi) // (else the route to 224.0.0.0/4 decides: maybe another network)
	_ = pc.SetMulticastTTL(255)
	q, err := mdnsQuestion()
	if err != nil {
		return nil
	}
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Second)
	}
	_ = conn.SetDeadline(deadline)
	// Asked twice (a lost packet on Wi-Fi is common), a moment apart.
	_, _ = conn.WriteToUDP(q, group)
	go func() {
		select {
		case <-time.After(300 * time.Millisecond):
			_, _ = conn.WriteToUDP(q, group)
		case <-ctx.Done():
		}
	}()
	var packets [][]byte
	buf := make([]byte, 9000)
	for len(packets) < 64 { // (answers from a whole network fit; a flood doesn't)
		nr, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		packets = append(packets, slices.Clone(buf[:nr]))
	}
	return parseMDNS(packets)
}

// parseMDNS reads the instances of _hammock._tcp in mDNS answers, and where
// each listens (its SRV's port, at its host's A records), each packet on
// its own: one responder can't add its addresses to another's host.
func parseMDNS(packets [][]byte) []bonjourFound {
	var out []bonjourFound
	for _, p := range packets {
		out = append(out, parseMDNSPacket(p)...)
	}
	return out
}

func parseMDNSPacket(pkt []byte) []bonjourFound {
	packets := [][]byte{pkt}
	instances := map[string]string{} // instance name → its SRV target host
	ports := map[string]int{}        // instance → port
	hosts := map[string][]net.IP{}   // host → addresses
	var names []string               // instances, in the order heard
	for _, pkt := range packets {
		var p dnsmessage.Parser
		if _, err := p.Start(pkt); err != nil {
			continue
		}
		if p.SkipAllQuestions() != nil {
			continue
		}
		for {
			h, err := p.AnswerHeader()
			if err != nil {
				break
			}
			mdnsRecord(&p, h, p.SkipAnswer, instances, ports, hosts, &names)
		}
		if p.SkipAllAuthorities() != nil {
			continue
		}
		for {
			h, err := p.AdditionalHeader()
			if err != nil {
				break
			}
			mdnsRecord(&p, h, p.SkipAdditional, instances, ports, hosts, &names)
		}
	}
	var out []bonjourFound
	for _, inst := range names {
		target, port := instances[inst], ports[inst]
		if target == "" || port == 0 {
			continue
		}
		f := bonjourFound{name: instanceLabel(inst)}
		for _, ip := range hosts[target] {
			f.addrs = append(f.addrs, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		}
		if len(f.addrs) > 0 {
			out = append(out, f)
		}
	}
	return out
}

func mdnsQuestion() ([]byte, error) {
	name, err := dnsmessage.NewName(bonjourService + ".local.")
	if err != nil {
		return nil, err
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{})
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// mdnsRecord takes the record whose header is h (PTR, SRV, A) into what's
// known, skipping anything else.
func mdnsRecord(p *dnsmessage.Parser, h dnsmessage.ResourceHeader, skip func() error, instances map[string]string, ports map[string]int, hosts map[string][]net.IP, names *[]string) {
	switch h.Type {
	case dnsmessage.TypePTR:
		r, err := p.PTRResource()
		if err != nil || !strings.EqualFold(h.Name.String(), bonjourService+".local.") {
			return // (read, so the next record follows)
		}
		inst := r.PTR.String()
		if _, ok := instances[inst]; !ok {
			instances[inst] = ""
			*names = append(*names, inst)
		}
	case dnsmessage.TypeSRV:
		r, err := p.SRVResource()
		if err != nil {
			return
		}
		inst := h.Name.String()
		if _, ok := instances[inst]; !ok {
			*names = append(*names, inst)
		}
		instances[inst], ports[inst] = strings.ToLower(r.Target.String()), int(r.Port)
	case dnsmessage.TypeA:
		r, err := p.AResource()
		if err != nil {
			return
		}
		host := strings.ToLower(h.Name.String())
		ip := net.IP(r.A[:])
		if !slices.ContainsFunc(hosts[host], ip.Equal) {
			hosts[host] = append(hosts[host], ip)
		}
	default:
		_ = skip()
	}
}

// instanceLabel is an instance's name as people read it: "Studio" from
// `Studio._hammock._tcp.local.` (escapes undone).
func instanceLabel(inst string) string {
	s := strings.TrimSuffix(inst, "."+bonjourService+".local.")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			n, _ := strconv.Atoi(s[i+1 : i+4])
			b.WriteByte(byte(n))
			i += 3
			continue
		}
		b.WriteByte(s[i+1])
		i++
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func deadlineOr(ctx context.Context, def time.Time) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return def
}
