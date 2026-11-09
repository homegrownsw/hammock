package tui

// Browser and simulator panes, live, in terminals that draw Kitty graphics
// with Unicode placeholders (Ghostty, Kitty; see kittyPlaceholders): the
// TUI polls the daemon for screenshots of the panes on screen (the page via
// DevTools' Page.captureScreenshot, the device via the simulator helper),
// scales each to the cells it will cover, and sends it only when it
// changed. Each pane has two image ids and alternates between them, as
// sending an id again drops its placement until the new picture arrives:
// the next frame goes to the id not on screen, then the cells switch to it.
// Clicks and drags in the picture go to the page (BrowserInput) or the
// device (simulator touches); the wheel scrolls a page. Elsewhere the panes
// keep their text placeholders.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/draw"
	_ "image/jpeg" // screenshots come as JPEG
	"image/png"
	"math"
	"os"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"golang.org/x/sys/unix"
)

// liveView is a browser or simulator pane drawn as a picture.
type liveView struct {
	ids        [2]uint32 // the pane's two terminal images
	cur        int       // which of them is on screen
	shown      bool      // a frame has arrived
	cols, rows int       // the cells its picture covers
	w, h       float64   // the page (pixels) or device (points), for input
	sum        uint64    // the last screenshot's hash
	made       [4]int    // the space it was fitted to: cols, rows, cell px
	busy       bool      // a screenshot is on its way
	at         time.Time // when the last one was asked for
	fails      int
	still      int  // screenshots in a row that didn't change
	box        rect // where the picture was last drawn
}

const maxLivePx = 2048 // a side, to keep frames small over SSH

// remoteTerminal: the TUI is reached over SSH, so frames cross the network.
var remoteTerminal = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""

// cellPixels is the terminal's cell size in pixels, as the tty reports it
// (Ghostty, Kitty and ssh pass it on), else a typical 10×20.
func cellPixels(fd int) (w, h int) {
	if ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ); err == nil && ws.Col > 0 && ws.Row > 0 && ws.Xpixel > 0 && ws.Ypixel > 0 {
		return int(ws.Xpixel) / int(ws.Col), int(ws.Ypixel) / int(ws.Row)
	}
	return 10, 20
}

// liveInterval is how often a pane on screen is asked for a picture: about
// as often as the phone polls a simulator, a page half as often (capturing
// one is heavier), all slower over SSH or once nothing has changed for a
// few looks, and backing off while it fails.
func liveInterval(p *pb.Pane, fails, still int) time.Duration {
	d := 250 * time.Millisecond
	if p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
		d = 500 * time.Millisecond
	}
	if remoteTerminal || still >= 8 {
		d = time.Second
	}
	if fails > 0 {
		d = max(d, min(time.Duration(fails)*time.Second, 10*time.Second))
	}
	return d
}

// fitBox fits an iw×ih picture into cols×rows cells of cw×ch pixels,
// keeping its shape: the cells it covers (at most as many as the
// placeholder diacritics can number) and the size in pixels to send it at
// (never larger than it is, nor than maxLivePx a side).
func fitBox(iw, ih, cols, rows, cw, ch int) (bc, br, pw, ph int) {
	if iw <= 0 || ih <= 0 || cols <= 0 || rows <= 0 || cw <= 0 || ch <= 0 {
		return 0, 0, 0, 0
	}
	cols, rows = min(cols, len(kittyDiacritics)), min(rows, len(kittyDiacritics))
	s := min(float64(cols*cw)/float64(iw), float64(rows*ch)/float64(ih))
	bc = min(max(int(math.Round(float64(iw)*s/float64(cw))), 1), cols)
	br = min(max(int(math.Round(float64(ih)*s/float64(ch))), 1), rows)
	ps := min(s, 1, float64(maxLivePx)/float64(max(iw, ih)))
	pw = max(int(math.Round(float64(iw)*ps)), 1)
	ph = max(int(math.Round(float64(ih)*ps)), 1)
	return bc, br, pw, ph
}

// scaleRGBA shrinks src to w×h by averaging the pixels each one covers
// (never enlarges: the terminal does that).
func scaleRGBA(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	rgba, ok := src.(*image.RGBA)
	if !ok || b.Min != (image.Point{}) {
		rgba = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	}
	sw, sh := rgba.Rect.Dx(), rgba.Rect.Dy()
	if w >= sw && h >= sh {
		return rgba
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	fx, fy := float64(sw)/float64(w), float64(sh)/float64(h)
	for y := 0; y < h; y++ {
		y0 := int(float64(y) * fy)
		y1 := min(sh, max(y0+1, int(math.Ceil(float64(y+1)*fy))))
		for x := 0; x < w; x++ {
			x0 := int(float64(x) * fx)
			x1 := min(sw, max(x0+1, int(math.Ceil(float64(x+1)*fx))))
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				row := rgba.Pix[sy*rgba.Stride+x0*4 : sy*rgba.Stride+x1*4]
				for i := 0; i < len(row); i += 4 {
					r, g, bl, a, n = r+uint32(row[i]), g+uint32(row[i+1]), bl+uint32(row[i+2]), a+uint32(row[i+3]), n+1
				}
			}
			o := y*dst.Stride + x*4
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return dst
}

// liveFrame is a screenshot made ready for the terminal.
type liveFrame struct {
	cmds       []byte // graphics commands sending and placing it
	cols, rows int
}

// makeFrame decodes a screenshot, fits it to cols×rows cells of cw×ch
// pixels, and returns the commands sending it as image id.
func makeFrame(shot []byte, id uint32, cols, rows, cw, ch int) (*liveFrame, error) {
	img, _, err := image.Decode(bytes.NewReader(shot))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	bc, br, pw, ph := fitBox(b.Dx(), b.Dy(), cols, rows, cw, ch)
	if bc == 0 {
		return nil, fmt.Errorf("no room for a %d×%d picture", b.Dx(), b.Dy())
	}
	// Locally the pty is fast and encoding time shows; over SSH, bytes do.
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if remoteTerminal {
		enc.CompressionLevel = png.DefaultCompression
	}
	var p bytes.Buffer
	if err := enc.Encode(&p, scaleRGBA(img, pw, ph)); err != nil {
		return nil, err
	}
	var cmds bytes.Buffer
	kittyTransmit(&cmds, id, p.Bytes(), bc, br)
	return &liveFrame{cmds: cmds.Bytes(), cols: bc, rows: br}, nil
}

// liveCell is the placeholder cell showing row, col of image id.
func liveCell(id uint32, row, col int, bg RGB) Cell {
	return Cell{
		Ch: "\U0010EEEE" + string(kittyDiacritics[row]) + string(kittyDiacritics[col]),
		FG: RGB{uint8(id >> 16), uint8(id >> 8), uint8(id)}, HasFG: true,
		BG: bg, HasBG: true,
	}
}

// deleteImages frees images' data in the terminal.
func deleteImages(b *bytes.Buffer, ids ...uint32) {
	for _, id := range ids {
		fmt.Fprintf(b, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
	}
}

// pollLive asks for new pictures of the browser and simulator panes on
// screen when they're due, and forgets the panes that left it. Called
// every tick.
func (a *App) pollLive() {
	if !a.pics.init() {
		return
	}
	for id, lv := range a.live {
		p := a.panes[id]
		if _, on := a.paneRects[id]; !on || !isLive(p) {
			deleteImages(&a.pics.pending, lv.ids[:]...)
			delete(a.live, id)
			a.dirty = true
		}
	}
	now := time.Now()
	for id, r := range a.paneRects {
		p := a.panes[id]
		if !isLive(p) || p.Exited || (p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR && !p.DeviceBooted) {
			continue
		}
		lv := a.live[id]
		if lv == nil {
			lv = &liveView{ids: [2]uint32{a.pics.newID(), a.pics.newID()}}
			a.live[id] = lv
		}
		if lv.busy || now.Sub(lv.at) < liveInterval(p, lv.fails, lv.still) {
			continue
		}
		a.fetchLive(id, p, lv, r.w, r.h-1)
	}
}

// fetchLive gets a pane's screenshot off the main loop and, if it changed
// (or the pane did), prepares a frame for the image not on screen.
func (a *App) fetchLive(id string, p *pb.Pane, lv *liveView, cols, rows int) {
	lv.busy, lv.at = true, time.Now()
	made := [4]int{cols, rows, a.cellW, a.cellH}
	prev, prevMade, next := lv.sum, lv.made, lv.ids[1-lv.cur]
	browser := p.Kind == pb.PaneKind_PANE_KIND_BROWSER
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		shot, w, h, err := a.screenshot(ctx, id, browser)
		var fr *liveFrame
		sum := uint64(0)
		if err == nil {
			f := fnv.New64a()
			f.Write(shot)
			sum = f.Sum64()
			if sum != prev || made != prevMade {
				fr, err = makeFrame(shot, next, made[0], made[1], made[2], made[3])
			}
		}
		a.actions <- func() {
			lv.busy = false
			if a.live[id] != lv {
				return // gone from the screen meanwhile
			}
			if err != nil {
				lv.fails++
				return
			}
			lv.fails = 0
			if w > 0 && h > 0 {
				lv.w, lv.h = w, h
			}
			if fr == nil {
				lv.still++
				return
			}
			a.pics.pending.Write(fr.cmds)
			lv.still = 0
			lv.cur, lv.shown = 1-lv.cur, true
			lv.cols, lv.rows, lv.sum, lv.made = fr.cols, fr.rows, sum, made
		}
	}()
}

// screenshot is a pane's picture as JPEG, and its size: a simulator's in
// points, a page's in the picture's pixels.
func (a *App) screenshot(ctx context.Context, id string, browser bool) (shot []byte, w, h float64, err error) {
	if browser {
		// No clip or scale: either makes Chromium resize the page on screen
		// for the capture (see browser_screenshot).
		r, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserCall{BrowserCall: &pb.BrowserCall{
			PaneId: id, Method: "Page.captureScreenshot", ParamsJson: `{"format":"jpeg","quality":80,"optimizeForSpeed":true}`, TimeoutMs: 6000,
		}}})
		if err != nil {
			return nil, 0, 0, err
		}
		var res struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal([]byte(r.GetBrowserResult().GetJson()), &res); err != nil {
			return nil, 0, 0, err
		}
		shot, err = base64.StdEncoding.DecodeString(res.Data)
		if err != nil {
			return nil, 0, 0, err
		}
		if c, _, err := image.DecodeConfig(bytes.NewReader(shot)); err == nil {
			w, h = float64(c.Width), float64(c.Height)
		}
		return shot, w, h, nil
	}
	r, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{
		PaneId: id, Op: "screenshot", ArgsJson: `{"format":"jpeg"}`,
	}}})
	if err != nil {
		return nil, 0, 0, err
	}
	var size struct{ Width, Height float64 }
	_ = json.Unmarshal([]byte(r.GetSimulatorResult().GetJson()), &size)
	return r.GetSimulatorResult().GetPng(), size.Width, size.Height, nil
}

// drawLive draws a browser or simulator pane's picture, centred in its
// body; false until there is one (the placeholder shows meanwhile).
func (a *App) drawLive(p *pb.Pane, body rect) bool {
	lv := a.live[p.Id]
	if lv == nil || !lv.shown {
		return false
	}
	cols, rows := min(lv.cols, body.w), min(lv.rows, body.h)
	x0, y0 := body.x+(body.w-cols)/2, body.y+(body.h-rows)/2
	id := lv.ids[lv.cur]
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			a.scr.Set(x0+c, y0+r, liveCell(id, r, c, a.th.bg))
		}
	}
	lv.box = rect{x0, y0, cols, rows}
	a.addZone(zone{r: lv.box, kind: zLive, id: p.Id})
	return true
}

// liveMouse passes a click, drag or wheel in a live picture to its page or
// device; reports whether it took the event.
func (a *App) liveMouse(z zone, m mouseEv) bool {
	id := a.livePress // a drag goes to where it started
	if id == "" {
		if z.kind != zLive || !((m.press && m.button == 0) || m.wheel != 0) {
			return false
		}
		id = z.id
	}
	lv, p := a.live[id], a.panes[id]
	if lv == nil || p == nil || lv.box.w == 0 || lv.box.h == 0 {
		a.livePress = ""
		return false
	}
	fx := clamp01((float64(m.x-lv.box.x) + 0.5) / float64(lv.box.w))
	fy := clamp01((float64(m.y-lv.box.y) + 0.5) / float64(lv.box.h))
	browser := p.Kind == pb.PaneKind_PANE_KIND_BROWSER
	switch {
	case m.wheel != 0 && a.livePress == "":
		if browser {
			a.browserInput(id, "wheel", map[string]any{"x": fx, "y": fy, "dx": 0, "dy": 0.1 * float64(m.wheel)})
		}
	case m.press && m.button == 0 && a.livePress == "":
		a.setFocus(id)
		a.livePress = id
		if browser {
			a.browserInput(id, "mouse", map[string]any{"type": "down", "x": fx, "y": fy, "button": "left", "clicks": 1})
		} else {
			a.simTouch(id, "down", fx*lv.w, fy*lv.h)
		}
	case m.release:
		a.livePress = ""
		if browser {
			a.browserInput(id, "mouse", map[string]any{"type": "up", "x": fx, "y": fy, "button": "left", "clicks": 1})
		} else {
			a.simTouch(id, "up", fx*lv.w, fy*lv.h)
		}
	case m.motion && a.livePress != "":
		if browser {
			a.browserInput(id, "mouse", map[string]any{"type": "move", "x": fx, "y": fy, "button": "left", "pressed": true})
		} else {
			a.simTouch(id, "move", fx*lv.w, fy*lv.h)
		}
	}
	return true
}

// browserInput sends a BrowserInput op; Send, not Call, keeps a drag's
// events in order.
func (a *App) browserInput(id, op string, args map[string]any) {
	b, _ := json.Marshal(args)
	_ = a.c.Send(bgctx, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: id, Op: op, ArgsJson: string(b)}}})
	a.liveSoon(id)
}

// simTouch is one finger on a simulator, in points.
func (a *App) simTouch(id, phase string, x, y float64) {
	b, _ := json.Marshal(map[string]any{"phase": phase, "x": x, "y": y})
	_ = a.c.Send(bgctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: id, Op: "touch", ArgsJson: string(b)}}})
	a.liveSoon(id)
}

// liveSoon makes a pane's next picture due now: input usually changes it.
func (a *App) liveSoon(id string) {
	if lv := a.live[id]; lv != nil {
		lv.at = time.Time{}
		lv.still = 0
	}
}

// dropLive frees every live picture in the terminal, on the way out.
func (a *App) dropLive() {
	var b bytes.Buffer
	for id, lv := range a.live {
		deleteImages(&b, lv.ids[:]...)
		delete(a.live, id)
	}
	if b.Len() > 0 {
		_, _ = a.out.Write(b.Bytes())
	}
}
