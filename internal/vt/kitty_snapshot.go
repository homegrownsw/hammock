//go:build ghostty

package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strconv"
	"strings"

	"go.mitchellh.com/libghostty"
)

// A snapshot is text and modes; Kitty graphics aren't in it. So a client
// that attached later (a reattached pane, the phone, a restart) lost the
// pictures Claude Code puts on screen until it drew them again. kittyImages
// re-sends them: each image on screen once (as PNG), then each placement at
// its cell, then the cursor back where it was.
//
// Images shown with Unicode placeholders (a virtual placement, U=1, and
// placeholder characters in the text: how Claude Code shows them) have no
// cell of their own: the text already carries the placeholders, so each
// image they refer to is sent with its virtual placement made again.

// pngCache holds each image's PNG, by image id, for the content generation
// it was made from: snapshots are taken often (every 30s to disk).
type pngCache map[uint32]struct {
	gen uint64
	b64 string
}

func (m *ghosttyMirror) kittyImages(text []byte) []byte {
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
	var shown map[uint32]bool // images the text's placeholders refer to
	for it.Next() {
		p, err := it.Info()
		if err != nil {
			continue
		}
		if p.IsVirtual {
			if shown == nil {
				shown = placeholderImages(text)
			}
			if !shown[p.ImageID&0xFFFFFF] {
				continue
			}
			img := kg.Image(p.ImageID)
			if img == nil {
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
			fmt.Fprintf(&b, "\x1b_Ga=p,U=1,i=%d", p.ImageID)
			for _, kv := range []struct {
				k string
				v uint32
			}{{"p", p.PlacementID}, {"c", p.Columns}, {"r", p.Rows}} {
				if kv.v != 0 {
					fmt.Fprintf(&b, ",%s=%d", kv.k, kv.v)
				}
			}
			b.WriteString(",q=2\x1b\\")
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

// placeholderImages are the images placeholder characters (U+10EEEE) in a
// formatted screen refer to: the low 24 bits of each one's id, which is its
// foreground colour (a 256-colour index or an RGB value; a third
// diacritic would carry the top 8 bits, which Claude Code's ids don't use).
func placeholderImages(text []byte) map[uint32]bool {
	const placeholder = "\U0010EEEE"
	ids := map[uint32]bool{}
	if !bytes.Contains(text, []byte(placeholder)) {
		return ids
	}
	var fg uint32
	for i := 0; i < len(text); {
		if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '[' {
			end := i + 2
			for end < len(text) && (text[end] < 0x40 || text[end] > 0x7e) {
				end++
			}
			if end < len(text) && text[end] == 'm' {
				fg = sgrForeground(string(text[i+2:end]), fg)
			}
			i = end + 1
			continue
		}
		if bytes.HasPrefix(text[i:], []byte(placeholder)) {
			ids[fg] = true
			i += len(placeholder)
			continue
		}
		i++
	}
	return ids
}

// sgrForeground follows an SGR sequence's parameters to the foreground
// colour as a number (0: the default). Extended colours (38, 48, 58) take
// their own parameters, ;- or :-separated: 5;n, or 2;r;g;b (2::r:g:b with
// an empty colour space).
func sgrForeground(params string, fg uint32) uint32 {
	var ps []string
	for _, p := range strings.Split(params, ";") {
		ps = append(ps, strings.Split(p, ":")...)
	}
	num := func(i int) uint32 {
		if i >= len(ps) {
			return 0
		}
		n, _ := strconv.Atoi(ps[i])
		return uint32(n)
	}
	for i := 0; i < len(ps); i++ {
		switch ps[i] {
		case "", "0", "39":
			fg = 0
		case "38", "48", "58":
			kind := ps[i]
			var c uint32
			switch {
			case i+1 < len(ps) && ps[i+1] == "5":
				c, i = num(i+2), i+2
			case i+1 < len(ps) && ps[i+1] == "2":
				j := i + 2
				if j+3 < len(ps) && ps[j] == "" { // 2::r:g:b
					j++
				}
				c, i = num(j)<<16|num(j+1)<<8|num(j+2), j+2
			}
			if kind == "38" {
				fg = c
			}
		}
	}
	return fg
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
