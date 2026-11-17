package tui

import (
	"slices"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func simLabels(items []menuItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.label)
	}
	return out
}

func TestSimItemsByPlatform(t *testing.T) {
	a := &App{panes: map[string]*pb.Pane{}}
	sim := func(id, platform string) *pb.Pane {
		p := &pb.Pane{Id: id, Kind: pb.PaneKind_PANE_KIND_SIMULATOR, DevicePlatform: platform}
		a.panes[id] = p
		return p
	}
	sim("iphone", "iOS")
	sim("appletv", "tvOS")
	sim("androidtv", "Android TV")
	phone := sim("android", "Android")

	if l := simLabels(a.simItems("iphone")); !slices.Contains(l, "Lock") || !slices.Contains(l, "Toggle Software Keyboard") {
		t.Errorf("iPhone: %v", l)
	}
	if l := simLabels(a.simItems("appletv")); !slices.Contains(l, "Siri") || slices.Contains(l, "Lock") {
		t.Errorf("Apple TV: %v", l)
	}
	if l := simLabels(a.simItems("androidtv")); !slices.Contains(l, "Select") || slices.Contains(l, "Siri") || slices.Contains(l, "Lock") {
		t.Errorf("Android TV: %v", l)
	}
	l := simLabels(a.simItems("android"))
	for _, want := range []string{"Back", "Home", "Recents", "Power", "Rotate Left"} {
		if !slices.Contains(l, want) {
			t.Errorf("Android: missing %q in %v", want, l)
		}
	}
	if slices.Contains(l, "Lock") || slices.ContainsFunc(l, func(s string) bool { return s == "Start in Hammock" }) {
		t.Errorf("Android, running in Hammock: %v", l)
	}
	// Offered: started, or taken over from Android Studio.
	phone.DeviceElsewhere, phone.DeviceNote = true, "The emulator isn't running"
	if l := simLabels(a.simItems("android")); !slices.Contains(l, "Start in Hammock") {
		t.Errorf("stopped: %v", l)
	}
	phone.DeviceNote = "Running in Android Studio"
	if l := simLabels(a.simItems("android")); !slices.Contains(l, "Restart in Hammock (stops it in Android Studio)") {
		t.Errorf("in Studio: %v", l)
	}
	// Each copies the device's clipboard to the terminal's.
	for _, id := range []string{"iphone", "androidtv", "android"} {
		if !slices.Contains(simLabels(a.simItems(id)), "Copy Device Clipboard") {
			t.Errorf("%s: no Copy Device Clipboard", id)
		}
	}
	if slices.Contains(simLabels(a.simItems("appletv")), "Copy Device Clipboard") {
		t.Error("an Apple TV has no clipboard")
	}
	if !isTVPane(a.panes["androidtv"]) || !isTVPane(a.panes["appletv"]) || isTVPane(phone) {
		t.Error("isTVPane")
	}
}
