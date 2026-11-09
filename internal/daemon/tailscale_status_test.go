package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTailscaleStatus(t *testing.T) {
	ok := `{"BackendState":"Running","Self":{"HostName":"mac","UserID":1}}`
	if st, err := parseTailscaleStatus([]byte(ok)); err != nil || st.Self.HostName != "mac" {
		t.Fatalf("running: %v %v", st, err)
	}
	if _, err := parseTailscaleStatus([]byte("Warning: client version mismatch\n" + ok)); err != nil {
		t.Fatalf("a warning before the JSON: %v", err)
	}
	for raw, want := range map[string]string{
		`{"BackendState":"NeedsLogin","Self":null}`:      "isn't logged in",
		`{"BackendState":"Stopped","Self":{"UserID":1}}`: "turned off",
		`{"BackendState":"Starting"}`:                    "still connecting",
		`{"BackendState":"NeedsMachineAuth"}`:            "approval",
		`{"BackendState":"Running"}`:                     "didn't say which computer",
		`nope`:                                           "couldn't be read",
	} {
		if _, err := parseTailscaleStatus([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", raw, err, want)
		}
	}
}

func TestInstalledAt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := agentInstallDirs
	t.Cleanup(func() { agentInstallDirs = old })
	agentInstallDirs = []string{"~/.local/bin", "~/.nvm/versions/node/*/bin"}
	bin := filepath.Join(home, ".nvm/versions/node/v22.1.0/bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "codex"), nil, 0o644); err != nil { // not executable
		t.Fatal(err)
	}
	if !installedAt("claude") || installedAt("codex") {
		t.Fatalf("claude %v codex %v", installedAt("claude"), installedAt("codex"))
	}
}

// Two CLIs, the first asking a tailscaled that's logged out (Homebrew's,
// say) and the second the app's, connected: the second's answer counts.
// Each is, like the app's binary, the CLI only when told it is.
func TestAskTailscaleStatusTriesEach(t *testing.T) {
	bin := t.TempDir()
	write := func(name, out string) string {
		p := filepath.Join(bin, name)
		sh := "#!/bin/sh\n[ \"$TAILSCALE_BE_CLI\" = 1 ] || { echo 'The Tailscale GUI failed to start'; exit 0; }\necho '" + out + "'\n"
		if err := os.WriteFile(p, []byte(sh), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	brew := write("brew-tailscale", `{"BackendState":"NeedsLogin"}`)
	app := write("app-tailscale", `{"BackendState":"Running","Self":{"HostName":"mac"}}`)
	old := tailscaleCommands
	t.Cleanup(func() { tailscaleCommands = old })
	tailscaleCommands = []string{filepath.Join(bin, "missing"), brew, app}
	if st, err := askTailscaleStatus(t.Context()); err != nil || st.Self.HostName != "mac" {
		t.Fatalf("%v %v", st, err)
	}
	tailscaleCommands = []string{brew}
	if _, err := askTailscaleStatus(t.Context()); err == nil || !strings.Contains(err.Error(), "logged in") {
		t.Fatalf("only the logged-out one: %v", err)
	}
}
