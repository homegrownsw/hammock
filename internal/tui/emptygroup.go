package tui

import (
	"os"
	"strings"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// drawEmptyGroup is a Group without tabs, selected: its name, the
// folder its new tabs open in, and buttons to open a terminal or start an
// agent there.
func (a *App) drawEmptyGroup(area rect, g *pb.Group) {
	th, s := a.th, a.scr
	center := func(y int, text string, c Cell) {
		w := min(textWidth(text), area.w)
		s.Text(area.x+(area.w-w)/2, y, w, truncate(text, area.w), c)
	}
	y := area.y + max(area.h/2-4, 0)
	center(y, a.groupTitle(g), Cell{FG: th.fg, HasFG: true, Bold: true})
	drive := g.HostUserId == "" || g.MyRole == pb.Role_ROLE_DRIVER || g.MyRole == pb.Role_ROLE_OWNER
	switch {
	case g.Offline:
		center(y+1, "Its computer can't be reached right now.", Cell{FG: th.dim, HasFG: true})
		return
	case !drive:
		center(y+1, "Only people who can drive can open tabs here.", Cell{FG: th.dim, HasFG: true})
		return
	}
	center(y+1, "Nothing open here yet. What would you like to start?", Cell{FG: th.dim, HasFG: true})
	folder := "your home folder"
	if g.Folder != "" {
		folder = shortPath(g.Folder)
	}
	center(y+2, "Opens in "+folder, Cell{FG: th.faint, HasFG: true})

	buttons := []struct {
		label string
		kind  zoneKind
		hover string
	}{
		{"  >_ New Terminal  ", zEmpty, "empty"},
		{"  ✦ Start an Agent…  ", zEmptyAgent, "emptyAgent"},
	}
	total := -2
	for _, b := range buttons {
		total += textWidth(b.label) + 2
	}
	bx := area.x + max((area.w-total)/2, 0)
	for i, b := range buttons {
		w := textWidth(b.label)
		amt := 0.3 + 0.4*a.hoverAmount(b.hover, a.hovered(b.kind, ""))
		if i > 0 {
			amt = 0.1 + 0.4*a.hoverAmount(b.hover, a.hovered(b.kind, ""))
		}
		bg := mix(th.menu, th.accent, amt)
		s.Text(bx, y+4, w, b.label, Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true, Bold: i == 0})
		a.addZone(zone{r: rect{bx, y + 4, w, 1}, kind: b.kind})
		bx += w + 2
	}
}

// setGroupFolder sets where a Group's new tabs open, saying so if the
// daemon refuses (not a folder, not absolute).
func (a *App) setGroupFolder(id, folder string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: id, Folder: folder}}}, nil)
}

// expandTilde turns ~/… back into the home folder's path.
func expandTilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == "~" || strings.HasPrefix(p, "~/")) {
		return home + strings.TrimPrefix(p, "~")
	}
	return p
}
