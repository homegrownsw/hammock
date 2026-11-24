package imaging

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
)

// Pin is a design note's pin, to draw on a screenshot: where it is, and
// the element under it, in fractions (0...1) of the picture.
type Pin struct {
	Number int
	X, Y   float64
	Rect   *Rect // nil: none
}

// Rect is a rectangle in fractions of the picture.
type Rect struct{ X, Y, W, H float64 }

var (
	pinFill = color.NRGBA{0xF0, 0x3E, 0x5A, 0xFF} // a vivid red-pink, legible on light and dark pages
	pinRing = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
	pinText = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
)

// Annotate is src with pins drawn on it: each element's outline, then a
// dot where the pin is and its number in a badge beside it.
func Annotate(src image.Image, pins []Pin) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	w, h := float64(b.Dx()), float64(b.Dy())
	// Sized to the picture: a phone's screen in points is ~400 wide, a
	// page in CSS pixels ~1400.
	unit := max(1, math.Round(min(w, h)/220))
	for _, p := range pins {
		if r := p.Rect; r != nil && r.W > 0 && r.H > 0 {
			x0, y0, x1, y1 := r.X*w, r.Y*h, (r.X+r.W)*w, (r.Y+r.H)*h
			fillRect(dst, x0, y0, x1, y1, withAlpha(pinFill, 0x1C))
			strokeRect(dst, x0, y0, x1, y1, unit, pinFill)
		}
	}
	for _, p := range pins {
		x, y := p.X*w, p.Y*h
		label := strconv.Itoa(p.Number)
		scale := unit * 2
		tw, th := textSize(label, scale)
		r := max(7*unit, tw/2+3*unit, th/2+2.5*unit)
		// Up and to the right of the point, so what's under it shows; kept
		// in the picture.
		cx := min(max(x+r*0.85, r+1), w-r-1)
		cy := min(max(y-r*0.85, r+1), h-r-1)
		fillCircle(dst, x, y, 2.5*unit+unit, pinRing)
		fillCircle(dst, x, y, 2.5*unit, pinFill)
		fillCircle(dst, cx, cy, r+unit, pinRing)
		fillCircle(dst, cx, cy, r, pinFill)
		drawText(dst, label, cx-tw/2, cy-th/2, scale, pinText)
	}
	return dst
}

// Same reports whether two screenshots show the same thing, near enough:
// the same size, and alike once shrunk to a 32×32 grey thumbnail (a caret
// blinking or a spinner turning doesn't count as a change).
func Same(a, b image.Image) bool {
	if a == nil || b == nil || a.Bounds().Size() != b.Bounds().Size() {
		return false
	}
	ta, tb := thumb(a), thumb(b)
	var diff float64
	for i := range ta {
		diff += math.Abs(ta[i] - tb[i])
	}
	return diff/float64(len(ta)) < 0.015
}

const thumbSide = 32

// thumb is img's brightness (0...1) averaged over a 32×32 grid, sampled.
func thumb(img image.Image) []float64 {
	b := img.Bounds()
	out := make([]float64, thumbSide*thumbSide)
	const samples = 4
	for gy := range thumbSide {
		for gx := range thumbSide {
			var sum float64
			for sy := range samples {
				for sx := range samples {
					x := b.Min.X + (gx*samples+sx)*b.Dx()/(thumbSide*samples)
					y := b.Min.Y + (gy*samples+sy)*b.Dy()/(thumbSide*samples)
					r, g, bl, _ := img.At(x, y).RGBA()
					sum += (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 0xFFFF
				}
			}
			out[gy*thumbSide+gx] = sum / (samples * samples)
		}
	}
	return out
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

// blend draws c over dst's pixel at (x, y), covering it by cover (0...1).
func blend(dst *image.NRGBA, x, y int, c color.NRGBA, cover float64) {
	if !(image.Point{x, y}.In(dst.Rect)) || cover <= 0 {
		return
	}
	a := float64(c.A) / 255 * min(cover, 1)
	i := dst.PixOffset(x, y)
	da := float64(dst.Pix[i+3]) / 255
	oa := a + da*(1-a)
	if oa == 0 {
		return
	}
	for k, v := range [3]uint8{c.R, c.G, c.B} {
		dv := float64(dst.Pix[i+k])
		dst.Pix[i+k] = uint8(math.Round((float64(v)*a + dv*da*(1-a)) / oa))
	}
	dst.Pix[i+3] = uint8(math.Round(oa * 255))
}

// fillCircle fills a circle, its edge antialiased.
func fillCircle(dst *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	b := dst.Rect
	if math.IsNaN(cx+cy+r) || math.IsInf(cx+cy+r, 0) {
		return
	}
	for y := max(int(math.Floor(cy-r-1)), b.Min.Y); y <= min(int(math.Ceil(cy+r+1)), b.Max.Y-1); y++ {
		for x := max(int(math.Floor(cx-r-1)), b.Min.X); x <= min(int(math.Ceil(cx+r+1)), b.Max.X-1); x++ {
			// Distance from the pixel's centre to the edge, as coverage.
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			blend(dst, x, y, c, r-d+0.5)
		}
	}
}

func fillRect(dst *image.NRGBA, x0, y0, x1, y1 float64, c color.NRGBA) {
	// Only what's in the picture: a rectangle can be any size.
	b := dst.Rect
	if math.IsNaN(x0 + y0 + x1 + y1) {
		return
	}
	ix0, iy0 := max(int(math.Round(max(x0, -1))), b.Min.X), max(int(math.Round(max(y0, -1))), b.Min.Y)
	ix1, iy1 := min(int(math.Round(min(x1, float64(b.Max.X)+1))), b.Max.X), min(int(math.Round(min(y1, float64(b.Max.Y)+1))), b.Max.Y)
	for y := iy0; y < iy1; y++ {
		for x := ix0; x < ix1; x++ {
			blend(dst, x, y, c, 1)
		}
	}
}

func strokeRect(dst *image.NRGBA, x0, y0, x1, y1, width float64, c color.NRGBA) {
	fillRect(dst, x0, y0, x1, y0+width, c)
	fillRect(dst, x0, y1-width, x1, y1, c)
	fillRect(dst, x0, y0+width, x0+width, y1-width, c)
	fillRect(dst, x1-width, y0+width, x1, y1-width, c)
}

// digits are 5×7 bitmaps, a row per byte, the high of 5 bits leftmost.
var digits = [10][7]byte{
	{0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E},
	{0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E},
	{0x0E, 0x11, 0x01, 0x02, 0x04, 0x08, 0x1F},
	{0x1E, 0x01, 0x01, 0x0E, 0x01, 0x01, 0x1E},
	{0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02},
	{0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E},
	{0x06, 0x08, 0x10, 0x1E, 0x11, 0x11, 0x0E},
	{0x1F, 0x01, 0x02, 0x04, 0x08, 0x08, 0x08},
	{0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E},
	{0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0C},
}

// textSize is how big drawText draws s (digits only) at scale.
func textSize(s string, scale float64) (float64, float64) {
	n := float64(len(s))
	return (n*5 + (n-1)*1) * scale, 7 * scale
}

// drawText draws s's digits from (x, y), each bitmap pixel scale wide.
func drawText(dst *image.NRGBA, s string, x, y, scale float64, c color.NRGBA) {
	for i, ch := range s {
		if ch < '0' || ch > '9' {
			continue
		}
		g := digits[ch-'0']
		ox := x + float64(i)*6*scale
		for row := range 7 {
			for col := range 5 {
				if g[row]&(0x10>>col) != 0 {
					px, py := ox+float64(col)*scale, y+float64(row)*scale
					fillRect(dst, px, py, px+scale, py+scale, c)
				}
			}
		}
	}
}
