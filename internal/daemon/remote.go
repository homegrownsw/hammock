package daemon

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/alexjsp/hammock/internal/wirezstd"

	"github.com/alexjsp/hammock/internal/client"
	"github.com/alexjsp/hammock/internal/hammockconfig"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
)

// Remote access (ADR-0007, ADR-0015): WebSocket + TLS listeners bound only
// to the addresses of the networks a person chose (netsel.go): Tailscale's
// (as Tailscale reports them, so another VPN using the same range isn't
// listened on), a local network's, a VPN's. Off by default. Devices pair once with a
// short-lived secret from a QR code, then authenticate every connection by
// signing a fresh nonce with their Ed25519 key. The TLS certificate is
// self-signed and pinned by fingerprint.

// DefaultRemotePort is the TCP port remote clients connect to.
const DefaultRemotePort = 7433

// remotePort is this daemon's own port: the app's HammockRemotePort (Hammock β
// has its own, so it can take remote clients while Hammock does), else the
// default. Other computers are assumed to listen on the default.
func remotePort() int {
	if n, err := strconv.Atoi(bundleInfo("HammockRemotePort")); err == nil && n > 0 && n < 65536 {
		return n
	}
	return DefaultRemotePort
}

const pairingTTL = 10 * time.Minute

// Until a remote connection has authenticated it gets little: small
// messages (a PeerHello with its 256 KB picture fits), a few seconds to
// sign in, and only so many such connections at once, per address and
// overall.
var authTimeout = 15 * time.Second // (a variable for tests)

const (
	preAuthReadLimit = 1 << 20
	authedReadLimit  = 32 << 20
	maxPreAuthPerIP  = 8
	maxPreAuth       = 64
)

type remoteState struct {
	on          bool // remote access is on (it may be listening nowhere: no chosen network here now)
	cert        tls.Certificate
	fingerprint string
	servers     []*http.Server
	addrs       []string    // where each of servers listens
	snap        netSnapshot // this computer's networks, as last looked at
	advertised  []string    // reachAt, as last told to the others
	watchStop   chan struct{}
	failed      map[string]bool      // addresses that couldn't be listened on (logged once)
	pairings    map[string]time.Time // hex(secret) → expiry
	gate        authGate
	preAuth     map[*conn]func()     // unauthenticated connections → what to do once they are
	failedAt    map[string]time.Time // source address → when a failed sign-in from it was last audited
	failWindow  time.Time            // the minute failAudited counts in
	failAudited int                  // failed sign-ins audited in it
	failMore    int                  // and not, to be audited as one row
}

// failAuditEvery is how often a failed sign-in from one address (an IPv6
// one's /64) is audited (the connection closes after each; the rest aren't
// recorded). failAuditBudget is how many are audited a minute in all:
// beyond it, one row a minute says how many more there were.
const (
	failAuditEvery  = time.Minute
	failAuditBudget = 20
)

// failSource is the address failed sign-ins are counted against: an IPv6
// address's /64 (one network can have any number of addresses in it), an
// IPv4 address itself.
func failSource(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || a.Unmap().Is4() {
		return ip
	}
	p, err := a.Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
}

// auditSignInFailed audits a failed sign-in (Pair, Authenticate,
// PeerHello, or a join with a bad invite) from c, at most once every
// failAuditEvery per source (failSource), and failAuditBudget a minute
// in all, the rest counted in one row a minute: anyone who can reach the
// listener can fail as often as they like, from as many addresses as
// they have. Callers hold d.mu.
func (d *Daemon) auditSignInFailed(c *conn, actor, action, target, detail string) {
	now := time.Now()
	r := &d.remote
	if r.failedAt == nil {
		r.failedAt = map[string]time.Time{}
	}
	src := failSource(c.srcIP)
	if now.Sub(r.failedAt[src]) < failAuditEvery {
		return
	}
	if len(r.failedAt) >= 1024 {
		for ip, at := range r.failedAt {
			if now.Sub(at) >= failAuditEvery {
				delete(r.failedAt, ip)
			}
		}
	}
	if len(r.failedAt) < 1024 {
		r.failedAt[src] = now
	}
	if now.Sub(r.failWindow) >= failAuditEvery {
		r.failWindow, r.failAudited = now, 0
	}
	if r.failAudited >= failAuditBudget {
		if r.failMore++; r.failMore == 1 {
			// One row for the rest, once this minute is up.
			time.AfterFunc(time.Until(r.failWindow.Add(failAuditEvery)), d.auditMoreFailed)
		}
		return
	}
	r.failAudited++
	d.audit(actor, action, deviceName(target), deviceName(detail))
}

// auditMoreFailed records how many failed sign-ins weren't audited one by
// one (auditSignInFailed).
func (d *Daemon) auditMoreFailed() {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.remote.failMore
	if n == 0 {
		return
	}
	d.remote.failMore = 0
	d.audit("remote", "auth-failed", "", strconv.Itoa(n)+" more failed sign-ins, not recorded one by one")
}

// authGate counts unauthenticated remote connections.
type authGate struct {
	mu    sync.Mutex
	total int
	perIP map[string]int
}

// enter takes a slot for ip, returning its release (safe to call twice).
func (g *authGate) enter(ip string) (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.perIP == nil {
		g.perIP = map[string]int{}
	}
	if g.total >= maxPreAuth || g.perIP[ip] >= maxPreAuthPerIP {
		return nil, false
	}
	g.total++
	g.perIP[ip]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.total--
			if g.perIP[ip]--; g.perIP[ip] <= 0 {
				delete(g.perIP, ip)
			}
		})
	}, true
}

// isTailscaleIP reports whether ip is in Tailscale's CGNAT or ULA ranges.
func isTailscaleIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 100 && v4[1]&0xC0 == 64 // 100.64.0.0/10
	}
	return len(ip) == net.IPv6len && ip[0] == 0xfd && ip[1] == 0x7a && ip[2] == 0x11 && ip[3] == 0x5c && ip[4] == 0xa1 && ip[5] == 0xe0
}

// tailscaleAddrs is this Mac's own Tailscale addresses: what Tailscale
// says they are, else (it can't be asked) every interface address in its
// ranges.
func tailscaleAddrs() []net.IP { return tailscaleNet().own }

// tailscaleAddrsCached is this computer's Tailscale addresses as last looked
// up (none if they never were): never asks Tailscale, so fine under d.mu.
func tailscaleAddrsCached() []net.IP {
	tsCache.Lock()
	defer tsCache.Unlock()
	return slices.Clone(tsCache.net.own)
}

// tsNet is what remote access knows of this Mac's Tailscale addresses.
type tsNet struct {
	own     []net.IP // Tailscale's own addresses here, to listen on
	others  []string // other interfaces' addresses in Tailscale's ranges, e.g. "utun5 100.96.0.2" (another VPN, such as Cloudflare WARP)
	unknown bool     // Tailscale couldn't be asked and several interfaces have addresses in its ranges
}

// ifAddr is an interface's address.
type ifAddr struct {
	name string
	ip   net.IP
	mask net.IPMask
}

// These are variables for tests.
var (
	// tailscaleCommands are the Tailscale CLIs tried in turn: on PATH (or
	// Homebrew's, or a Linux sbin, as an init script's PATH may lack it), and
	// the one inside the Mac App Store and standalone app.
	tailscaleCommands = []string{"tailscale", "/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale", "/usr/local/sbin/tailscale", "/usr/sbin/tailscale", "/usr/bin/tailscale", "/Applications/Tailscale.app/Contents/MacOS/Tailscale"}
	interfaceAddrs    = func() []ifAddr {
		var out []ifAddr
		ifs, _ := net.Interfaces()
		for _, i := range ifs {
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok {
					out = append(out, ifAddr{i.Name, n.IP, n.Mask})
				}
			}
		}
		return out
	}
)

var tsCache struct {
	sync.Mutex
	at         time.Time
	net        tsNet
	refreshing bool
}

// tailscaleNet asks Tailscale for its addresses (cached briefly: pairing,
// invites and listening all want them). Callers often hold d.mu, so once
// there's an answer a stale one is served while a fresh one is fetched in
// the background: the CLI can take seconds (or hang) and every pane would
// wait on it.
func tailscaleNet() tsNet {
	tsCache.Lock()
	defer tsCache.Unlock()
	if time.Since(tsCache.at) < 10*time.Second {
		return tsCache.net
	}
	if !tsCache.at.IsZero() {
		if !tsCache.refreshing {
			tsCache.refreshing = true
			go func() {
				ips, err := askTailscale()
				n := pickTailscale(ips, err == nil, interfaceAddrs())
				tsCache.Lock()
				tsCache.net, tsCache.at, tsCache.refreshing = n, time.Now(), false
				tsCache.Unlock()
			}()
		}
		return tsCache.net
	}
	ips, err := askTailscale()
	tsCache.net, tsCache.at = pickTailscale(ips, err == nil, interfaceAddrs()), time.Now()
	return tsCache.net
}

// askTailscale runs `tailscale ip` with the first CLI that answers.
func askTailscale() ([]net.IP, error) {
	var last error = errors.New("no Tailscale CLI")
	for _, name := range tailscaleCommands {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := tailscaleCLI(ctx, path, "ip").Output()
		cancel()
		if err != nil {
			last = err
			continue
		}
		var ips []net.IP
		for _, line := range strings.Fields(string(out)) {
			if ip := net.ParseIP(line); ip != nil && isTailscaleIP(ip) {
				ips = append(ips, ip)
			}
		}
		if len(ips) > 0 {
			return ips, nil
		}
		last = errors.New("tailscale ip: no addresses")
	}
	return nil, last
}

// pickTailscale chooses the addresses to listen on from the interfaces'
// addresses in Tailscale's ranges: those Tailscale says are its own when
// asked, else all of them. Anything else in its ranges is another
// network's (a VPN like Cloudflare WARP uses 100.64.0.0/10 too).
func pickTailscale(said []net.IP, asked bool, addrs []ifAddr) tsNet {
	var in []ifAddr
	for _, a := range addrs {
		if isTailscaleIP(a.ip) {
			in = append(in, a)
		}
	}
	var n tsNet
	if !asked {
		names := map[string]bool{}
		for _, a := range in {
			n.own = append(n.own, a.ip)
			names[a.name] = true
		}
		n.unknown = len(names) > 1
		return n
	}
	for _, a := range in {
		if slices.ContainsFunc(said, a.ip.Equal) {
			n.own = append(n.own, a.ip)
		} else {
			n.others = append(n.others, a.name+" "+a.ip.String())
		}
	}
	return n
}

// loadOrCreateCert returns the daemon's TLS certificate, creating a
// self-signed one on first use.
func loadOrCreateCert(dir string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, "tls", "cert.pem"), filepath.Join(dir, "tls", "key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		sum := sha256.Sum256(c.Certificate[0])
		return c, hex.EncodeToString(sum[:]), nil
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "hmkd " + host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host, "localhost"},
		IPAddresses:  append(tailscaleAddrs(), net.IPv4(127, 0, 0, 1)),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return c, hex.EncodeToString(sum[:]), nil
}

// startRemote turns remote access on: listening on the chosen networks
// this computer is on now (or the addresses given explicitly), and on
// each chosen network it joins later. Callers hold d.mu (warmRemote first,
// off it, so the networks are known).
func (d *Daemon) startRemote() error {
	if d.remote.on {
		return nil
	}
	cert, fp, err := loadOrCreateCert(d.cfg.Dir)
	if err != nil {
		return err
	}
	d.remote.cert, d.remote.fingerprint = cert, fp
	if listen := d.cfg.RemoteListen; len(listen) > 0 {
		d.remote.addrs = nil
		for _, addr := range listen {
			d.listenOn(addr, "")
		}
		if len(d.remote.servers) == 0 {
			return errors.New("could not listen on any address given")
		}
		d.media.listen(d.remote.addrs, cert, nil)
	} else {
		snap := cachedNetSnapshot()
		if len(snap.chosen) == 0 {
			return errNoNetwork
		}
		d.remote.snap = snap
		d.syncListeners(snap.listenAddrs(remotePort()))
		d.remote.watchStop = make(chan struct{})
		go d.watchNetworks(d.remote.watchStop)
	}
	d.remote.on = true
	d.remote.advertised = d.advertisedLocked()
	d.bonjour.update(d.log, d.bonjourTargets())
	if d.comp.self != nil {
		d.refreshLinked() // linked computers reach it here
	}
	return nil
}

// listenOn opens a remote listener on addr, reporting whether it could.
// Callers hold d.mu.
func (d *Daemon) listenOn(addr, iface string) bool {
	lc := net.ListenConfig{}
	if iface != "" {
		lc.Control = bindToInterface(iface)
	}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		if d.remote.failed == nil {
			d.remote.failed = map[string]bool{}
		}
		if !d.remote.failed[addr] { // (tried again every few seconds: said once)
			d.remote.failed[addr] = true
			d.log.Warn("remote listen", "addr", addr, "err", err)
		}
		return false
	}
	delete(d.remote.failed, addr)
	tcfg := &tls.Config{Certificates: []tls.Certificate{d.remote.cert}, MinVersion: tls.VersionTLS13}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1", d.handleRemoteWS)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, TLSConfig: tcfg}
	d.remote.servers = append(d.remote.servers, srv)
	d.remote.addrs = append(d.remote.addrs, ln.Addr().String())
	go srv.ServeTLS(ln, "", "")
	d.log.Info("remote access listening", "addr", ln.Addr().String())
	return true
}

// warmRemote asks Tailscale for this computer's addresses off the lock,
// if remote access may be about to start, so startRemote (under d.mu)
// finds them cached instead of running the CLI with everything waiting.
// Listening on every address (":7433"), this computer is reached at its
// Tailscale addresses (linkAddrs): they're looked up too, and again now
// if none were known.
func (d *Daemon) warmRemote() {
	d.mu.Lock()
	need := !d.remote.on && len(d.cfg.RemoteListen) == 0
	wild := false
	for _, a := range slices.Concat(d.cfg.RemoteListen, d.remote.addrs) {
		if host, _, err := net.SplitHostPort(a); err == nil {
			if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
				wild = true
			}
		}
	}
	d.mu.Unlock()
	if need || wild {
		if len(tailscaleAddrs()) == 0 && wild {
			refreshTailscale()
		}
	}
	if need {
		snap := takeNetSnapshot()
		d.mu.Lock()
		d.remote.snap = snap
		d.mu.Unlock()
	}
}

// refreshTailscale asks Tailscale for its addresses now (off the lock).
func refreshTailscale() {
	ips, err := askTailscale()
	n := pickTailscale(ips, err == nil, interfaceAddrs())
	tsCache.Lock()
	tsCache.net, tsCache.at = n, time.Now()
	tsCache.Unlock()
}

// stopRemote closes the listeners and disconnects remote clients. Callers hold d.mu.
func (d *Daemon) stopRemote() {
	for _, s := range d.remote.servers {
		_ = s.Close()
	}
	d.remote.servers, d.remote.addrs = nil, nil
	d.remote.on, d.remote.advertised = false, nil
	if d.remote.watchStop != nil {
		close(d.remote.watchStop)
		d.remote.watchStop = nil
	}
	d.bonjour.update(d.log, nil)
	go d.media.stopListening()
	for c := range d.conns {
		if c.remote {
			c.cancel()
		}
	}
}

// RemoteAddrs returns the addresses remote clients can use (for tests).
func (d *Daemon) RemoteAddrs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.remote.addrs...)
}

func (d *Daemon) handleRemoteWS(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	var local net.IP
	localAt := ""
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		localAt = la.String()
		if h, _, err := net.SplitHostPort(localAt); err == nil {
			local = net.ParseIP(h)
		}
	}
	if !d.remoteSourceOK(net.ParseIP(ip), local) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	release, ok := d.remote.gate.enter(ip)
	if !ok {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer release()
	// Compressed when the client offers it (wirezstd); plain otherwise.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{wirezstd.Subprotocol, wirezstd.Plain}})
	if err != nil {
		return
	}
	t := newWSTransport(ws, preAuthReadLimit)
	t.setReadLimit(preAuthReadLimit)
	nonce := make([]byte, client.NonceSize)
	_, _ = rand.Read(nonce)
	var cc *conn
	d.serveConnWith(t, func(c *conn) {
		cc = c
		c.remote, c.nonce, c.srcIP, c.localAt = true, nonce, ip, localAt
		d.mu.Lock()
		if d.remote.preAuth == nil {
			d.remote.preAuth = map[*conn]func(){}
		}
		d.remote.preAuth[c] = func() {
			t.setReadLimit(authedReadLimit)
			release()
		}
		d.mu.Unlock()
		time.AfterFunc(authTimeout, func() {
			d.mu.Lock()
			authed := c.authed
			d.mu.Unlock()
			if !authed {
				c.cancel()
			}
		})
		c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_AuthChallenge{AuthChallenge: &pb.AuthChallenge{Nonce: nonce}}})
	})
	d.mu.Lock()
	delete(d.remote.preAuth, cc)
	d.mu.Unlock()
}

// remoteAuthed lifts a remote connection's pre-authentication limits, once
// it has signed in. Called from its read loop; callers hold d.mu.
func (d *Daemon) remoteAuthed(c *conn) {
	if f := d.remote.preAuth[c]; f != nil {
		delete(d.remote.preAuth, c)
		f()
	}
}

// errPairingInShared refuses `hmk pair --print` in a pane people you share
// its Group with can see.
var errPairingInShared = errors.New("this pane is in a shared Group, where others can see the code: run hmk pair (it shows the code on the Mac), or --print in a terminal outside Hammock")

// mayShowPairingSecret refuses a pairing secret to a process in a pane
// that's an agent's, or in a shared Group: the code would sit in a
// terminal they can read. The app (outside the panes) is fine. Callers
// hold d.mu.
func (d *Daemon) mayShowPairingSecret(c *conn) error {
	cl := c.caller
	if cl == nil || cl.pane == "" {
		return nil
	}
	if cl.agent && !cl.scoped { // (one scoped as an agent asked you first: personCheck)
		return errors.New("agents can't make pairing codes")
	}
	if g := d.groups[d.groupOfPane(cl.pane)]; g != nil && slices.ContainsFunc(g.Members, func(m *pb.Member) bool { return guestRole(m.Role) }) {
		return errPairingInShared
	}
	return nil
}

// showPairing asks the Mac app to show its pairing code (`hmk pair`),
// replying with this Mac's name. Callers hold d.mu.
func (d *Daemon) showPairing() (*pb.ServerMessage, error) {
	shown := false
	for o := range d.conns {
		// The app: local, not a guest, not a process in one of the panes.
		if o.kind == "mac" && !o.remote && o.peer == "" && o.caller == nil && o.subscribed {
			o.send(&pb.ServerMessage{Msg: &pb.ServerMessage_ShowPairing{ShowPairing: &pb.ShowPairing{}}})
			shown = true
		}
	}
	if !shown {
		return nil, errors.New("Hammock isn't open on this Mac: open it and run hmk pair again, or use hmk pair --print")
	}
	d.audit("user", "pairing-shown", "", "")
	return &pb.ServerMessage{Msg: &pb.ServerMessage_ShowPairing{ShowPairing: &pb.ShowPairing{HostName: d.macName()}}}, nil
}

// macName is this Mac's name, as people know it.
func (d *Daemon) macName() string {
	if d.id.me.GetDeviceName() != "" {
		return d.id.me.DeviceName
	}
	return computerName()
}

// toLocal sends an event to this Mac's own clients (not remote devices or
// guests). Callers hold d.mu.
func (d *Daemon) toLocal(msg *pb.ServerMessage) {
	for o := range d.conns {
		if !o.remote && o.peer == "" && o.subscribed {
			o.send(msg)
		}
	}
}

// createPairing issues a single-use pairing secret. Callers hold d.mu.
func (d *Daemon) createPairing() (*pb.Pairing, error) {
	if !d.remote.on {
		// Make sure a certificate exists so its fingerprint can be pinned.
		cert, fp, err := loadOrCreateCert(d.cfg.Dir)
		if err != nil {
			return nil, err
		}
		d.remote.cert, d.remote.fingerprint = cert, fp
	}
	secret := make([]byte, 24)
	_, _ = rand.Read(secret)
	exp := time.Now().Add(pairingTTL)
	for k, t := range d.remote.pairings {
		if time.Now().After(t) {
			delete(d.remote.pairings, k)
		}
	}
	d.remote.pairings[hex.EncodeToString(secret)] = exp
	host := d.macName()
	addrs, lan := d.reachAt()
	return &pb.Pairing{
		HostName: host, Addresses: addrs, CertSha256: d.remote.fingerprint, Secret: secret,
		ExpiresUnixMs: exp.UnixMilli(), RemoteEnabled: d.remote.on, LanSubnets: lan,
	}, nil
}

// pair registers a device presenting a valid pairing secret. Callers hold d.mu.
func (d *Daemon) pair(c *conn, m *pb.Pair) (*pb.ServerMessage, error) {
	if len(m.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("invalid public key")
	}
	key := hex.EncodeToString(m.Secret)
	var match string
	for k, exp := range d.remote.pairings {
		if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 && time.Now().Before(exp) {
			match = k
		}
	}
	if match == "" {
		d.auditSignInFailed(c, "remote:"+deviceName(m.DeviceName), "pair-rejected", "", "invalid or expired pairing code")
		return nil, errors.New("invalid or expired pairing code")
	}
	delete(d.remote.pairings, match)
	dev := state.Device{
		ID: newID("dv"), Name: deviceName(m.DeviceName), Platform: m.Platform, PublicKey: m.PublicKey,
		Paired: time.Now(), LastSeen: time.Now(),
	}
	if err := d.store.PutDevice(dev); err != nil {
		return nil, err
	}
	c.authed, c.deviceID, c.name = true, dev.ID, dev.Name
	d.remoteAuthed(c)
	d.audit("device:"+dev.Name, "paired", dev.ID, m.Platform)
	d.toLocal(&pb.ServerMessage{Msg: &pb.ServerMessage_DevicePaired{DevicePaired: &pb.DevicePaired{DeviceId: dev.ID, Name: dev.Name, Platform: dev.Platform}}})
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Paired{Paired: &pb.Paired{DeviceId: dev.ID}}}, nil
}

// authenticate checks a device's signature over the connection's nonce
// (bound to this host's certificate). Callers hold d.mu.
// deviceName is a name a device gives itself, as this Mac shows it: one
// line, no control characters (bidi controls among them), at most 64
// characters.
func deviceName(s string) string {
	return strings.TrimSpace(cleanLine(strings.TrimSpace(s), 64))
}

func (d *Daemon) authenticate(c *conn, m *pb.Authenticate) error {
	devices, err := d.store.Devices()
	if err != nil {
		return err
	}
	for _, dev := range devices {
		if dev.ID == m.DeviceId && ed25519.Verify(dev.PublicKey, client.AuthMessage(client.DeviceAuthTag, d.remote.fingerprint, c.nonce), m.Signature) {
			if n := deviceName(m.DeviceName); n != "" && n != dev.Name {
				dev.Name = n // renamed on the device
			}
			c.authed, c.deviceID, c.name = true, dev.ID, dev.Name
			d.remoteAuthed(c)
			d.phoneUsed() // (it connects as you open it)
			dev.LastSeen = time.Now()
			d.persist(d.store.PutDevice(dev))
			return nil
		}
	}
	d.auditSignInFailed(c, "remote", "auth-failed", m.DeviceId, "")
	return errors.New("authentication failed")
}

func (d *Daemon) listDevices() (*pb.ServerMessage, error) {
	devices, err := d.store.Devices()
	if err != nil {
		return nil, err
	}
	ts := tailscaleNet()
	snap := cachedNetSnapshot()
	out := &pb.Devices{OtherTailscaleRange: ts.others, TailscaleUnknown: ts.unknown,
		Networks: snap.networks(), RemoteEnabled: d.remote.on}
	out.ExtraAddresses, _ = hammockconfig.Repeated("remote-address")
	for _, dev := range devices {
		out.Devices = append(out.Devices, &pb.Device{
			Id: dev.ID, Name: dev.Name, Platform: dev.Platform,
			PairedUnixMs: dev.Paired.UnixMilli(), LastSeenUnixMs: dev.LastSeen.UnixMilli(),
		})
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Devices{Devices: out}}, nil
}

// revokeDevice deletes a device and drops its connections. Callers hold d.mu.
func (d *Daemon) revokeDevice(id string) error {
	name := ""
	if devs, err := d.store.Devices(); err == nil {
		for _, dv := range devs {
			if dv.ID == id {
				name = dv.Name
			}
		}
	}
	if err := d.store.DeleteDevice(id); err != nil {
		return err
	}
	d.persist(d.store.DeleteRecord("push", id))
	for c := range d.conns {
		if c.deviceID == id {
			c.cancel()
		}
	}
	d.audit("user", "device-revoked", id, name)
	return nil
}

// setRemoteAccess turns remote access on or off and remembers it. Callers hold d.mu.
func (d *Daemon) setRemoteAccess(on bool) error {
	if !on && d.linkedCount() > 0 {
		return errors.New("your linked computers reach this one through remote access: unlink them first (Settings ▸ Devices ▸ Your Computers, or hmk computers unlink)")
	}
	if on {
		if err := d.startRemote(); err != nil {
			return err
		}
	} else {
		d.stopRemote()
	}
	return d.store.SetSetting("remote", strconv.FormatBool(on))
}
