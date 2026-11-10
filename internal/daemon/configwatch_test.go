package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/state"
)

// A security setting loosened in the config file by nobody here is said
// (an audit entry, an item in Needs You) and stands; one the app says it
// wrote, the daemon's own, and tightening one aren't.
func TestConfigLoosenedIsSaid(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	path := filepath.Join(cfg, "config")
	write := func(s string) {
		// A new size each time: the watch goes by the file's stamp.
		if err := os.WriteFile(path, []byte(s+"\n# "+time.Now().String()+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("screen-sharing = false\nbrowser-open-schemes = zoommtg")
	old := configWatchEvery
	configWatchEvery = time.Hour // only the test checks
	t.Cleanup(func() { configWatchEvery = old })
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	check := func() {
		d.checkConfig()
		d.cfgw.mu.Lock()
		for k, p := range d.cfgw.pending {
			p.since = p.since.Add(-time.Hour) // as if a tick had passed
			d.cfgw.pending[k] = p
		}
		d.cfgw.mu.Unlock()
		d.checkConfig()
	}
	said := func(key string) string {
		d.mu.Lock()
		defer d.mu.Unlock()
		if a := d.attention["at_config_"+key]; a != nil {
			return a.Summary
		}
		return ""
	}
	check() // what it was at the start: yours

	write("screen-sharing = true\nbrowser-open-schemes = zoommtg,vscode")
	check()
	if s := said("screen-sharing"); !strings.Contains(s, "Screen sharing was turned on") {
		t.Fatalf("screen sharing: %q", s)
	}
	if s := said("browser-open-schemes"); !strings.Contains(s, "vscode:") || strings.Contains(s, "zoommtg") {
		t.Fatalf("schemes: %q", s)
	}
	if !screenSharing() {
		t.Fatal("the change didn't stand")
	}
	d.mu.Lock()
	audit, _ := d.store.RecentAudit(50)
	d.mu.Unlock()
	if !slices.ContainsFunc(audit, func(e state.AuditEvent) bool { return e.Action == "config-loosened" }) {
		t.Fatal("no audit entry")
	}

	// The app turned paste protection off (Settings), and says so.
	ui := dial(t, dir)
	write("screen-sharing = true\nbrowser-open-schemes = zoommtg,vscode\nclipboard-paste-protection = false")
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_ConfigWritten{ConfigWritten: &pb.ConfigWritten{
		Entries: []*pb.ConfigEntry{{Key: "clipboard-paste-protection", Values: []string{"false"}}},
	}}})
	check()
	if s := said("clipboard-paste-protection"); s != "" {
		t.Fatalf("the app's own change was said: %q", s)
	}
	// Tightening isn't news; nor is the daemon's own write.
	write("screen-sharing = false\nclipboard-paste-protection = false\nssh-agent-forwarding = false")
	check()
	d.cfgw.noteWritten("remote-network", []string{"192.168.7.0/24"}, true)
	write("screen-sharing = false\nclipboard-paste-protection = false\nssh-agent-forwarding = false\nremote-network = 192.168.7.0/24")
	check()
	if s := said("remote-network") + said("ssh-agent-forwarding"); s != "" {
		t.Fatalf("said: %q", s)
	}
	// Turned on by hand: said.
	write("screen-sharing = false\nclipboard-paste-protection = false\nremote-network = 192.168.7.0/24\nremote-network = 10.0.0.0/8")
	check()
	if s := said("ssh-agent-forwarding"); !strings.Contains(s, "forwarding was turned on") {
		t.Fatalf("forwarding: %q", s)
	}
	if s := said("remote-network"); !strings.Contains(s, "10.0.0.0/8") || strings.Contains(s, "192.168.7.0/24") {
		t.Fatalf("networks: %q", s)
	}

	// An edit of the same size, its time put back: still a change.
	fi, _ := os.Stat(path)
	b, _ := os.ReadFile(path)
	same := strings.Replace(string(b), "screen-sharing = false", "screen-sharing =  true", 1)
	if len(same) != len(b) {
		t.Fatalf("sizes %d, %d", len(same), len(b))
	}
	os.WriteFile(path, []byte(same), 0o600)
	os.Chtimes(path, fi.ModTime(), fi.ModTime())
	d.mu.Lock()
	d.clearAttention("at_config_screen-sharing")
	d.mu.Unlock()
	check()
	if s := said("screen-sharing"); s == "" {
		t.Fatal("a same-size edit with its time put back went unseen")
	}

	// Only you clear it: not a program scoped as an agent.
	asPerson(t, func(cl *caller, _ int) bool { return cl != nil && cl.direct && !cl.agent })
	outside := dialAs(t, dir, nil, "cli")
	if _, err := try(outside, &pb.ClientMessage{Msg: &pb.ClientMessage_ClearAttention{ClearAttention: &pb.ClearAttention{Id: "at_config_screen-sharing"}}}); err == nil {
		t.Fatal("a program that isn't you cleared the warning")
	}
	if said("screen-sharing") == "" {
		t.Fatal("the warning went")
	}

	// Stopped, edited, started again: said at the start, not taken as it.
	d.Shutdown()
	write("screen-sharing = false\nclipboard-paste-protection = false\nremote-network = 192.168.7.0/24\nremote-network = 10.0.0.0/8\nbrowser-open-schemes = slack")
	d = startDaemon(t, dir)
	check()
	if s := said("browser-open-schemes"); !strings.Contains(s, "slack:") {
		t.Fatalf("a change made while the daemon was stopped: %q", s)
	}
}
