package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// plainShell sets $SHELL to sh run without its login profile, so a PATH a
// test sets (with a stand-in agent on it) reaches the panes' shells: a
// login sh reads /etc/profile, which on Debian (and Unraid) sets PATH
// afresh.
func plainShell(t *testing.T) {
	t.Helper()
	sh := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(sh, []byte("#!/bin/sh\n[ \"$1\" = -l ] && shift\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", sh)
}
