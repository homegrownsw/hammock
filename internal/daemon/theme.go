package daemon

// This Mac's theme, for the iPhone to follow (unless it picks its own):
// app-theme in the config file, which the Mac app's Settings ▸ Appearance
// and `hmk theme` write. A curated theme is only its name (the apps have
// its colours, AppTheme.swift); a custom one brings what the phone can't
// know: the terminal themes' names and files, the agent colour and the
// window theme (which the agent colour follows when it's unset).

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/hammockconfig"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// themeEvery is how often the config file is looked at for a change (the Mac
// app's own poll is 1.5 s; a phone following a few seconds later is fine).
const themeEvery = 3 * time.Second

// maxThemeFile caps a terminal theme file sent to the phone (Ghostty's are
// about 1 KB).
const maxThemeFile = 64 << 10

// watchTheme reads the theme every so often until the daemon stops,
// telling clients when it changes.
func (d *Daemon) watchTheme() {
	t := time.NewTicker(themeEvery)
	defer t.Stop()
	var r themeReader
	for {
		if th, changed := r.read(); changed {
			d.mu.Lock()
			if !proto.Equal(th, d.theme) {
				d.theme = th
				d.publish(&pb.StateUpdate{Theme: proto.CloneOf(th)})
			}
			d.mu.Unlock()
		}
		select {
		case <-d.done:
			return
		case <-t.C:
		}
	}
}

// themeReader reads the theme again only when a file it comes from might
// have changed: the config, and the terminal themes' files (where each
// was found, and the folders before it, where one could appear).
type themeReader struct {
	stamps []fileStamp // nil: not read yet
	theme  *pb.HostTheme
}

// fileStamp is a file's modification time and size (zero: none there).
type fileStamp struct {
	path    string
	modTime int64
	size    int64
}

func stamp(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{path: path}
	}
	return fileStamp{path, st.ModTime().UnixNano(), st.Size()}
}

// read is the theme, and whether it was read again (else it's the last).
func (r *themeReader) read() (*pb.HostTheme, bool) {
	if r.stamps != nil {
		same := true
		for _, s := range r.stamps {
			if stamp(s.path) != s {
				same = false
				break
			}
		}
		if same {
			return r.theme, false
		}
	}
	// Stamped before reading, so a change while reading is seen next time.
	paths := append([]string{hammockconfig.Path()}, r.themePaths()...)
	stamps := make([]fileStamp, len(paths))
	for i, p := range paths {
		stamps[i] = stamp(p)
	}
	r.theme = readHostTheme()
	if np := r.themePaths(); !slices.Equal(np, paths[1:]) {
		// Different terminal themes: stamp theirs (read just now).
		stamps = stamps[:1]
		for _, p := range np {
			stamps = append(stamps, stamp(p))
		}
	}
	r.stamps = stamps
	return r.theme, true
}

// themePaths are the files the last theme read could come from.
func (r *themeReader) themePaths() []string {
	if r.theme == nil {
		return nil
	}
	var out []string
	for _, name := range []string{r.theme.LightTerminal, r.theme.DarkTerminal} {
		if !themeFileName(name) {
			continue
		}
		for _, dir := range themeDirs() {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// readHostTheme is the theme the config file says.
func readHostTheme() *pb.HostTheme {
	values := hammockconfig.Values()
	th := &pb.HostTheme{Name: hammockconfig.AppTheme(values)}
	if th.Name != hammockconfig.CustomTheme {
		return th
	}
	th.LightTerminal, th.DarkTerminal = hammockconfig.TerminalThemes(values["theme"])
	th.LightTerminalFile = terminalThemeFile(th.LightTerminal)
	th.DarkTerminalFile = terminalThemeFile(th.DarkTerminal)
	th.AgentColor = strings.TrimSpace(values["agent-color"])
	th.WindowTheme = strings.TrimSpace(values["window-theme"])
	return th
}

// themeDirs are where terminal theme files are looked for, in the Mac
// app's order (AppConfig.themeDirs).
var themeDirs = func() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{filepath.Join(hammockconfig.Dir(), "themes")}
	// A β's config is elsewhere; your own themes still count.
	if own := filepath.Join(hammockconfig.DefaultDir(), "themes"); own != dirs[0] {
		dirs = append(dirs, own)
	}
	return append(dirs,
		filepath.Join(home, ".config", "ghostty", "themes"),
		"/Applications/Ghostty.app/Contents/Resources/ghostty/themes",
	)
}

// terminalThemeFile is a terminal theme's file text, "" for a built-in one
// or one not found (the phone knows the curated themes' terminals by name).
func terminalThemeFile(name string) string {
	if !themeFileName(name) {
		return ""
	}
	for _, dir := range themeDirs() {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() || st.Size() > maxThemeFile {
			continue
		}
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	return ""
}

// themeFileName is whether a terminal theme of that name could have a file:
// not a built-in one, and not a path.
func themeFileName(name string) bool {
	return name != "" && name != hammockconfig.BuiltInLight && name != hammockconfig.BuiltInDark &&
		!strings.ContainsAny(name, `/\`) && !strings.HasPrefix(name, ".")
}
