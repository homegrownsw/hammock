package holder

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/rendezvous"
)

// A nonce is taken once, only while fresh, and a wrong one doesn't spend it.
func TestProofNonces(t *testing.T) {
	var p proofs
	now := time.Now()
	w := &proofWait{}
	if err := p.issue(w, now); err != nil {
		t.Fatal(err)
	}
	if len(w.nonce) != proofNonceLen || bytes.Equal(w.nonce, make([]byte, proofNonceLen)) {
		t.Fatalf("nonce %x", w.nonce)
	}
	w2 := &proofWait{}
	p.issue(w2, now)
	if w2.id == w.id || bytes.Equal(w2.nonce, w.nonce) {
		t.Fatal("two connections share a number or a nonce")
	}
	wrong := append([]byte(nil), w.nonce...)
	wrong[0] ^= 1
	if got, _ := p.take(w.id, wrong, now); got != nil {
		t.Fatal("taken with a wrong nonce")
	}
	if got, _ := p.take(w.id, w.nonce[:16], now); got != nil {
		t.Fatal("taken with a short nonce")
	}
	if got, _ := p.take(w2.id, w.nonce, now); got != nil {
		t.Fatal("taken with another connection's nonce")
	}
	if got, fresh := p.take(w.id, w.nonce, now); got != w || !fresh {
		t.Fatal("the right nonce, after wrong ones, not taken")
	}
	if got, _ := p.take(w.id, w.nonce, now); got != nil {
		t.Fatal("taken twice")
	}
	if got, fresh := p.take(w2.id, w2.nonce, now.Add(ProofFor()+time.Millisecond)); got != w2 || fresh {
		t.Fatal("an expired nonce taken as fresh")
	}
	w3 := &proofWait{}
	p.issue(w3, now)
	if !p.drop(w3) || p.drop(w3) {
		t.Fatal("drop")
	}
	if got, _ := p.take(w3.id, w3.nonce, now); got != nil {
		t.Fatal("taken once dropped")
	}
}

// proofHolder starts a holder of `cat` that checks its client (Peer: this
// test binary, as a holder takes its daemon), and the socket.
func proofHolder(t *testing.T) (*Server, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "p.sock")
	s, err := Start(Config{Socket: sock, Command: []string{"/bin/cat"}, Token: []byte("s3cret"), Peer: DaemonPeer(exe, "")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s, sock
}

// challenged connects to sock and asks to prove itself: the connection,
// and its challenge.
func challenged(t *testing.T, sock string) (net.Conn, uint64, []byte, string) {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := WriteFrame(c, FrameProve, nil); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFrame(c)
	if err != nil || f.Type != FrameChallenge {
		t.Fatalf("no challenge: %v %v", f.Type, err)
	}
	id, nonce, svc, err := parseChallenge(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	return c, id, nonce, svc
}

// closed reports whether the holder closed c (within wait), having sent it
// nothing more.
func closed(c net.Conn, wait time.Duration) bool {
	c.SetReadDeadline(time.Now().Add(wait))
	_, err := ReadFrame(c)
	return err != nil && !errors.Is(err, os.ErrDeadlineExceeded)
}

func clientCount(s *Server) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// Until its proof is in, a connection is served nothing: an attach, input,
// a resize, a signal or a close sent ahead of it are never read, and the
// connection is closed once its time is up. An attach with no proof at all
// is refused, whatever it carries, and a legacy client is not served by the
// socket's checks instead.
func TestProofFirst(t *testing.T) {
	defer setProofFor(setProofFor(300 * time.Millisecond))
	s, sock := proofHolder(t)

	c, _, _, _ := challenged(t, sock)
	for _, f := range []struct {
		t FrameType
		p []byte
	}{{FrameAttach, append(u64(0), "s3cret"...)}, {FrameInput, []byte("x\n")}, {FrameResize, resizePayload(10, 10)}, {FrameSignal, i32(9)}, {FrameClose, nil}} {
		if err := WriteFrame(c, f.t, f.p); err != nil {
			t.Fatal(err)
		}
	}
	if !closed(c, 2*time.Second) {
		t.Fatal("a connection that never proved itself wasn't closed (or was answered)")
	}
	select {
	case <-s.Done():
		t.Fatal("a close sent before the proof shut the holder")
	default:
	}
	if n := clientCount(s); n != 0 {
		t.Fatalf("%d clients attached", n)
	}

	raw, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	WriteFrame(raw, FrameAttach, append(u64(0), "s3cret"...))
	if !closed(raw, 2*time.Second) {
		t.Fatal("an attach without a proof answered")
	}

	// The daemon itself, proving itself, still attaches.
	hc, err := Dial(sock, 0, []byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	hc.Close()
}

// A proof is taken once, while fresh, and only with its connection's
// nonce: a wrong one is ignored and doesn't spend the right one; a replayed
// one finds nothing; one that comes too late finds its connection gone.
func TestProofOnceAndFresh(t *testing.T) {
	defer setProofFor(setProofFor(2 * time.Second))
	_, sock := proofHolder(t)

	c, id, nonce, svc := challenged(t, sock)
	wrong := append([]byte(nil), nonce...)
	wrong[31] ^= 0x80
	if a, err := sendProof(svc, id, wrong, time.Second); err != nil || a == "ok" {
		t.Fatalf("a wrong nonce: %q %v", a, err)
	}
	if a, err := sendProof(svc, id+1, nonce, time.Second); err != nil || a == "ok" {
		t.Fatalf("the nonce for another connection's number: %q %v", a, err)
	}
	if a, err := sendProof(svc, id, nonce, time.Second); err != nil || a != "ok" {
		t.Fatalf("the right nonce after wrong ones: %q %v", a, err)
	}
	if a, _ := sendProof(svc, id, nonce, time.Second); a == "ok" {
		t.Fatal("a nonce taken twice")
	}
	hc, err := attach(c, 0, []byte("s3cret"), false) // (proven above)
	if err != nil {
		t.Fatalf("attach once proven: %v", err)
	}
	hc.Close()

	// A replay on another connection: its own nonce is needed.
	c2, id2, _, _ := challenged(t, sock)
	if a, _ := sendProof(svc, id2, nonce, time.Second); a == "ok" {
		t.Fatal("another connection's spent nonce taken")
	}
	c2.Close()

	setProofFor(200 * time.Millisecond)
	c3, id3, nonce3, _ := challenged(t, sock)
	time.Sleep(400 * time.Millisecond)
	if a, _ := sendProof(svc, id3, nonce3, time.Second); a == "ok" {
		t.Fatal("an expired nonce taken")
	}
	if !closed(c3, time.Second) {
		t.Fatal("a connection whose proof came too late not closed")
	}
}

// TestHelperProves isn't a test: run with HMK_TEST_PROVE=<svc> <id> <nonce
// hex>, it sends that proof and prints the holder's answer; with
// HMK_TEST_PROVE_DIAL=<socket>, it connects itself, sends its own proof
// (keeping a copy of the connection open across exec) without waiting for
// the answer, says "sent", and execs /bin/sleep.
func TestHelperProves(t *testing.T) {
	if v := os.Getenv("HMK_TEST_PROVE"); v != "" {
		f := strings.Fields(v)
		id, _ := strconv.ParseUint(f[1], 10, 64)
		nonce, _ := hex.DecodeString(f[2])
		a, err := sendProof(f[0], id, nonce, 2*time.Second)
		if err != nil {
			a = "error: " + err.Error()
		}
		os.Stdout.WriteString(a + "\n")
		os.Exit(0)
	}
	sock := os.Getenv("HMK_TEST_PROVE_DIAL")
	if sock == "" {
		t.Skip("a helper")
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		os.Exit(2)
	}
	WriteFrame(c, FrameProve, nil)
	f, err := ReadFrame(c)
	if err != nil {
		os.Exit(2)
	}
	id, nonce, svc, err := parseChallenge(f.Payload)
	if err != nil {
		os.Exit(2)
	}
	rc, _ := c.(*net.UnixConn).SyscallConn()
	rc.Control(func(fd uintptr) { syscall.Dup(int(fd)) }) // (no close-on-exec)
	body := binary.BigEndian.AppendUint64(nil, id)
	rendezvous.Ask(svc, proofHello, append(body, nonce...), 50*time.Millisecond) // (sent; not waiting)
	os.Stdout.WriteString("sent\n")
	syscall.Exec("/bin/sleep", []string{"sleep", "5"}, nil)
	os.Exit(3)
}

// A proof is the connection's own process's: one sent by another (a child
// handed the nonce) is refused, and so is one whose sender has exec'd
// another program by the time the holder looks at it.
func TestProofIsTheConnectionsProcess(t *testing.T) {
	exe, _ := os.Executable()
	s, sock := proofHolder(t)
	c, id, nonce, svc := challenged(t, sock)
	cmd := exec.Command(exe, "-test.run=^TestHelperProves$")
	cmd.Env = append(os.Environ(), "HMK_TEST_PROVE="+svc+" "+strconv.FormatUint(id, 10)+" "+hex.EncodeToString(nonce))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if a := strings.TrimSpace(string(out)); a != errProofPeer.Error() {
		t.Fatalf("another process's proof: %q", a)
	}
	if !closed(c, 2*time.Second) {
		t.Fatal("the connection wasn't closed")
	}
	// (The nonce is spent: the connection's own process can't use it now.)
	if a, _ := sendProof(svc, id, nonce, time.Second); a == "ok" {
		t.Fatal("taken after another process tried it")
	}

	// Sent, then exec'd before the holder looked (as a holder stopped while
	// it happens would).
	verdict := make(chan error, 1)
	execd := make(chan struct{})
	heard, said := func() { <-execd }, func(err error) { verdict <- err }
	proofHeardHook.Store(&heard)
	proofVerdictHook.Store(&said)
	defer func() { proofHeardHook.Store(nil); proofVerdictHook.Store(nil) }()
	cmd = exec.Command(exe, "-test.run=^TestHelperProves$")
	cmd.Env = append(os.Environ(), "HMK_TEST_PROVE_DIAL="+sock)
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	buf := make([]byte, 5)
	if _, err := stdout.Read(buf); err != nil || string(buf) != "sent\n" {
		t.Fatalf("the helper: %q %v", buf, err)
	}
	for i := 0; ; i++ { // until it's /bin/sleep
		if b, _ := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(cmd.Process.Pid)).Output(); strings.Contains(string(b), "sleep") {
			break
		}
		if i > 100 {
			t.Fatal("the helper didn't exec")
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(execd)
	select {
	case err := <-verdict:
		if !errors.Is(err, errProofGone) {
			t.Fatalf("a proof whose sender exec'd since: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no verdict")
	}
	if n := clientCount(s); n != 0 {
		t.Fatalf("%d clients attached", n)
	}
}

// A new daemon attaches to a holder from before the proof (which closes a
// connection whose first frame isn't an attach) as it always did; and a
// holder from before it (legacy) never sees a proof.
func TestOldHolder(t *testing.T) {
	exe, _ := os.Executable()
	sock := filepath.Join(t.TempDir(), "p.sock")
	s, err := Start(Config{Socket: sock, Command: []string{"/bin/cat"}, Token: []byte("s3cret"), Peer: DaemonPeer(exe, ""), legacy: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	hc, err := Dial(sock, 0, []byte("s3cret"))
	if err != nil {
		t.Fatalf("an old holder: %v", err)
	}
	r := newReader(hc)
	hc.Input([]byte("old-holder\n"))
	r.until(t, "old-holder")
	hc.Close()
	if s.proofSvc.name != "" {
		t.Error("an old holder checked in a proof service")
	}
	// Its checks still refuse another program.
	sock2 := filepath.Join(t.TempDir(), "q")
	s2, err := Start(Config{Socket: sock2, Command: []string{"/bin/cat"}, Token: []byte("s3cret"), Peer: DaemonPeer("/usr/bin/true", ""), legacy: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Shutdown()
	if hc, err := Dial(sock2, 0, []byte("s3cret")); err == nil {
		hc.Close()
		t.Fatal("an old holder took another program")
	}
}

// BenchmarkAttach is what a connection costs the daemon (once per
// connection, not per message): attaching with the proof, and to a holder
// from before it (the socket's checks).
func BenchmarkAttach(b *testing.B) {
	exe, _ := os.Executable()
	for _, legacy := range []bool{false, true} {
		name := map[bool]string{false: "proof", true: "socket-checks"}[legacy]
		b.Run(name, func(b *testing.B) {
			sock := filepath.Join(b.TempDir(), "p.sock")
			s, err := Start(Config{Socket: sock, Command: []string{"/bin/cat"}, Token: []byte("s3cret"), Peer: DaemonPeer(exe, ""), legacy: legacy})
			if err != nil {
				b.Fatal(err)
			}
			defer s.Shutdown()
			for i := 0; i < b.N; i++ {
				var c *Client
				if legacy {
					c, err = dial(sock, 0, []byte("s3cret"), false)
				} else {
					c, err = Dial(sock, 0, []byte("s3cret"))
				}
				if err != nil {
					b.Fatal(err)
				}
				c.Close()
			}
		})
	}
}
