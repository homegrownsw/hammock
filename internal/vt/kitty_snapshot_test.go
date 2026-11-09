//go:build ghostty

package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"go.mitchellh.com/libghostty"
)

// A client fed a snapshot sees the Kitty images that were on screen, where
// they were.
func TestSnapshotKeepsKittyImages(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for i := range src.Pix {
		src.Pix[i] = 200
	}
	src.Set(3, 4, color.NRGBA{255, 0, 0, 255})
	var p bytes.Buffer
	if err := png.Encode(&p, src); err != nil {
		t.Fatal(err)
	}
	m := New(40, 12).(*ghosttyMirror)
	defer m.Close()
	m.SetCellSize(10, 20)
	m.Write([]byte("line one\r\nline two\r\n  "))
	m.Write([]byte(fmt.Sprintf("\x1b_Ga=T,f=100,i=7,q=2;%s\x1b\\", base64.StdEncoding.EncodeToString(p.Bytes()))))
	m.Write([]byte("\r\nafter"))

	before := placements(t, m)
	if len(before) != 1 {
		t.Fatalf("source placements %v", before)
	}
	m2 := New(40, 12).(*ghosttyMirror)
	defer m2.Close()
	m2.SetCellSize(10, 20)
	m2.Write(m.Snapshot())
	after := placements(t, m2)
	if len(after) != 1 || after[0] != before[0] {
		t.Fatalf("placements before %v after %v", before, after)
	}
	if x, _ := m2.t.CursorX(); x != 5 {
		t.Fatalf("cursor x %d, want 5 (after \"after\")", x)
	}
	img := kgImage(t, m2, 7)
	if info, err := img.Info(); err != nil || info.Width != 20 || info.Height != 10 {
		t.Fatalf("image %+v %v", info, err)
	}
}

type spot struct {
	id       uint32
	col, row int32
}

func placements(t *testing.T, m *ghosttyMirror) []spot {
	t.Helper()
	kg, err := m.t.KittyGraphics()
	if err != nil {
		t.Fatal(err)
	}
	it, err := libghostty.NewKittyGraphicsPlacementIterator()
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	if err := kg.PlacementIterator(it); err != nil {
		t.Fatal(err)
	}
	var out []spot
	for it.Next() {
		id, _ := it.ImageID()
		col, row, err := it.ViewportPos(kg.Image(id), m.t)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, spot{id, col, row})
	}
	return out
}

func kgImage(t *testing.T, m *ghosttyMirror, id uint32) *libghostty.KittyGraphicsImage {
	t.Helper()
	kg, err := m.t.KittyGraphics()
	if err != nil {
		t.Fatal(err)
	}
	img := kg.Image(id)
	if img == nil {
		t.Fatalf("no image %d", id)
	}
	return img
}

// image-file-medium = false: images sent as a file path (t=f) aren't read.
func TestKittyFileMediumSwitch(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	var p bytes.Buffer
	if err := png.Encode(&p, src); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/shot.png"
	if err := os.WriteFile(path, p.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	old := ImageFileMedium
	t.Cleanup(func() { ImageFileMedium = old })
	for _, on := range []bool{true, false} {
		ImageFileMedium = func() bool { return on }
		m := New(40, 12).(*ghosttyMirror)
		m.SetCellSize(10, 20)
		m.Write([]byte(fmt.Sprintf("\x1b_Ga=T,t=f,f=100,i=9,q=2;%s\x1b\\", base64.StdEncoding.EncodeToString([]byte(path)))))
		if got := len(placements(t, m)) == 1; got != on {
			t.Errorf("file medium %v: placed %v", on, got)
		}
		m.Close()
	}
}
