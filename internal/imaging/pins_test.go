package imaging

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
	"time"
)

func plain(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	return img
}

func TestAnnotate(t *testing.T) {
	white := color.NRGBA{255, 255, 255, 255}
	src := plain(400, 800, white)
	out := Annotate(src, []Pin{
		{Number: 1, X: 0.5, Y: 0.5, Rect: &Rect{X: 0.25, Y: 0.25, W: 0.5, H: 0.1}},
		{Number: 12, X: 0.99, Y: 0.01}, // in a corner: its badge stays in the picture
	})
	if out.Bounds() != src.Bounds() {
		t.Fatalf("size %v, want %v", out.Bounds(), src.Bounds())
	}
	if got := out.NRGBAAt(200, 400); got != pinFill {
		t.Errorf("the pin's point = %v, want %v", got, pinFill)
	}
	// The element's outline, at its left edge.
	if got := out.NRGBAAt(100, 220); got != pinFill {
		t.Errorf("the element's outline = %v, want %v", got, pinFill)
	}
	// Somewhere nothing was drawn.
	if got := out.NRGBAAt(20, 700); got != white {
		t.Errorf("untouched pixel = %v, want white", got)
	}
	// The source is left alone.
	if got := src.NRGBAAt(200, 400); got != white {
		t.Errorf("Annotate drew on its source")
	}
	// The corner pin's badge: pin-coloured pixels near the top right.
	found := false
	for y := 0; y < 40 && !found; y++ {
		for x := 360; x < 400; x++ {
			if out.NRGBAAt(x, y) == pinFill {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("the corner pin's badge isn't drawn in the picture")
	}
}

func TestSame(t *testing.T) {
	a := plain(300, 600, color.NRGBA{240, 240, 240, 255})
	b := plain(300, 600, color.NRGBA{240, 240, 240, 255})
	if !Same(a, b) {
		t.Error("identical pictures aren't the same")
	}
	// A caret's worth of change.
	draw.Draw(b, image.Rect(100, 100, 102, 120), &image.Uniform{color.NRGBA{0, 0, 0, 255}}, image.Point{}, draw.Src)
	if !Same(a, b) {
		t.Error("a blinking caret made them differ")
	}
	// The screen moved on: a third of it changed.
	draw.Draw(b, image.Rect(0, 0, 300, 200), &image.Uniform{color.NRGBA{20, 20, 60, 255}}, image.Point{}, draw.Src)
	if Same(a, b) {
		t.Error("a changed screen is the same")
	}
	if Same(a, plain(301, 600, color.NRGBA{240, 240, 240, 255})) {
		t.Error("pictures of different sizes are the same")
	}
}

// A page can say its element is any size: drawing it costs only the picture.
func TestAnnotateHugeRect(t *testing.T) {
	src := plain(300, 200, color.NRGBA{255, 255, 255, 255})
	start := time.Now()
	out := Annotate(src, []Pin{{Number: 1, X: 0.5, Y: 0.5, Rect: &Rect{X: -1e9, Y: -1e9, W: 2e9, H: 2e9}},
		{Number: 2, X: math.NaN(), Y: 0.5, Rect: &Rect{X: math.Inf(1), Y: 0, W: 1, H: 1}}})
	if time.Since(start) > time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
	if out.Bounds() != src.Bounds() {
		t.Fatalf("size %v", out.Bounds())
	}
}
