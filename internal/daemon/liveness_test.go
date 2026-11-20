package daemon

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// lossyProxy forwards TCP to target. dropAll makes the connections through
// it now go silent, both ways, without closing (a network that went away
// under them); refuse turns new ones away (the host out of reach).
type lossyProxy struct {
	ln     net.Listener
	target string

	mu     sync.Mutex
	refuse bool
	conns  []*lossyConn
}

type lossyConn struct {
	a, b net.Conn
	mu   sync.Mutex
	dead bool
}

func startLossyProxy(t *testing.T, target string) *lossyProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &lossyProxy{ln: ln, target: target}
	t.Cleanup(func() {
		ln.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, c := range p.conns {
			c.a.Close()
			c.b.Close()
		}
	})
	go func() {
		for {
			a, err := ln.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			refuse := p.refuse
			p.mu.Unlock()
			if refuse {
				a.Close()
				continue
			}
			b, err := net.Dial("tcp", target)
			if err != nil {
				a.Close()
				continue
			}
			c := &lossyConn{a: a, b: b}
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			go c.pipe(a, b)
			go c.pipe(b, a)
		}
	}()
	return p
}

func (c *lossyConn) pipe(from, to net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := from.Read(buf)
		if err != nil {
			to.Close()
			return
		}
		c.mu.Lock()
		dead := c.dead
		c.mu.Unlock()
		if dead {
			continue // read and dropped: nothing comes back up the other side
		}
		if _, err := to.Write(buf[:n]); err != nil {
			from.Close()
			return
		}
	}
}

func (p *lossyProxy) addr() string { return p.ln.Addr().String() }

func (p *lossyProxy) dropAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.mu.Lock()
		c.dead = true
		c.mu.Unlock()
	}
}

func (p *lossyProxy) setRefuse(on bool) {
	p.mu.Lock()
	p.refuse = on
	p.mu.Unlock()
}

// setLinkTimings shortens the liveness intervals for the daemons a test
// starts next (not parallel).
func setLinkTimings(t *testing.T, every, within, redial time.Duration) {
	oldEvery, oldWithin := linkPingEvery, linkCheckWithin
	oldRedial, oldMax := peerRedial, peerRedialMax
	linkPingEvery, linkCheckWithin = every, within
	peerRedial, peerRedialMax = redial, redial
	t.Cleanup(func() {
		linkPingEvery, linkCheckWithin = oldEvery, oldWithin
		peerRedial, peerRedialMax = oldRedial, oldMax
	})
}

// guestLinkVia makes guest a link to host through p, kept (a join waiting)
// and running.
func guestLinkVia(t *testing.T, guest, host *Daemon, p *lossyProxy) *peerLink {
	t.Helper()
	host.mu.Lock()
	tok := &pb.InviteToken{HostName: "host", Addresses: []string{p.addr()}, CertSha256: host.remote.fingerprint, HostUserId: host.id.me.UserId}
	host.mu.Unlock()
	guest.mu.Lock()
	l := newLink(guest, tok)
	l.pending = 1 // (so it isn't tidied away: in no Group there)
	guest.links[linkKey(tok)] = l
	guest.mu.Unlock()
	go l.run()
	t.Cleanup(l.close)
	return l
}

func waitOnline(t *testing.T, d *Daemon, l *peerLink, want bool, within time.Duration, what string) time.Duration {
	t.Helper()
	start := time.Now()
	for deadline := start.Add(within); ; time.Sleep(10 * time.Millisecond) {
		d.mu.Lock()
		on := l.online
		d.mu.Unlock()
		if on == want {
			return time.Since(start)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v", what, within)
		}
	}
}

// remoteConns is the host's remote clients now.
func remoteConns(d *Daemon) map[*conn]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[*conn]bool{}
	for c := range d.conns {
		if c.remote {
			out[c] = true
		}
	}
	return out
}

// A link whose connection goes silent (the network went away under it,
// TCP none the wiser) is noticed within a few pings on both ends: the
// guest shows the host offline and dials again, and the host drops the
// guest's connection.
func TestLinkNoticesSilentConnection(t *testing.T) {
	setLinkTimings(t, 200*time.Millisecond, 200*time.Millisecond, 100*time.Millisecond)
	host, _ := startHost(t)
	guest := startDaemon(t, shortDir(t))
	p := startLossyProxy(t, host.RemoteAddrs()[0])
	l := guestLinkVia(t, guest, host, p)
	waitOnline(t, guest, l, true, 10*time.Second, "the link didn't come up")
	before := remoteConns(host)
	if len(before) == 0 {
		t.Fatal("the host has no remote client")
	}

	p.dropAll()
	// Two pings' silence, noticed at the next: 3 intervals (600ms) at most.
	took := waitOnline(t, guest, l, false, 3*time.Second, "the guest didn't notice the silent host")
	t.Logf("guest noticed in %v", took)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		now := remoteConns(host)
		kept := false
		for c := range before {
			kept = kept || now[c]
		}
		if !kept {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host didn't drop the silent guest")
		}
	}
	waitOnline(t, guest, l, true, 5*time.Second, "the link didn't come back")
}

// When the network changes (or the computer wakes), a live link is checked
// at once, without waiting for its next ping, and a link waiting to dial
// again dials now, not after its backoff.
func TestKickChecksAndRedials(t *testing.T) {
	// Pings and redials an hour apart: only the kick can do it in time.
	setLinkTimings(t, time.Hour, 300*time.Millisecond, time.Hour)
	host, _ := startHost(t)
	guest := startDaemon(t, shortDir(t))
	p := startLossyProxy(t, host.RemoteAddrs()[0])
	p.setRefuse(true)
	l := guestLinkVia(t, guest, host, p)
	time.Sleep(500 * time.Millisecond) // (its first dial refused: now waiting an hour)
	p.setRefuse(false)
	guest.kickLinks("test")
	waitOnline(t, guest, l, true, 5*time.Second, "a kick didn't dial again")
	cl, err := l.connected(context.Background(), 5*time.Second) // (online is shown a moment before)
	if err != nil {
		t.Fatal(err)
	}

	p.dropAll()
	start := time.Now()
	guest.kickLinks("test")
	select {
	case <-cl.Done():
		t.Logf("noticed in %v", time.Since(start))
	case <-time.After(3 * time.Second):
		t.Fatal("a kick didn't notice the silent host")
	}
	// Dialled again at once (it may be back already: offline only a moment).
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if again, err := l.connected(context.Background(), 0); err == nil && again != cl {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the link didn't dial again at once after its check failed")
		}
	}
}

func TestSlept(t *testing.T) {
	if slept(5*time.Second, 5*time.Second) || slept(5100*time.Millisecond, 5*time.Second) {
		t.Fatal("awake taken for asleep")
	}
	// The wall clock ran on a minute that the monotonic one didn't.
	if !slept(65*time.Second, 5*time.Second) {
		t.Fatal("a minute asleep not noticed")
	}
}
