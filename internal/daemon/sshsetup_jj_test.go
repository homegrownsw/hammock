package daemon

import (
	"os"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestJJVersionAtLeast(t *testing.T) {
	for out, want := range map[string]bool{
		"jj 0.47.1-6b0e1c": true,
		"jj 0.39.0":        true,
		"jj 0.38.2":        false,
		"jj 1.0.0":         true,
		"something else":   false,
	} {
		if got := jjVersionAtLeast(out, jjMinVersion); got != want {
			t.Errorf("%q: %v", out, got)
		}
	}
}

// jj that signs with 1Password gets Hammock's file in its conf.d, scoped to
// Hammock's panes; jj that doesn't sign with SSH is left alone.
func TestJJSetup(t *testing.T) {
	d := startDaemon(t, t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := map[string]string{
		"--version":                    "jj 0.47.1",
		"signing.backend":              "ssh",
		"signing.backends.ssh.program": "/Applications/1Password.app/Contents/MacOS/op-ssh-sign",
	}
	oldFind, oldRun := findJJ, runJJ
	t.Cleanup(func() { findJJ, runJJ = oldFind, oldRun })
	findJJ = func() string { return "/opt/homebrew/bin/jj" }
	runJJ = func(_ string, args ...string) (string, error) { return cfg[args[len(args)-1]], nil }

	r := &pb.SshSetupReply{}
	if err := d.jjSetup(r, false); err != nil {
		t.Fatal(err)
	}
	if r.JjPath == "" || r.JjRouted || r.JjProblem != "" || !strings.Contains(r.JjChange, `--when.environments = ["HMK_PANE_ID"]`) ||
		!strings.Contains(r.JjChange, "program = '"+d.jjSignLink()+"'") || !strings.Contains(r.JjChange, "op-ssh-sign") {
		t.Fatalf("before: %+v", r)
	}
	if err := d.jjSetup(r, true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(r.JjConfigPath)
	if err != nil || string(b) != r.JjChange || !r.JjRouted || !strings.HasSuffix(r.JjConfigPath, "/jj/conf.d/zz-hammock.toml") {
		t.Fatalf("after: %+v %q %v", r, b, err)
	}
	r = &pb.SshSetupReply{}
	_ = d.jjSetup(r, false)
	if !r.JjRouted {
		t.Fatal("not routed on a second look")
	}

	cfg["--version"] = "jj 0.38.0"
	os.Remove(r.JjConfigPath)
	r = &pb.SshSetupReply{}
	_ = d.jjSetup(r, true)
	if r.JjProblem == "" || r.JjRouted {
		t.Fatalf("old jj: %+v", r)
	}

	cfg["signing.backend"] = "gpg"
	r = &pb.SshSetupReply{}
	_ = d.jjSetup(r, true)
	if r.JjPath != "" {
		t.Fatalf("gpg: %+v", r)
	}
}
