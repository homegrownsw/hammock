package daemon

import (
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Remote connections that die silently. A Wi-Fi switch, sleep or a NAT
// forgetting a connection leaves TCP thinking it's up for minutes, so both
// ends of a remote connection ping the other (linkalive): this daemon its
// links to other computers (peerLink), and its remote listener the phones
// and other computers connected to it. One that answers nothing for two
// pings is dropped: a link dials again, and its Groups show offline
// meanwhile. When this computer's networks change or it wakes, every link
// is checked at once (an answer within linkCheckWithin) and one waiting to
// dial again dials now.
//
// WebSocket pings are answered by every client: the Mac's and the phones'
// (their own checks ping too), and other daemons, old ones included
// (coder/websocket answers while reading, which a daemon always is).

// Variables for tests: a daemon takes them as it starts (linkTimings).
var (
	linkPingEvery   = 15 * time.Second
	linkCheckWithin = 5 * time.Second
	linkNetPoll     = 5 * time.Second // how often networks are looked at, and a wake noticed
)

// linkTimings is a daemon's copy of its links' timings (so a test that
// shortens them for its daemons doesn't race the daemons of others).
type linkTimings struct {
	every, within, netPoll time.Duration
	redial, redialMax      time.Duration // peer.go
}

func currentLinkTimings() linkTimings {
	return linkTimings{linkPingEvery, linkCheckWithin, linkNetPoll, peerRedial, peerRedialMax}
}

// keepAlive pings a remote client until it's gone, dropping it if it falls
// silent.
func (c *conn) keepAlive(ws *websocket.Conn) {
	if c.live.Run(c.ctx, c.d.linkT.every, c.d.linkT.within, ws.Ping) != nil {
		c.d.log.Info("remote client stopped answering: dropped", "kind", c.kindName(), "from", c.srcIP)
		c.cancel()
	}
}

// kindName is c's kind, for the log.
func (c *conn) kindName() string {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	return c.kind
}

// kick makes sure of l's connection now (the network changed, or the
// computer woke), and if it's waiting to dial again, dials now.
func (l *peerLink) kick() {
	l.cmu.Lock()
	cl := l.cl
	l.cmu.Unlock()
	if cl != nil {
		cl.CheckAlive()
	}
	select {
	case l.redial <- struct{}{}:
	default:
	}
}

// kickLinks checks every remote connection now, and dials again the links
// that are down.
func (d *Daemon) kickLinks(why string) {
	d.mu.Lock()
	var links []*peerLink
	for _, l := range d.links {
		links = append(links, l)
	}
	n := 0
	for c := range d.conns {
		if c.live != nil {
			c.live.Check()
			n++
		}
	}
	d.mu.Unlock()
	if len(links)+n > 0 {
		d.log.Info("checking remote connections", "why", why, "links", len(links), "clients", n)
	}
	for _, l := range links {
		l.kick()
	}
}

// watchLinks kicks the links when this computer's networks change or it
// wakes from sleep.
func (d *Daemon) watchLinks() {
	t := time.NewTicker(d.linkT.netPoll)
	defer t.Stop()
	nets, prev := netFingerprint(), time.Now()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		}
		now := time.Now()
		woke := slept(now.Round(0).Sub(prev.Round(0)), now.Sub(prev))
		prev = now
		n := netFingerprint()
		changed := n != nets
		nets = n
		switch {
		case woke:
			d.kickLinks("woke")
		case changed:
			d.kickLinks("network changed")
		}
	}
}

// slept: more time passed on the wall clock (wall) than on the monotonic
// one (mono), which stops while the computer sleeps.
func slept(wall, mono time.Duration) bool { return wall-mono > 5*time.Second }

// netFingerprint is this computer's interfaces and their addresses.
func netFingerprint() string {
	var out []string
	for _, a := range interfaceAddrs() {
		out = append(out, a.name+" "+a.ip.String())
	}
	slices.Sort(out)
	return strings.Join(out, ";")
}
