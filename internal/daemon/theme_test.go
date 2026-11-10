package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

func TestReadHostTheme(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	ghostty := t.TempDir()
	old := themeDirs
	themeDirs = func() []string { return []string{filepath.Join(cfg, "themes"), ghostty} }
	t.Cleanup(func() { themeDirs = old })
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(cfg, "config"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(cfg, "themes"), 0o755)
	os.WriteFile(filepath.Join(cfg, "themes", "Mine"), []byte("background = #101010\n"), 0o644)
	os.WriteFile(filepath.Join(ghostty, "Mine"), []byte("background = #202020\n"), 0o644) // Hammock's folder first
	os.WriteFile(filepath.Join(ghostty, "Dracula"), []byte("background = #282a36\n"), 0o644)

	for _, c := range []struct {
		config string
		want   *pb.HostTheme
	}{
		{"", &pb.HostTheme{Name: "Hammock"}},
		{"app-theme = nord\ntheme = dark:Dracula\n", &pb.HostTheme{Name: "Nord"}},
		// Legacy settings and no app-theme: custom.
		{"theme = dark:Dracula\nagent-color = Ocean\n", &pb.HostTheme{Name: "custom", LightTerminal: "Hammock Light", DarkTerminal: "Dracula",
			DarkTerminalFile: "background = #282a36\n", AgentColor: "Ocean"}},
		{"app-theme = custom\ntheme = dark:Mine,light:TokyoNight Day\nagent-color = light:#112233,dark:#445566\n",
			&pb.HostTheme{Name: "custom", LightTerminal: "TokyoNight Day", DarkTerminal: "Mine", DarkTerminalFile: "background = #101010\n",
				AgentColor: "light:#112233,dark:#445566"}},
		{"app-theme = custom\ntheme = ../config\n", &pb.HostTheme{Name: "custom", LightTerminal: "../config", DarkTerminal: "../config"}},
		{"app-theme = custom\nwindow-theme = Ocean\n", &pb.HostTheme{Name: "custom", LightTerminal: "Hammock Light", DarkTerminal: "Hammock Dark",
			WindowTheme: "Ocean"}},
		{"app-theme =\nwindow-theme = Rose\n", &pb.HostTheme{Name: "custom", LightTerminal: "Hammock Light", DarkTerminal: "Hammock Dark",
			WindowTheme: "Rose"}},
	} {
		write(c.config)
		if got := readHostTheme(); !proto.Equal(got, c.want) {
			t.Errorf("%q:\n got %v\nwant %v", c.config, got, c.want)
		}
	}
}

// The reader reads again only when the config or a terminal theme's file
// changes (or one appears where it would win).
func TestThemeReader(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	ghostty := t.TempDir()
	old := themeDirs
	themeDirs = func() []string { return []string{filepath.Join(cfg, "themes"), ghostty} }
	t.Cleanup(func() { themeDirs = old })
	config := filepath.Join(cfg, "config")
	os.WriteFile(config, []byte("app-theme = custom\ntheme = Dracula\n"), 0o644)
	os.WriteFile(filepath.Join(ghostty, "Dracula"), []byte("background = #282a36\n"), 0o644)

	var r themeReader
	th, changed := r.read()
	if !changed || th.GetDarkTerminalFile() != "background = #282a36\n" {
		t.Fatalf("first read: %v %v", th, changed)
	}
	if _, changed := r.read(); changed {
		t.Fatal("read again with nothing changed")
	}
	os.WriteFile(filepath.Join(ghostty, "Dracula"), []byte("background = #000000\n"), 0o644)
	if th, changed := r.read(); !changed || th.GetDarkTerminalFile() != "background = #000000\n" {
		t.Fatalf("theme file changed: %v %v", th, changed)
	}
	os.MkdirAll(filepath.Join(cfg, "themes"), 0o755)
	os.WriteFile(filepath.Join(cfg, "themes", "Dracula"), []byte("background = #111111\n"), 0o644)
	if th, changed := r.read(); !changed || th.GetDarkTerminalFile() != "background = #111111\n" {
		t.Fatalf("file in Hammock's folder: %v %v", th, changed)
	}
	os.WriteFile(config, []byte("app-theme = Nord\n"), 0o644)
	if th, changed := r.read(); !changed || th.GetName() != "Nord" {
		t.Fatalf("config changed: %v %v", th, changed)
	}
	if _, changed := r.read(); changed {
		t.Fatal("read again with nothing changed (curated)")
	}
}

// Clients get the theme with the state, and again when it changes.
func TestThemeUpdates(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	os.WriteFile(filepath.Join(cfg, "config"), []byte("app-theme = Ocean\n"), 0o644)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	for deadline := time.Now().Add(5 * time.Second); !d.themeIs("Ocean"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("theme not read")
		}
	}
	st := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState()
	if st.GetTheme().GetName() != "Ocean" {
		t.Fatalf("full state's theme: %v", st.GetTheme())
	}
	os.WriteFile(filepath.Join(cfg, "config"), []byte("app-theme = Rosé Pine\n"), 0o644)
	waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetState().GetTheme().GetName() == "Rosé Pine" })
}

func (d *Daemon) themeIs(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.theme.GetName() == name
}
