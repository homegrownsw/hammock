package daemon

import (
	"slices"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestSplitAndRemove(t *testing.T) {
	root := leaf("a")
	if !splitLayout(root, "a", "b", pb.Split_DIRECTION_HORIZONTAL, false) {
		t.Fatal("split a")
	}
	// Same direction: sibling, not nesting.
	splitLayout(root, "b", "c", pb.Split_DIRECTION_HORIZONTAL, false)
	if got := len(root.GetSplit().Children); got != 3 {
		t.Fatalf("children %d", got)
	}
	// Different direction: nested split.
	splitLayout(root, "c", "d", pb.Split_DIRECTION_VERTICAL, false)
	if got := layoutPanes(root); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("panes %v", got)
	}
	root = removeFromLayout(root, "d") // collapses the vertical split back to "c"
	if root.GetSplit().Children[2].GetPaneId() != "c" {
		t.Fatalf("not collapsed: %v", root)
	}
	root = removeFromLayout(root, "a")
	root = removeFromLayout(root, "b")
	if root.GetPaneId() != "c" {
		t.Fatalf("expected leaf c, got %v", root)
	}
	if removeFromLayout(root, "c") != nil {
		t.Fatal("expected empty")
	}
}

func TestInsertLayout(t *testing.T) {
	// A horizontal subtree next to a horizontally split target is spliced in.
	root := leaf("a")
	splitLayout(root, "a", "b", pb.Split_DIRECTION_HORIZONTAL, false)
	sub := leaf("c")
	splitLayout(sub, "c", "d", pb.Split_DIRECTION_HORIZONTAL, false)
	insertLayout(root, "a", sub, pb.Split_DIRECTION_HORIZONTAL, false)
	if s := root.GetSplit(); len(s.Children) != 4 || !slices.Equal(layoutPanes(root), []string{"a", "c", "d", "b"}) {
		t.Fatalf("got %v", root)
	}
	// A vertical one stays nested.
	sub = leaf("e")
	splitLayout(sub, "e", "f", pb.Split_DIRECTION_VERTICAL, false)
	insertLayout(root, "b", sub, pb.Split_DIRECTION_HORIZONTAL, false)
	if s := root.GetSplit(); len(s.Children) != 5 || s.Children[4].GetSplit() == nil {
		t.Fatalf("got %v", root)
	}
}

func TestSplitBefore(t *testing.T) {
	// Split left of a leaf: the new pane first.
	root := leaf("a")
	splitLayout(root, "a", "b", pb.Split_DIRECTION_HORIZONTAL, true)
	if got := layoutPanes(root); !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("left of a leaf: %v", got)
	}
	// Left of a pane already in a row: just before it.
	splitLayout(root, "a", "c", pb.Split_DIRECTION_HORIZONTAL, true)
	if got := layoutPanes(root); !slices.Equal(got, []string{"b", "c", "a"}) || len(root.GetSplit().Children) != 3 {
		t.Fatalf("left in a row: %v", root)
	}
	// Above a pane in a row: nested, the new pane on top.
	splitLayout(root, "c", "d", pb.Split_DIRECTION_VERTICAL, true)
	if s := root.GetSplit().Children[1].GetSplit(); s == nil || s.Direction != pb.Split_DIRECTION_VERTICAL || !slices.Equal(layoutPanes(root.GetSplit().Children[1]), []string{"d", "c"}) {
		t.Fatalf("above: %v", root)
	}
}
