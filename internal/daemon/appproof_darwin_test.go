package daemon

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// frameClient speaks the raw local transport, as the Mac app does.
type frameClient struct {
	t  *testing.T
	c  net.Conn
	r  *bufio.Reader
	id uint64
}

func dialFrames(t *testing.T, dir string) *frameClient {
	t.Helper()
	c, err := net.Dial("unix", SocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Write([]byte(frameMagic)); err != nil {
		t.Fatal(err)
	}
	return &frameClient{t: t, c: c, r: bufio.NewReader(c)}
}

// call sends m and returns its reply (an Error as an error).
func (f *frameClient) call(m *pb.ClientMessage) (*pb.ServerMessage, error) {
	f.t.Helper()
	f.id++
	m.Id = f.id
	b, _ := proto.Marshal(m)
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(b)))
	if _, err := f.c.Write(append(frame, b...)); err != nil {
		return nil, err
	}
	_ = f.c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(f.r, hdr[:]); err != nil {
			return nil, err
		}
		body := make([]byte, binary.BigEndian.Uint32(hdr[:]))
		if _, err := io.ReadFull(f.r, body); err != nil {
			return nil, err
		}
		r := &pb.ServerMessage{}
		if err := proto.Unmarshal(body, r); err != nil {
			return nil, err
		}
		if r.ReplyTo != m.Id {
			continue
		}
		if e := r.GetError(); e != nil {
			return r, fmt.Errorf("%s", e.Message)
		}
		return r, nil
	}
}

// challenge asks to prove the connection is the app's.
func (f *frameClient) challenge() *pb.AppChallenge {
	f.t.Helper()
	r, err := f.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ProveApp{ProveApp: &pb.ProveApp{}}})
	if err != nil || r.GetAppChallenge() == nil {
		f.t.Fatalf("ProveApp: %v %v", r, err)
	}
	return r.GetAppChallenge()
}

// person reports whether the connection may do what only you may (answer
// a request: a made-up one gets past the check, and isn't found).
func (f *frameClient) person() bool {
	_, err := f.call(&pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{
		Id: "gr_made_up", Decision: pb.RespondGrant_DECISION_DENY,
	}}})
	switch {
	case err == nil:
		f.t.Fatal("answered a made-up request")
	case err.Error() == "no such grant request":
		return true
	case err.Error() == errAnswerInApp.Error():
		return false
	}
	f.t.Fatalf("answering: %v", err)
	return false
}

// hello says it's the app, and reports whether the daemon took it for
// the app (kind "mac": notifications and pairing codes go there).
func (f *frameClient) hello(d *Daemon) bool {
	f.t.Helper()
	if _, err := f.call(&pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{ProtocolVersion: 1, ClientKind: "mac"}}}); err != nil {
		f.t.Fatal(err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for c := range d.conns {
		if c.read > 0 && c.name == "" && c.kind == "mac" && c.pid == os.Getpid() {
			return true
		}
	}
	return false
}

// asApp makes the proof's signature check take this test's own process
// for the app (and nothing else), and leaves the rest of the checks real.
func asApp(t *testing.T) {
	personHook, selfIsPerson = nil, false
	appSignedHook = func(tok codesign.Token) bool { return tok.PID() == os.Getpid() }
	old := appProofFor
	appProofFor = time.Second
	t.Cleanup(func() { personHook, selfIsPerson, appSignedHook, appProofFor = testPerson, true, nil, old })
}

// The app is the app only by its proof: a Mach message, from the process
// at the other end of the connection, with that connection's nonce.
func TestAppProof(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	asApp(t)

	// Without a proof, nothing: not even by the socket's peer.
	plain := dialFrames(t, dir)
	if plain.hello(d) || plain.person() {
		t.Fatal("a connection without a proof is the app")
	}

	// With one: the app.
	app := dialFrames(t, dir)
	ch := app.challenge()
	if len(ch.Nonce) != 32 || ch.Service == "" {
		t.Fatalf("challenge %v", ch)
	}
	if got, err := sendAppProof(ch.Service, ch.Conn, ch.Nonce, 2*time.Second); err != nil || got != "ok" {
		t.Fatalf("proof: %q %v", got, err)
	}
	if !app.hello(d) || !app.person() {
		t.Fatal("the app, proven, isn't the app")
	}
	// Once: the same proof again is for nothing.
	if got, _ := sendAppProof(ch.Service, ch.Conn, ch.Nonce, 2*time.Second); got == "ok" {
		t.Fatal("a nonce used twice")
	}
	// Only first: a proof asked for later (by the app or anything) is refused.
	if _, err := app.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ProveApp{ProveApp: &pb.ProveApp{}}}); err == nil || err.Error() != errProofLate.Error() {
		t.Fatalf("a second ProveApp: %v", err)
	}
	if _, err := plain.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ProveApp{ProveApp: &pb.ProveApp{}}}); err == nil || err.Error() != errProofLate.Error() {
		t.Fatalf("a late ProveApp: %v", err)
	}

	// Another connection's nonce: ignored, and that connection's own
	// still works.
	a, b := dialFrames(t, dir), dialFrames(t, dir)
	chA, chB := a.challenge(), b.challenge()
	if got, _ := sendAppProof(chB.Service, chB.Conn, chA.Nonce, 2*time.Second); got == "ok" {
		t.Fatal("proved with another connection's nonce")
	}
	if got, _ := sendAppProof(chB.Service, chB.Conn, chB.Nonce, 2*time.Second); got != "ok" {
		t.Fatalf("its own nonce, after another's: %q", got)
	}
	if !b.person() {
		t.Fatal("proven, but not the app")
	}
	// a never proves: after a while it goes on, not the app.
	if a.person() {
		t.Fatal("a connection that never proved is the app")
	}
	// Too late: its nonce has gone with the wait.
	if got, _ := sendAppProof(chA.Service, chA.Conn, chA.Nonce, 2*time.Second); got == "ok" {
		t.Fatal("proved after the wait")
	}

	// Not signed as the app: refused, and not the app.
	appSignedHook = func(codesign.Token) bool { return false }
	u := dialFrames(t, dir)
	chU := u.challenge()
	if got, _ := sendAppProof(chU.Service, chU.Conn, chU.Nonce, 2*time.Second); got != errProofUnsigned.Error() {
		t.Fatalf("an unsigned sender: %q", got)
	}
	if u.person() {
		t.Fatal("an unsigned sender's connection is the app")
	}
}

// A proof from another process than the one at the other end of the
// connection (the app, handed a relay's nonce; or a child of the process
// that connected) is refused, even from the app.
func TestAppProofOtherProcess(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	asApp(t)
	appSignedHook = func(codesign.Token) bool { return true } // anyone is "the app"
	f := dialFrames(t, dir)
	ch := f.challenge()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "HMK_TEST_APP_PROOF="+ch.Service+","+strconv.FormatUint(ch.Conn, 10)+","+hex.EncodeToString(ch.Nonce))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the other process: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != errProofPeer.Error() {
		t.Fatalf("a proof from another process: %q", got)
	}
	if f.person() {
		t.Fatal("proven by another process")
	}
}
