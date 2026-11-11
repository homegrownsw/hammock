package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// TestSimulatorBarrierAfterClose: the shutdown op waits on a barrier after closing
// the pane, so the host has torn the pane down before the device goes (a
// device shut down under a pane's teardown crashed the host). The host
// answers a barrier after the lines before it, and the answer reaches ask.
func TestSimulatorBarrierAfterClose(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ln, err := net.Listen("unix", SimulatorSocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var got []string
	go func() { // a stand-in host: records lines, answers barriers
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			var m struct {
				T   string `json:"t"`
				Seq int    `json:"seq"`
			}
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			mu.Lock()
			got = append(got, m.T)
			mu.Unlock()
			if m.T == "barrier" {
				fmt.Fprintf(c, `{"t":"barrierResult","seq":%d}`+"\n", m.Seq)
			}
		}
	}()
	h := d.simulators
	h.path = "stand-in" // found, so run dials the socket above
	h.ensure()
	for i := 0; ; i++ {
		h.mu.Lock()
		up := h.c != nil
		h.mu.Unlock()
		if up {
			break
		}
		if i > 200 {
			t.Fatal("never connected to the stand-in host")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.close("p1", "")
	if _, err := h.ask(map[string]any{"t": "barrier"}, 2*time.Second); err != nil {
		t.Fatalf("barrier: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "close" || got[1] != "barrier" {
		t.Fatalf("host saw %v, want [close barrier]", got)
	}
}

// TestSimulatorShutdownBookkeeping: a device Hammock booted (seen off, then on)
// is shut down a while after its last pane closes, unless shown again first;
// one already running when opened is left alone.
func TestSimulatorShutdownBookkeeping(t *testing.T) {
	t.Setenv("HMK_CONFIG_DIR", t.TempDir()) // no config: auto shut-down on
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	old := simulatorShutdownDelay
	simulatorShutdownDelay = 50 * time.Millisecond
	defer func() { simulatorShutdownDelay = old }()
	h := d.simulators
	startedNow := func(udid string) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.started[udid]
	}
	pending := func(udid string) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.shutdown[udid] != nil
	}

	// Booted by Hammock, closed: shut down after the delay.
	h.booted("A", false)
	h.booted("A", true)
	if !startedNow("A") {
		t.Fatal("A not recorded as booted by Hammock")
	}
	h.close("p1", "A")
	time.Sleep(200 * time.Millisecond)
	if startedNow("A") || pending("A") {
		t.Fatal("A not shut down after its pane closed")
	}

	// Closed, then shown again in time: kept.
	h.booted("B", false)
	h.booted("B", true)
	h.close("p2", "B")
	h.open("p3", "B")
	time.Sleep(200 * time.Millisecond)
	if !startedNow("B") || pending("B") {
		t.Fatal("B was shut down though it was shown again")
	}

	// Already running when opened: never shut down.
	h.booted("C", true)
	h.close("p4", "C")
	if pending("C") {
		t.Fatal("C, which Hammock didn't boot, is due to be shut down")
	}
}
