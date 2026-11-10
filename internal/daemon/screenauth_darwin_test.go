package daemon

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func testScreenHub(t *testing.T, path string) *screenHub {
	h := &screenHub{d: &Daemon{log: slog.New(slog.NewTextHandler(io.Discard, nil))}, path: path, secret: make([]byte, 32)}
	_, _ = rand.Read(h.secret)
	return h
}

// The daemon gives its secret only to the screen host it chose: a hello
// from any other program (here, the screen host is said to be somewhere
// else) gets nothing; one from that program gets the secret and the socket
// to serve.
func TestScreenRendezvous(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	other := testScreenHub(t, filepath.Join(t.TempDir(), "Hammock Screen"))
	name, err := other.rendezvousFor("/x/screen-host.sock")
	if err != nil {
		t.Fatal(err)
	}
	if b, _, err := rendezvousFetch(name, screenHello, 500*time.Millisecond); err == nil {
		t.Fatalf("another program got the secret: %s", b)
	}

	h := testScreenHub(t, exe) // this test is the "screen host"
	name, err = h.rendezvousFor("/x/screen-host.sock")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rendezvousFetch(name, "hmk-key-hello-v1", 300*time.Millisecond); err == nil {
		t.Fatal("answered another helper's hello")
	}
	b, from, err := rendezvousFetch(name, screenHello, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		V      int    `json:"v"`
		Secret []byte `json:"secret"`
		Socket string `json:"socket"`
	}
	if json.Unmarshal(b, &m) != nil || m.V != screenProtocol || !bytes.Equal(m.Secret, h.secret) || m.Socket != "/x/screen-host.sock" {
		t.Fatalf("answer %s", b)
	}
	if from.PID() != os.Getpid() {
		t.Fatalf("answered by pid %d", from.PID())
	}
}

// fakeScreenHost is the host's end of a connection (host.m): hello with a
// challenge, then whether the answer is screenMAC under secret, bound to
// the daemon's pid and its own (one process here, so both are ours).
func fakeScreenHost(t *testing.T, c net.Conn, secret []byte) {
	defer c.Close()
	challenge := make([]byte, 32)
	_, _ = rand.Read(challenge)
	b, _ := json.Marshal(map[string]any{"t": "hello", "pid": os.Getpid(), "challenge": challenge})
	c.Write(append(b, '\n'))
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return
	}
	var m struct {
		T   string `json:"t"`
		Pid int    `json:"pid"`
		Mac []byte `json:"mac"`
	}
	ok := json.Unmarshal(line, &m) == nil && m.T == "auth" && m.Pid == os.Getpid() &&
		bytes.Equal(m.Mac, screenMAC(secret, challenge, os.Getpid(), os.Getpid()))
	if ok {
		c.Write([]byte(`{"t":"authed"}` + "\n"))
	} else {
		c.Write([]byte(`{"t":"hello","pid":1,"refused":true}` + "\n"))
	}
}

// unixPair is two connected Unix sockets, so each end's peer pid is ours.
func unixPair(t *testing.T) (net.Conn, net.Conn) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	conn := func(fd int) net.Conn {
		f := os.NewFile(uintptr(fd), "pair")
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	return conn(fds[0]), conn(fds[1])
}

// The daemon answers a host's challenge with its secret, and a host that
// has another (a previous daemon's) refuses it, to be replaced.
func TestScreenGreet(t *testing.T) {
	h := testScreenHub(t, "")
	greet := func(secret []byte) error {
		a, b := unixPair(t)
		go fakeScreenHost(t, b, secret)
		defer a.Close()
		_, err := h.greet(a, bufio.NewReader(a))
		return err
	}
	if err := greet(h.secret); err != nil {
		t.Fatalf("this daemon's host: %v", err)
	}
	var refused errScreenHostRefused
	if err := greet(make([]byte, 32)); !errors.As(err, &refused) {
		t.Fatalf("another daemon's host: %v", err)
	}
	// One that can't challenge (from before the secret) is replaced too.
	a, b := unixPair(t)
	go func() { b.Write([]byte(`{"t":"hello","pid":1,"built":1}` + "\n")); b.Close() }()
	if _, err := h.greet(a, bufio.NewReader(a)); !errors.As(err, &refused) {
		t.Fatalf("an older host: %v", err)
	}
}
