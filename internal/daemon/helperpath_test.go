package daemon

import (
	"io"
	"log/slog"
	"testing"
)

// In a build signed by a team, a program given from outside (--holder, an
// HMK_*_HOST) is taken only if it's the app's own or the team's: not some
// other program, even one Apple signed.
func TestHelperOverridesInSignedBuilds(t *testing.T) {
	t.Setenv("HMK_BROWSER_HOST", "/bin/sh")
	if helperEnv("HMK_BROWSER_HOST") != "/bin/sh" {
		t.Fatal("a development build refused a helper given from outside")
	}
	old := signedTeam
	signedTeam = func() string { return "TESTTEAM00" }
	defer func() { signedTeam = old }()
	if helperAllowed("--holder", "/bin/sh") {
		t.Error("a signed build took another team's program as its holder")
	}
	if p := helperEnv("HMK_BROWSER_HOST"); p != "" {
		t.Errorf("a signed build took %q as its browser host", p)
	}
	if browserHostPath() == "/bin/sh" {
		t.Error("browserHostPath took the override")
	}
	d, err := New(Config{Dir: shortDir(t), HolderPath: "/bin/sh", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	defer d.Shutdown()
	if d.cfg.HolderPath == "/bin/sh" {
		t.Error("New kept a --holder that isn't the app's or the team's")
	}
}
