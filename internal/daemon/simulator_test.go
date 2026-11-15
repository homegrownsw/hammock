package daemon

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"slices"
	"testing"
)

func TestRemotePresses(t *testing.T) {
	for _, c := range []struct {
		args simArgs
		want []string
		bad  bool
	}{
		{args: simArgs{Name: "select"}, want: []string{"select"}},
		{args: simArgs{Name: "down", Repeat: 3}, want: []string{"down", "down", "down"}},
		{args: simArgs{Name: "home", HoldMs: 1500}, want: []string{"home"}},
		{args: simArgs{Swipe: "left"}, want: []string{"left", "left", "left"}},
		{args: simArgs{Swipe: "up", Distance: "short"}, want: []string{"up"}},
		{args: simArgs{Swipe: "right", Distance: "long"}, want: slices.Repeat([]string{"right"}, 6)},
		{args: simArgs{}, bad: true},                               // neither
		{args: simArgs{Name: "select", Swipe: "up"}, bad: true},    // both
		{args: simArgs{Name: "power"}, bad: true},                  // not on the remote
		{args: simArgs{Name: "up", Repeat: 51}, bad: true},         // too many
		{args: simArgs{Name: "up", HoldMs: -1}, bad: true},         // negative hold
		{args: simArgs{Swipe: "select"}, bad: true},                // not a direction
		{args: simArgs{Swipe: "up", Distance: "miles"}, bad: true}, // unknown distance
	} {
		got, err := remotePresses(c.args)
		if c.bad {
			if err == nil {
				t.Errorf("%+v: want an error, got %v", c.args, got)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%+v: got %v, %v; want %v", c.args, got, err, c.want)
		}
	}
}

func TestRemotePressesCapped(t *testing.T) {
	if _, err := remotePresses(simArgs{Name: "down", Repeat: 30, HoldMs: 1000}); err == nil {
		t.Fatal("30 presses held a second each (34.5 s): want an error past 30 s")
	}
	if _, err := remotePresses(simArgs{Name: "down", Repeat: 50}); err != nil {
		t.Fatalf("50 quick presses: %v", err)
	}
}

func TestSimulatorOpRefused(t *testing.T) {
	for _, c := range []struct {
		platform, op string
		refused      bool
	}{
		{"tvOS", "remote", false},
		{"tvOS", "screenshot", false},
		{"tvOS", "text", false}, // its text fields
		{"tvOS", "tap", true},
		{"tvOS", "touch", true},
		{"tvOS", "swipe", true},
		{"tvOS", "button", true}, // the iPhone's Home crashes its backboardd
		{"tvOS", "rotate", true},
		{"tvOS", "pinch", true},
		{"tvOS", "fold", true},
		{"iOS", "tap", false},
		{"iOS", "remote", true},
		{"", "remote", false}, // not known yet: the host decides
		{"", "tap", false},
		{"Android", "tap", false},
		{"Android", "pinch", false},
		{"Android", "button", false}, // back, home, recents…
		{"Android", "remote", true},
		{"Android", "takeover", false},
		{"Android TV", "remote", false},
		{"Android TV", "tap", true},
		{"Android TV", "button", true},
		{"Android TV", "takeover", false},
		{"iOS", "takeover", true},
		{"tvOS", "takeover", true},
	} {
		if err := simulatorOpRefused(c.platform, c.op); (err != nil) != c.refused {
			t.Errorf("%s %s: refused %v, want %v", c.platform, c.op, err, c.refused)
		}
	}
}

func TestSimulatorActionRemote(t *testing.T) {
	if a := simulatorAction("remote", simArgs{Name: "select", Phase: "down"}, [2]float64{}); a == nil || a.Kind != "key" || a.Label != "select" {
		t.Errorf("press: %+v", a)
	}
	if a := simulatorAction("remote", simArgs{Name: "select", Phase: "up"}, [2]float64{}); a != nil {
		t.Errorf("a let go shows nothing: %+v", a)
	}
	if a := simulatorAction("remote", simArgs{Swipe: "left"}, [2]float64{}); a == nil || a.Label != "left" {
		t.Errorf("swipe: %+v", a)
	}
}

func TestToJPEG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 80))
	for y := range 80 {
		for x := range 40 {
			src.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 3), 128, 255})
		}
	}
	var p bytes.Buffer
	if err := png.Encode(&p, src); err != nil {
		t.Fatal(err)
	}
	j, err := toJPEG(p.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(j))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 80 {
		t.Fatalf("size %v", b)
	}
}

// TestToScreen pins how a point as the screen shows maps to the emulator's
// natural orientation, as measured on a 674×842 dp foldable turned left:
// Chrome at (432, 600) in landscape was a touch at (74, 432).
func TestToScreen(t *testing.T) {
	for _, c := range []struct {
		x, y, w, h float64
		turn       float32
		wx, wy     int
	}{
		{10, 20, 674, 842, 0, 10, 20},
		{432, 600, 842, 674, 90, 74, 432},
		{10, 20, 674, 842, 180, 664, 822},
		{432, 600, 842, 674, 270, 600, 410},
	} {
		x, y := toScreen(c.x, c.y, c.w, c.h, c.turn, 1)
		if x != c.wx || y != c.wy {
			t.Errorf("%+v: got %d,%d", c, x, y)
		}
	}
	if x, y := toScreen(100, 200, 674, 842, 0, 2.625); x != 263 || y != 525 {
		t.Errorf("scaled: got %d,%d", x, y)
	}
}
