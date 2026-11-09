package tui

import (
	"encoding/json"
	"os"
	"path/filepath"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// ---------- prefs ----------

// prefs are the TUI's own UI settings, kept across runs (as the Mac app
// keeps collapsed Groups in its defaults).
type prefs struct {
	Collapsed  map[string]bool `json:"collapsed"`
	Sidebar    int             `json:"sidebar"`              // width in cells; 0 = automatic
	Layout     string          `json:"layout,omitempty"`     // "" (vertical tabs) or "horizontal"
	Animations *bool           `json:"animations,omitempty"` // overrides tui-animations
	NerdFont   *bool           `json:"nerd_font,omitempty"`  // overrides tui-nerd-font
	Computer   string          `json:"computer,omitempty"`   // the linked computer the sidebar shows ("": all)
	// CollapsedAgents: a collapsed Group also shows its tabs whose agent is
	// active (agentActive), as the Mac's setting of the same name does.
	CollapsedAgents bool `json:"collapsedAgents,omitempty"`
	path            string
}

func loadPrefs() prefs {
	p := prefs{Collapsed: map[string]bool{}}
	if home, err := os.UserHomeDir(); err == nil {
		p.path = filepath.Join(home, ".config", "hammock", "tui.json")
		if b, err := os.ReadFile(p.path); err == nil {
			_ = json.Unmarshal(b, &p)
		}
	}
	if p.Collapsed == nil {
		p.Collapsed = map[string]bool{}
	}
	animationsPref = p.Animations
	if p.Animations != nil {
		animations = *p.Animations
	}
	nerdFontPref = p.NerdFont
	if p.NerdFont != nil {
		nerdFont = *p.NerdFont
	}
	return p
}

func (p prefs) save() {
	if p.path == "" {
		return
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	_ = os.MkdirAll(filepath.Dir(p.path), 0o755)
	_ = os.WriteFile(p.path, b, 0o644)
}

// ---------- layout ----------

// horizontal is the Horizontal Tabs layout: Groups in the sidebar, the
// selected Group's tabs in a bar under the toolbar.
func (a *App) horizontal() bool { return a.prefs.Layout == "horizontal" }

// toolbarRows: the toolbar, and the tab bar with Horizontal Tabs.
func (a *App) toolbarRows() int {
	if a.horizontal() {
		return 2
	}
	return 1
}

func (a *App) sidebarWidth() int {
	if a.w < 60 {
		return 0
	}
	if a.prefs.Sidebar > 0 {
		return max(18, min(a.prefs.Sidebar, a.w/2))
	}
	return max(22, min(32, a.w/4))
}

// mainRect is everything right of the sidebar.
func (a *App) mainRect() rect {
	sw := a.sidebarWidth()
	return rect{sw, 0, a.w - sw, a.h}
}

// paneArea is the main area below the toolbar.
func (a *App) paneArea() rect {
	m := a.mainRect()
	tr := a.toolbarRows()
	return rect{m.x, m.y + tr, m.w, m.h - tr}
}

// divider is the one-cell gap between two children of a split, which can be
// dragged to resize them.
type divider struct {
	r          rect
	split      *pb.Split
	index      int // between children index and index+1
	horizontal bool
	start      int   // the split's first cell along its axis
	sizes      []int // children's sizes along the axis
}

// layoutPanes splits r according to l, leaving one cell between children,
// and records the dividers.
func (a *App) layoutPanes(l *pb.Layout, r rect, out map[string]rect, _ []int) {
	if l == nil {
		return
	}
	if id := l.GetPaneId(); id != "" {
		out[id] = r
		return
	}
	s := l.GetSplit()
	n := len(s.Children)
	if n == 0 {
		return
	}
	horizontal := s.Direction != pb.Split_DIRECTION_VERTICAL
	total, start := r.h, r.y
	if horizontal {
		total, start = r.w, r.x
	}
	avail := total - (n - 1)
	sizes := make([]int, n)
	pos := 0
	for i := range s.Children {
		wgt := 1 / float32(n)
		if len(s.Weights) == n {
			wgt = s.Weights[i]
		}
		size := int(float32(avail)*wgt + 0.5)
		if i == n-1 {
			size = total - pos // last child takes the rounding remainder
		}
		sizes[i] = max(size, 1)
		pos += size + 1
	}
	pos = 0
	for i, c := range s.Children {
		size := sizes[i]
		if horizontal {
			a.layoutPanes(c, rect{r.x + pos, r.y, size, r.h}, out, nil)
		} else {
			a.layoutPanes(c, rect{r.x, r.y + pos, r.w, size}, out, nil)
		}
		if i < n-1 {
			d := divider{split: s, index: i, horizontal: horizontal, start: start, sizes: sizes}
			if horizontal {
				d.r = rect{r.x + pos + size, r.y, 1, r.h}
			} else {
				d.r = rect{r.x, r.y + pos + size, r.w, 1}
			}
			a.dividers = append(a.dividers, d)
		}
		pos += size + 1
	}
}

// dragDivider moves divider d so its gap sits at cell p, resizing the two
// children either side of it.
func (a *App) dragDivider(d divider, p int) {
	sizes := append([]int(nil), d.sizes...)
	off := d.start
	for i := 0; i < d.index; i++ {
		off += sizes[i] + 1
	}
	pair := sizes[d.index] + sizes[d.index+1]
	left := max(4, min(p-off, pair-4))
	sizes[d.index], sizes[d.index+1] = left, pair-left
	avail := 0
	for _, s := range sizes {
		avail += s
	}
	w := make([]float32, len(sizes))
	for i, s := range sizes {
		w[i] = float32(s) / float32(avail)
	}
	d.split.Weights = w
}
