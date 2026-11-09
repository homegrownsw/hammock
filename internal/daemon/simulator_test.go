package daemon

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

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
