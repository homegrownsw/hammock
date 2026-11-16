package daemon

import (
	"crypto/rand"
	"encoding/base32"
	"slices"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var idEncoding = base32.NewEncoding("abcdefghijkmnpqrstuvwxyz23456789").WithPadding(base32.NoPadding)

// newID returns a short random id with a type prefix, e.g. "pn_k3x9q2ma".
func newID(prefix string) string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + strings.ToLower(idEncoding.EncodeToString(b[:]))
}

// validID: an ID as newID makes them, with this prefix (one a client chose).
func validID(id, prefix string) bool {
	rest, ok := strings.CutPrefix(id, prefix+"_")
	if !ok || len(rest) != 8 {
		return false
	}
	_, err := idEncoding.DecodeString(rest)
	return err == nil
}

func leaf(paneID string) *pb.Layout { return &pb.Layout{Node: &pb.Layout_PaneId{PaneId: paneID}} }

// splitLayout inserts newPane next to target. If target's parent is already a
// split in the same direction the new pane becomes a sibling and takes half of
// target's share, leaving the other panes as they were; otherwise target is
// replaced by a two-way split.
// before puts newPane ahead of target (left of it, or above). Reports
// whether target was found.
func splitLayout(root *pb.Layout, target, newPane string, dir pb.Split_Direction, before bool) bool {
	return insertLayout(root, target, leaf(newPane), dir, before)
}

// insertLayout is splitLayout for a whole subtree (a merged tab's layout).
// A subtree split in the same direction has its children spliced in.
func insertLayout(root *pb.Layout, target string, node *pb.Layout, dir pb.Split_Direction, before bool) bool {
	if root == nil {
		return false
	}
	nodes := []*pb.Layout{node}
	if s := node.GetSplit(); s != nil && s.Direction == dir {
		nodes = s.Children
	}
	pair := func() *pb.Split {
		children := append([]*pb.Layout{leaf(target)}, nodes...)
		if before {
			children = append(slices.Clone(nodes), leaf(target))
		}
		return &pb.Split{Direction: dir, Children: children, Weights: equalWeights(len(children))}
	}
	if s := root.GetSplit(); s != nil {
		for i, child := range s.Children {
			if child.GetPaneId() == target {
				if s.Direction == dir {
					at := i + 1
					if before {
						at = i
					}
					s.Weights = splitWeight(s.Weights, len(s.Children), i, at, node, len(nodes))
					s.Children = slices.Insert(s.Children, at, nodes...)
				} else {
					s.Children[i] = &pb.Layout{Node: &pb.Layout_Split{Split: pair()}}
				}
				return true
			}
			if insertLayout(child, target, node, dir, before) {
				return true
			}
		}
		return false
	}
	if root.GetPaneId() == target {
		root.Node = &pb.Layout_Split{Split: pair()}
		return true
	}
	return false
}

// splitWeight is weights (of n children) once target, at index i, gives half
// its share to k new children inserted at index at: shared among them by the
// subtree's own weights when it's a split of the same direction spliced in,
// equally otherwise. The other children keep theirs.
func splitWeight(weights []float32, n, i, at int, node *pb.Layout, k int) []float32 {
	w := normalise(slices.Clone(weights), n)
	half := w[i] / 2
	w[i] = half
	add := equalWeights(k)
	if s := node.GetSplit(); s != nil && len(s.Children) == k {
		add = normalise(slices.Clone(s.Weights), k)
	}
	for j := range add {
		add[j] *= half
	}
	return slices.Insert(w, at, add...)
}

// removeFromLayout removes paneID, collapsing single-child splits. It returns
// the new root (nil when the layout is now empty). The pane's share goes to
// the one before it (or after, when it was first), so closing a pane just
// split off returns its neighbour to the size it had before.
func removeFromLayout(root *pb.Layout, paneID string) *pb.Layout {
	if root == nil {
		return nil
	}
	if root.GetPaneId() == paneID {
		return nil
	}
	s := root.GetSplit()
	if s == nil {
		return root
	}
	w := normalise(slices.Clone(s.Weights), len(s.Children))
	var kept []*pb.Layout
	var weights []float32
	for i, child := range s.Children {
		if c := removeFromLayout(child, paneID); c != nil {
			kept = append(kept, c)
			weights = append(weights, w[i])
		} else if len(weights) > 0 {
			weights[len(weights)-1] += w[i]
		} else if i+1 < len(w) {
			w[i+1] += w[i]
		}
	}
	switch len(kept) {
	case 0:
		return nil
	case 1:
		return kept[0]
	}
	s.Children = kept
	s.Weights = normalise(weights, len(kept))
	return root
}

// layoutPanes lists pane ids in layout order.
func layoutPanes(root *pb.Layout) []string {
	if root == nil {
		return nil
	}
	if id := root.GetPaneId(); id != "" {
		return []string{id}
	}
	var out []string
	for _, c := range root.GetSplit().GetChildren() {
		out = append(out, layoutPanes(c)...)
	}
	return out
}

func equalWeights(n int) []float32 {
	w := make([]float32, n)
	for i := range w {
		w[i] = 1 / float32(n)
	}
	return w
}

func normalise(w []float32, n int) []float32 {
	if len(w) != n {
		return equalWeights(n)
	}
	var sum float32
	for _, v := range w {
		sum += v
	}
	if sum <= 0 {
		return equalWeights(n)
	}
	for i := range w {
		w[i] /= sum
	}
	return w
}

// replaceLeaf puts newPane where oldPane is. Reports whether it was found.
func replaceLeaf(root *pb.Layout, oldPane, newPane string) bool {
	if root == nil {
		return false
	}
	if root.GetPaneId() == oldPane {
		root.Node = &pb.Layout_PaneId{PaneId: newPane}
		return true
	}
	for _, c := range root.GetSplit().GetChildren() {
		if replaceLeaf(c, oldPane, newPane) {
			return true
		}
	}
	return false
}
