package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDownscalePNG(t *testing.T) {
	// 4x2: left half red, right half blue, top-right pixel transparent.
	src := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			c := color.NRGBA{255, 0, 0, 255}
			if x >= 2 {
				c = color.NRGBA{0, 0, 255, 255}
			}
			src.SetNRGBA(x, y, c)
		}
	}
	src.SetNRGBA(3, 0, color.NRGBA{})
	var in bytes.Buffer
	if err := png.Encode(&in, src); err != nil {
		t.Fatal(err)
	}
	out, err := downscalePNG(in.Bytes(), 2)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 2 || b.Dy() != 1 {
		t.Fatalf("size %v, want 2x1", b)
	}
	if got := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA); got != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("left = %v, want opaque red", got)
	}
	// Three opaque blue pixels and one transparent: still pure blue, 3/4 alpha.
	if got := color.NRGBAModel.Convert(img.At(1, 0)).(color.NRGBA); got.B != 255 || got.R != 0 || got.A < 190 || got.A > 192 {
		t.Errorf("right = %v, want blue at ~75%% alpha", got)
	}

	// Factor 1 (or less) leaves it alone.
	if same, _ := downscalePNG(in.Bytes(), 1); !bytes.Equal(same, in.Bytes()) {
		t.Error("factor 1 changed the image")
	}
}
