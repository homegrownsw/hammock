package acknowledgements

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const stale = "Acknowledgements.json is out of date: run `just acknowledgements`"

func TestEveryComponentHasItsLicence(t *testing.T) {
	cs := All()
	if len(cs) == 0 {
		t.Fatal("no components")
	}
	for _, c := range cs {
		if c.Name == "" || c.License == "" || strings.TrimSpace(c.Text) == "" || len(c.Apps) == 0 {
			t.Errorf("%q lacks a name, licence, text or apps", c.Name)
		}
		for _, a := range c.Apps {
			if a != "mac" && a != "ios" && a != "linux" {
				t.Errorf("%s: unknown app %q", c.Name, a)
			}
		}
	}
	for _, app := range []string{"mac", "ios", "linux"} {
		if len(For(app)) == 0 {
			t.Errorf("nothing in the %s app", app)
		}
	}
}

// versions is every package listed, name to version.
func versions() map[string]string {
	out := map[string]string{}
	for _, c := range All() {
		if c.Version != "" && len(c.Packages) == 0 {
			out[c.Name] = c.Version
		}
		for _, p := range c.Packages {
			out[p.Name] = p.Version
		}
	}
	return out
}

// The Go modules linked into hmk, hmkd and pane-holder, on macOS and Linux.
func TestGoModulesAreCurrent(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	have := versions()
	for _, goos := range []string{"darwin", "linux"} {
		cmd := exec.Command("go", "list", "-deps", "-tags", "ghostty",
			"-f", "{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}", "github.com/alexjsp/hammock/cmd/...")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=arm64", "CGO_ENABLED=1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list (%s): %v", goos, err)
		}
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			path, version, ok := strings.Cut(l, " ")
			if !ok {
				continue
			}
			if have[path] != version {
				t.Errorf("%s: %s %s is linked, listed as %q. %s", goos, path, version, have[path], stale)
			}
		}
	}
}

func TestSwiftPackagesAreCurrent(t *testing.T) {
	var resolved struct {
		Pins []struct {
			Identity string `json:"identity"`
			State    struct {
				Version string `json:"version"`
			} `json:"state"`
		} `json:"pins"`
	}
	b, err := os.ReadFile("../../apple/Package.resolved")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &resolved); err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, c := range All() {
		if strings.Contains(c.URL, "github.com/") {
			listed = append(listed, strings.ToLower(c.URL)+"@"+c.Version)
		}
	}
	for _, p := range resolved.Pins {
		if !slices.ContainsFunc(listed, func(s string) bool {
			return strings.Contains(s, "/"+strings.ToLower(p.Identity)+"@"+p.State.Version)
		}) {
			t.Errorf("Swift package %s %s isn't listed. %s", p.Identity, p.State.Version, stale)
		}
	}
}

// The editor's npm packages listed are the versions its lock file installs.
func TestEditorPackagesAreCurrent(t *testing.T) {
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	b, err := os.ReadFile("../../apple/Editor/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatal(err)
	}
	n := 0
	for name, version := range versions() {
		if l, ok := lock.Packages["node_modules/"+name]; ok {
			n++
			if l.Version != version {
				t.Errorf("%s is %s in package-lock.json, listed as %s. %s", name, l.Version, version, stale)
			}
		}
	}
	if n == 0 {
		t.Error("no npm packages listed")
	}
	// Every CodeMirror and Lezer package the editor asks for by name.
	pj, err := os.ReadFile("../../apple/Editor/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var deps struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(pj, &deps); err != nil {
		t.Fatal(err)
	}
	have := versions()
	for name := range deps.Dependencies {
		if name != "esbuild" && have[name] == "" {
			t.Errorf("the editor's dependency %s isn't listed. %s", name, stale)
		}
	}
}

// Ghostty's and CEF's pinned versions.
func TestPinnedVersionsAreCurrent(t *testing.T) {
	for file, name := range map[string]string{
		"../../third_party/ghostty/VERSION": "Ghostty (libghostty-vt)",
		"../../third_party/cef/VERSION":     "Chromium Embedded Framework and Chromium",
	} {
		b, err := os.ReadFile(filepath.FromSlash(file))
		if err != nil {
			t.Fatal(err)
		}
		pinned := ""
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				pinned = l
				break
			}
		}
		if v := versions()[name]; v != pinned {
			t.Errorf("%s is pinned at %s, listed as %q. %s", name, pinned, v, stale)
		}
	}
}

func TestFind(t *testing.T) {
	cs := All()
	if got := Find(cs, "codemirror"); len(got) == 0 || got[0].Name != "CodeMirror" {
		t.Errorf("Find(codemirror) = %v", names(got))
	}
	if got := Find(cs, "@lezer/json"); len(got) != 1 || got[0].Name != "Lezer" {
		t.Errorf("Find(@lezer/json) = %v", names(got))
	}
	if got := Find(cs, "no such thing"); got != nil {
		t.Errorf("Find(no such thing) = %v", names(got))
	}
}

func names(cs []Component) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}
