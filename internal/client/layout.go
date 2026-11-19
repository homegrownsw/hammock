package client

import (
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// RotateSplit is Rotate Split: a copy of l with the split directly holding
// paneID turned from side by side to stacked, or back, its panes' sizes
// kept; nil when no split holds it (it floats, or is its tab's only pane).
// The Mac's is AppModel.rotatingSplit.
func RotateSplit(l *pb.Layout, paneID string) *pb.Layout {
	l = proto.CloneOf(l)
	if !rotateSplit(l, paneID) {
		return nil
	}
	return l
}

func rotateSplit(l *pb.Layout, paneID string) bool {
	s := l.GetSplit()
	if s == nil {
		return false
	}
	for _, c := range s.Children {
		if c.GetPaneId() == paneID {
			if s.Direction == pb.Split_DIRECTION_VERTICAL {
				s.Direction = pb.Split_DIRECTION_HORIZONTAL
			} else {
				s.Direction = pb.Split_DIRECTION_VERTICAL
			}
			return true
		}
	}
	for _, c := range s.Children {
		if rotateSplit(c, paneID) {
			return true
		}
	}
	return false
}
