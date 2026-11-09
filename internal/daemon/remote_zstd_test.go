package daemon

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/wirezstd"
)

// Remote links are compressed when the client offers it, and plain for
// clients from before (an older phone or guest).
func TestRemoteCompression(t *testing.T) {
	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	defer d.Shutdown()
	for deadline := time.Now().Add(5 * time.Second); len(d.RemoteAddrs()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
	}
	addr := d.RemoteAddrs()[0]
	local := dial(t, dir)
	pin := call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}).GetPairing().CertSha256
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c, err := client.DialRemote(ctx, addr, pin, "ios", "phone")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !c.Compressed() {
		t.Fatal("not compressed")
	}
	if waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetAuthChallenge() != nil }) == nil {
		t.Fatal("no challenge")
	}

	// A client that only knows the plain protocol.
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	ws, _, err := websocket.Dial(ctx, "wss://"+addr+"/v1", &websocket.DialOptions{HTTPClient: hc, Subprotocols: []string{wirezstd.Plain}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if ws.Subprotocol() != wirezstd.Plain {
		t.Fatalf("subprotocol %q", ws.Subprotocol())
	}
	b, _ := proto.Marshal(&pb.ClientMessage{Id: 1, Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{ProtocolVersion: 1, ClientKind: "ios", ClientName: "old"}}})
	if err := ws.Write(ctx, websocket.MessageBinary, b); err != nil {
		t.Fatal(err)
	}
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m pb.ServerMessage
		if err := proto.Unmarshal(b, &m); err != nil {
			t.Fatalf("plain client got something it can't read: %v", err)
		}
		if m.GetHelloReply() != nil {
			return
		}
	}
}
