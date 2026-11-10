package holder

import (
	"path/filepath"
	"strings"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// PeerProc is the process at the other end of a connection: its pid and,
// on macOS, an audit token (the socket's peer's as it was asked, or a
// proof's sender's as it sent it).
type PeerProc struct {
	PID   int
	Token codesign.Token
}

// DaemonPeer is a Config.Peer that takes only a daemon: the program that
// started this holder (daemon, its path then), or an hmkd of the same
// install (install as in Config.Install: a restarted or updated daemon, in
// the same app or beside this holder), or, signed by a team, an hmkd signed
// by this holder's own team (one the app was moved with). The token alone
// would let anything of yours that read the daemon's state attach; this
// way it also has to be the daemon.
//
// On macOS it's given the audit token of a proof's sender as it sent it
// (proof.go), which the holder has checked is the process at the other end
// of the connection: not the socket's peer token, which macOS works out
// when asked from whichever process last used the socket, so a program
// that connects and then execs a suspended hmkd in its own place (or hands
// the connection to a child) would pass for it. And a holder with the
// hardened runtime takes, by path, only an hmkd with it too: hmkd run with
// DYLD_INSERT_LIBRARIES (which the runtime refuses) is anyone's code.
// (A plain `go build`'s holder and hmkd have neither: nothing to require.)
// On Linux, by the socket's peer pid: see peerExe.
func DaemonPeer(daemon, install string) func(PeerProc) bool {
	daemon = resolve(daemon)
	install = resolve(install)
	self, _ := codesign.Self()
	return func(p PeerProc) bool {
		exe, err := peerExe(p)
		if err != nil {
			return false
		}
		exe = resolve(exe)
		byPath := daemon != "" && exe == daemon
		if !byPath && filepath.Base(exe) == "hmkd" && install != "" {
			byPath = strings.HasSuffix(install, ".app") && strings.HasPrefix(exe, install+"/") ||
				!strings.HasSuffix(install, ".app") && filepath.Dir(exe) == filepath.Dir(install)
		}
		if byPath {
			if !self.Runtime {
				return true
			}
			s, ok := peerSignature(p, "")
			return ok && s.Runtime
		}
		if self.Team != "" {
			s, ok := peerSignature(p, `anchor apple generic and certificate leaf[subject.OU] = "`+self.Team+`" and identifier "hmkd"`)
			return ok && s.Runtime
		}
		return false
	}
}

func resolve(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p)) // (gone: where it was)
	}
	return p
}
