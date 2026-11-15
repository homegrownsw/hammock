// Package emulator finds Android Virtual Devices and the Android Emulators
// running them, boots them, and talks to an emulator's gRPC controller
// (emupb, vendored from the SDK): its screen's frames, touches, keys,
// rotation and fold posture. Simulator panes show emulators through it
// (internal/daemon/emulator.go).
//
// An emulator is reached only with a token: Hammock boots AVDs with
// -grpc-use-token, and attaches to running emulators started that way. One
// Android Studio runs uses signed JWTs instead, whose allowlist admits only
// Studio's own issuer: Hammock doesn't pose as Studio, so those are listed as
// running elsewhere, to be taken over (stopped and booted again) if you ask.
package emulator

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Prefix marks an AVD where a simulator's UDID would be (a pane's
// device_udid, sim_open's device): "avd:Pixel_9".
const Prefix = "avd:"

// IsAVD reports whether a device id names an AVD.
func IsAVD(id string) bool { return strings.HasPrefix(id, Prefix) }

// AVDName is the AVD a device id names ("avd:Pixel_9" → "Pixel_9").
func AVDName(id string) string { return strings.TrimPrefix(id, Prefix) }

// SDKDir finds the Android SDK: configured (the android-sdk key), else
// $ANDROID_HOME or $ANDROID_SDK_ROOT, else where Android Studio puts it.
// Empty if there's none with an emulator in it.
func SDKDir(configured string) string {
	home, _ := os.UserHomeDir()
	if rest, ok := strings.CutPrefix(configured, "~/"); ok {
		configured = filepath.Join(home, rest)
	}
	studio := filepath.Join(home, "Library", "Android", "sdk")
	if runtime.GOOS == "linux" {
		studio = filepath.Join(home, "Android", "Sdk")
	}
	for _, d := range []string{configured, os.Getenv("ANDROID_HOME"), os.Getenv("ANDROID_SDK_ROOT"), studio} {
		if d != "" && fileExists(filepath.Join(d, "emulator", "emulator")) {
			return d
		}
	}
	return ""
}

// Platform names, as simulator panes have them.
const (
	PlatformAndroid   = "Android"
	PlatformAndroidTV = "Android TV"
)

// AVD is an Android Virtual Device, from its config.ini.
type AVD struct {
	Name     string // its id: what -avd and Prefix take
	Display  string // what Device Manager calls it
	API      string // "android-35"
	TV       bool   // an Android TV or Google TV image
	Foldable bool   // a hinge
	Width    int    // the screen, in pixels
	Height   int
	Density  int // dpi (160 is one pixel per dp)
}

// Platform is Android or Android TV.
func (a AVD) Platform() string {
	if a.TV {
		return PlatformAndroidTV
	}
	return PlatformAndroid
}

// AVDDir is where AVDs live: $ANDROID_AVD_HOME, else ~/.android/avd.
func AVDDir() string {
	if d := os.Getenv("ANDROID_AVD_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".android", "avd")
}

// ListAVDs reads the AVDs in dir (AVDDir), by name.
func ListAVDs(dir string) []AVD {
	inis, _ := filepath.Glob(filepath.Join(dir, "*.ini"))
	var out []AVD
	for _, ini := range inis {
		name := strings.TrimSuffix(filepath.Base(ini), ".ini")
		top := readINI(ini)
		path := top["path"]
		if path == "" || !fileExists(filepath.Join(path, "config.ini")) {
			path = filepath.Join(dir, name+".avd") // moved: path.rel's place
		}
		c := readINI(filepath.Join(path, "config.ini"))
		if len(c) == 0 {
			continue
		}
		tag := c["tag.id"]
		out = append(out, AVD{
			Name:     name,
			Display:  cmp.Or(c["avd.ini.displayname"], strings.ReplaceAll(name, "_", " ")),
			API:      top["target"],
			TV:       tag == "android-tv" || tag == "google-tv" || strings.Contains(c["image.sysdir.1"], "-tv/"),
			Foldable: c["hw.sensor.hinge"] == "yes",
			Width:    atoi(c["hw.lcd.width"]),
			Height:   atoi(c["hw.lcd.height"]),
			Density:  atoi(c["hw.lcd.density"]),
		})
	}
	slices.SortFunc(out, func(a, b AVD) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Running is an emulator that's running, from its discovery file.
type Running struct {
	AVD      string // the AVD's id
	Pid      int
	GRPCPort int
	Token    string // its gRPC token; empty when it uses JWTs (Studio's) or none
	JWT      bool   // signed tokens only: Android Studio's
	Serial   int    // the console port: adb knows it as emulator-<serial>
}

// Reachable reports whether Hammock may drive it: by a token of its own.
func (r Running) Reachable() bool { return r.GRPCPort > 0 && r.Token != "" }

// ADBSerial is how adb names it.
func (r Running) ADBSerial() string { return fmt.Sprintf("emulator-%d", r.Serial) }

// DiscoveryDir is where running emulators write their pid_N.ini.
func DiscoveryDir() string {
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Caches", "TemporaryItems", "avd", "running")
	}
	return filepath.Join(cmp.Or(os.Getenv("XDG_RUNTIME_DIR"), os.TempDir()), "avd", "running")
}

// ListRunning reads the emulators running, from dir (DiscoveryDir). A file
// whose process has gone is skipped.
func ListRunning(dir string) []Running {
	files, _ := filepath.Glob(filepath.Join(dir, "pid_*.ini"))
	var out []Running
	for _, f := range files {
		pid := atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "pid_"), ".ini"))
		if pid <= 0 || !alive(pid) {
			continue
		}
		c := readINI(f)
		out = append(out, Running{
			AVD: c["avd.id"], Pid: pid, GRPCPort: atoi(c["grpc.port"]), Token: c["grpc.token"],
			JWT: c["grpc.jwks"] != "", Serial: atoi(c["port.serial"]),
		})
	}
	return out
}

// RunningAVD finds the emulator running an AVD, if any.
func RunningAVD(dir, avd string) (Running, bool) {
	for _, r := range ListRunning(dir) {
		if r.AVD == avd {
			return r, true
		}
	}
	return Running{}, false
}

// Boot starts an emulator for an AVD with no window and its gRPC controller
// on a free port, behind a token (in its discovery file). It runs in its own
// session, so it outlives the daemon, as simulators do; log is its output.
func Boot(sdk, avd string, log *os.File) (*exec.Cmd, error) {
	if sdk == "" {
		return nil, errors.New("no Android SDK found (install Android Studio, or set android-sdk in the config file)")
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(filepath.Join(sdk, "emulator", "emulator"), "-avd", avd, "-no-window", "-no-boot-anim",
		"-grpc", strconv.Itoa(port), "-grpc-use-token")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "ANDROID_SDK_ROOT="+sdk)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go cmd.Wait()
	return cmd, nil
}

// WaitRunning waits for an AVD's emulator to write its discovery file with a
// gRPC port and token.
func WaitRunning(dir, avd string, timeout time.Duration) (Running, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if r, ok := RunningAVD(dir, avd); ok && r.Reachable() {
			return r, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return Running{}, fmt.Errorf("the emulator for %s didn't start in time", avd)
}

// Stop ends an emulator: adb's emu kill shuts it down cleanly, but one that's
// only just booted can ignore it, so SIGTERM if it's still there after a
// while (or adb can't reach it).
func Stop(sdk string, r Running) {
	_ = exec.Command(ADB(sdk), "-s", r.ADBSerial(), "emu", "kill").Run()
	if r.Pid <= 0 {
		return
	}
	for i := 0; i < 40 && alive(r.Pid); i++ {
		time.Sleep(250 * time.Millisecond)
	}
	if alive(r.Pid) {
		_ = syscall.Kill(r.Pid, syscall.SIGTERM)
	}
}

// ADB is the SDK's adb, else one on PATH.
func ADB(sdk string) string {
	if p := filepath.Join(sdk, "platform-tools", "adb"); sdk != "" && fileExists(p) {
		return p
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p
	}
	return "adb"
}

// alive reports whether a process exists (a stale discovery file outlives a crash).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// readINI reads key=value lines (an AVD's config, a discovery file).
func readINI(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
