package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestRemotePairingAndAuth(t *testing.T) {
	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	defer d.Shutdown()
	deadline := time.Now().Add(5 * time.Second)
	for len(d.RemoteAddrs()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	addr := d.RemoteAddrs()[0]
	local := dial(t, dir)
	call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	pairing := call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}).GetPairing()
	if pairing.CertSha256 == "" || len(pairing.Secret) == 0 || !pairing.RemoteEnabled {
		t.Fatalf("pairing %v", pairing)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A wrong pin is refused at the TLS layer.
	if _, err := client.DialRemote(ctx, addr, strings.Repeat("00", 32), "ios", "evil"); err == nil {
		t.Fatal("connected despite a wrong certificate pin")
	}

	remoteDial := func() (*client.Client, []byte) {
		c, err := client.DialRemote(ctx, addr, pairing.CertSha256, "ios", "phone")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		ev := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetAuthChallenge() != nil })
		return c, ev.GetAuthChallenge().Nonce
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	c1, _ := remoteDial()
	if _, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}); err == nil {
		t.Fatal("unauthenticated subscribe allowed")
	}
	if _, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: []byte("nope"), DeviceName: "phone", PublicKey: pub}}}); err == nil {
		t.Fatal("bad pairing secret accepted")
	}
	r, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: pairing.Secret, DeviceName: "phone", Platform: "ios", PublicKey: pub}}})
	if err != nil {
		t.Fatal(err)
	}
	deviceID := r.GetPaired().DeviceId
	// The Mac hears about it (and notifies).
	if p := waitEvent(t, local, func(ev *pb.ServerMessage) bool { return ev.GetDevicePaired() != nil }).GetDevicePaired(); p.Name != "phone" || p.DeviceId != deviceID {
		t.Fatalf("device paired event %v", p)
	}
	if _, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}); err != nil {
		t.Fatalf("subscribe after pairing: %v", err)
	}
	// A Group's picture: in the phone's state, and fetched by it.
	gid := client.CreatedID(call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "pics"}}}))
	call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIconImage{SetGroupIconImage: &pb.SetGroupIconImage{GroupId: gid, Image: testPNG(t, 64, 64, 0)}}})
	st, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	if err != nil {
		t.Fatal(err)
	}
	hash := ""
	for _, g := range st.GetState().Groups {
		if g.Id == gid {
			hash = g.IconImage
		}
	}
	if hash == "" {
		t.Fatalf("the phone's state has no picture: %v", st.GetState().Groups)
	}
	ic, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadGroupIcon{ReadGroupIcon: &pb.ReadGroupIcon{GroupId: gid}}})
	if err != nil || len(ic.GetGroupIcon().GetPng()) == 0 || ic.GetGroupIcon().Hash != hash {
		t.Fatalf("the phone fetching the picture: %v %v", err, ic)
	}

	if _, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}); err == nil {
		t.Fatal("remote client could create pairings")
	}
	if _, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ShowPairing{ShowPairing: &pb.ShowPairing{}}}); err == nil {
		t.Fatal("remote client could show pairing codes")
	}
	// Sharing's administration stays on the Mac; answering a join doesn't.
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: "gr_x"}}},
		{Msg: &pb.ClientMessage_RevokeInvite{RevokeInvite: &pb.RevokeInvite{GroupId: "gr_x"}}},
		{Msg: &pb.ClientMessage_SetMember{SetMember: &pb.SetMember{GroupId: "gr_x", UserId: "us_x"}}},
		{Msg: &pb.ClientMessage_StopSharing{StopSharing: &pb.StopSharing{}}},
		{Msg: &pb.ClientMessage_SetProfile{SetProfile: &pb.SetProfile{Name: "x"}}},
		{Msg: &pb.ClientMessage_JoinShared{JoinShared: &pb.JoinShared{Link: "hammock://join/x"}}},
		{Msg: &pb.ClientMessage_LeaveShared{LeaveShared: &pb.LeaveShared{GroupId: "gr_x"}}},
	} {
		if _, err := c1.Call(ctx, m); err == nil || !strings.Contains(err.Error(), "only available on this Mac") {
			t.Errorf("phone sent %T: %v", m.Msg, err)
		}
	}
	if _, err := c1.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondJoin{RespondJoin: &pb.RespondJoin{Id: "jr_x"}}}); err == nil || !strings.Contains(err.Error(), "no such join") {
		t.Errorf("phone answering a join: %v", err)
	}

	// The secret is single use.
	c2, nonce := remoteDial()
	if _, err := c2.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: pairing.Secret, DeviceName: "again", PublicKey: pub}}}); err == nil {
		t.Fatal("pairing secret reused")
	}
	// Signed challenge authenticates; a signature over the wrong nonce doesn't.
	if _, err := c2.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Authenticate{Authenticate: &pb.Authenticate{DeviceId: deviceID, Signature: ed25519.Sign(priv, []byte("other"))}}}); err == nil {
		t.Fatal("bad signature accepted")
	}
	// A bare signature over the nonce (not bound to this purpose and host) doesn't either.
	if _, err := c2.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Authenticate{Authenticate: &pb.Authenticate{DeviceId: deviceID, Signature: ed25519.Sign(priv, nonce)}}}); err == nil {
		t.Fatal("raw nonce signature accepted")
	}
	if _, err := c2.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Authenticate{Authenticate: &pb.Authenticate{DeviceId: deviceID, Signature: ed25519.Sign(priv, client.AuthMessage(client.PeerAuthTag, pairing.CertSha256, nonce))}}}); err == nil {
		t.Fatal("peer-tagged signature accepted as a device's")
	}
	if _, err := c2.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Authenticate{Authenticate: &pb.Authenticate{DeviceId: deviceID, Signature: ed25519.Sign(priv, client.AuthMessage(client.DeviceAuthTag, pairing.CertSha256, nonce))}}}); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if _, err := c2.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}); err != nil {
		t.Fatal(err)
	}

	// Revoking the device drops its connections and stops it authenticating.
	call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_RevokeDevice{RevokeDevice: &pb.RevokeDevice{DeviceId: deviceID}}})
	select {
	case <-c2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("revoked device still connected")
	}
	c3, nonce3 := remoteDial()
	if _, err := c3.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Authenticate{Authenticate: &pb.Authenticate{DeviceId: deviceID, Signature: ed25519.Sign(priv, client.AuthMessage(client.DeviceAuthTag, pairing.CertSha256, nonce3))}}}); err == nil {
		t.Fatal("revoked device authenticated")
	}
}

// Remote access listens on the addresses Tailscale says are its own, not
// on another VPN's in the same range; when Tailscale can't be asked, on
// every address in its ranges.
func TestTailscaleAddressSelection(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "tailscale")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1\" = ip ] && printf '100.86.15.86\\nfd7a:115c:a1e0::733:2260\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := tailscaleCommands
	t.Cleanup(func() { tailscaleCommands = old })
	tailscaleCommands = []string{filepath.Join(bin, "missing"), fake}
	said, err := askTailscale()
	if err != nil || len(said) != 2 || !said[0].Equal(net.ParseIP("100.86.15.86")) {
		t.Fatalf("asked: %v %v", said, err)
	}
	ifs := []ifAddr{
		{"en0", net.ParseIP("192.168.1.20"), nil},
		{"utun3", net.ParseIP("100.86.15.86"), nil},
		{"utun3", net.ParseIP("fd7a:115c:a1e0::733:2260"), nil},
		{"utun5", net.ParseIP("100.96.0.2"), nil}, // Cloudflare WARP
	}
	n := pickTailscale(said, true, ifs)
	if len(n.own) != 2 || !n.own[0].Equal(net.ParseIP("100.86.15.86")) || len(n.others) != 1 || n.others[0] != "utun5 100.96.0.2" || n.unknown {
		t.Fatalf("asked: %+v", n)
	}
	n = pickTailscale(nil, false, ifs)
	if len(n.own) != 3 || len(n.others) != 0 || !n.unknown {
		t.Fatalf("not asked: %+v", n)
	}
	if n := pickTailscale(nil, false, ifs[:3]); len(n.own) != 2 || n.unknown {
		t.Fatalf("not asked, one network: %+v", n)
	}
	tailscaleCommands = []string{filepath.Join(bin, "missing")}
	if _, err := askTailscale(); err == nil {
		t.Fatal("no CLI, yet an answer")
	}
}

func TestDeviceNameCleaned(t *testing.T) {
	for in, want := range map[string]string{
		"  Alex’s iPhone \n":    "Alex’s iPhone",
		"a\x00b\x1bc":           "abc",
		"":                      "",
		strings.Repeat("é", 70): strings.Repeat("é", 64),
	} {
		if got := deviceName(in); got != want {
			t.Errorf("deviceName(%q) = %q, want %q", in, got, want)
		}
	}
}
