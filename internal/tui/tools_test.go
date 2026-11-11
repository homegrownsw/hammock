package tui

import "testing"

func TestToolsOffLeaveMenus(t *testing.T) {
	defer func(old []string) { toolsOff = old }(toolsOff)
	items := []menuItem{{label: "New Tab"}, {label: "New Browser Tab…"}, {label: "New Editor Tab"}, {sep: true},
		{label: "Split Right with Editor"}, {sep: true}, {label: "Close Pane"}}
	toolsOff = nil
	if got := withoutToolsOff(items); len(got) != len(items) {
		t.Fatalf("all on: %d items", len(got))
	}
	toolsOff = []string{"editor", "browser"}
	got := withoutToolsOff(items)
	var labels []string
	for _, it := range got {
		labels = append(labels, it.label)
	}
	if len(got) != 3 || got[0].label != "New Tab" || !got[1].sep || got[2].label != "Close Pane" {
		t.Fatalf("editor and browser off: %q", labels)
	}
}
