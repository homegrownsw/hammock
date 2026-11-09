package main

import (
	"strings"

	"rsc.io/qr"
)

// qrText draws s as a QR code in text, two rows of modules to a line with
// half blocks, dark on light whatever the terminal's colours (a phone's
// camera needs dark modules on a light quiet zone), so a computer without
// a screen of its own (a server over SSH) can show a pairing code.
func qrText(s string) (string, error) {
	c, err := qr.Encode(s, qr.L)
	if err != nil {
		return "", err
	}
	const quiet = 2
	n := c.Size + 2*quiet
	dark := func(x, y int) bool {
		x, y = x-quiet, y-quiet
		return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Black(x, y)
	}
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		b.WriteString("\x1b[30;107m") // black on bright white
		for x := 0; x < n; x++ {
			top, bot := dark(x, y), y+1 < n && dark(x, y+1)
			switch {
			case top && bot:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bot:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), nil
}

// qrPNG is s as a QR code image (for hmk pair --json: a web page shows it).
func qrPNG(s string) ([]byte, error) {
	c, err := qr.Encode(s, qr.L)
	if err != nil {
		return nil, err
	}
	c.Scale = 6
	return c.PNG(), nil
}
