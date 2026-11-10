package tui

// Groups' pictures in the sidebar, through the Kitty graphics
// protocol's Unicode placeholders: the image is sent to the terminal once,
// then drawn by ordinary cells (U+10EEEE with diacritics for row and
// column, the image's id as their colour), so it moves and clears with the
// text like any other character. Only in terminals known to do this
// (Ghostty, Kitty); elsewhere a Group without an emoji shows none, as before.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

type pictures struct {
	checked, on bool
	ids         map[string]uint32 // picture hash → the image id sent to the terminal
	loading     map[string]bool
	pending     bytes.Buffer // graphics commands to write before the next frame
	next        uint32
}

// kittyPlaceholders reports whether this terminal draws Kitty images placed
// with Unicode placeholders. HMK_TUI_IMAGES=1 or 0 overrides.
func kittyPlaceholders() bool {
	switch os.Getenv("HMK_TUI_IMAGES") {
	case "1":
		return true
	case "0":
		return false
	}
	if os.Getenv("TMUX") != "" || strings.HasPrefix(os.Getenv("TERM"), "screen") {
		return false // tmux doesn't pass the graphics through
	}
	// TERM survives SSH where TERM_PROGRAM usually doesn't.
	t := os.Getenv("TERM")
	return os.Getenv("TERM_PROGRAM") == "ghostty" || t == "xterm-ghostty" || t == "xterm-kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

// pictureID is the terminal image for g's picture, fetching and sending it
// the first time (ok is false until then, and when there's none to show).
func (a *App) pictureID(g *pb.Group) (id uint32, ok bool) {
	p := &a.pics
	if !p.init() || g.Icon != "" || g.IconImage == "" {
		return 0, false
	}
	if id, ok := p.ids[g.IconImage]; ok {
		return id, true
	}
	if !p.loading[g.IconImage] {
		p.loading[g.IconImage] = true
		hash := g.IconImage
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadGroupIcon{ReadGroupIcon: &pb.ReadGroupIcon{GroupId: g.Id}}}, func(r *pb.ServerMessage) {
			if png := r.GetGroupIcon().GetPng(); len(png) > 0 {
				p.ids[hash] = p.send(png)
				a.dirty = true
			}
		})
	}
	return 0, false
}

// send transmits a PNG and places it virtually (two cells by one), to be
// drawn by placeholder cells; returns its id.
func (p *pictures) send(png []byte) uint32 {
	id := p.newID()
	kittyTransmit(&p.pending, id, png, 2, 1)
	return id
}

// init decides once whether this terminal gets pictures; reports whether
// it does.
func (p *pictures) init() bool {
	if !p.checked {
		p.checked, p.on = true, kittyPlaceholders()
		p.ids, p.loading, p.next = map[string]uint32{}, map[string]bool{}, 0x4b0001
	}
	return p.on
}

// newID is an image id not used yet (its 24 bits are the placeholder
// cells' colour).
func (p *pictures) newID() uint32 {
	id := p.next
	p.next++
	if p.next > 0xffffff {
		p.next = 0x4b0001
	}
	return id
}

// kittyTransmit writes the commands sending a PNG as image id, in
// 4096-byte chunks, and placing it virtually over cols×rows cells, to be
// drawn by placeholder cells. Sending an id again replaces its picture.
func kittyTransmit(b *bytes.Buffer, id uint32, png []byte, cols, rows int) {
	data := base64.StdEncoding.EncodeToString(png)
	for i := 0; i < len(data); i += 4096 {
		chunk := data[i:min(i+4096, len(data))]
		more := 0
		if i+4096 < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(b, "\x1b_Ga=t,f=100,t=d,i=%d,q=2,m=%d;%s\x1b\\", id, more, chunk)
		} else {
			fmt.Fprintf(b, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	fmt.Fprintf(b, "\x1b_Ga=p,U=1,i=%d,c=%d,r=%d,q=2\x1b\\", id, cols, rows)
}

// placeholder is the cell showing column col (0 or 1) of image id's
// one-row placement.
func placeholder(id uint32, col int, bg RGB) Cell {
	diacritics := []rune{0x0305, 0x030D} // rows and columns 0 and 1
	return Cell{
		Ch: "\U0010EEEE" + string(diacritics[0]) + string(diacritics[col]),
		FG: RGB{uint8(id >> 16), uint8(id >> 8), uint8(id)}, HasFG: true,
		BG: bg, HasBG: true,
	}
}
