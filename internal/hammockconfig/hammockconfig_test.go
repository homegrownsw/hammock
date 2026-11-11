package hammockconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Set changes the key's line where it is, removes repeats, appends a new
// key before the final newline, and leaves comments and the rest alone.
func TestSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config")
	read := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	if err := Set("app-theme", "Nord"); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != header+"\napp-theme = Nord\n" {
		t.Fatalf("new file: %q", got)
	}

	os.Chmod(path, 0o600)
	os.WriteFile(path, []byte("# app-theme = x\nfont-size = 14\napp-theme = Ocean\nappearance = dark\napp-theme = Rose\n"), 0o600)
	if err := Set("app-theme", "Tokyo Night"); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), "# app-theme = x\nfont-size = 14\napp-theme = Tokyo Night\nappearance = dark\n"; got != want {
		t.Fatalf("replace: %q, want %q", got, want)
	}
	if v, _ := Value("app-theme"); v != "Tokyo Night" {
		t.Fatalf("Value: %q", v)
	}

	if err := Set("theme", "dark:Nord,light:Nord Light"); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), "# app-theme = x\nfont-size = 14\napp-theme = Tokyo Night\nappearance = dark\ntheme = dark:Nord,light:Nord Light\n"; got != want {
		t.Fatalf("append: %q, want %q", got, want)
	}
	if v := Values()["theme"]; v != "dark:Nord,light:Nord Light" {
		t.Fatalf("Values: %q", v)
	}

	if err := Set("app-theme", ""); err != nil {
		t.Fatal(err)
	}
	if got, want := read(), "# app-theme = x\nfont-size = 14\nappearance = dark\ntheme = dark:Nord,light:Nord Light\n"; got != want {
		t.Fatalf("remove: %q, want %q", got, want)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v", st.Mode())
	}
}

func TestAppTheme(t *testing.T) {
	for _, c := range []struct {
		values map[string]string
		want   string
	}{
		{nil, "Hammock"},
		{map[string]string{"font-size": "14"}, "Hammock"},
		{map[string]string{"theme": "dark:Nord"}, "custom"},
		{map[string]string{"agent-color": "Ocean"}, "custom"},
		{map[string]string{"app-theme": "tokyo night", "theme": "dark:Nord"}, "Tokyo Night"},
		{map[string]string{"app-theme": "CUSTOM"}, "custom"},
		{map[string]string{"app-theme": "rosé pine"}, "Rosé Pine"},
		{map[string]string{"app-theme": "nonsense"}, "Hammock"},
		{map[string]string{"app-theme": "rose pine"}, "Rosé Pine"},
		{map[string]string{"app-theme": "Rose\u0301 Pine"}, "Rosé Pine"}, // NFD
		{map[string]string{"app-theme": "ROSÉ PINE"}, "Rosé Pine"},
		{map[string]string{"app-theme": "", "theme": "dark:Nord"}, "custom"},
		{map[string]string{"app-theme": " "}, "Hammock"},
		{map[string]string{"theme": "", "agent-color": ""}, "Hammock"},
	} {
		if got := AppTheme(c.values); got != c.want {
			t.Errorf("AppTheme(%v) = %q, want %q", c.values, got, c.want)
		}
	}
}

func TestTerminalThemes(t *testing.T) {
	for _, c := range []struct{ in, light, dark string }{
		{"", "Hammock Light", "Hammock Dark"},
		{"dark:Nord,light:Nord Light", "Nord Light", "Nord"},
		{"light:A , dark:B", "A", "B"},
		{"Dracula", "Dracula", "Dracula"},
		{"dark:TokyoNight", "Hammock Light", "TokyoNight"},
	} {
		if l, d := TerminalThemes(c.in); l != c.light || d != c.dark {
			t.Errorf("TerminalThemes(%q) = %q, %q", c.in, l, d)
		}
	}
}

// A symlinked config (stow, chezmoi) stays a symlink; its target changes.
func TestSetSymlink(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	target := filepath.Join(t.TempDir(), "dotfiles-config")
	os.WriteFile(target, []byte("font-size = 14\n"), 0o644)
	if err := os.Symlink(target, filepath.Join(dir, "config")); err != nil {
		t.Fatal(err)
	}
	if err := SetAll(Change{"app-theme", "Nord"}, Change{"font-size", ""}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(filepath.Join(dir, "config")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("not a symlink any more: %v %v", st, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "app-theme = Nord\n" {
		t.Fatalf("target: %q", b)
	}
}

// The curated table has every theme, in the names' order.
func TestCuratedMatchesNames(t *testing.T) {
	if len(Curated) != len(AppThemes) {
		t.Fatalf("%d curated, %d names", len(Curated), len(AppThemes))
	}
	for i, c := range Curated {
		if c.Name != AppThemes[i] {
			t.Errorf("Curated[%d] = %q, AppThemes[%d] = %q", i, c.Name, i, AppThemes[i])
		}
	}
}

func TestCustomChanges(t *testing.T) {
	str := func(cs []Change) string {
		var b []string
		for _, c := range cs {
			b = append(b, c.Key+"="+c.Value)
		}
		return strings.Join(b, "; ")
	}
	for _, c := range []struct {
		values map[string]string
		want   string
	}{
		// Custom values already there: they're kept.
		{map[string]string{"app-theme": "Nord", "agent-color": "Rose"}, "app-theme=custom"},
		{map[string]string{"app-theme": "Nord", "sidebar-color": "#112233"}, "app-theme=custom"},
		// Seeded from the theme in use.
		{nil, "app-theme=custom; window-theme=Hammock; theme=; agent-color=Hammock; " +
			"active-pane-border-color=light:#1F7A57,dark:#3FB88A; sidebar-color=; toolbar-color="},
		{map[string]string{"app-theme": "native"}, "app-theme=custom; window-theme=; theme=; agent-color=; " +
			"active-pane-border-color=; sidebar-color=; toolbar-color="},
		{map[string]string{"app-theme": "Ocean"}, "app-theme=custom; window-theme=Ocean; theme=dark:Ocean Dark,light:Ocean Light; " +
			"agent-color=Ocean; active-pane-border-color=light:#2B6CC4,dark:#72A8F0; sidebar-color=; toolbar-color="},
		{map[string]string{"app-theme": "Solarized", "theme": ""}, "app-theme=custom; window-theme=Match Terminal; " +
			"theme=dark:iTerm2 Solarized Dark,light:iTerm2 Solarized Light; agent-color=Solarized; " +
			"active-pane-border-color=#268BD2; sidebar-color=; toolbar-color="},
	} {
		if got := str(CustomChanges(c.values)); got != c.want {
			t.Errorf("CustomChanges(%v):\n got %s\nwant %s", c.values, got, c.want)
		}
	}
}

// The editor-* keys: defaults when unset, and what can't be read ignored.
func TestEditorSettings(t *testing.T) {
	if got, want := EditorSettings(nil), (Editor{LineNumbers: true, TabWidth: 4, Indent: "spaces"}); got != want {
		t.Fatalf("defaults: %+v", got)
	}
	got := EditorSettings(map[string]string{
		"editor-wrap": "true", "editor-line-numbers": "false", "editor-tab-width": "2", "editor-indent": "tabs",
		"editor-font-size": "13.5", "editor-vim": "true", "editor-tui-external": "true",
	})
	if want := (Editor{Wrap: true, TabWidth: 2, Indent: "tabs", FontSize: 13.5, Vim: true, TUIExternal: true}); got != want {
		t.Fatalf("set: %+v", got)
	}
	got = EditorSettings(map[string]string{"editor-tab-width": "0", "editor-indent": "both", "editor-font-size": "", "editor-wrap": "yes"})
	if want := (Editor{LineNumbers: true, TabWidth: 4, Indent: "spaces"}); got != want {
		t.Fatalf("unreadable: %+v", got)
	}
	for _, k := range []string{"editor-wrap", "editor-line-numbers", "editor-tab-width", "editor-indent", "editor-font-size", "editor-vim", "editor-tui-external"} {
		if !strings.Contains(header, k) {
			t.Errorf("the header doesn't list %s", k)
		}
	}
}

func TestPaneKinds(t *testing.T) {
	if off := PaneKindsOff(nil); off != nil {
		t.Fatalf("every kind is on by default: %v off", off)
	}
	values := map[string]string{"diff-enabled": "false", "browser-enabled": "true", "log-enabled": "no"}
	if off := PaneKindsOff(values); len(off) != 1 || off[0] != "diff" {
		t.Fatalf("off: %v", off)
	}
	if !PaneKindOn(values, "log") {
		t.Fatal("only false turns a kind off")
	}
	for _, k := range PaneKinds {
		if !strings.Contains(header, PaneKindKey(k)) {
			t.Errorf("the header doesn't list %s", PaneKindKey(k))
		}
	}
}
