package holder

import (
	"path/filepath"
	"strings"

	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
)

// DaemonPeer is a Config.Peer that takes only a daemon: the program that
// started this holder (daemon, its path then), or an hmkd of the same
// install (install as in Config.Install: a restarted or updated daemon, in
// the same app or beside this holder), or, signed by a team, an hmkd signed
// by this holder's own team (one the app was moved with). The token alone
// would let anything of yours that read the daemon's state attach; this
// way it also has to be the daemon.
func DaemonPeer(daemon, install string) func(pid int) bool {
	daemon = resolve(daemon)
	install = resolve(install)
	return func(pid int) bool {
		exe, err := procinfo.Path(pid)
		if err != nil {
			return false
		}
		exe = resolve(exe)
		if daemon != "" && exe == daemon {
			return true
		}
		if filepath.Base(exe) == "hmkd" && install != "" {
			if strings.HasSuffix(install, ".app") && strings.HasPrefix(exe, install+"/") {
				return true
			}
			if !strings.HasSuffix(install, ".app") && filepath.Dir(exe) == filepath.Dir(install) {
				return true
			}
		}
		if self, _ := codesign.Self(); self.Team != "" {
			s, ok := codesign.Check(pid, `anchor apple generic and certificate leaf[subject.OU] = "`+self.Team+`" and identifier "hmkd"`)
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
