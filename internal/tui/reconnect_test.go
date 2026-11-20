package tui

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/daemon"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// hmkd going away and coming back: the TUI stays up, says it's
// reconnecting, keeps trying until it answers, then shows its state again.
func TestReconnectsWhenHmkdComesBack(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "hmk-tui") // short: it holds a socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	start := func() *daemon.Daemon {
		d, err := daemon.New(daemon.Config{Dir: dir, HolderPath: "/nonexistent", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
		if err != nil {
			t.Fatal(err)
		}
		go d.Serve()
		t.Cleanup(d.Shutdown)
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if c, err := net.Dial("unix", daemon.SocketPath(dir)); err == nil {
				c.Close()
				return d
			}
			if time.Now().After(deadline) {
				t.Fatal("no daemon")
			}
		}
	}
	var dials atomic.Int32
	dial := func(ctx context.Context) (*client.Client, error) {
		dials.Add(1)
		return client.Dial(ctx, daemon.SocketPath(dir), "tui", t.Name())
	}
	call := func(c *client.Client, m *pb.ClientMessage) *pb.ServerMessage {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := c.Call(ctx, m)
		if err != nil {
			t.Fatalf("%T: %v", m.Msg, err)
		}
		return r
	}
	first := redialFirst
	redialFirst = 20 * time.Millisecond
	t.Cleanup(func() { redialFirst = first })

	d := start()
	c, err := dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	stop, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := &App{c: newLink(c), redial: dial, stop: stop, anim: newSprings(), th: newTheme(RGB{0, 0, 0}, RGB{255, 255, 255}),
		w: 80, h: 24, scr: NewScreen(80, 24), tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}, terms: map[string]*paneTerm{},
		sizes: map[string][2]int{}, editors: map[string]*editorView{}, paneRects: map[string]rect{}, actions: make(chan func(), 64)}
	t.Cleanup(func() {
		if cur := a.c.client(); cur != c {
			cur.Close()
		}
	})
	a.apply(call(c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState())

	// hmkd goes: the connection's events end, and the TUI is offline.
	d.Shutdown()
	<-d.Done()
	timeout := time.After(5 * time.Second)
	for !a.offline {
		select {
		case ev, ok := <-a.c.client().Events:
			if !ok {
				a.lost()
			} else {
				a.handle(ev)
			}
		case <-timeout:
			t.Fatal("the connection never dropped")
		}
	}
	a.drawOffline()
	if screen := a.screenText(); !strings.Contains(screen, reconnecting) || !strings.Contains(screen, offlineQuit) {
		t.Errorf("offline screen:\n%s", screen)
	}
	// Quitting still works while it's away.
	a.command([]byte("q"))
	if !a.quit {
		t.Error("⌃B q didn't quit while offline")
	}
	a.quit = false

	// It keeps trying while nothing answers, then hmkd is back.
	time.Sleep(100 * time.Millisecond)
	if dials.Load() < 2 {
		t.Errorf("dialled %d times while hmkd was away", dials.Load())
	}
	start()
	other, err := dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	gid := client.CreatedID(call(other, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Back"}}}))
	select {
	case f := <-a.actions:
		f()
	case <-time.After(10 * time.Second):
		t.Fatal("never reconnected")
	}
	if a.offline {
		t.Fatal("still offline")
	}
	if a.c.client() == c {
		t.Error("still the old connection")
	}
	// The new hmkd's state, from its snapshot or the update after it.
	timeout = time.After(5 * time.Second)
	for a.group(gid) == nil {
		select {
		case ev, ok := <-a.c.client().Events:
			if !ok {
				t.Fatal("the new connection dropped")
			}
			a.handle(ev)
		case <-timeout:
			t.Fatalf("the new hmkd's state isn't shown: groups %v", a.groups)
		}
	}
}

// screenText is the screen's characters, a line a row.
func (a *App) screenText() string {
	var rows []string
	for y := 0; y < a.scr.H; y++ {
		var b strings.Builder
		for x := 0; x < a.scr.W; x++ {
			ch := a.scr.cells[y*a.scr.W+x].Ch
			if ch == "" {
				ch = " "
			}
			b.WriteString(ch)
		}
		rows = append(rows, b.String())
	}
	return strings.Join(rows, "\n")
}
