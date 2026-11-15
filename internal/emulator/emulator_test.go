package emulator

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListAVDs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Pixel_9.ini"), "path="+filepath.Join(dir, "Pixel_9.avd")+"\ntarget=android-35\n")
	write(t, filepath.Join(dir, "Pixel_9.avd", "config.ini"),
		"avd.ini.displayname=Pixel 9\ntag.id=google_apis_playstore\nhw.lcd.width=1080\nhw.lcd.height=2424\nhw.lcd.density=420\n")
	write(t, filepath.Join(dir, "Fold.ini"), "path=/nowhere/Fold.avd\ntarget=android-35\n") // moved: found beside its .ini
	write(t, filepath.Join(dir, "Fold.avd", "config.ini"), "hw.sensor.hinge=yes\nhw.lcd.width=2208\nhw.lcd.height=1840\n")
	write(t, filepath.Join(dir, "TV.ini"), "path="+filepath.Join(dir, "TV.avd")+"\n")
	write(t, filepath.Join(dir, "TV.avd", "config.ini"), "tag.id=android-tv\nimage.sysdir.1=system-images/android-34/android-tv/arm64-v8a/\n")
	write(t, filepath.Join(dir, "Broken.ini"), "path=/nowhere\n") // no config: skipped

	avds := ListAVDs(dir)
	if len(avds) != 3 {
		t.Fatalf("got %d AVDs: %+v", len(avds), avds)
	}
	fold, pixel, tv := avds[0], avds[1], avds[2]
	if !fold.Foldable || fold.Display != "Fold" || fold.Platform() != PlatformAndroid {
		t.Errorf("fold: %+v", fold)
	}
	if pixel.Display != "Pixel 9" || pixel.API != "android-35" || pixel.Width != 1080 || pixel.Density != 420 || pixel.TV {
		t.Errorf("pixel: %+v", pixel)
	}
	if !tv.TV || tv.Platform() != PlatformAndroidTV {
		t.Errorf("tv: %+v", tv)
	}
}

func TestListRunning(t *testing.T) {
	dir := t.TempDir()
	me := os.Getpid()
	write(t, filepath.Join(dir, fmt.Sprintf("pid_%d.ini", me)),
		"avd.id=Pixel_9\ngrpc.port=8554\ngrpc.token=abc\nport.serial=5554\n")
	write(t, filepath.Join(dir, "pid_999999.ini"), "avd.id=Gone\ngrpc.port=8556\n") // its process has gone
	rs := ListRunning(dir)
	if len(rs) != 1 {
		t.Fatalf("got %+v", rs)
	}
	r := rs[0]
	if r.AVD != "Pixel_9" || !r.Reachable() || r.ADBSerial() != "emulator-5554" || r.JWT {
		t.Errorf("%+v", r)
	}
	// Studio's: signed tokens, no token of its own.
	write(t, filepath.Join(dir, fmt.Sprintf("pid_%d.ini", me)), "avd.id=Pixel_9\ngrpc.port=8554\ngrpc.jwks=/x/jwks\n")
	if r, ok := RunningAVD(dir, "Pixel_9"); !ok || r.Reachable() || !r.JWT {
		t.Errorf("studio's: %+v %v", r, ok)
	}
}

func TestDeviceIDs(t *testing.T) {
	if !IsAVD("avd:Pixel_9") || IsAVD("00000000-0000-4000-8000-000000000001") || AVDName("avd:Pixel_9") != "Pixel_9" {
		t.Fatal("avd ids")
	}
}

// TestLive boots an AVD and drives it: HMK_EMULATOR_TEST=<avd name> (it's
// shut down after).
func TestLive(t *testing.T) {
	avd := os.Getenv("HMK_EMULATOR_TEST")
	if avd == "" {
		t.Skip("set HMK_EMULATOR_TEST to an AVD's name")
	}
	sdk := SDKDir("")
	log, _ := os.Create(cmp.Or(os.Getenv("HMK_EMULATOR_LOG"), filepath.Join(t.TempDir(), "emu.log")))
	if _, err := Boot(sdk, avd, log); err != nil {
		t.Fatal(err)
	}
	r, err := WaitRunning(DiscoveryDir(), avd, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer Stop(sdk, r)
	c, err := Dial(r)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	for {
		st, err := c.Status(ctx)
		if err == nil && st.Booted {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("never booted: %v", err)
		}
		time.Sleep(time.Second)
	}
	fr, err := NewFrames(filepath.Join(t.TempDir(), "frames"), 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Close()
	got := make(chan [2]int, 1024)
	sctx, scancel := context.WithTimeout(ctx, 6*time.Second)
	defer scancel()
	streamed := make(chan error, 1)
	go func() {
		streamed <- c.StreamFrames(sctx, fr, func(w, h int, seq uint32) {
			select {
			case got <- [2]int{w, h}:
			default:
			}
		})
	}()
	time.Sleep(time.Second)
	for i := 0; i < 4; i++ {
		_ = c.Key(ctx, map[bool]string{true: "ArrowDown", false: "ArrowUp"}[i%2 == 0], KeyPress)
		time.Sleep(400 * time.Millisecond)
	}
	<-sctx.Done()
	t.Log("stream:", <-streamed)
	if len(got) < 2 {
		t.Fatalf("%d frames", len(got))
	}
	size := <-got
	t.Logf("%d frames, %dx%d", len(got)+1, size[0], size[1])
	// The clipboard both ways, and a tap (fingers down and up).
	if err := c.SetClipboard(ctx, "hammock-clip"); err != nil {
		t.Fatal(err)
	}
	if text, err := c.Clipboard(ctx); err != nil || text != "hammock-clip" {
		t.Fatalf("clipboard: %q %v", text, err)
	}
	if err := c.Touches(ctx, Touch{ID: 1, X: 100, Y: 100, Pressure: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.Touches(ctx, Touch{ID: 1, X: 100, Y: 100}); err != nil {
		t.Fatal(err)
	}
	if turn, err := c.Rotation(ctx); err != nil || turn != 0 {
		t.Fatalf("rotation: %v %v", turn, err)
	}
	if png, w, h, err := c.Screenshot(ctx); err != nil || len(png) == 0 {
		t.Fatalf("screenshot: %v", err)
	} else {
		t.Logf("screenshot %dx%d, %d bytes", w, h, len(png))
	}
}

func TestSDKDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	if d := SDKDir(""); d != "" && !strings.HasPrefix(d, home) {
		t.Fatalf("no SDK anywhere: got %q", d)
	}
	sdk := filepath.Join(home, "sdks", "android")
	write(t, filepath.Join(sdk, "emulator", "emulator"), "")
	if d := SDKDir("~/sdks/android"); d != sdk {
		t.Fatalf("configured with ~: got %q, want %q", d, sdk)
	}
	t.Setenv("ANDROID_HOME", sdk)
	if d := SDKDir(""); d != sdk {
		t.Fatalf("ANDROID_HOME: got %q", d)
	}
}
