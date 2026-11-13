package tui

import (
	"strings"
	"testing"
)

// A sleeping agent's pane: dimmed, an accent Wake button in its middle
// that a click finds.
func TestDrawAsleep(t *testing.T) {
	a := &App{anim: newSprings()}
	a.th = newTheme(RGB{0, 0, 0}, RGB{255, 255, 255})
	a.scr = NewScreen(60, 14)
	body := rect{0, 1, 60, 13}
	for y := body.y; y < body.y+body.h; y++ {
		a.scr.Text(0, y, 60, strings.Repeat("x", 60), Cell{FG: a.th.fg, HasFG: true})
	}
	a.drawAsleep("p1", body)
	var rows []string
	for y := 0; y < 14; y++ {
		var b strings.Builder
		for x := 0; x < 60; x++ {
			ch := a.scr.cells[y*60+x].Ch
			if ch == "" {
				ch = " "
			}
			b.WriteString(ch)
		}
		rows = append(rows, b.String())
	}
	screen := strings.Join(rows, "\n")
	if !strings.Contains(screen, "☾  Wake Agent") {
		t.Fatalf("screen:\n%s", screen)
	}
	z, ok := a.zoneAt(30, body.y+(body.h-4)/2+1)
	if !ok || z.kind != zWake || z.id != "p1" {
		t.Errorf("the button's middle: %+v %v", z, ok)
	}
	if c := a.scr.cells[2*60+2]; c.FG == a.th.fg {
		t.Error("the pane isn't dimmed")
	}
	t.Logf("\n%s", screen)
}
