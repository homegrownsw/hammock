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
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

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
	output := testOutput(p)

	// Other messages fill the queue to the limit (output alone would be
	// merged into one message).
	for i := 0; i < lagHigh; i++ {
		c.send(&pb.ServerMessage{})
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

// testOutput returns a func that feeds s to p as its holder's output would
// be: into its mirror, and to each attached client.
func testOutput(p *pane) func(s string) {
	return func(s string) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.mirror != nil {
			p.mirror.Write([]byte(s))
		}
		data := []byte(s)
		for cl := range p.attached {
			cl.sendOutput(p, p.offset, data)
		}
		p.offset += uint64(len(s))
	}
}

// take is the write loop's side of the queue: the next message, as it's
// about to be written.
func take(t *testing.T, c *conn) *pb.ServerMessage {
	t.Helper()
	select {
	case msg := <-c.out:
		c.taken(msg)
		return msg
	default:
		t.Fatal("nothing queued")
		return nil
	}
}

// A pane's output joins its message still in the queue, rather than queueing
// one per read; once the message is being written, more output starts another.
func TestOutputMergesWhileQueued(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &conn{out: make(chan *pb.ServerMessage, sendQueue), ctx: ctx, cancel: cancel}
	a := &pane{id: "pn_a", attached: map[*conn]struct{}{c: {}}}
	b := &pane{id: "pn_b", attached: map[*conn]struct{}{c: {}}}
	outA, outB := testOutput(a), testOutput(b)

	first := []byte("one")
	a.mu.Lock()
	c.sendOutput(a, 0, first)
	a.offset = 3
	a.mu.Unlock()
	outB("x")
	outA("two")
	outB("y")
	outA("three")
	if len(c.out) != 2 {
		t.Fatalf("queued %d messages, want 2 (one per pane)", len(c.out))
	}
	if o := take(t, c).GetPaneOutput(); o.PaneId != "pn_a" || o.Offset != 0 || string(o.Data) != "onetwothree" {
		t.Fatalf("first message %v", o)
	}
	if string(first) != "one" {
		t.Fatalf("the read merged into was changed: %q", first)
	}
	// pn_a's message is gone from the queue: its output starts a new one.
	outA("four")
	if o := take(t, c).GetPaneOutput(); o.PaneId != "pn_b" || string(o.Data) != "xy" {
		t.Fatalf("second message %v", o)
	}
	if o := take(t, c).GetPaneOutput(); o.PaneId != "pn_a" || o.Offset != 11 || string(o.Data) != "four" {
		t.Fatalf("third message %v", o)
	}

	// Nothing grows past a message sent after it (a snapshot, a state change).
	outA("five")
	c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_PaneSnapshot{PaneSnapshot: &pb.PaneSnapshot{PaneId: "pn_a"}}})
	outA("six")
	if o := take(t, c).GetPaneOutput(); string(o.Data) != "five" {
		t.Fatalf("output before the snapshot: %v", o)
	}
	if take(t, c).GetPaneSnapshot() == nil {
		t.Fatal("snapshot out of order")
	}
	if o := take(t, c).GetPaneOutput(); string(o.Data) != "six" {
		t.Fatalf("output after the snapshot: %v", o)
	}

	// A message gathers at most mergeMax.
	big := strings.Repeat("z", mergeMax/2+1)
	outA(big)
	outA(big)
	if len(c.out) != 2 {
		t.Fatalf("queued %d messages past mergeMax, want 2", len(c.out))
	}
}

// A flood of one pane's output, merged into few messages, still counts as
// falling behind once enough of it is queued.
func TestSlowClientLagsOnQueuedBytes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &conn{out: make(chan *pb.ServerMessage, sendQueue), ctx: ctx, cancel: cancel}
	p := &pane{id: "pn_test", attached: map[*conn]struct{}{c: {}}}
	output := testOutput(p)
	chunk := strings.Repeat("x", 64<<10)
	for i := 0; i < lagBytes/len(chunk); i++ {
		output(chunk)
	}
	if c.hasLag.Load() || c.queuedBytes.Load() != lagBytes {
		t.Fatalf("lagging %v with %d bytes queued, before the limit", c.hasLag.Load(), c.queuedBytes.Load())
	}
	if want := lagBytes / mergeMax; len(c.out) != want {
		t.Fatalf("queued %d messages, want %d", len(c.out), want)
	}
	output("y")
	if !c.hasLag.Load() || c.queuedBytes.Load() != lagBytes {
		t.Fatalf("past the limit: lagging %v, %d bytes queued", c.hasLag.Load(), c.queuedBytes.Load())
	}
	for len(c.out) > 0 {
		take(t, c)
	}
	if c.queuedBytes.Load() != 0 {
		t.Fatalf("%d bytes still counted as queued", c.queuedBytes.Load())
	}
}

// recordingTransport keeps what the write loop writes.
type recordingTransport struct {
	mu     sync.Mutex
	writes [][]byte
	closed chan struct{}
	once   sync.Once
}

func (r *recordingTransport) Read(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *recordingTransport) Write(_ context.Context, b []byte) error {
	r.mu.Lock()
	r.writes = append(r.writes, b)
	r.mu.Unlock()
	return nil
}

func (r *recordingTransport) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

// The real write loop, writing while output keeps arriving, sends every byte
// once and in order (run with -race: merging into a message being written
// would be a race).
func TestWriteLoopSendsMergedOutputWhole(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := &recordingTransport{closed: make(chan struct{})}
	c := &conn{t: tr, out: make(chan *pb.ServerMessage, sendQueue), ctx: ctx, cancel: cancel}
	p := &pane{id: "pn_test", attached: map[*conn]struct{}{c: {}}}
	go c.writeLoop()
	output := testOutput(p)
	var want strings.Builder
	for i := 0; i < 20000; i++ {
		s := fmt.Sprintf("line %d\n", i)
		want.WriteString(s)
		output(s)
	}
	c.send(nil) // hang up once everything before it is written
	<-tr.closed

	var got strings.Builder
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, b := range tr.writes {
		msg := &pb.ServerMessage{}
		if err := proto.Unmarshal(b, msg); err != nil {
			t.Fatal(err)
		}
		o := msg.GetPaneOutput()
		if o == nil {
			t.Fatalf("wrote %v", msg)
		}
		if o.Offset != uint64(got.Len()) {
			t.Fatalf("message at offset %d, want %d", o.Offset, got.Len())
		}
		got.Write(o.Data)
	}
	if got.String() != want.String() {
		t.Fatalf("wrote %d bytes in %d messages, want %d", got.Len(), len(tr.writes), want.Len())
	}
	if len(tr.writes) >= 20000 {
		t.Logf("nothing merged (%d messages): the write loop kept up", len(tr.writes))
	}
	if c.queuedBytes.Load() != 0 {
		t.Fatalf("%d bytes still counted as queued", c.queuedBytes.Load())
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
