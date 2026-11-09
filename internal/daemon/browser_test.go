package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBrowserURL(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		"":                        "about:blank",
		"https://example.tv/x":    "https://example.tv/x",
		"example.com":             "https://example.com",
		"example.com/a b":         "https://www.google.com/search?q=example.com%2Fa+b",
		"localhost:3000":          "http://localhost:3000",
		"localhost:3000/api":      "http://localhost:3000/api",
		"127.0.0.1:8080":          "http://127.0.0.1:8080",
		"myapp.test":              "http://myapp.test",
		"about:blank":             "about:blank",
		"/tmp/index.html":         "file:///tmp/index.html",
		"~/a.html":                "file://" + filepath.Join(home, "a.html"),
		"how do terminals work":   "https://www.google.com/search?q=how+do+terminals+work",
		"hammock":                 "https://www.google.com/search?q=hammock",
		"  https://example.com  ": "https://example.com",
	} {
		if got := BrowserURL(in); got != want {
			t.Errorf("BrowserURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenNewTabsOutside(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("browser panes are the Mac's")
	}
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	for _, c := range []struct {
		url  string
		want bool
	}{
		{"https://example.com/a", true},
		{"http://localhost:3000/", true},
		{"about:blank", false},
		{"data:text/html,hi", false},
		{"blob:https://example.com/x", false},
		{"", false},
	} {
		if got := openNewTabsOutside(c.url); got != c.want {
			t.Errorf("default, %q: %v, want %v", c.url, got, c.want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("browser-new-tab = hammock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if openNewTabsOutside("https://example.com/") {
		t.Error("browser-new-tab = hammock still opens outside")
	}
}
