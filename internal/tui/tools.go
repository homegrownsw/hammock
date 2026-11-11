package tui

import (
	"slices"
	"strings"
)

// Hammock's tools can be turned off (the <kind>-enabled keys: the Mac's
// Settings ▸ Tools, or the TUI's Settings): off, the TUI doesn't offer them
// in its menus, palette or ⌃B keys either. What's open stays open.

// toolsOff is the kinds turned off, from the config file (config.load).
var toolsOff []string

func toolOff(kind string) bool { return slices.Contains(toolsOff, kind) }

// menuTool is the tool a menu item opens, by its label ("" if none).
func menuTool(label string) string {
	switch {
	case strings.Contains(label, "with Browser"), strings.HasPrefix(label, "New Browser Tab"):
		return "browser"
	case strings.Contains(label, "with Log"), strings.HasPrefix(label, "New Log Tab"):
		return "log"
	case strings.Contains(label, "with Editor"), label == "New Editor Tab", label == "Quick Open…":
		return "editor"
	case label == "Review Changes":
		return "diff"
	}
	return ""
}

// paletteTool is the tool a palette action opens, by its id.
func paletteTool(id string) string {
	switch id {
	case "new-browser":
		return "browser"
	case "new-log":
		return "log"
	case "quick-open", "new-editor", "split-right-editor", "split-down-editor":
		return "editor"
	case "review-changes":
		return "diff"
	}
	return ""
}

// withoutToolsOff drops the items for tools turned off, and a separator
// left doubled or at the start.
func withoutToolsOff(items []menuItem) []menuItem {
	if len(toolsOff) == 0 {
		return items
	}
	var out []menuItem
	for _, it := range items {
		if k := menuTool(it.label); k != "" && toolOff(k) {
			continue
		}
		if it.sep && (len(out) == 0 || out[len(out)-1].sep) {
			continue
		}
		out = append(out, it)
	}
	return out
}
