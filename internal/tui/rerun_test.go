package tui

import (
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// An ended pane's bar: its status and both buttons on the last row, which
// clicks find; narrow, Close gives way first.
func TestDrawExitedBar(t *testing.T) {
	a := &App{anim: newSprings()}
	a.th = newTheme(RGB{0, 0, 0}, RGB{255, 255, 255})
	a.scr = NewScreen(60, 10)
	body := rect{0, 1, 60, 9}
	p := &pb.Pane{Id: "p1", Exited: true, ExitStatus: 2}
	a.drawExitedBar(p, body)
	row := func(y, w int) string {
		var b strings.Builder
		for x := 0; x < w; x++ {
			ch := a.scr.cells[y*w+x].Ch
			if ch == "" {
				ch = " "
			}
			b.WriteString(ch)
		}
		return b.String()
	}
	last := row(9, 60)
	for _, want := range []string{"Exited 2", "Run Again ↩", "Close"} {
		if !strings.Contains(last, want) {
			t.Fatalf("no %q in %q", want, last)
		}
	}
	x := strings.Index(last, "Close")
	if z, ok := a.zoneAt(len([]rune(last[:x])), 9); !ok || z.kind != zRunAgain || z.idx != 1 {
		t.Errorf("Close's zone: %+v %v", z, ok)
	}

	a.zones = nil
	a.scr = NewScreen(14, 4)
	a.drawExitedBar(p, rect{0, 1, 14, 3})
	if last := row(3, 14); !strings.Contains(last, "Run Again") || strings.Contains(last, "Close") || strings.Contains(last, "Exited") {
		t.Errorf("narrow: %q", last)
	}
}

func TestCanRunAgain(t *testing.T) {
	ended := func() *pb.Pane { return &pb.Pane{Exited: true} }
	if !canRunAgain(ended()) {
		t.Fatal("an ended terminal can't run again")
	}
	for name, change := range map[string]func(p *pb.Pane){
		"running":   func(p *pb.Pane) { p.Exited = false },
		"asleep":    func(p *pb.Pane) { p.Asleep = true },
		"log":       func(p *pb.Pane) { p.Log = &pb.LogSource{} },
		"scheduled": func(p *pb.Pane) { p.ScheduleRunId = "r" },
		"browser":   func(p *pb.Pane) { p.Kind = pb.PaneKind_PANE_KIND_BROWSER },
	} {
		p := ended()
		change(p)
		if canRunAgain(p) {
			t.Errorf("%s: can run again", name)
		}
	}
	if p := (&pb.Pane{Exited: true, Agent: &pb.Agent{Harness: "claude", SessionId: "s"}}); runAgainLabel(p) != "Resume" {
		t.Errorf("an agent's pane: %q", runAgainLabel(p))
	}
}
