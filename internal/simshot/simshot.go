// Package simshot names and places the simulator screenshots people save
// (from the TUI and `hmk sim shot`), as the Mac app does: where macOS puts
// screenshots, named as Simulator.app names them.
package simshot

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Folder is where screenshots go: the screencapture location set in
// Screenshot.app's Options, else the Desktop, else the home folder.
func Folder() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "com.apple.screencapture", "location").Output(); err == nil {
			p := strings.TrimSpace(string(out))
			if p == "~" || strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[1:])
			}
			if isDir(p) {
				return p
			}
		}
	}
	if d := filepath.Join(home, "Desktop"); isDir(d) {
		return d
	}
	return home
}

// FileName is "Simulator Screenshot - iPhone 17 Pro - 2026-11-04 at 10.15.32.png".
func FileName(device string, t time.Time) string {
	if device == "" {
		device = "Simulator"
	}
	device = strings.NewReplacer("/", "-", ":", "-").Replace(device)
	return fmt.Sprintf("Simulator Screenshot - %s - %s.png", device, t.Format("2006-01-02 at 15.04.05"))
}

// Save writes png into dir as name, or "name 2.png" and so on if that's
// taken, and returns its path.
func Save(dir, name string, png []byte) (string, error) {
	if len(png) == 0 {
		return "", errors.New("the simulator sent no picture")
	}
	base := strings.TrimSuffix(name, ".png")
	for n := 1; n < 1000; n++ {
		p := filepath.Join(dir, name)
		if n > 1 {
			p = filepath.Join(dir, fmt.Sprintf("%s %d.png", base, n))
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(png)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(p) // not half a picture
			return "", err
		}
		return p, nil
	}
	return "", errors.New("too many screenshots with that name")
}

// Copy puts a PNG on this Mac's clipboard.
func Copy(png []byte) error {
	if runtime.GOOS != "darwin" {
		return errors.New("copying a picture needs a Mac")
	}
	if len(png) == 0 {
		return errors.New("the simulator sent no picture")
	}
	f, err := os.CreateTemp("", "hmk-shot-*.png")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(png)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	script := "on run argv\n\tset the clipboard to (read (POSIX file (item 1 of argv)) as «class PNGf»)\nend run"
	if out, err := exec.Command("osascript", "-e", script, f.Name()).CombinedOutput(); err != nil {
		return fmt.Errorf("copying: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
