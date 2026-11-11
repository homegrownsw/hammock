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

// Images sent as a file path (t=f): the mirror never opens the path itself;
// the daemon inlines the image (KittyFiles) and the mirror places that.
func TestKittyFileImages(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	var p bytes.Buffer
	if err := png.Encode(&p, src); err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/shot.png"
	if err := os.WriteFile(path, p.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := []byte(fmt.Sprintf("\x1b_Ga=T,t=f,f=100,i=9,q=2;%s\x1b\\", base64.StdEncoding.EncodeToString([]byte(path))))
	for _, inline := range []bool{true, false} {
		m := New(40, 12).(*ghosttyMirror)
		m.SetCellSize(10, 20)
		if inline {
			var k KittyFiles
			m.Write(k.Rewrite(cmd))
		} else {
			m.Write(cmd)
		}
		if got := len(placements(t, m)) == 1; got != inline {
			t.Errorf("inlined %v: placed %v", inline, got)
		}
		m.Close()
	}
}

// Images shown with Unicode placeholders (a virtual placement and
// placeholder characters, as Claude Code shows them) come with the
// snapshot too: a client that receives the screen again (opening the pane
// on the phone, a resize, catching up) otherwise had placeholders pointing
// at nothing until the program drew the image again.
func TestSnapshotKeepsPlaceholderImages(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for i := range src.Pix {
		src.Pix[i] = 150
	}
	var p bytes.Buffer
	if err := png.Encode(&p, src); err != nil {
		t.Fatal(err)
	}
	const id = 10350049 // an RGB colour: 157;237;225
	cells := "\x1b[38;2;157;237;225m\U0010EEEE̅̅\U0010EEEE̅̍\x1b[39m"
	transmitCmd := fmt.Sprintf("\x1b_Ga=T,U=1,i=%d,f=100,c=2,r=1,q=2;%s\x1b\\", id, base64.StdEncoding.EncodeToString(p.Bytes()))

	m := New(40, 12).(*ghosttyMirror)
	defer m.Close()
	m.SetCellSize(10, 20)
	m.Write([]byte(transmitCmd + "image: " + cells + "\r\n"))
	snap := m.Snapshot()
	if !bytes.Contains(snap, []byte(fmt.Sprintf("a=p,U=1,i=%d", id))) || !bytes.Contains(snap, []byte(fmt.Sprintf("a=t,f=100,i=%d", id))) {
		t.Fatalf("the snapshot doesn't bring the placeholder image: %q", snap[max(0, len(snap)-300):])
	}
	c := New(40, 12).(*ghosttyMirror)
	defer c.Close()
	c.SetCellSize(10, 20)
	c.Write(snap)
	kg, err := c.t.KittyGraphics()
	if err != nil || kg.Image(id) == nil {
		t.Fatal("a client fed the snapshot has no image")
	}

	// An image nothing on screen refers to stays out.
	o := New(40, 12).(*ghosttyMirror)
	defer o.Close()
	o.SetCellSize(10, 20)
	o.Write([]byte(transmitCmd + "no placeholders\r\n"))
	if bytes.Contains(o.Snapshot(), []byte("a=p,U=1")) {
		t.Fatal("sent an image no placeholder shows")
	}
}

func TestSGRForeground(t *testing.T) {
	for _, c := range []struct {
		params string
		was    uint32
		want   uint32
	}{
		{"38;2;157;237;225", 0, 10350049},
		{"38:2::157:237:225", 0, 10350049},
		{"38;5;42", 0, 42},
		{"1;38;5;42;48;2;0;0;0", 0, 42}, // a black background doesn't reset it
		{"48;2;0;0;0", 42, 42},
		{"0", 42, 0},
		{"39", 42, 0},
		{"1", 42, 42},
	} {
		if got := sgrForeground(c.params, c.was); got != c.want {
			t.Errorf("%q from %d: %d, want %d", c.params, c.was, got, c.want)
		}
	}
}
