package daemon

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Two daemons on this Mac, as two people: a host sharing a Group and a
// guest joining it.
func TestSharedGroup(t *testing.T) {
	hostDir, guestDir := shortDir(t), shortDir(t)
	host, err := New(Config{Dir: hostDir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go host.Serve()
	defer host.Shutdown()
	for deadline := time.Now().Add(5 * time.Second); len(host.RemoteAddrs()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
	}
	// Invite links name the listen addresses.
	addrs := host.RemoteAddrs()
	host.mu.Lock()
	host.cfg.RemoteListen = addrs
	host.mu.Unlock()
	guest, err := New(Config{Dir: guestDir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go guest.Serve()
	defer guest.Shutdown()

	h := dial(t, hostDir)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Atlas"}}}))
	tab := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}}}}}))
	other := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Private"}}}))
	host.mu.Lock()
	paneID := host.tabs[tab].FocusedPaneId
	host.mu.Unlock()
	link := call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: gid}}}).GetInvite().Link
	if !strings.HasPrefix(link, "hammock://join/") {
		t.Fatalf("link %q", link)
	}

	g := dial(t, guestDir)
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	joinedC := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r, err := g.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_JoinShared{JoinShared: &pb.JoinShared{Link: link}}})
		if err != nil {
			joinedC <- "error: " + err.Error()
			return
		}
		joinedC <- client.CreatedID(r)
	}()

	// The host is asked, and approves as a driver.
	ev := waitEvent(t, h, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetJoinRequests()) > 0 })
	req := ev.GetState().JoinRequests[0]
	if req.GroupId != gid {
		t.Fatalf("request %+v", req)
	}
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondJoin{RespondJoin: &pb.RespondJoin{Id: req.Id, Role: pb.Role_ROLE_DRIVER}}})
	select {
	case got := <-joinedC:
		if got != gid {
			t.Fatalf("join: %s", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("join never answered")
	}

	// The guest sees the shared Group (and nothing else of the host's), as a driver.
	waitEvent(t, g, func(ev *pb.ServerMessage) bool {
		for _, x := range ev.GetState().GetGroups() {
			if x.Id == gid && x.MyRole == pb.Role_ROLE_DRIVER && x.HostUserId != "" {
				return true
			}
		}
		return false
	})
	full := call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState()
	for _, x := range full.Groups {
		if x.Id == other {
			t.Fatal("guest sees a Group that wasn't shared")
		}
	}

	// It can view and type into the pane on the host.
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("hello from the guest\n")}}})
	waitOutput(t, g, paneID, "hello from the guest")

	// Its typing and where it is show on the host.
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Presence{Presence: &pb.Presence{Foreground: true, VisiblePaneIds: []string{paneID}}}})
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: paneID}}})
	waitEvent(t, h, func(ev *pb.ServerMessage) bool {
		for _, x := range ev.GetState().GetGroups() {
			for _, m := range x.Members {
				if m.Role == pb.Role_ROLE_DRIVER && m.Online && m.PaneId == paneID {
					return true
				}
			}
		}
		return false
	})

	// Shared Groups mix in with your own, in the order you choose.
	mine := client.CreatedID(call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Mine"}}}))
	call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_MoveGroup{MoveGroup: &pb.MoveGroup{GroupId: gid, Index: 0}}})
	orders := map[string]int32{}
	for _, x := range call(t, g, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Groups {
		orders[x.Id] = x.Order
	}
	if !(orders[gid] < orders[mine]) {
		t.Fatalf("shared Group not moved before yours: %v", orders)
	}
	// The host renaming it doesn't move it back.
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_RenameGroup{RenameGroup: &pb.RenameGroup{GroupId: gid, Name: "Atlas 2"}}})
	waitEvent(t, g, func(ev *pb.ServerMessage) bool {
		for _, x := range ev.GetState().GetGroups() {
			if x.Id == gid && x.Name == "Atlas 2" {
				if x.Order >= orders[mine] {
					t.Errorf("host's update moved it: %d", x.Order)
				}
				return true
			}
		}
		return false
	})

	// Something in a Group that isn't shared is out of reach.
	if _, err := g.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: other}}}); err == nil {
		t.Fatal("guest reached an unshared Group")
	}

	// A guest's agents act with its role: Drive means Drive.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	agent, err := client.Dial(ctx, SocketPath(guestDir), "mcp", "agent")
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	sees := false
	for _, x := range call(t, agent, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().Groups {
		sees = sees || x.Id == gid
	}
	if !sees {
		t.Fatal("a guest's agent doesn't see the shared Group")
	}
	if _, err := agent.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("x")}}}); err != nil {
		t.Fatalf("a driver's agent typing: %v", err)
	}

	// As a viewer, typing is refused.
	guest.mu.Lock()
	guestUser := guest.id.me.UserId
	guest.mu.Unlock()
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_SetMember{SetMember: &pb.SetMember{GroupId: gid, UserId: guestUser, Role: pb.Role_ROLE_VIEWER}}})
	waitEvent(t, g, func(ev *pb.ServerMessage) bool {
		for _, x := range ev.GetState().GetGroups() {
			if x.Id == gid && x.MyRole == pb.Role_ROLE_VIEWER {
				return true
			}
		}
		return false
	})
	if _, err := g.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("x")}}}); err == nil || !strings.Contains(err.Error(), "view") {
		t.Fatalf("viewer typed: %v", err)
	}

	// Stopping sharing takes it away.
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_StopSharing{StopSharing: &pb.StopSharing{GroupId: gid}}})
	waitEvent(t, g, func(ev *pb.ServerMessage) bool {
		for _, id := range ev.GetState().GetDeletedGroupIds() {
			if id == gid {
				return true
			}
		}
		return false
	})
}
