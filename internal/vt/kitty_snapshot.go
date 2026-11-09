//go:build ghostty

package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"

	"go.mitchellh.com/libghostty"
)

// A snapshot is text and modes; Kitty graphics aren't in it. So a client
// that attached later (a reattached pane, the phone, a restart) lost the
// pictures Claude Code puts on screen until it drew them again. kittyImages
// re-sends them: each image on screen once (as PNG), then each placement at
// its cell, then the cursor back where it was.

// pngCache holds each image's PNG, by image id, for the content generation
// it was made from: snapshots are taken often (every 30s to disk).
type pngCache map[uint32]struct {
	gen uint64
	b64 string
}

func (m *ghosttyMirror) kittyImages() []byte {
	kg, err := m.t.KittyGraphics()
	if err != nil {
		return nil
	}
	it, err := libghostty.NewKittyGraphicsPlacementIterator()
	if err != nil {
		return nil
	}
	defer it.Close()
	if kg.PlacementIterator(it) != nil {
		return nil
	}
	var b bytes.Buffer
	sent := map[uint32]bool{}
	for it.Next() {
		p, err := it.Info()
		if err != nil || p.IsVirtual {
			continue
		}
		img := kg.Image(p.ImageID)
		if img == nil {
			continue
		}
		r, err := it.RenderInfo(img, m.t)
		// Placements that start above the screen (scrolled partly off) or are
		// off it altogether are left out.
		if err != nil || !r.ViewportVisible || r.ViewportRow < 0 || r.ViewportCol < 0 {
			continue
		}
		if !sent[p.ImageID] {
			data := m.imagePNG(img)
			if data == "" {
				continue
			}
			transmit(&b, p.ImageID, data)
			sent[p.ImageID] = true
		}
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b_Ga=p,i=%d", r.ViewportRow+1, r.ViewportCol+1, p.ImageID)
		for _, kv := range []struct {
			k string
			v uint32
		}{{"p", p.PlacementID}, {"c", p.Columns}, {"r", p.Rows}, {"x", p.SourceX}, {"y", p.SourceY},
			{"w", p.SourceWidth}, {"h", p.SourceHeight}, {"X", p.XOffset}, {"Y", p.YOffset}} {
			if kv.v != 0 {
				fmt.Fprintf(&b, ",%s=%d", kv.k, kv.v)
			}
		}
		// C=1: don't move the cursor; q=2: no replies (clients never answer).
		fmt.Fprintf(&b, ",z=%d,C=1,q=2\x1b\\", p.Z)
	}
	if b.Len() == 0 {
		return nil
	}
	x, _ := m.t.CursorX()
	y, _ := m.t.CursorY()
	fmt.Fprintf(&b, "\x1b[%d;%dH", y+1, x+1)
	return b.Bytes()
}

// transmit writes an image's PNG (base64) in the protocol's 4 KB chunks.
func transmit(b *bytes.Buffer, id uint32, data string) {
	const chunk = 4096
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		more := 0
		if end < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(b, "\x1b_Ga=t,f=100,i=%d,q=2,m=%d;%s\x1b\\", id, more, data[i:end])
		} else {
			fmt.Fprintf(b, "\x1b_Gm=%d;%s\x1b\\", more, data[i:end])
		}
	}
}

// imagePNG is an image's pixels as base64 PNG, cached by content generation.
func (m *ghosttyMirror) imagePNG(img *libghostty.KittyGraphicsImage) string {
	info, err := img.Info()
	if err != nil || info.DataPending || info.Width == 0 || info.Height == 0 {
		return ""
	}
	if c, ok := m.pngs[info.ID]; ok && c.gen == info.Generation {
		return c.b64
	}
	var raw []byte
	if info.Format == libghostty.KittyImageFormatPNG {
		raw = append([]byte(nil), info.Data...)
	} else {
		pic := toImage(info)
		if pic == nil {
			return ""
		}
		var out bytes.Buffer
		if png.Encode(&out, pic) != nil {
			return ""
		}
		raw = out.Bytes()
	}
	s := base64.StdEncoding.EncodeToString(raw)
	if m.pngs == nil {
		m.pngs = pngCache{}
	}
	m.pngs[info.ID] = struct {
		gen uint64
		b64 string
	}{info.Generation, s}
	return s
}

// toImage wraps (or converts) an image's decoded pixels.
func toImage(info *libghostty.KittyGraphicsImageInfo) image.Image {
	w, h, d := int(info.Width), int(info.Height), info.Data
	rect := image.Rect(0, 0, w, h)
	switch info.Format {
	case libghostty.KittyImageFormatRGBA:
		if len(d) < w*h*4 {
			return nil
		}
		return &image.NRGBA{Pix: d[:w*h*4], Stride: w * 4, Rect: rect}
	case libghostty.KittyImageFormatRGB:
		if len(d) < w*h*3 {
			return nil
		}
		out := image.NewNRGBA(rect)
		for i := range w * h {
			copy(out.Pix[i*4:], d[i*3:i*3+3])
			out.Pix[i*4+3] = 255
		}
		return out
	case libghostty.KittyImageFormatGray:
		if len(d) < w*h {
			return nil
		}
		return &image.Gray{Pix: d[:w*h], Stride: w, Rect: rect}
	case libghostty.KittyImageFormatGrayAlpha:
		if len(d) < w*h*2 {
			return nil
		}
		out := image.NewNRGBA(rect)
		for i := range w * h {
			g, a := d[i*2], d[i*2+1]
			out.Pix[i*4], out.Pix[i*4+1], out.Pix[i*4+2], out.Pix[i*4+3] = g, g, g, a
		}
		return out
	}
	return nil
}
