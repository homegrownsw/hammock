package tui

import (
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Program icons: with a Nerd Font (tui-nerd-font = true, or Settings),
// a tab shows its focused pane's program as a Nerd Font glyph where it
// showed a prompt, and pane headers show it before their name. Off, the
// TUI keeps its plain glyphs, which every font has. The tables are
// generated (programicons_gen.go, `just program-icons`).

// nerdFont is whether program icons show (tui-nerd-font, or Settings,
// which wins).
var nerdFont = false

// nerdFontPref is the Settings choice, if made.
var nerdFontPref *bool

// iconOverrides are pane-icon's mappings (program=icon): an icon the TUI
// has a glyph for; others (SF Symbols, for the Mac) fall back to the
// built-in one.
var iconOverrides = map[string]string{}

// parsePaneIcon adds a pane-icon value's mappings: "btop=monitor", or
// several separated by commas.
func parsePaneIcon(v string, into map[string]string) {
	for _, part := range strings.Split(v, ",") {
		prog, icon, ok := strings.Cut(part, "=")
		prog, icon = strings.ToLower(strings.TrimSpace(prog)), strings.TrimSpace(icon)
		if ok && prog != "" && icon != "" {
			into[prog] = icon
		}
	}
}

// programGlyph is the glyph for the first of a pane's program names that
// has one.
func programGlyph(names []string) (rune, bool) {
	for _, n := range names {
		if r, ok := nerdGlyphs[iconOverrides[n]]; ok {
			return r, true
		}
		if r, ok := nerdGlyphs[programIcons[n]]; ok {
			return r, true
		}
	}
	return 0, false
}

// isTerminalKind: a terminal, or a pane from before kinds were recorded.
func isTerminalKind(k pb.PaneKind) bool {
	return k == pb.PaneKind_PANE_KIND_TERMINAL || k == pb.PaneKind_PANE_KIND_UNSPECIFIED
}

// paneIcon is what a pane header shows before its name with program icons
// on: an agent's mark, the program's glyph, else a terminal.
func (a *App) paneIcon(p *pb.Pane) (glyph, bool) {
	if !nerdFont || p == nil || !isTerminalKind(p.Kind) {
		return glyph{}, false
	}
	if p.Agent != nil && p.Agent.Harness != "" && !p.Exited && p.Agent.State != pb.AgentState_AGENT_STATE_EXITED {
		return a.harnessGlyph(p.Agent.Harness), true
	}
	if r, ok := programGlyph(p.Program); ok && !p.Exited {
		return glyph{string(r), a.th.dim, false}, true
	}
	return glyph{string(nerdGlyphs["terminal"]), a.th.faint, false}, true
}
