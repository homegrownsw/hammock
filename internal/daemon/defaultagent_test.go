package daemon

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// default-agent names the agent a start that names none runs, only while
// it's installed here; unset, or one Hammock doesn't know, is none.
func TestDefaultAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "config"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for cfg, want := range map[string]string{
		"":                              "",
		"default-agent = codex\n":       "codex",
		"default-agent = Claude Code\n": "claude",
		"default-agent = nope\n":        "",
	} {
		write(cfg)
		if got := readDefaultAgent(); got != want {
			t.Errorf("%q: %q, want %q", cfg, got, want)
		}
	}

	d := &Daemon{defaultAgentSet: "codex"}
	if got := d.defaultAgent(); got != "codex" {
		t.Errorf("not asked yet: %q", got) // as clients take it: Claude Code and Codex
	}
	d.agentKinds = agentKinds([]string{"claude"})
	if got := d.defaultAgent(); got != "" {
		t.Errorf("codex not installed: %q", got)
	}
	m := &pb.StartAgent{}
	if err := d.resolveHarness(m); err != errNoDefaultAgent {
		t.Errorf("none installed: %v", err)
	}
	d.agentKinds = agentKinds([]string{"claude", "codex"})
	if err := d.resolveHarness(m); err != nil || m.Harness != "codex" {
		t.Errorf("resolved %q, %v", m.Harness, err)
	}
	m = &pb.StartAgent{Harness: "claude"}
	if err := d.resolveHarness(m); err != nil || m.Harness != "claude" {
		t.Errorf("named: %q, %v", m.Harness, err) // a named one is kept
	}
}
