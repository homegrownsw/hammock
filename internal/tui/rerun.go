package tui

// Run Again: a terminal whose program ended has a bar along its bottom,
// "Exited 1 · Run Again ↩ · Close", over its last row (the Mac's
// ExitedPaneBar). An agent's says Resume, and carries on its session.
// Return in the pane does the same as the button.

import (
	"fmt"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// canRunAgain: the pane's program ended and can start again in it
// (RestartPane): a terminal's, not a log's, nor a scheduled run's (its
// task runs again instead).
func canRunAgain(p *pb.Pane) bool {
	return p != nil && isTerm(p) && p.Exited && !p.Asleep && p.Log == nil && p.ScheduleRunId == ""
}

// resumesWhenRunAgain: running it again resumes its agent's session (the
// daemon runs its command again when there's nothing said in it).
func resumesWhenRunAgain(p *pb.Pane) bool {
	if p.GetAgent().GetSessionId() == "" {
		return false
	}
	ag := agents.Get(p.Agent.Harness)
	return ag != nil && ag.Resume != nil
}

func runAgainLabel(p *pb.Pane) string {
	if resumesWhenRunAgain(p) {
		return "Resume"
	}
	return "Run Again"
}

// restartPane starts an ended pane's program again, in the same pane.
func (a *App) restartPane(id string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_RestartPane{RestartPane: &pb.RestartPane{PaneId: id}}}, nil)
}

// runAgainOnReturn takes Return typed in an ended pane as Run Again,
// reporting whether it did.
func (a *App) runAgainOnReturn(b []byte) bool {
	s := string(b)
	if s != "\r" && s != "\n" && s != "\x1b[13u" {
		return false
	}
	p := a.panes[a.focus]
	if !canRunAgain(p) || !canDrive(a.paneGroup(p.Id)) {
		return false
	}
	a.restartPane(p.Id)
	return true
}

// drawExitedBar draws the bar on the pane's last row: its status, then
// Run Again (or Resume) and Close at the right, as many as fit.
func (a *App) drawExitedBar(p *pb.Pane, body rect) {
	th, s := a.th, a.scr
	y := body.y + body.h - 1
	bg := mix(th.bg, th.fg, 0.07)
	s.Fill(body.x, y, body.w, 1, Cell{BG: bg, HasBG: true})
	status := fmt.Sprintf("Exited %d", p.ExitStatus)
	if p.StartError != "" {
		status = "Couldn't start"
	}
	buttons := []string{runAgainLabel(p) + " ↩", "Close"}
	width := 0
	for _, b := range buttons {
		width += textWidth(b) + 3
	}
	if body.w < width+2 {
		buttons = []string{runAgainLabel(p) + " ↩"}
		width = textWidth(buttons[0]) + 3
	}
	x := body.x + body.w - width
	if x-body.x > textWidth(status)+2 {
		s.Text(body.x+1, y, x-body.x-2, status, Cell{FG: th.dim, HasFG: true, BG: bg, HasBG: true})
	}
	for i, label := range buttons {
		w := textWidth(label) + 2
		on := a.hovered(zRunAgain, p.Id) && a.mouseY == y && a.mouseX >= x && a.mouseX < x+w
		bbg, fg := mix(bg, th.fg, 0.08+0.12*b2f(on)), th.fg
		if i == 0 {
			bbg, fg = mix(th.accent, RGB{255, 255, 255}, 0.18*b2f(on)), RGB{255, 255, 255}
		}
		s.Fill(x, y, w, 1, Cell{BG: bbg, HasBG: true})
		s.Text(x+1, y, w-2, label, Cell{FG: fg, HasFG: true, BG: bbg, HasBG: true, Bold: i == 0})
		a.addZone(zone{r: rect{x, y, w, 1}, kind: zRunAgain, id: p.Id, idx: i})
		x += w + 1
	}
}

// runAgainClick acts on the bar's buttons.
func (a *App) runAgainClick(z zone) {
	a.setFocus(z.id)
	if z.idx == 0 {
		a.restartPane(z.id)
	} else {
		a.closePane(z.id)
	}
}
