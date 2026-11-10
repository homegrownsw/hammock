package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
)

// isHammockApp: there's no Hammock app on Linux.
func isHammockApp(int, codesign.Token) bool { return false }

// responsibleFor: Linux keeps no such record.
func responsibleFor(int) int { return 0 }

// personPage reports whether pid is Unraid's plugin page asking for you:
// started with this run's key (below).
func personPage(pid int) bool { return hasPersonKey(pid) }

// Unraid's Hammock page (action.php) runs hmk for you from php-fpm. Any
// program of root's can send php-fpm a FastCGI request naming a script of
// its own, so php-fpm's children aren't you by being its children: the
// page reads a key hmkd leaves in its state folder (root's only) and
// passes it to hmk in its environment (HMK_PERSON_KEY), which hmkd reads
// from the process, as it reads SSH_CONNECTION.
//
// On Unraid everything runs as root, agents included, and root reads any
// file: there the person boundary is nominal, a guardrail against an agent
// wandering, not against one set on getting through.

const personKeyFile = "person.key"

var personKey atomic.Value // string

// writePersonKey makes a new key for this run of the daemon, in dir.
func writePersonKey(dir string, log *slog.Logger) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return
	}
	k := hex.EncodeToString(b)
	p := filepath.Join(dir, personKeyFile)
	tmp := p + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(k+"\n"), 0o600); err != nil || os.Rename(tmp, p) != nil {
		log.Warn("can't write the web page's key: Unraid's page can't do what only you may", "err", err)
		_ = os.Remove(tmp)
		return
	}
	personKey.Store(k)
}

// hasPersonKey reports whether pid was started with this run's key.
func hasPersonKey(pid int) bool {
	k, _ := personKey.Load().(string)
	if k == "" {
		return false
	}
	for _, kv := range procinfo.Env(pid) {
		if v, ok := strings.CutPrefix(kv, "HMK_PERSON_KEY="); ok {
			return subtle.ConstantTimeCompare([]byte(v), []byte(k)) == 1
		}
	}
	return false
}
