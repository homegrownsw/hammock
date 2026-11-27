package daemon

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// startOutOnly is a computer with remote access off: it listens nowhere.
func startOutOnly(t *testing.T) *testComputer {
	t.Helper()
	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "run", "hmkd.sock")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
	}
	d.mu.Lock()
	uid := d.id.me.UserId
	d.mu.Unlock()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	return &testComputer{d: d, dir: dir, c: c, uid: uid}
}

func isOutOnly(uid string, online bool) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == uid {
				return c.OutOnly && c.Online == online
			}
		}
		return false
	}
}

// waitNoLink waits until tc has no link of its own to uid.
func waitNoLink(t *testing.T, tc *testComputer, uid string) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		tc.d.mu.Lock()
		l := tc.d.linkOf(uid)
		tc.d.mu.Unlock()
		if l == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("still dials it")
		}
	}
}

// A computer with remote access off links out only: it sees the other's
// Groups, and the other never dials it, sees none of its Groups, can't use
// its SSH keys nor lend it its own, and doesn't introduce it to anyone.
func TestOutOnlyLink(t *testing.T) {
	a, b := startOutOnly(t), startComputer(t)
	aGroup := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On A"}}}))
	bGroup := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On B"}}}))

	linkWith(t, a, b, true)
	if len(a.d.RemoteAddrs()) > 0 || !func() bool { a.d.mu.Lock(); defer a.d.mu.Unlock(); return a.d.outOnly() }() {
		t.Fatal("linking turned remote access on")
	}
	waitComputers(t, a, "b online on a", hasComputer(b.uid, true))
	waitGroup(t, a, "b's group on a", func(g *pb.Group) bool { return g.Id == bGroup && g.MyRole == pb.Role_ROLE_OWNER })
	waitComputers(t, b, "a online, out only, on b", isOutOnly(a.uid, true))
	waitNoLink(t, b, a.uid)

	// Never a's Groups on b.
	time.Sleep(300 * time.Millisecond)
	b.d.mu.Lock()
	for _, g := range b.d.fullState().Groups {
		if g.Id == aGroup {
			b.d.mu.Unlock()
			t.Fatal("a's Group shows on b")
		}
	}
	b.d.mu.Unlock()

	// SSH keys, neither way: a doesn't ask, and b refuses it if it does.
	if _, err := a.d.sshCall(b.uid, &pb.SshAgentCall{List: true}, 2*time.Second); err == nil {
		t.Fatal("a asked b for its SSH keys")
	}
	a.d.mu.Lock()
	l := a.d.linkOf(b.uid)
	a.d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cl, err := l.connected(ctx, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SshAgentCall{SshAgentCall: &pb.SshAgentCall{List: true}}}); err == nil ||
		!strings.Contains(err.Error(), "out only") {
		t.Fatalf("b lent its SSH keys over a link out only: %v", err)
	}

	// A third computer linked with b isn't introduced to a.
	c := startComputer(t)
	link(t, c, b)
	waitComputers(t, c, "c knows b", hasComputer(b.uid, true))
	b.d.mu.Lock()
	b.d.introduceAll()
	b.d.mu.Unlock()
	time.Sleep(500 * time.Millisecond)
	c.d.mu.Lock()
	_, introduced := c.d.comp.intros[a.uid]
	_, linked := c.d.comp.links[a.uid]
	c.d.mu.Unlock()
	if introduced || linked {
		t.Fatal("b introduced a, which links out only, to c")
	}

	// Notifications: a's own, whichever computer was used last (b never
	// sees a's Groups, so never notifies of them).
	a.d.mu.Lock()
	rb := a.d.comp.links[b.uid].Record
	rb.Notifies, rb.LastInputUnixMs = true, time.Now().UnixMilli()
	active, elsewhere := a.d.computersState(false).ActiveId, a.d.inUseElsewhere()
	a.d.mu.Unlock()
	if active == b.uid || elsewhere {
		t.Fatal("a leaves its notifications to b")
	}

	// Off: offline on b.
	a.c.Close()
	a.d.Shutdown()
	waitComputers(t, b, "a offline on b", isOutOnly(a.uid, false))
}

// Turning remote access off while linked makes the links out only from
// this computer, and on again, both ways.
func TestRemoteOffLinksOutOnly(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	bGroup := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On B"}}}))
	link(t, a, b)
	waitComputers(t, b, "a online on b", hasComputer(a.uid, true))

	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{Enabled: false}}})
	waitComputers(t, b, "a out only on b", isOutOnly(a.uid, true))
	waitNoLink(t, b, a.uid)
	waitComputers(t, a, "b still online on a", hasComputer(b.uid, true))
	waitGroup(t, a, "b's group still on a", func(g *pb.Group) bool { return g.Id == bGroup })

	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{Enabled: true}}})
	waitComputers(t, b, "a both ways on b", func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == a.uid {
				return !c.OutOnly && c.Online && len(c.Addresses) > 0
			}
		}
		return false
	})
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		b.d.mu.Lock()
		l := b.d.linkOf(a.uid)
		up := l != nil && l.online
		b.d.mu.Unlock()
		if up {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("b doesn't dial a again")
		}
	}
}

// With remote access off, nothing is pushed to phones.
func TestNoPushWhileRemoteOff(t *testing.T) {
	d, err := New(Config{Dir: shortDir(t), HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Shutdown()
	var asked []string
	old := pushed
	pushed = func(event, _ string) { asked = append(asked, event) }
	defer func() { pushed = old }()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.push("finished", "at_1", pushNote{}, nil)
	if len(asked) > 0 {
		t.Fatal("pushed with remote access off")
	}
	_ = d.store.SetSetting("remote", "true")
	d.push("finished", "at_1", pushNote{}, nil)
	if len(asked) != 1 {
		t.Fatal("not pushed with remote access on")
	}
}

// Both out only, the link goes quiet; the first to take connections again
// is dialled by the other, where it was reached, and they converge.
func TestBothOutOnlyRecover(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, b, "a online on b", hasComputer(a.uid, true))
	off := &pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{}}}
	call(t, a.c, off)
	waitComputers(t, b, "a out only on b", isOutOnly(a.uid, true))
	call(t, b.c, off)
	// Neither listens, so neither can tell the other: b just goes quiet on a.
	waitComputers(t, a, "b offline on a", func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == b.uid {
				return !c.Online
			}
		}
		return false
	})

	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{Enabled: true}}})
	waitComputers(t, a, "b back, out only, on a", isOutOnly(b.uid, true))
	waitComputers(t, b, "a both ways on b", func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == a.uid {
				return !c.OutOnly && c.Online
			}
		}
		return false
	})
	waitNoLink(t, a, b.uid)
}

// An out-only computer that unlinks another tells it, over its own link:
// the other never dials it to hear so.
func TestOutOnlyUnlinkTells(t *testing.T) {
	a, b := startOutOnly(t), startComputer(t)
	linkWith(t, a, b, true)
	waitComputers(t, b, "a online on b", isOutOnly(a.uid, true))
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{UserId: b.uid}}})
	waitComputers(t, b, "a refused on b", func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == a.uid {
				return c.Refused
			}
		}
		return false
	})
}
