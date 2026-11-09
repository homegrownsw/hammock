package main

import (
	"bytes"
	"image"
	"image/png"
	"math"
)

// downscalePNG shrinks a PNG by factor (> 1), averaging each output pixel
// over the source pixels it covers: browser screenshots go from device
// pixels to CSS pixels without the shimmer of nearest-neighbour sampling.
func downscalePNG(data []byte, factor float64) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dw := max(1, int(math.Round(float64(sw)/factor)))
	dh := max(1, int(math.Round(float64(sh)/factor)))
	if dw >= sw && dh >= sh {
		return data, nil
	}
	fx, fy := float64(sw)/float64(dw), float64(sh)/float64(dh)
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := int(float64(y)*fy), min(sh, max(int(float64(y)*fy)+1, int(math.Ceil(float64(y+1)*fy))))
		for x := 0; x < dw; x++ {
			x0, x1 := int(float64(x)*fx), min(sw, max(int(float64(x)*fx)+1, int(math.Ceil(float64(x+1)*fx))))
			var r, g, b, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					// Premultiplied, 16-bit.
					cr, cg, cb, ca := src.At(sb.Min.X+sx, sb.Min.Y+sy).RGBA()
					r, g, b, a, n = r+uint64(cr), g+uint64(cg), b+uint64(cb), a+uint64(ca), n+1
				}
			}
			i := dst.PixOffset(x, y)
			if a == 0 {
				continue // transparent
			}
			// Back to straight alpha, 8-bit.
			dst.Pix[i+0] = uint8(r * 255 / a)
			dst.Pix[i+1] = uint8(g * 255 / a)
			dst.Pix[i+2] = uint8(b * 255 / a)
			dst.Pix[i+3] = uint8(a / n >> 8)
		}
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
