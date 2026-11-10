package tui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"regexp"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestDiacritics(t *testing.T) {
	if len(kittyDiacritics) != 297 || kittyDiacritics[0] != 0x0305 || kittyDiacritics[1] != 0x030D || kittyDiacritics[296] != 0x1D244 {
		t.Fatalf("table: %d entries, %U %U … %U", len(kittyDiacritics), kittyDiacritics[0], kittyDiacritics[1], kittyDiacritics[len(kittyDiacritics)-1])
	}
	for i := 1; i < len(kittyDiacritics); i++ {
		if kittyDiacritics[i] <= kittyDiacritics[i-1] {
			t.Fatalf("not sorted at %d", i)
		}
	}
}

// A picture keeps its shape, fills the side that binds, and is sent no
// larger than it is (nor than maxLivePx).
func TestFitBox(t *testing.T) {
	for _, c := range []struct {
		iw, ih, cols, rows, cw, ch int
		bc, br, pw, ph             int
	}{
		// A 1600×1000 page in 80×40 cells of 10×20 px (800×800 px): width binds.
		{1600, 1000, 80, 40, 10, 20, 80, 25, 800, 500},
		// A phone (393×852 points' screenshot at 3×) in the same: height binds.
		{1179, 2556, 80, 40, 10, 20, 37, 40, 369, 800},
		// A small picture in a big pane: the box fills it, the pixels stay.
		{200, 100, 200, 100, 10, 20, 200, 50, 200, 100},
		// Huge: capped at maxLivePx a side.
		{8000, 4000, 250, 100, 20, 40, 250, 63, 2048, 1024},
		// More cells than diacritics: at most 297 a side.
		{1000, 1000, 400, 400, 10, 10, 297, 297, 1000, 1000},
		{0, 100, 10, 10, 10, 20, 0, 0, 0, 0},
	} {
		bc, br, pw, ph := fitBox(c.iw, c.ih, c.cols, c.rows, c.cw, c.ch)
		if bc != c.bc || br != c.br || pw != c.pw || ph != c.ph {
			t.Errorf("fitBox(%d×%d in %d×%d cells of %d×%d) = %d×%d cells, %d×%d px; want %d×%d, %d×%d",
				c.iw, c.ih, c.cols, c.rows, c.cw, c.ch, bc, br, pw, ph, c.bc, c.br, c.pw, c.ph)
		}
	}
}

// Shrinking averages the pixels each one covers.
func TestScaleRGBA(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		for y := 0; y < 2; y++ {
			v := uint8(0)
			if x%2 == 1 {
				v = 200
			}
			src.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	dst := scaleRGBA(src, 2, 1)
	if dst.Bounds().Dx() != 2 || dst.Bounds().Dy() != 1 || dst.RGBAAt(0, 0) != (color.RGBA{100, 100, 100, 255}) {
		t.Fatalf("scaled: %v %v", dst.Bounds(), dst.RGBAAt(0, 0))
	}
	if same := scaleRGBA(src, 8, 8); same.Bounds().Dx() != 4 {
		t.Fatalf("enlarged to %v", same.Bounds())
	}
}

// A JPEG screenshot goes to the terminal as a PNG of the fitted size,
// placed over the cells it covers.
func TestMakeFrame(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1000))
	var j bytes.Buffer
	if err := jpeg.Encode(&j, img, nil); err != nil {
		t.Fatal(err)
	}
	fr, err := makeFrame(j.Bytes(), 0x4b0007, 80, 40, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	out := string(fr.cmds)
	if fr.cols != 80 || fr.rows != 25 || !strings.HasPrefix(out, "\x1b_Ga=t,f=100,t=d,i=4915207,q=2,m=") ||
		!strings.HasSuffix(out, "\x1b_Ga=p,U=1,i=4915207,c=80,r=25,q=2\x1b\\") {
		t.Fatalf("frame %d×%d:\n%.200q…%q", fr.cols, fr.rows, out, out[max(0, len(out)-60):])
	}
	// Reassemble the chunks: a PNG, 800×500.
	var data strings.Builder
	for _, m := range regexp.MustCompile("\x1b_G[^;\x1b]*;([^\x1b]*)\x1b\\\\").FindAllStringSubmatch(out, -1) {
		data.WriteString(m[1])
	}
	raw, err := base64.StdEncoding.DecodeString(data.String())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width != 800 || cfg.Height != 500 {
		t.Fatalf("picture %+v, %v", cfg, err)
	}
	if _, err := makeFrame([]byte("not a picture"), 1, 80, 40, 10, 20); err == nil {
		t.Fatal("made a frame of nothing")
	}
}

// The picture is drawn centred, one placeholder cell per cell, each
// numbering its row and column, coloured with the image on screen.
func TestDrawLive(t *testing.T) {
	a := &App{scr: NewScreen(40, 20), live: map[string]*liveView{}}
	p := &pb.Pane{Id: "p1", Kind: pb.PaneKind_PANE_KIND_SIMULATOR}
	body := rect{0, 1, 40, 19}
	if a.drawLive(p, body) {
		t.Fatal("drew a pane with no picture yet")
	}
	a.live["p1"] = &liveView{ids: [2]uint32{0x4b0001, 0x4b0002}, cur: 1, shown: true, cols: 10, rows: 19}
	if !a.drawLive(p, body) {
		t.Fatal("didn't draw")
	}
	lv := a.live["p1"]
	if lv.box != (rect{15, 1, 10, 19}) {
		t.Fatalf("box %+v", lv.box)
	}
	c := a.scr.Get(15+3, 1+2)
	if c.FG != (RGB{0x4b, 0x00, 0x02}) || c.Ch != "\U0010EEEE"+string(kittyDiacritics[2])+string(kittyDiacritics[3]) {
		t.Fatalf("cell %+v", c)
	}
	if a.scr.Get(14, 5).Ch != "" || a.scr.Get(25, 5).Ch != "" {
		t.Fatal("drew outside the box")
	}
	if z, ok := a.zoneAt(20, 10); !ok || z.kind != zLive || z.id != "p1" {
		t.Fatalf("zone %+v", z)
	}
	var b bytes.Buffer
	deleteImages(&b, lv.ids[:]...)
	if b.String() != "\x1b_Ga=d,d=I,i=4915201,q=2\x1b\\\x1b_Ga=d,d=I,i=4915202,q=2\x1b\\" {
		t.Fatalf("delete %q", b.String())
	}
}

// Simulators are polled faster than pages, both slow down once nothing
// changes, and failures back off.
func TestLiveInterval(t *testing.T) {
	if remoteTerminal {
		t.Skip("running over SSH")
	}
	sim, page := &pb.Pane{Kind: pb.PaneKind_PANE_KIND_SIMULATOR}, &pb.Pane{Kind: pb.PaneKind_PANE_KIND_BROWSER}
	for _, c := range []struct {
		p            *pb.Pane
		fails, still int
		want         time.Duration
	}{
		{sim, 0, 0, 250 * time.Millisecond},
		{page, 0, 0, 500 * time.Millisecond},
		{sim, 0, 8, time.Second},
		{page, 3, 0, 3 * time.Second},
		{sim, 50, 0, 10 * time.Second},
	} {
		if got := liveInterval(c.p, c.fails, c.still); got != c.want {
			t.Errorf("liveInterval(%v, %d, %d) = %v, want %v", c.p.Kind, c.fails, c.still, got, c.want)
		}
	}
}
