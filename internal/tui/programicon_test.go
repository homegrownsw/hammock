package tui

import (
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestProgramGlyph(t *testing.T) {
	defer func() { iconOverrides = map[string]string{} }()
	if r, ok := programGlyph([]string{"dev", "npm", "node"}); !ok || r != nerdGlyphs["npm"] {
		t.Errorf("npm run dev: %q %v, want npm's glyph", r, ok)
	}
	if _, ok := programGlyph([]string{"zsh"}); ok {
		t.Error("a shell has no program glyph")
	}
	iconOverrides = map[string]string{}
	parsePaneIcon("frobnicate=git, btop = cpu", iconOverrides)
	if r, ok := programGlyph([]string{"frobnicate"}); !ok || r != nerdGlyphs["git"] {
		t.Errorf("override to a built-in icon: %q %v", r, ok)
	}
	// An SF Symbol has no glyph here: the built-in one stands.
	if r, ok := programGlyph([]string{"btop"}); !ok || r != nerdGlyphs["monitor"] {
		t.Errorf("override to a symbol: %q %v", r, ok)
	}
}

// With program icons on, a tab without agent state shows its focused
// pane's program; agent state and resting agents still come first.
func TestTabGlyphProgram(t *testing.T) {
	defer func() { nerdFont = false }()
	a := &App{panes: map[string]*pb.Pane{}}
	a.th = newTheme(RGB{0, 0, 0}, RGB{255, 255, 255})
	a.panes["p1"] = &pb.Pane{Id: "p1", Kind: pb.PaneKind_PANE_KIND_TERMINAL, Program: []string{"nvim"}}
	tab := &pb.Tab{Id: "t", FocusedPaneId: "p1", Layout: &pb.Layout{Node: &pb.Layout_PaneId{PaneId: "p1"}}}

	nerdFont = false
	if g, _ := a.tabGlyph(tab); g.ch != "❯" {
		t.Errorf("off: %q, want the prompt", g.ch)
	}
	nerdFont = true
	if g, _ := a.tabGlyph(tab); g.ch != string(nerdGlyphs["neovim"]) {
		t.Errorf("on: %q, want neovim's glyph", g.ch)
	}
	if g, ok := a.paneIcon(a.panes["p1"]); !ok || g.ch != string(nerdGlyphs["neovim"]) {
		t.Errorf("header: %q %v, want neovim's glyph", g.ch, ok)
	}
	a.panes["p1"].Agent = &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_IDLE, Seen: true}
	if g, _ := a.tabGlyph(tab); g.ch != "○" {
		t.Errorf("resting agent: %q, want its ring", g.ch)
	}
	if g, _ := a.paneIcon(a.panes["p1"]); g.ch != a.harnessGlyph("claude").ch {
		t.Errorf("agent header: %q, want its mark", g.ch)
	}
	nerdFont = false
	if _, ok := a.paneIcon(a.panes["p1"]); ok {
		t.Error("off: a header icon")
	}
}

// The TUI's Settings win even without a config file.
func TestPrefsWithoutConfigFile(t *testing.T) {
	defer func() { animationsPref, nerdFontPref, animations, nerdFont = nil, nil, true, false }()
	off, on := false, true
	animationsPref, nerdFontPref = &off, &on
	c := &config{path: t.TempDir() + "/missing"}
	c.load()
	if animations || !nerdFont {
		t.Errorf("animations %v nerdFont %v, want the Settings' false and true", animations, nerdFont)
	}
}
