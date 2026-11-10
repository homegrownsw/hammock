package client

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// TabTitle is a tab's name, or "Tab N" by its position in its Group when it
// hasn't been named, so the number stays current as tabs move or close.
func TabTitle(t *pb.Tab, group *pb.Group) string {
	if t.Name != "" {
		return t.Name
	}
	if t.AutoName != "" {
		return t.AutoName
	}
	if group != nil {
		if i := slices.Index(group.TabIds, t.Id); i >= 0 {
			return fmt.Sprintf("Tab %d", i+1)
		}
	}
	return "Tab"
}

// GroupTitle is a Group's name; unnamed, it's named after the directory its
// last-focused pane is in ("Home" for the home directory), following it as it
// changes, else the folder it remembers, else "Group N" by position.
func GroupTitle(s *pb.Group, ordered []*pb.Group, panes map[string]*pb.Pane) string {
	if s.Name != "" {
		return s.Name
	}
	if p := panes[s.LastFocusedPaneId]; p != nil && p.Cwd != "" {
		return DirTitle(p.Cwd)
	}
	if s.Folder != "" { // its tabs have closed: where it last worked
		return DirTitle(s.Folder)
	}
	if i := slices.IndexFunc(ordered, func(o *pb.Group) bool { return o.Id == s.Id }); i >= 0 {
		return fmt.Sprintf("Group %d", i+1)
	}
	return "Group"
}

// DirTitle names a directory for display: its last component, or "Home".
func DirTitle(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(dir) == home {
		return "Home"
	}
	return filepath.Base(dir)
}
