package daemon

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// The Mac's name: its own by default, yours once set, its own again when
// cleared; a profile change without one leaves it be.
func TestDeviceName(t *testing.T) {
	old := computerName
	computerName = func() string { return "Aloha Isle" }
	t.Cleanup(func() { computerName = old })
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	host := func() string {
		return call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{ClientKind: "test"}}}).GetHelloReply().GetHostName()
	}
	if h := host(); h != "Aloha Isle" {
		t.Fatalf("default %q", h)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetProfile{SetProfile: &pb.SetProfile{Name: "Alex", DeviceName: proto.String("Studio Mac")}}})
	if h := host(); h != "Studio Mac" {
		t.Fatalf("set %q", h)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetProfile{SetProfile: &pb.SetProfile{Name: "Alex B"}}})
	if h := host(); h != "Studio Mac" {
		t.Fatalf("a name change reset it: %q", h)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetProfile{SetProfile: &pb.SetProfile{Name: "Alex B", DeviceName: proto.String("")}}})
	if h := host(); h != "Aloha Isle" {
		t.Fatalf("cleared %q", h)
	}
}
