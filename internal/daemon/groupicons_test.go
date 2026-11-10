package daemon

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// testPNG is a w×h picture, solid red inside a transparent margin.
func testPNG(t *testing.T, w, h, margin int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := margin; y < h-margin; y++ {
		for x := margin; x < w-margin; x++ {
			img.SetNRGBA(x, y, color.NRGBA{200, 30, 30, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// A Group's picture: set from a client (kept with Hammock's data, never in the
// project), served small without its transparent margin, else found in a
// .hammock/icon.png in its folders (not through a link), and gone with it.
func TestGroupPicture(t *testing.T) {
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Pics"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, Cwd: work}}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tid].FocusedPaneId]
	d.groups[gid].LastFocusedPaneId = p.id
	d.mu.Unlock()
	p.mu.Lock()
	p.info.Cwd = work
	p.mu.Unlock()

	hash := func() string {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.groups[gid].IconImage
	}
	set := func(img []byte) {
		t.Helper()
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIconImage{SetGroupIconImage: &pb.SetGroupIconImage{GroupId: gid, Image: img}}})
	}
	served := func() image.Image {
		t.Helper()
		ic := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadGroupIcon{ReadGroupIcon: &pb.ReadGroupIcon{GroupId: gid}}}).GetGroupIcon()
		img, err := png.Decode(bytes.NewReader(ic.GetPng()))
		if err != nil || ic.Hash != hash() {
			t.Fatalf("served: %v %q", err, ic.GetHash())
		}
		return img
	}

	// Not an image: refused.
	if _, err := c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIconImage{SetGroupIconImage: &pb.SetGroupIconImage{GroupId: gid, Image: []byte("hello")}}}); err == nil {
		t.Fatal("set a picture from text")
	}
	set(testPNG(t, 600, 300, 0))
	kept, err := os.ReadFile(d.storedPicture(gid))
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := png.DecodeConfig(bytes.NewReader(kept)); cfg.Width != iconWritten {
		t.Fatalf("kept %dx%d", cfg.Width, cfg.Height)
	}
	if _, err := os.Stat(filepath.Join(work, ".hammock")); err == nil {
		t.Fatal("wrote into the project")
	}
	img := served()
	// Wide: letterboxed, transparent above and solid in the middle.
	if img.Bounds().Dx() != iconServed {
		t.Fatalf("served %v", img.Bounds())
	}
	if _, _, _, a := img.At(64, 2).RGBA(); a != 0 {
		t.Error("top edge isn't transparent")
	}
	if r, _, _, a := img.At(64, 64).RGBA(); a == 0 || r>>8 < 190 {
		t.Error("middle isn't the picture")
	}

	// An app icon's margin is trimmed: it fills the square.
	set(testPNG(t, 400, 400, 60))
	img = served()
	if _, _, _, a := img.At(1, 1).RGBA(); img.Bounds().Dx() != iconServed || a == 0 {
		t.Errorf("margin kept: %v, corner alpha %d", img.Bounds(), a)
	}

	// Forgotten: nothing in the folder, so none.
	set(nil)
	if hash() != "" || fileIsRegular(d.storedPicture(gid)) {
		t.Fatal("not removed")
	}

	// A project's own .hammock/icon.png shows when none is set in Hammock, from a
	// folder inside it too; never through a link.
	os.MkdirAll(filepath.Join(work, ".hammock"), 0o755)
	secret := filepath.Join(t.TempDir(), "elsewhere.png")
	os.WriteFile(secret, testPNG(t, 10, 10, 0), 0o644)
	os.Symlink(secret, filepath.Join(work, ".hammock", "icon.png"))
	d.scanGroupIcons()
	if hash() != "" {
		t.Fatal("followed a link")
	}
	os.Remove(filepath.Join(work, ".hammock", "icon.png"))
	os.WriteFile(filepath.Join(work, ".hammock", "icon.jpg"), nil, 0o644) // unreadable: no picture, no crash
	d.scanGroupIcons()
	if hash() != "" {
		t.Fatal("an empty file made a picture")
	}
	os.Remove(filepath.Join(work, ".hammock", "icon.jpg"))
	os.WriteFile(filepath.Join(work, ".hammock", "icon.png"), testPNG(t, 40, 40, 0), 0o644)
	sub := filepath.Join(work, "app", "src")
	os.MkdirAll(sub, 0o755)
	p.mu.Lock()
	p.info.Cwd = sub
	p.mu.Unlock()
	d.scanGroupIcons()
	folderHash := hash()
	if folderHash == "" {
		t.Fatal("didn't find the project's picture from a folder inside it")
	}
	// One set in Hammock wins over it.
	set(testPNG(t, 50, 50, 0))
	if hash() == "" || hash() == folderHash {
		t.Fatal("the project's picture won")
	}

	// Closing the Group forgets its picture.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: gid}}})
	if fileIsRegular(d.storedPicture(gid)) {
		t.Fatal("kept a closed Group's picture")
	}
}
