package daemon

import (
	"testing"
	"time"
)

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
