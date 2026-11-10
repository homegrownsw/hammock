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
// split in the same direction the new pane becomes a sibling (weights are
// rebalanced equally); otherwise target is replaced by a two-way split.
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
					s.Children = slices.Insert(s.Children, at, nodes...)
					s.Weights = equalWeights(len(s.Children))
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

// removeFromLayout removes paneID, collapsing single-child splits. It returns
// the new root (nil when the layout is now empty).
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
	var kept []*pb.Layout
	var weights []float32
	for i, child := range s.Children {
		if c := removeFromLayout(child, paneID); c != nil {
			kept = append(kept, c)
			if i < len(s.Weights) {
				weights = append(weights, s.Weights[i])
			}
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
