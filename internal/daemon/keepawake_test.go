package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// agent-keep-awake holds the assertion while an agent works or starts,
// not while one waits, rests or sleeps, and never with the setting off.
func TestKeepAwake(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	setting := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "config"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	held, calls := false, 0
	keepAwakeHold = func(on bool) bool { held = on; calls++; return on }
	t.Cleanup(func() { keepAwakeHold = holdAwake })

	p := &pane{id: "p1", info: &pb.Pane{Id: "p1"}}
	d := &Daemon{panes: map[string]*pane{"p1": p}, log: slog.Default()}
	state := func(s pb.AgentState, asleep bool) {
		p.info.Agent = &pb.Agent{Harness: "claude", State: s}
		p.info.Asleep = asleep
		d.keepAwake()
	}

	state(pb.AgentState_AGENT_STATE_WORKING, false)
	if held {
		t.Fatal("held with the setting unset (off by default)")
	}
	setting("agent-keep-awake = true\n")
	for _, c := range []struct {
		s      pb.AgentState
		asleep bool
		want   bool
	}{
		{pb.AgentState_AGENT_STATE_WORKING, false, true},
		{pb.AgentState_AGENT_STATE_IDLE, false, false},
		{pb.AgentState_AGENT_STATE_STARTING, false, true},
		{pb.AgentState_AGENT_STATE_NEEDS_INPUT, false, false},
		{pb.AgentState_AGENT_STATE_WORKING, true, false},
		{pb.AgentState_AGENT_STATE_WORKING, false, true},
	} {
		state(c.s, c.asleep)
		if held != c.want {
			t.Errorf("%v (asleep %v): held %v, want %v", c.s, c.asleep, held, c.want)
		}
	}
	n := calls
	d.keepAwake()
	if calls != n {
		t.Error("took the assertion again while it's held")
	}
	setting("agent-keep-awake = false\n")
	d.keepAwake()
	if held {
		t.Error("still held once the setting's turned off")
	}
	setting("agent-keep-awake = true\n")
	d.keepAwake()
	d.releaseAwake()
	if held || d.awake {
		t.Error("still held after shutdown")
	}
}
