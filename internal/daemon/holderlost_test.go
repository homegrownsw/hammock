package daemon

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A holder that drops its connection (handing over to an updated holder)
// just as the daemon shuts down is dialled again after the shutdown has
// left it to the next daemon: it must be left running, its program too, not
// ended as if its pane had been closed. (`just prod` swapping the app in,
// then stopping the daemon, ended the panes it caught that way.)
func TestHolderRedialedDuringShutdownKeepsRunning(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tb := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "echo ready; exec cat"}}}}}))
	d.mu.Lock()
	id := d.tabs[tb].FocusedPaneId
	p := d.panes[id]
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: id}}})
	waitOutput(t, c, id, "ready")

	// The shutdown comes while the daemon's reaching the holder again.
	redialed := make(chan struct{})
	var once sync.Once
	holderRedialed = func(q *pane) {
		if q != p {
			return
		}
		once.Do(func() {
			q.detachHolder()
			close(redialed)
		})
	}
	t.Cleanup(func() { holderRedialed = nil })

	p.mu.Lock()
	socket, hc := p.socket, p.hc
	p.mu.Unlock()
	t.Cleanup(func() { _, _ = endHolderAt(socket) })
	hc.Close() // as the holder's handoff drops it
	select {
	case <-redialed:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't dial the holder again")
	}
	// An ended holder unlinks its socket as it goes.
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("the holder was ended: %v", err)
	}
}
