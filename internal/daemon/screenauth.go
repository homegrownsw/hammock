package daemon

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// How the daemon proves to the screen host (helpers/screen/host.m) that it's
// the daemon that started it. The host holds Screen Recording and
// Accessibility, so whoever it serves can see the screen and type: it
// must serve only this daemon, and can't go by its socket's peer (a
// process of yours can connect, then exec a suspended hmkd in its own
// place, and the peer token then says hmkd: helpers/common/rendezvous.h).
//
//   - The daemon makes a secret as it starts (screenHub.secret), and starts
//     each host with a rendezvous name (--rendezvous=, helpers/common/
//     rendezvous.h). It answers a hello there only from the screen host it
//     chose (its program, h.path, validly signed by the daemon's team with
//     the hardened runtime, by the hello's audit token as it was sent),
//     with the secret and the socket the host is to serve. Started by
//     Launch Services, the host isn't the daemon's child, so its pid can't
//     say which one; any genuine host may have the secret, as it only ever
//     uses it to check the daemon.
//   - The host believes the answer only if its sender, by the message's
//     audit token, is hmkd signed by its own team with the hardened runtime
//     (ad hoc: the hmkd of its own build), and serves only that socket.
//     Started any other way (no name, or one it gets no such answer from),
//     it serves no one.
//   - On every connection the host's hello carries a fresh challenge, and
//     nothing is served until the client answers with an HMAC of it under
//     the secret, bound to both ends' pids as each sees them (the
//     daemon's own, and its socket's peer; the host checks them against its
//     own and its peer's). So the secret never crosses the socket, and a
//     program sitting between the two (its socket put in the host's place)
//     can't pass the daemon's answer on: the pids it binds aren't the
//     relay's.
//
// A restarted daemon has a new secret: the old host refuses it, and is
// replaced by one of its own (screenHub.current).

// screenHello is the screen host's rendezvous hello (helpers/screen/host.m),
// and screenProtocol the version of what the daemon answers it with.
const (
	screenHello    = "hmk-scr-hello-v1"
	screenProtocol = 1
	// How long a host has to say hello once started: a first launch through
	// Launch Services can be slow.
	screenRendezvousFor = 30 * time.Second
)

// screenMAC is what the daemon answers a host's challenge with; the host
// works out the same (host.m, -authOK:).
func screenMAC(secret, challenge []byte, daemonPid, hostPid int) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("hmk-screen-auth-v1\n"))
	m.Write(challenge)
	var b [8]byte
	binary.BigEndian.PutUint32(b[:4], uint32(daemonPid))
	binary.BigEndian.PutUint32(b[4:], uint32(hostPid))
	m.Write(b[:])
	return m.Sum(nil)
}

// screenHostGenuine says whether the token (a rendezvous hello's sender, as
// it sent it) is the screen host at path: that program, still running,
// validly signed by this daemon's team (any valid signature, ad hoc), with
// the hardened runtime if this daemon has it.
func screenHostGenuine(t codesign.Token, path string) bool {
	exe, err := t.Path() // fails if it has exec'd since it said hello
	if err != nil {
		return false
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	want := path
	if r, err := filepath.EvalSymlinks(path); err == nil {
		want = r
	}
	if exe != want {
		return false
	}
	self, _ := codesign.Self()
	req := ""
	if self.Team != "" {
		req = fmt.Sprintf(`anchor apple generic and certificate leaf[subject.OU] = "%s"`, self.Team)
	}
	sig, ok := codesign.CheckToken(t, req)
	return ok && (sig.Runtime || !self.Runtime)
}

// rendezvousFor starts answering the screen host this daemon is about to
// start, for a while: its rendezvous name, for the host's arguments.
func (h *screenHub) rendezvousFor(socket string) (string, error) {
	rv, err := newRendezvous("com.homegrownsw.hammock.screen.", screenHello)
	if err != nil {
		return "", fmt.Errorf("can't make a way to the screen host (%v)", err)
	}
	answer, _ := json.Marshal(map[string]any{"v": screenProtocol, "secret": h.secret, "socket": socket})
	path := h.path
	stop := make(chan struct{})
	time.AfterFunc(screenRendezvousFor, func() { close(stop) })
	go func() {
		defer rv.close()
		_ = rv.serve(func(t codesign.Token) ([]byte, bool) {
			if !screenHostGenuine(t, path) {
				h.d.log.Warn("screen host rendezvous: ignored a hello from something else", "pid", t.PID())
				return nil, false
			}
			return answer, false
		}, stop)
	}()
	return rv.name, nil
}

// errScreenHostRefused: the host won't take this daemon (another daemon
// started it, or another build), or can't prove anything (one from before
// the secret): run replaces it.
type errScreenHostRefused struct{ pid int }

func (e errScreenHostRefused) Error() string {
	return fmt.Sprintf("the screen host (pid %d) won't take this daemon", e.pid)
}

// greet reads the host's hello on a new connection and answers its
// challenge, until the host says it's taken this daemon. The hello.
func (h *screenHub) greet(c net.Conn, r *bufio.Reader) (screenMsg, error) {
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	var hello screenMsg
	line, err := r.ReadBytes('\n')
	if err != nil {
		return hello, err
	}
	if json.Unmarshal(line, &hello) != nil || hello.T != "hello" {
		return hello, errors.New("the screen host didn't say hello")
	}
	// The process at the other end, as the socket says: the one to stop if
	// it won't take us, and what the answer is bound to.
	peer := 0
	if uc, ok := c.(*net.UnixConn); ok {
		peer = sockPeerPID(uc)
	}
	pid := hello.Pid
	if peer > 0 {
		pid = peer
	}
	if hello.Refused || len(hello.Challenge) == 0 || len(h.secret) == 0 {
		return hello, errScreenHostRefused{pid}
	}
	b, _ := json.Marshal(map[string]any{"t": "auth", "pid": os.Getpid(), "mac": screenMAC(h.secret, hello.Challenge, os.Getpid(), peer)})
	_ = c.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := c.Write(append(b, '\n')); err != nil {
		return hello, err
	}
	_ = c.SetWriteDeadline(time.Time{})
	line, err = r.ReadBytes('\n')
	if err != nil {
		return hello, err
	}
	var m screenMsg
	if json.Unmarshal(line, &m) != nil || m.T != "authed" {
		return hello, errScreenHostRefused{pid}
	}
	return hello, nil
}
