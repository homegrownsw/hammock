package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/emulator"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
)

// Apps, links and logs on an Android emulator in a simulator pane: through
// adb, as the iOS ones go through simctl.

// adbFor finds adb and the serial of the emulator running an AVD.
func adbFor(udid string) (adb, serial string, err error) {
	r, ok := emulator.RunningAVD(emulator.DiscoveryDir(), emulator.AVDName(udid))
	if !ok {
		return "", "", errors.New("its emulator isn't running yet")
	}
	v, _ := hammockconfig.Value("android-sdk")
	return emulator.ADB(emulator.SDKDir(v)), r.ADBSerial(), nil
}

func adbRun(ctx context.Context, udid string, args ...string) (string, error) {
	adb, serial, err := adbFor(udid)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, adb, append([]string{"-s", serial}, args...)...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return "", fmt.Errorf("adb %s: %v: %s", args[0], err, s)
	}
	return s, nil
}

// androidLaunch (re)starts an app by its package, as its launcher icon would.
func androidLaunch(ctx context.Context, udid, pkg string) (string, error) {
	if _, err := adbRun(ctx, udid, "shell", "am", "force-stop", pkg); err != nil {
		return "", err
	}
	out, err := adbRun(ctx, udid, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	if err != nil {
		return "", err
	}
	if strings.Contains(out, "No activities found") {
		return "", fmt.Errorf("%s has no launcher activity (is it installed? sim_install_app takes an .apk)", pkg)
	}
	time.Sleep(time.Second)
	pid, _ := adbRun(ctx, udid, "shell", "pidof", pkg)
	return fmt.Sprintf("%s: %s", pkg, pid), nil
}

// androidLogs is logcat since a while ago (last: 30s, 5m), with a filter
// spec ("MyApp:D *:S"), compactly.
func androidLogs(ctx context.Context, udid, last, filter string) (string, error) {
	d, err := time.ParseDuration(last)
	if err != nil {
		return "", fmt.Errorf("last: %q isn't a duration such as 30s or 5m", last)
	}
	args := []string{"logcat", "-d", "-v", "time", "-T", time.Now().Add(-d).Format("01-02 15:04:05.000")}
	args = append(args, strings.Fields(filter)...)
	return adbRun(ctx, udid, args...)
}
