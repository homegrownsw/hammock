package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var (
	lookPath  = exec.LookPath
	runOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).Output()
	}
)

// tailscaleCLI is the command running Tailscale's CLI at path with args.
// The Mac app's binary is its CLI too, but only when it thinks it was run
// from a terminal: run from the daemon (launchd's environment) it tries
// to be the app, says "The Tailscale GUI failed to start" and exits 0.
// TAILSCALE_BE_CLI makes it the CLI.
func tailscaleCLI(ctx context.Context, path string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, path, args...)
	c.Env = append(os.Environ(), "TAILSCALE_BE_CLI=1")
	return c
}

// hwInfo is what kind of computer this is, for its badge.
type hwInfo struct{ model, kind, os string }

// hardware is this computer's model ("Mac Studio") and kind, asked once
// (system_profiler takes a moment: knownHardware doesn't wait for it).
var hardware = sync.OnceValue(func() hwInfo {
	defer hwKnown.Store(true)
	if runtime.GOOS != "darwin" {
		kind := "server"
		if m, _ := filepath.Glob("/sys/class/power_supply/BAT*"); len(m) > 0 {
			kind = "laptop"
		}
		return hwInfo{model: "Linux", kind: kind, os: "linux"}
	}
	model := ""
	if out, err := runOutput(context.Background(), "/usr/sbin/system_profiler", "SPHardwareDataType", "-json"); err == nil {
		var v struct {
			Items []struct {
				Name string `json:"machine_name"`
			} `json:"SPHardwareDataType"`
		}
		if json.Unmarshal(out, &v) == nil && len(v.Items) > 0 {
			model = v.Items[0].Name
		}
	}
	return hwInfo{model: model, kind: kindOf(model), os: "macos"}
})

var hwKnown atomic.Bool

// knownHardware is hardware() once it's been asked, else what was known
// before (the record kept from last time).
func knownHardware(was *pb.Computer) hwInfo {
	if hwKnown.Load() {
		return hardware()
	}
	if runtime.GOOS != "darwin" {
		return hardware() // quick
	}
	return hwInfo{model: was.GetModel(), kind: cmpOr(was.GetKind(), "desktop"), os: "macos"}
}

// kindOf maps a Mac's model name to its badge.
func kindOf(model string) string {
	switch {
	case strings.Contains(model, "MacBook"):
		return "laptop"
	case strings.Contains(model, "Studio"):
		return "studio"
	case strings.Contains(model, "mini"):
		return "mini"
	case strings.Contains(model, "Mac Pro"):
		return "tower"
	}
	return "desktop"
}

// discoverComputers lists the computers on your tailnet owned by your
// Tailscale user, and Hammock found by Bonjour on the chosen local networks
// this computer is on, and whether Hammock answers on each. One found both
// ways (the same certificate) is listed once.
func (d *Daemon) discoverComputers(ctx context.Context) *pb.DiscoveredComputers {
	out := &pb.DiscoveredComputers{}
	d.mu.Lock()
	known := map[string]string{} // certificate → linked computer
	for uid, l := range d.comp.links {
		if d.isLinked(uid) {
			known[l.CertSha256] = uid
		}
	}
	d.mu.Unlock()
	snap := takeNetSnapshot()
	var bonjour []bonjourFound
	bdone := make(chan struct{})
	go func() {
		defer close(bdone)
		bctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		bonjour = browseBonjour(bctx, snap.listening())
	}()
	var wg sync.WaitGroup
	var mu sync.Mutex
	fps := map[*pb.DiscoveredComputer]string{}
	probe := func(e *pb.DiscoveredComputer) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			fp, ok := probeHammock(pctx, e.Addresses[0])
			mu.Lock()
			e.Answering, e.LinkedUserId = ok, known[fp]
			if ok {
				fps[e] = fp
			}
			mu.Unlock()
		}()
	}
	st, tsErr := askTailscaleStatus(ctx)
	if tsErr == nil {
		for _, p := range st.Peer {
			if p == nil || p.UserID != st.Self.UserID {
				continue
			}
			e := &pb.DiscoveredComputer{Name: cmpOr(p.HostName, strings.Split(p.DNSName, ".")[0]), Os: p.OS, Online: p.Online, Via: "tailscale"}
			ips := slices.Clone(p.TailscaleIPs)
			slices.SortStableFunc(ips, func(a, b string) int { // IPv4 first
				return boolInt(strings.Contains(a, ":")) - boolInt(strings.Contains(b, ":"))
			})
			for _, ip := range ips {
				e.Addresses = append(e.Addresses, net.JoinHostPort(ip, strconv.Itoa(DefaultRemotePort)))
			}
			out.List = append(out.List, e)
			if p.Online && len(e.Addresses) > 0 {
				probe(e)
			}
		}
	}
	<-bdone
	var local []*pb.DiscoveredComputer
	for _, f := range bonjour {
		e := &pb.DiscoveredComputer{Name: f.name, Online: true, Addresses: f.addrs, Via: "local"}
		local = append(local, e)
		probe(e)
	}
	wg.Wait()
	// Found by Bonjour and on the tailnet: one entry, the tailnet's (its
	// addresses work away from here too), with the local ones after.
	byFP := map[string]*pb.DiscoveredComputer{}
	for _, e := range out.List {
		if fp := fps[e]; fp != "" {
			byFP[fp] = e
		}
	}
	for _, e := range local {
		if t := byFP[fps[e]]; fps[e] != "" && t != nil {
			for _, a := range e.Addresses {
				if !slices.Contains(t.Addresses, a) {
					t.Addresses = append(t.Addresses, a)
				}
			}
			continue
		}
		if fps[e] != "" {
			byFP[fps[e]] = e
		}
		out.List = append(out.List, e)
	}
	if len(out.List) == 0 && tsErr != nil {
		out.Error = tsErr.Error()
	}
	slices.SortFunc(out.List, func(a, b *pb.DiscoveredComputer) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// tlsFingerprint connects to addr with TLS and returns its certificate's
// fingerprint (Hammock's remote listener answers TLS; nothing is sent).
func tlsFingerprint(ctx context.Context, addr string) (string, bool) {
	dl := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}} //nolint:gosec // only fingerprinted
	c, err := dl.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", false
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", false
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), true
}

// peerHelloFor is a PeerHello: this daemon's key, proven over the other's
// certificate and challenge, and a nonce of ours for it to sign.
func peerHelloFor(key ed25519.PrivateKey, me *pb.Person, certSHA256 string, nonce, hostNonce []byte) *pb.PeerHello {
	return &pb.PeerHello{
		PublicKey: key.Public().(ed25519.PublicKey), Signature: ed25519.Sign(key, client.AuthMessage(client.PeerAuthTag, certSHA256, nonce)),
		Name: me.Name, DeviceName: me.DeviceName, Avatar: me.Avatar, HostNonce: hostNonce,
	}
}

// verifyWelcome checks the other daemon proved its key (bound to its
// certificate and our nonce), returning its user id.
func verifyWelcome(w *pb.PeerWelcome, certSHA256 string, hostNonce []byte) (string, error) {
	if len(w.GetPublicKey()) != ed25519.PublicKeySize ||
		!ed25519.Verify(w.PublicKey, client.AuthMessage(client.PeerHostTag, certSHA256, hostNonce), w.Signature) {
		return "", errors.New("the other computer couldn't prove who it is")
	}
	return userIDFor(w.PublicKey), nil
}
