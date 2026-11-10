package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/vt"
)

// startDaemonWithHolder starts a daemon whose pane holders are run by script
// (a shell script; "$@" are the holder's arguments).
func startDaemonWithHolder(t *testing.T, dir, script string) *Daemon {
	t.Helper()
	path := filepath.Join(dir, "holder.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := New(Config{Dir: dir, HolderPath: path, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if c, err := client.Dial(context.Background(), SocketPath(dir), "test", "probe"); err == nil {
			c.Close()
			return d
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
	}
}

// A holder that's slow to start doesn't hold up the daemon: the tab is made
// at once, other requests are answered meanwhile, and input sent before the
// holder answers reaches it.
func TestSlowHolderDoesNotBlockDaemon(t *testing.T) {
	dir := shortDir(t)
	startDaemonWithHolder(t, dir, "sleep 1.5; exec '"+holderBin+"' \"$@\"")
	c := dial(t, dir)
	other := dial(t, dir)

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Slow"}}}))

	start := time.Now()
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Name: "slow",
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}, Cwd: "/tmp"},
	}}})
	if took := time.Since(start); took > time.Second {
		t.Fatalf("CreateTab took %v: it waited for the holder", took)
	}
	tabID := client.CreatedID(r)

	// Another client is answered while the holder starts.
	start = time.Now()
	call(t, other, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Other"}}})
	if took := time.Since(start); took > time.Second {
		t.Fatalf("another client waited %v", took)
	}

	var paneID string
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, tb := range ev.GetState().GetTabs() {
			if tb.Id == tabID && tb.FocusedPaneId != "" {
				paneID = tb.FocusedPaneId
				return true
			}
		}
		return false
	})
	// Attached and typed into before the holder answers.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("early bird\n")}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: paneID, Cols: 90, Rows: 20}}})
	if took := time.Since(start); took > 1200*time.Millisecond {
		t.Skipf("too slow to check before the holder answered (%v)", took)
	}

	got := false
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		if s := ev.GetPaneSnapshot(); s != nil && s.PaneId == paneID {
			got = true
			if s.Cols != 90 || s.Rows != 20 {
				t.Errorf("snapshot is %dx%d, want the size set while it started (90x20)", s.Cols, s.Rows)
			}
		}
		return got
	})
	waitOutput(t, c, paneID, "early bird")
}

// A holder that never answers leaves the pane failed, saying why.
func TestHolderThatNeverAnswers(t *testing.T) {
	old := holderConnectTimeout
	holderConnectTimeout = 300 * time.Millisecond
	t.Cleanup(func() { holderConnectTimeout = old })
	dir := shortDir(t)
	startDaemonWithHolder(t, dir, "exec sleep 30")
	c := dial(t, dir)

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Stuck"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/sh"}, Cwd: "/tmp"},
	}}}))
	var paneID string
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, tb := range ev.GetState().GetTabs() {
			if tb.Id == tabID {
				paneID = tb.FocusedPaneId
			}
		}
		return paneID != ""
	})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, p := range ev.GetState().GetPanes() {
			if p.Id == paneID && p.Exited && p.ExitStatus == -1 {
				return true
			}
		}
		return false
	})
	text := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: paneID}}}).GetPaneText().Text
	if !strings.Contains(text, "couldn't start") {
		t.Fatalf("pane text %q", text)
	}
	if _, err := c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("x")}}}); err == nil {
		t.Fatal("input to a failed pane was accepted")
	}
}

// A client that falls behind stops getting a pane's output instead of being
// dropped, then gets its screen afresh once it has caught up.
func TestSlowClientResyncsInsteadOfDropping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &conn{out: make(chan *pb.ServerMessage, sendQueue), ctx: ctx, cancel: cancel}
	p := &pane{id: "pn_test", info: &pb.Pane{Cols: 20, Rows: 5}, attached: map[*conn]struct{}{c: {}}}
	p.mirror = vt.New(20, 5)
	defer p.mirror.Close()
	output := func(s string) {
		p.mu.Lock()
		p.mirror.Write([]byte(s))
		msg := &pb.ServerMessage{Msg: &pb.ServerMessage_PaneOutput{PaneOutput: &pb.PaneOutput{PaneId: p.id, Offset: p.offset, Data: []byte(s)}}}
		p.offset += uint64(len(s))
		for cl := range p.attached {
			cl.sendOutput(p, msg)
		}
		p.mu.Unlock()
	}

	for i := 0; i < lagHigh; i++ {
		output("x")
	}
	if len(c.out) != lagHigh || c.hasLag.Load() {
		t.Fatalf("queued %d (lagging %v) before the limit", len(c.out), c.hasLag.Load())
	}
	// Past it: skipped, not queued, and the client isn't dropped.
	for i := 0; i < sendQueue; i++ {
		output("y")
	}
	output("\r\nlast line")
	if len(c.out) != lagHigh || !c.hasLag.Load() || ctx.Err() != nil {
		t.Fatalf("queued %d, lagging %v, dropped %v", len(c.out), c.hasLag.Load(), ctx.Err() != nil)
	}

	// Caught up: the pane's screen comes afresh, with what it missed.
	for len(c.out) > 0 {
		<-c.out
	}
	c.resync()
	if c.hasLag.Load() || len(c.out) != 1 {
		t.Fatalf("after resync: lagging %v, queued %d", c.hasLag.Load(), len(c.out))
	}
	snap := (<-c.out).GetPaneSnapshot()
	if snap == nil || snap.Offset != p.offset {
		t.Fatalf("resync sent %v, want a snapshot at offset %d", snap, p.offset)
	}
	m := vt.New(20, 5)
	defer m.Close()
	m.Write(snap.Data)
	if !strings.Contains(m.Text(false), "last line") {
		t.Fatalf("snapshot screen %q", m.Text(false))
	}
	// And its output flows again.
	output("more")
	if o := (<-c.out).GetPaneOutput(); o == nil || string(o.Data) != "more" {
		t.Fatalf("after resync got %v", o)
	}
}

// A pane closed while its holder starts doesn't leave the holder (and its
// program) running once it answers.
func TestClosePaneWhileHolderStarts(t *testing.T) {
	dir := shortDir(t)
	d := startDaemonWithHolder(t, dir, "sleep 0.5; exec '"+holderBin+"' \"$@\"")
	c := dial(t, dir)
	marker := fmt.Sprintf("sleep %d", 50000+os.Getpid()%10000)

	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Brief"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "exec " + marker}, Cwd: "/tmp"},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: paneID}}})

	time.Sleep(1500 * time.Millisecond) // past when the holder answers
	if out, _ := exec.Command("pgrep", "-f", marker).Output(); len(out) > 0 {
		t.Fatalf("the closed pane's program still runs (pids %s)", out)
	}
	snap, err := d.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Runtimes[paneID]; ok {
		t.Fatal("the closed pane's runtime record came back")
	}
}

// waitPaneStarted waits for a new pane's holder to answer (panes start in
// the background: spawnAsync).
func waitPaneStarted(t *testing.T, d *Daemon, paneID string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		d.mu.Lock()
		p := d.panes[paneID]
		d.mu.Unlock()
		if p != nil {
			p.mu.Lock()
			started := p.pid > 0
			p.mu.Unlock()
			if started {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s didn't start", paneID)
		}
	}
}
