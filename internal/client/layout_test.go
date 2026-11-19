package client

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

func leaf(id string) *pb.Layout { return &pb.Layout{Node: &pb.Layout_PaneId{PaneId: id}} }

func split(d pb.Split_Direction, w []float32, c ...*pb.Layout) *pb.Layout {
	return &pb.Layout{Node: &pb.Layout_Split{Split: &pb.Split{Direction: d, Children: c, Weights: w}}}
}

func TestRotateSplit(t *testing.T) {
	h, v := pb.Split_DIRECTION_HORIZONTAL, pb.Split_DIRECTION_VERTICAL
	// a | (b over c)
	l := split(h, []float32{0.3, 0.7}, leaf("a"), split(v, []float32{0.5, 0.5}, leaf("b"), leaf("c")))
	before := proto.CloneOf(l)

	// a's split turns stacked; the nested one and the sizes stay.
	got := RotateSplit(l, "a")
	want := split(v, []float32{0.3, 0.7}, leaf("a"), split(v, []float32{0.5, 0.5}, leaf("b"), leaf("c")))
	if !proto.Equal(got, want) {
		t.Errorf("rotate a = %v, want %v", got, want)
	}
	// c's is the inner split, which turns side by side.
	got = RotateSplit(l, "c")
	want = split(h, []float32{0.3, 0.7}, leaf("a"), split(h, []float32{0.5, 0.5}, leaf("b"), leaf("c")))
	if !proto.Equal(got, want) {
		t.Errorf("rotate c = %v, want %v", got, want)
	}
	if !proto.Equal(l, before) {
		t.Error("RotateSplit changed the layout it was given")
	}
	if RotateSplit(l, "x") != nil || RotateSplit(leaf("a"), "a") != nil {
		t.Error("rotated a pane in no split")
	}
}
