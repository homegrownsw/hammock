package daemon

import (
	"context"
	"testing"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// A client may choose a new tab's ID (to show it before the reply): a
// well-formed one that isn't used yet.
func TestCreateTabWithClientID(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	mk := func(id string) (*pb.ServerMessage, error) {
		return c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
			GroupId: gid, TabId: id, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	}
	r, err := mk("tb_abcd2345")
	if err != nil || client.CreatedID(r) != "tb_abcd2345" {
		t.Fatalf("chosen id: %v %v", r, err)
	}
	for _, bad := range []string{"tb_abcd2345", "tb_short", "pn_abcd2345", "tb_ABCD2345", "tb_abcd234l", "tb_abcd23450"} {
		if _, err := mk(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if r, err := mk(""); err != nil || len(client.CreatedID(r)) != 11 {
		t.Fatalf("daemon's own id: %v %v", r, err)
	}
}
