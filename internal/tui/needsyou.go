package tui

import (
	"fmt"
	"slices"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The sidebar's Needs You: at most this many rows of permission requests,
// then of agents, with "+N more" for the rest.
const (
	needsGrantRows = 2
	needsRows      = 4
)

// needsLayout is how many permission requests and agents the sidebar's
// Needs You shows, and how many it leaves to "+N more".
func (a *App) needsLayout() (grants, agents, more int) {
	grants = min(len(a.grants), needsGrantRows)
	agents = min(len(a.attention), needsRows-grants)
	more = len(a.grants) + len(a.attention) - grants - agents
	return
}

// needsYouPanes are the agents waiting on a person (a question, a
// permission, a failure), not finished ones, in Needs You's order: the
// newest first.
func (a *App) needsYouPanes() []string {
	var ids []string
	for _, at := range a.attention {
		if at.State == pb.AgentState_AGENT_STATE_IDLE || a.panes[at.PaneId] == nil || slices.Contains(ids, at.PaneId) {
			continue
		}
		ids = append(ids, at.PaneId)
	}
	return ids
}

// nextNeedingYou is the agent after the focused pane in Needs You (before
// it, going back), wrapping; from anywhere else, the first (or the last).
// "" when no agent needs you.
func (a *App) nextNeedingYou(d int) string {
	ids := a.needsYouPanes()
	if len(ids) == 0 {
		return ""
	}
	if i := slices.Index(ids, a.focus); i >= 0 {
		return ids[((i+d)%len(ids)+len(ids))%len(ids)]
	}
	if d < 0 {
		return ids[len(ids)-1]
	}
	return ids[0]
}

// revealNeedingYou is Next (d 1) or Previous (d -1) Agent That Needs You.
func (a *App) revealNeedingYou(d int) {
	id := a.nextNeedingYou(d)
	if id == "" {
		a.flash("No agent needs you")
		return
	}
	a.reveal(id)
}

// openNeedsYou is "+N more": the palette, listing only what needs you.
func (a *App) openNeedsYou() {
	a.openPalette()
	if p, ok := a.overlay.(*palette); ok {
		p.needs = true
	}
}

// needsItems are the palette's rows for Needs You, in the sidebar's order:
// permission requests (their menu), then agents (shown).
func (a *App) needsItems() []paletteItem {
	var out []paletteItem
	for _, g := range a.grants {
		id := g.Id
		out = append(out, paletteItem{id: "grant:" + id, title: grantText(g), sub: "Permission request", glyph: glyph{"?", a.th.orange, true}, run: func() {
			r := (&palette{}).frame(a)
			a.grantMenu(id, r.x+2, r.y+1)
		}})
	}
	for _, at := range a.attention {
		name, sym, sub := "pane", "!", "Needs you"
		if p := a.panes[at.PaneId]; p != nil {
			if t := a.tabs[p.TabId]; t != nil {
				name = a.tabTitle(t)
			}
		}
		if at.State == pb.AgentState_AGENT_STATE_IDLE {
			sym, sub = "✓", "Finished"
		}
		pane := at.PaneId
		out = append(out, paletteItem{id: "needs:" + at.Id, title: name, sub: sub + " · " + oneLine(at.Summary), glyph: glyph{sym, a.th.orange, true}, run: func() { a.reveal(pane) }})
	}
	return out
}

// moreText is the "+N more" row's text.
func moreText(n int) string { return fmt.Sprintf("+%d more", n) }
