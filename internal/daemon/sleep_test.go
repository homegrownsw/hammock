package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/holder"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestParseAgentSleep(t *testing.T) {
	for v, want := range map[string]time.Duration{
		"": 0, "off": 0, "Off": 0, "never": 0, "0": 0, "soon": 0, "30s": 0,
		"3h": 3 * time.Hour, " 48h ": 48 * time.Hour, "90m": 90 * time.Minute,
	} {
		if got := parseAgentSleep(v); got != want {
			t.Errorf("parseAgentSleep(%q) = %v, want %v", v, got, want)
		}
	}
}

// claudeSession makes session id's transcript, as if something had been
// said in it, and returns its path.
func claudeSession(t *testing.T, id string) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	path := filepath.Join(cfg, "projects", "-tmp-x", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOnlyReports(t *testing.T) {
	for in, want := range map[string]bool{
		"\x1b[<35;2;9M\x1b[<35;12;10M": true, "\x1b[<0;5;5m": true, "\x1b[I": true, "\x1b[O\x1b[I": true,
		"\x1b[M !!": true, "\x1b[32;10;5M": true,
		"": false, "a": false, "\x1b[<35;2;9Mx": false, "\x1b[A": false, "\r": false,
	} {
		if got := onlyReports([]byte(in)); got != want {
			t.Errorf("onlyReports(%q) = %v", in, got)
		}
	}
}

func TestWhyAwake(t *testing.T) {
	transcript := claudeSession(t, "s")
	ready := func() *pane {
		return &pane{pid: 1, ops: make(chan func(*holder.Client)), info: &pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL, Processes: []string{"claude"},
			Agent: &pb.Agent{Harness: "claude", SessionId: "s", TranscriptPath: transcript, State: pb.AgentState_AGENT_STATE_IDLE}}}
	}
	if why := ready().whyAwakeLocked(); why != "" {
		t.Fatalf("an idle agent can't sleep: %s", why)
	}
	for name, change := range map[string]func(p *pane){
		"working":    func(p *pane) { p.info.Agent.State = pb.AgentState_AGENT_STATE_WORKING },
		"asking":     func(p *pane) { p.info.Agent.State = pb.AgentState_AGENT_STATE_NEEDS_INPUT },
		"shells":     func(p *pane) { p.info.Agent.Shells = 1 },
		"command":    func(p *pane) { p.info.Agent.RunningCommand = true },
		"subagents":  func(p *pane) { p.info.Agent.Subagents = 2 },
		"no session": func(p *pane) { p.info.Agent.SessionId = "" },
		"unwritten":  func(p *pane) { p.info.Agent.SessionId, p.info.Agent.TranscriptPath = "new", "" },
		"scheduled":  func(p *pane) { p.info.ScheduleRunId = "r" },
		"asleep":     func(p *pane) { p.info.Asleep = true },
		"exited":     func(p *pane) { p.info.Exited = true },
		"no agent":   func(p *pane) { p.info.Agent = nil },
		"other":      func(p *pane) { p.info.Processes = []string{"claude", "node"} },
		"browser":    func(p *pane) { p.info.Kind = pb.PaneKind_PANE_KIND_BROWSER },
		"no resume":  func(p *pane) { p.info.Agent.Harness = "aider-not-a-harness" },
	} {
		p := ready()
		change(p)
		if p.whyAwakeLocked() == "" {
			t.Errorf("%s: could sleep", name)
		}
	}
}

// An idle agent put to sleep ends its process and keeps its screen; it
// stays asleep through a daemon restart; focusing its pane doesn't wake
// it, typing in it does, resuming its session with the options it was started with, and what
// was typed meanwhile goes in once it's up.
func TestSleepAndWake(t *testing.T) {
	bin := shortDir(t)
	// An agent by its name: this test, copied as claude, idling (TestMain).
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HMK_TEST_IDLE_AGENT", "1")
	transcript := claudeSession(t, "s1")
	t.Setenv("SHELL", "/bin/sh") // woken panes' shell, not your own's startup files
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	old := wakeWait
	wakeWait = 2 * time.Second // no hooks here to say it's at its prompt
	defer func() { wakeWait = old }()

	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh"}}}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tab].FocusedPaneId]
	d.mu.Unlock()
	pid := p.id
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: pid, Cols: 100, Rows: 30}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pid,
		Data: []byte("echo before-sleep; " + filepath.Join(bin, "claude") + " --model opus 'say hi'\r")}}})

	var agentPID int
	waitFor(t, "the agent to start", func() bool {
		p.mu.Lock()
		shell := p.pid
		p.mu.Unlock()
		if shell == 0 {
			return false
		}
		var h string
		h, agentPID = findAgentProc(shell)
		return h == "claude"
	})
	waitFor(t, "its processes to be seen", func() bool {
		p.refreshProcesses()
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.info.Processes) == 1 && p.info.Processes[0] == "claude"
	})
	p.mu.Lock()
	p.info.Agent = &pb.Agent{Harness: "claude", SessionId: "s1", TranscriptPath: transcript, State: pb.AgentState_AGENT_STATE_IDLE}
	p.hookAt = time.Now() // agent detection leaves it be
	p.mu.Unlock()

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SleepPane{SleepPane: &pb.SleepPane{PaneId: pid}}})
	info := p.snapshotInfo()
	if !info.Asleep || info.Exited || info.Agent.GetState() != pb.AgentState_AGENT_STATE_IDLE {
		t.Fatalf("not asleep as it was: asleep %v, exited %v, agent %v", info.Asleep, info.Exited, info.Agent)
	}
	waitFor(t, "the agent's process to end", func() bool { return syscall.Kill(agentPID, 0) != nil })
	if !strings.Contains(p.text(false), "before-sleep") {
		t.Errorf("its screen went: %q", p.text(false))
	}
	if want := "claude --resume 's1' '--model' 'opus'"; p.wakeCmd != want {
		t.Errorf("wakes with %q, want %q", p.wakeCmd, want)
	}
	time.Sleep(500 * time.Millisecond) // nothing of its ending reaches the pane
	if info := p.snapshotInfo(); !info.Asleep || info.Exited || info.Agent == nil {
		t.Fatalf("its ending got through: %v", info)
	}

	// A restart: it sleeps on, its wake command and screen kept.
	d.Shutdown()
	d = startDaemon(t, dir)
	defer d.Shutdown()
	d.mu.Lock()
	p = d.panes[pid]
	d.mu.Unlock()
	if p == nil {
		t.Fatal("pane gone after restart")
	}
	if info := p.snapshotInfo(); !info.Asleep || info.Exited {
		t.Fatalf("not asleep after restart: %v", info)
	}
	p.mu.Lock()
	running, cmd := p.pid, p.wakeCmd
	p.mu.Unlock()
	if running != 0 || cmd != "claude --resume 's1' '--model' 'opus'" {
		t.Errorf("after restart: pid %d, wake command %q", running, cmd)
	}
	if !strings.Contains(p.text(false), "before-sleep") {
		t.Errorf("its screen went in the restart: %q", p.text(false))
	}
	recs, _ := d.store.Records(sleepRecord)
	var rec wrapperspb.StringValue
	if err := proto.Unmarshal(recs[pid], &rec); err != nil || rec.GetValue() != cmd {
		t.Errorf("record: %q, %v", rec.GetValue(), err)
	}

	// Focusing it leaves it asleep; typing wakes it, and what's typed
	// waits for it to be up.
	c = dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: pid}}})
	if info := p.snapshotInfo(); !info.Asleep {
		t.Fatal("focusing it woke it")
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pid, Data: []byte("\x1b[<35;2;9M\x1b[I")}}})
	if info := p.snapshotInfo(); !info.Asleep {
		t.Fatal("the mouse moving over it woke it")
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pid, Data: []byte("echo typed-$((40+2))\r")}}})
	if info := p.snapshotInfo(); info.Asleep {
		t.Fatal("still asleep after typing in it")
	}
	waitFor(t, "a new process", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.pid != 0
	})
	var resumed []string
	waitFor(t, "the session to be resumed", func() bool {
		p.mu.Lock()
		shell := p.pid
		p.mu.Unlock()
		if shell == 0 {
			return false
		}
		h, apid := findAgentProc(shell)
		if h == "claude" {
			resumed = procinfo.Args(apid)
		}
		return h == "claude"
	})
	if got := strings.Join(resumed[1:], " "); got != "--resume s1 --model opus" {
		t.Errorf("resumed with %q", got)
	}
	waitFor(t, "what was typed to go in", func() bool { return strings.Contains(p.text(true), "echo typed-") })
	screen := p.text(true)
	if !strings.Contains(screen, "before-sleep") || !strings.Contains(screen, "resumed after sleeping") {
		t.Errorf("woken screen: %q", screen)
	}
	recs, _ = d.store.Records(sleepRecord)
	if _, ok := recs[pid]; ok {
		t.Error("its sleep record outlived its sleep")
	}
}

// After a reboot an agent resumes with the options it was last seen
// running with, not its first message.
func TestResumeCommandKeepsFlags(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	defer d.Shutdown()
	info := &pb.Pane{Id: "pn_1", Agent: &pb.Agent{Harness: "claude", SessionId: "s9", State: pb.AgentState_AGENT_STATE_IDLE}}
	if got := d.agentHub.resumeCommand(info); !strings.Contains(got[len(got)-1], "claude --resume 's9'; exec") {
		t.Errorf("without a command line: %q", got)
	}
	d.saveAgentArgs("pn_1", []string{"/opt/claude", "--permission-mode", "plan", "write the docs"})
	got := d.agentHub.resumeCommand(info)
	if cmd := got[len(got)-1]; !strings.Contains(cmd, "claude --resume 's9' '--permission-mode' 'plan'; exec") || strings.Contains(cmd, "docs") {
		t.Errorf("with one: %q", got)
	}
}
