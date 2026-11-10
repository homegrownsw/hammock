package daemon

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func readLog(t *testing.T, c *client.Client, pane string, f *pb.LogFilter) []*pb.LogEntry {
	t.Helper()
	return call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadLog{ReadLog: &pb.ReadLog{PaneId: pane, Filter: f, Limit: 1000}}}).GetLogView().GetEntries()
}

// A log pane's output becomes entries: watched live with a filter, read
// at once, and kept (without repeats) across a daemon restart.
func TestLogPane(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	script := `echo "ERROR boot failed"; echo "  at main"; i=0; while true; do echo "INFO tick $i"; i=$((i+1)); sleep 0.1; done`
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{
		Log: &pb.LogSource{Kind: "command", Command: []string{script}, Label: "ticks"},
	}}}}))
	d.mu.Lock()
	pane := d.tabs[tid].FocusedPaneId
	info := d.panes[pane].snapshotInfo()
	tabName := d.tabs[tid].Name
	d.mu.Unlock()
	if tabName != "ticks" {
		t.Fatalf("tab named %q", tabName)
	}
	if info.Log.GetLabel() != "ticks" || info.Title != "ticks" {
		t.Fatalf("pane: %v", info)
	}

	// Watch the errors: the first page has the error, with its trace.
	if _, err := c.Call(t.Context(), &pb.ClientMessage{Msg: &pb.ClientMessage_WatchLog{WatchLog: &pb.WatchLog{PaneId: pane, Watch: true,
		Filter: &pb.LogFilter{Levels: []pb.LogLevel{pb.LogLevel_LOG_LEVEL_ERROR}}}}}); err != nil {
		t.Fatal(err)
	}
	var errs []*pb.LogEntry
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		if v := ev.GetLogView(); v != nil && v.PaneId == pane {
			if v.Replace {
				errs = nil
			}
			errs = append(errs, v.Entries...)
		}
		return len(errs) > 0
	})
	if len(errs) != 1 || errs[0].Message != "boot failed\n  at main" {
		t.Fatalf("errors: %v", errs)
	}
	// Then live: a filter for one tick a little ahead.
	ahead := len(readLog(t, c, pane, nil)) + 3
	want := fmt.Sprintf("tick %d", ahead)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchLog{WatchLog: &pb.WatchLog{PaneId: pane, Watch: true, Filter: &pb.LogFilter{Text: want}}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		v := ev.GetLogView()
		return v != nil && !v.Replace && len(v.Entries) == 1 && v.Entries[0].Message == want
	})

	// Restart the daemon; the holder carries on.
	d.Shutdown()
	before := readLogDirect(t, dir, pane)
	d = startDaemon(t, dir)
	defer d.Shutdown()
	c = dial(t, dir)
	time.Sleep(600 * time.Millisecond)
	after := readLog(t, c, pane, nil)
	if len(after) <= len(before) {
		t.Fatalf("no new entries after the restart: %d then %d", len(before), len(after))
	}
	seen := map[string]bool{}
	for i, e := range after {
		if seen[e.Message] {
			t.Fatalf("%q twice", e.Message)
		}
		seen[e.Message] = true
		if i > 0 && e.Seq <= after[i-1].Seq {
			t.Fatalf("seq %d after %d", e.Seq, after[i-1].Seq)
		}
	}
	for i := 0; i < len(after)-1; i++ {
		if !seen[fmt.Sprintf("tick %d", i)] && i < len(after)-3 {
			t.Fatalf("tick %d missing", i)
		}
	}

	// Cleared: nothing held, the pane still there.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClearLog{ClearLog: &pb.ClearLog{PaneId: pane}}})
	if n := len(readLog(t, c, pane, &pb.LogFilter{Text: "tick 0"})); n != 0 {
		t.Fatalf("%d entries after clearing", n)
	}
}

// readLogDirect: the entries a stopped daemon left in the pane's file.
func readLogDirect(t *testing.T, dir, pane string) []string {
	t.Helper()
	d := &Daemon{cfg: Config{Dir: dir}}
	lp := d.newLogPane(pane)
	defer lp.close(false)
	es := lp.read(&pb.ReadLog{Limit: 5000}).Entries
	var out []string
	for _, e := range es {
		out = append(out, e.Message)
	}
	return out
}

// A log command that finishes cleanly keeps its pane (and entries).
func TestLogPaneStaysAfterExit(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{
		Log: &pb.LogSource{Kind: "command", Command: []string{"/bin/echo", "warning: it's 'quoted'"}},
	}}}}))
	d.mu.Lock()
	pane := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		d.mu.Lock()
		p := d.panes[pane]
		exited := p != nil && p.snapshotInfo().Exited
		d.mu.Unlock()
		if p == nil {
			t.Fatal("the pane closed")
		}
		if exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never exited")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	es := readLog(t, c, pane, nil)
	if len(es) != 1 || es[0].Level != pb.LogLevel_LOG_LEVEL_WARNING || !strings.Contains(es[0].Message, "it's 'quoted'") {
		t.Fatalf("entries: %v", es)
	}
}

func TestLogCommand(t *testing.T) {
	for _, tc := range []struct {
		src  *pb.LogSource
		want string
	}{
		{&pb.LogSource{Kind: "file", Path: "/var/log/x.log"}, "/usr/bin/tail -n 5000 -F /var/log/x.log|x.log"},
		{&pb.LogSource{Kind: "macos", Process: "Safari", Level: "debug"}, "/usr/bin/log stream --style compact --level debug --process Safari|Console: Safari"},
		{&pb.LogSource{Kind: "simulator", Device: "ABC", Predicate: `subsystem == "x"`}, `/usr/bin/xcrun simctl spawn ABC log stream --style compact --predicate subsystem == "x"|Simulator log`},
	} {
		cmd, label, err := logCommand(tc.src)
		if err != nil || strings.Join(cmd, " ")+"|"+label != tc.want {
			t.Errorf("%v: %q %q %v", tc.src, cmd, label, err)
		}
	}
	for _, bad := range []*pb.LogSource{{Kind: "file"}, {Kind: "file", Path: "rel.log"}, {Kind: "macos", Level: "loud"}, {Kind: "nope"}, {Kind: "command"}} {
		if _, _, err := logCommand(bad); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}
