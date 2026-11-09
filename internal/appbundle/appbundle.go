// Package appbundle reads the Info.plist of the Mac app a binary is bundled
// in (hmkd, hmk and the helpers are, in Contents/MacOS or Contents/Helpers):
// which state dir, launchd label and remote port that copy of Hammock uses.
package appbundle

import (
	"os"
	"path/filepath"
	"regexp"
)

// Info is a string key from the enclosing app's Info.plist
// (Contents/MacOS/x or Contents/Helpers/x -> Contents/Info.plist), or ""
// outside an app.
func Info(key string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "..", "Info.plist"))
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]+)</string>`)
	if m := re.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}
