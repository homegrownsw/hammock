package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// A daemon started by an init script (no $SHELL) takes the user's shell
// from /etc/passwd, skipping ones that only refuse logins.
func TestPasswdShell(t *testing.T) {
	f := filepath.Join(t.TempDir(), "passwd")
	os.WriteFile(f, []byte("root:x:0:0:Console:/root:/bin/sh\nalex:x:1000:100::/:/bin/false\nduncan:x:1001:100::/home/d:/usr/sbin/nologin\nghost:x:1002:100::/:/no/such/shell\n"), 0o644)
	for uid, want := range map[int]string{0: "/bin/sh", 1000: "", 1001: "", 1002: "", 4242: ""} {
		if got := passwdShell(f, uid); got != want {
			t.Errorf("uid %d: %q, want %q", uid, got, want)
		}
	}
}
