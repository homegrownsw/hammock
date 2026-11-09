package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Kimi Code's config.toml: Hammock's [[hooks]] go in a fenced block beside
// yours (herdr's, say), once; reinstalling for another hmk replaces the
// block where it is; uninstalling gives back your file byte for byte.
func TestKimiHookTarget(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".kimi-code")
	_ = os.MkdirAll(dir, 0o755)
	mine := `# my settings
default_model = "kimi-code/kimi-for-coding"

# >>> herdr kimi integration
[[hooks]]
event = "Stop"
command = "bash '/h/herdr-agent-state.sh' idle"
timeout = 10

# <<< herdr kimi integration

[providers."kimi-code"]
type = "kimi"
`
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}
	apply := func(exe string, uninstall bool) map[string]configEdit {
		edits, notes, err := kimiHookTarget.edits(home, exe, uninstall)
		if err != nil || len(notes) > 0 {
			t.Fatal(err, notes)
		}
		out := map[string]configEdit{}
		for _, e := range edits {
			out[filepath.Base(e.path)] = e
			if e.changed {
				if err := os.WriteFile(e.path, e.new, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		return out
	}
	exe := "/Applications/Hammock β.app/Contents/Helpers/hmk"
	e := apply(exe, false)
	if !e["config.toml"].changed || !e["mcp.json"].changed {
		t.Fatalf("edits: %v", e)
	}
	got := read("config.toml")
	if !strings.HasPrefix(got, mine+"\n"+kimiFenceOpen+"\n") || !strings.HasSuffix(got, kimiFenceClose+"\n") {
		t.Fatalf("not appended as a block:\n%s", got)
	}
	cmd := `command = "'/Applications/Hammock β.app/Contents/Helpers/hmk' hook kimi"`
	for _, ev := range kimiHookEvents {
		if !strings.Contains(got, "[[hooks]]\nevent = \""+ev+"\"\n"+cmd+"\ntimeout = 10\n") {
			t.Errorf("%s: no Hammock hook", ev)
		}
	}
	if n := strings.Count(got, "[[hooks]]"); n != len(kimiHookEvents)+1 {
		t.Errorf("%d tables", n)
	}
	var mcp struct {
		MCPServers map[string]struct {
			Command string
			Args    []string
		}
	}
	if err := json.Unmarshal([]byte(read("mcp.json")), &mcp); err != nil || mcp.MCPServers["hammock"].Command != exe || mcp.MCPServers["hammock"].Args[0] != "mcp" {
		t.Fatalf("mcp: %s", read("mcp.json"))
	}

	// Again: nothing to do.
	if e := apply(exe, false); e["config.toml"].changed || e["mcp.json"].changed {
		t.Fatal("not idempotent")
	}
	// Your own settings added after Hammock's block stay where they are.
	withMore := read("config.toml") + "\n[loop]\nmax_steps = 50\n"
	_ = os.WriteFile(path, []byte(withMore), 0o644)
	// Another copy of Hammock: the block, in place.
	apply("/opt/hmk", false)
	got = read("config.toml")
	if strings.Contains(got, "Hammock β") || !strings.Contains(got, `command = "/opt/hmk hook kimi"`) || !strings.HasSuffix(got, kimiFenceClose+"\n\n[loop]\nmax_steps = 50\n") {
		t.Fatalf("reinstall:\n%s", got)
	}
	// A stray table of Hammock's (something rewrote the file without its
	// comments) goes too, on uninstall.
	stray := strings.Replace(got, "\n[loop]", "\n[[hooks]]\nevent = \"Stop\"\ncommand = \"/old/hmk hook kimi\"\n\n[loop]", 1)
	_ = os.WriteFile(path, []byte(stray), 0o644)
	apply("", true)
	if got := read("config.toml"); got != mine+"\n[loop]\nmax_steps = 50\n" {
		t.Fatalf("uninstall:\n%q\nwant\n%q", got, mine+"\n[loop]\nmax_steps = 50\n")
	}
	if strings.Contains(read("mcp.json"), "hammock") {
		t.Fatalf("mcp after uninstall: %s", read("mcp.json"))
	}

	// Installed onto a file that ends without a newline, and taken out.
	_ = os.WriteFile(path, []byte("default_model = \"k\"\n"), 0o644)
	apply(exe, false)
	apply("", true)
	if got := read("config.toml"); got != "default_model = \"k\"\n" {
		t.Fatalf("round trip: %q", got)
	}
}

// hooks = [ … ] (or a [hooks] table) leaves no room for [[hooks]]: Hammock
// says so rather than break the file.
func TestKimiHooksElsewhere(t *testing.T) {
	for doc, want := range map[string]bool{
		"hooks = []\n":     true,
		"[hooks]\nx = 1\n": true,
		"[[hooks]]\nevent = \"Stop\"\ncommand = \"x\"\n": false,
		"[loop]\nhooks = 1\n":                            false,
		"":                                               false,
	} {
		if got := kimiHooksElsewhere([]byte(doc)); got != want {
			t.Errorf("%q: %v", doc, got)
		}
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".kimi-code"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".kimi-code", "config.toml"), []byte("hooks = []\n"), 0o644)
	edits, notes, err := kimiHookTarget.edits(home, "/x/hmk", false)
	if err != nil || len(notes) != 1 || edits[0].changed {
		t.Fatalf("edits %v notes %v err %v", edits, notes, err)
	}
}

func TestKimiTOMLString(t *testing.T) {
	if got := kimiTOMLString(`'/a "b"\c' hook kimi`); got != `"'/a \"b\"\\c' hook kimi"` {
		t.Errorf("%s", got)
	}
	if got := kimiTOMLValue(kimiTOMLString(`x "y" \z`)); got != `x "y" \z` {
		t.Errorf("%s", got)
	}
	if got := kimiTOMLValue(`'/x/hmk hook kimi'`); got != "/x/hmk hook kimi" {
		t.Errorf("%s", got)
	}
}
