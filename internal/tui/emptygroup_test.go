package tui

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// An empty Group's keys do what its buttons do, only where it shows them.
func TestEmptyGroupKeys(t *testing.T) {
	a, addTab := paletteApp()
	a.groups = []*pb.Group{{Id: "g"}}
	a.selGroup = "g"
	if a.emptyKey('x') {
		t.Error("x answered as an empty Group's key")
	}
	if !a.emptyKey('a') {
		t.Fatal("a didn't start an agent in an empty Group")
	}
	if d, ok := a.overlay.(*dialog); !ok || d.title != "Start an Agent" {
		t.Errorf("a opened %T", a.overlay)
	}

	a.overlay = nil
	a.groups[0].Offline = true
	if a.emptyKey('a') || a.emptyKey('\r') {
		t.Error("an offline Group's empty state took a key")
	}
	a.groups[0].Offline = false
	a.groups[0].HostUserId, a.groups[0].MyRole = "host", pb.Role_ROLE_VIEWER
	if a.emptyKey('a') || a.emptyKey('\r') {
		t.Error("a Group you can only watch took a key")
	}

	a.groups[0].HostUserId = ""
	addTab("t")
	a.selTab = "t"
	if a.emptyKey('a') || a.emptyKey('\r') {
		t.Error("a tab's keys went to the empty state")
	}
}
