package hammockconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const swiftHeaderPath = "../../apple/Sources/HammockMac/ConfigHeader.generated.swift"

// The Mac app's header is generated from Keys: run `just config-header`.
func TestSwiftHeaderIsCurrent(t *testing.T) {
	b, err := os.ReadFile(swiftHeaderPath)
	if err != nil {
		t.Skip("no Mac app source here:", err)
	}
	if string(b) != SwiftHeaderFile() {
		t.Error("ConfigHeader.generated.swift is stale: run `just config-header`")
	}
}

func TestKeysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Keys {
		if seen[k.Name] {
			t.Errorf("%s is listed twice", k.Name)
		}
		seen[k.Name] = true
		if !slices.Contains(Groups, k.Group) {
			t.Errorf("%s: group %q isn't in Groups", k.Name, k.Group)
		}
		if k.Value == "" {
			t.Errorf("%s has no Value to show", k.Name)
		}
		if strings.Contains(k.Doc, "\n") {
			t.Errorf("%s: Doc is one line", k.Name)
		}
	}
	for _, g := range Groups {
		if !slices.ContainsFunc(Keys, func(k Key) bool { return k.Group == g }) {
			t.Errorf("group %s has no keys", g)
		}
	}
	for _, k := range PaneKinds {
		if KeyNamed(PaneKindKey(k)) == nil {
			t.Errorf("%s isn't in Keys", PaneKindKey(k))
		}
	}
}

// Every setting the apps read through Pref("…") is a key people can look up.
func TestEveryPrefIsAKey(t *testing.T) {
	root := filepath.Join("..", "..", "apple", "Sources")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no app source here:", err)
	}
	pref := regexp.MustCompile(`Pref\("([a-z0-9-]+)"`)
	// Read for old files: usage-live replaced claude-usage-live, and each
	// kind's notify-sound-* replaced notify-sound (AppConfig.migrateSoundSwitch).
	retired := map[string]bool{"claude-usage-live": true, "notify-sound": true}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".swift") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range pref.FindAllStringSubmatch(string(b), -1) {
			if KeyNamed(m[1]) == nil && !retired[m[1]] {
				t.Errorf("%s reads %s, which isn't in Keys (keys.go)", filepath.Base(path), m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
