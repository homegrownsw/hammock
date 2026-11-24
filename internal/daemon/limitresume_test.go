package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// limitPane starts a daemon with a pane running cat (whatever's typed
// into it shows), as the agent's, with the config file config.
func limitPane(t *testing.T, config string) (*Daemon, *client.Client, string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := shortDir(t)
	d := startDaemon(t, dir)
	t.Cleanup(d.Shutdown)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Hammock"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "printf 'ready\\n'; exec cat"}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitOutput(t, c, paneID, "ready")
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt":"go"}`)})
	return d, c, paneID
}

func limitStop(t *testing.T, d *Daemon, paneID, msg string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"hook_event_name": "StopFailure", "session_id": "s1", "error": "rate_limit", "last_assistant_message": msg})
	d.mu.Lock()
	d.agentHub.handle(HookEvent{PaneID: paneID, Harness: "claude", Payload: b})
	d.mu.Unlock()
}

func limitAgent(d *Daemon, paneID string) *pb.Agent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.panes[paneID].snapshotInfo().GetAgent()
}

// An agent stopped at its usage limit is sent "continue" once the limit
// resets, and then waits no more.
func TestLimitResumeContinues(t *testing.T) {
	old := limitResumeDelay
	limitResumeDelay = func() time.Duration { return 0 }
	defer func() { limitResumeDelay = old }()
	d, c, paneID := limitPane(t, "")

	before := time.Now()
	limitStop(t, d, paneID, "You've hit your 5-hour limit · resets in 2s")
	a := limitAgent(d, paneID)
	lr := a.GetLimitResume()
	if a.State != pb.AgentState_AGENT_STATE_ERRORED || lr == nil || !lr.ResetKnown {
		t.Fatalf("after the limit: %v, resume %v", a.State, lr)
	}
	if at := time.UnixMilli(lr.AtUnixMs); at.Before(before.Add(2*time.Second-time.Millisecond)) || at.After(time.Now().Add(2*time.Second)) {
		t.Errorf("continues at %v, want 2s after %v", at, before)
	}
	// Claude's "waiting for your input" a minute later leaves it waiting.
	hook(d, paneID, "Notification", map[string]any{"session_id": "s1", "message": "Claude is waiting for your input", "notification_type": "idle_prompt"})
	if a := limitAgent(d, paneID); a.State != pb.AgentState_AGENT_STATE_ERRORED || a.GetLimitResume() == nil {
		t.Fatalf("after an idle notification: %v, resume %v", a.State, a.GetLimitResume())
	}
	waitOutput(t, c, paneID, continueText)
	if limitAgent(d, paneID).GetLimitResume() != nil {
		t.Error("still waiting after it was sent continue")
	}
}

// Cancelling, typing in the pane or the agent moving on drops the resume;
// with the setting off there's none.
func TestLimitResumeCancelled(t *testing.T) {
	d, c, paneID := limitPane(t, "")
	limitStop(t, d, paneID, "You've hit your weekly limit · resets in 3 days")
	if lr := limitAgent(d, paneID).GetLimitResume(); lr == nil || time.Until(time.UnixMilli(lr.AtUnixMs)) < 71*time.Hour {
		t.Fatalf("weekly: %v", lr)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CancelLimitResume{CancelLimitResume: &pb.CancelLimitResume{PaneId: paneID}}})
	if limitAgent(d, paneID).GetLimitResume() != nil {
		t.Fatal("cancelled, still waiting")
	}
	if _, err := callErr(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CancelLimitResume{CancelLimitResume: &pb.CancelLimitResume{PaneId: paneID}}}); err == nil {
		t.Error("cancelling nothing should say so")
	}

	limitStop(t, d, paneID, "You've hit your 5-hour limit · resets in 1h")
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("x")}}})
	if limitAgent(d, paneID).GetLimitResume() != nil {
		t.Error("typed in, still waiting")
	}

	limitStop(t, d, paneID, "You've hit your 5-hour limit · resets in 1h")
	hook(d, paneID, "UserPromptSubmit", map[string]any{"session_id": "s1", "prompt": "carry on"})
	if limitAgent(d, paneID).GetLimitResume() != nil {
		t.Error("working again, still waiting")
	}

	limitStop(t, d, paneID, "API Error: Request rejected (429) · this may be a temporary capacity issue.")
	if a := limitAgent(d, paneID); a.State != pb.AgentState_AGENT_STATE_ERRORED || a.GetLimitResume() != nil {
		t.Errorf("a passing 429: %v, resume %v", a.State, a.GetLimitResume())
	}

	if err := os.WriteFile(filepath.Join(os.Getenv("HMK_CONFIG_DIR"), "config"), []byte("agent-resume-after-limit = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	limitStop(t, d, paneID, "You've hit your 5-hour limit · resets in 1h")
	if limitAgent(d, paneID).GetLimitResume() != nil {
		t.Error("the setting's off, but it waits")
	}
}

// With no reset in its words, the agent's usage says when; with neither,
// Hammock tries again in an hour.
func TestLimitResumeFromUsage(t *testing.T) {
	d, _, paneID := limitPane(t, "")
	reset := time.Now().Add(90 * time.Minute).Truncate(time.Second)
	d.mu.Lock()
	d.usage = &pb.UsageLimits{Agents: []*pb.AgentUsage{{Harness: "claude", Windows: []*pb.UsageWindow{
		{Label: "5h", UsedPercent: 100, ResetsUnix: reset.Unix(), WindowMinutes: 300},
		{Label: "1w", UsedPercent: 60, ResetsUnix: reset.Add(72 * time.Hour).Unix(), WindowMinutes: 10080},
	}}}}
	d.mu.Unlock()
	limitStop(t, d, paneID, "You've hit your usage limit")
	lr := limitAgent(d, paneID).GetLimitResume()
	if lr == nil || !lr.ResetKnown || time.UnixMilli(lr.AtUnixMs).Before(reset) || time.UnixMilli(lr.AtUnixMs).After(reset.Add(2*time.Minute)) {
		t.Fatalf("from its usage: %v, want just after %v", lr, reset)
	}

	d.mu.Lock()
	d.usage = nil
	d.mu.Unlock()
	limitStop(t, d, paneID, "You've hit your usage limit")
	lr = limitAgent(d, paneID).GetLimitResume()
	if lr == nil || lr.ResetKnown || time.Until(time.UnixMilli(lr.AtUnixMs)) < 59*time.Minute {
		t.Fatalf("unknown: %v, want a try in an hour", lr)
	}
}

// Codex runs no hook when its usage limit ends a turn: its session file
// says, and it waits for the limit like any other.
func TestLimitResumeCodexSessionFile(t *testing.T) {
	d, _, paneID := limitPane(t, "")
	reset := time.Now().Add(3 * time.Hour).Unix()
	start := time.Now().Add(-time.Second).UTC()
	rollout := filepath.Join(t.TempDir(), "rollout-x.jsonl")
	lines := `{"timestamp":"` + start.Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}
{"timestamp":"` + start.Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":100.0,"window_minutes":300,"resets_at":` + itoa(reset) + `}}}}
{"timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) + `","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","error":{"message":"You've hit your usage limit.","codex_error_info":"usage_limit_exceeded"}}}
`
	if err := os.WriteFile(rollout, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	p := d.panes[paneID]
	p.mu.Lock()
	p.info.Agent = &pb.Agent{Harness: "codex", SessionId: "s1", State: pb.AgentState_AGENT_STATE_WORKING, StateSinceUnixMs: start.Add(-time.Second).UnixMilli()}
	p.hookAt = time.Now().Add(-time.Minute)
	p.limit.turn.path, p.limit.turn.sid = rollout, "s1"
	p.mu.Unlock()
	d.checkTurnFailed(p) // reads it off d.mu
	d.mu.Unlock()
	var a *pb.Agent
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if a = limitAgent(d, paneID); a.State == pb.AgentState_AGENT_STATE_ERRORED || time.Now().After(deadline) {
			break
		}
	}
	if a.State != pb.AgentState_AGENT_STATE_ERRORED || a.Summary != "You've hit your usage limit." {
		t.Fatalf("after its limit: %v %q", a.State, a.Summary)
	}
	if lr := a.GetLimitResume(); lr == nil || !lr.ResetKnown || time.UnixMilli(lr.AtUnixMs).Unix() < reset {
		t.Fatalf("resume %v, want after %d", lr, reset)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// A scheduled run whose agent waits out its usage limit carries on; once
// that's cancelled, it's failed, as an error would.
func TestScheduledRunWaitsOnLimit(t *testing.T) {
	d, _, paneID := limitPane(t, "")
	r := &pb.ScheduleRun{Id: "r1", PaneId: paneID, Status: pb.ScheduleRun_STATUS_RUNNING, StartedUnixMs: time.Now().UnixMilli()}
	s := &pb.Schedule{Id: "s1", Kind: "claude", Name: "Nightly", Runs: []*pb.ScheduleRun{r}}
	limitStop(t, d, paneID, "You've hit your 5-hour limit · resets in 1h")
	d.mu.Lock()
	d.followRun(s, r, time.Now())
	d.mu.Unlock()
	if r.Status != pb.ScheduleRun_STATUS_RUNNING {
		t.Fatalf("waiting on its limit: %v", r.Status)
	}
	d.mu.Lock()
	d.dropLimitResume(d.panes[paneID], limitCancelled)
	d.followRun(s, r, time.Now())
	d.mu.Unlock()
	if r.Status != pb.ScheduleRun_STATUS_FAILED {
		t.Fatalf("cancelled: %v", r.Status)
	}
}

func attentionFor(d *Daemon, paneID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.attention["at_"+paneID]
	return ok
}

// Seen, an agent waiting out its usage limit is a calm wait, out of Needs
// You; cancelled, its error's back there.
func TestLimitResumeSeenIsCalm(t *testing.T) {
	d, c, paneID := limitPane(t, "")
	limitStop(t, d, paneID, "You've hit your 5-hour limit · resets in 1h")
	if a := limitAgent(d, paneID); a.Seen || !attentionFor(d, paneID) {
		t.Fatalf("just stopped: seen %v, in Needs You %v; want an error to see", a.Seen, attentionFor(d, paneID))
	}
	d.mu.Lock()
	d.focusPane(paneID)
	d.mu.Unlock()
	if a := limitAgent(d, paneID); !a.Seen || a.State != pb.AgentState_AGENT_STATE_ERRORED || a.GetLimitResume() == nil || attentionFor(d, paneID) {
		t.Fatalf("seen: %v seen %v resume %v, in Needs You %v; want a calm wait", a.State, a.Seen, a.GetLimitResume(), attentionFor(d, paneID))
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CancelLimitResume{CancelLimitResume: &pb.CancelLimitResume{PaneId: paneID}}})
	if a := limitAgent(d, paneID); a.Seen || a.GetLimitResume() != nil || !attentionFor(d, paneID) {
		t.Fatalf("cancelled: seen %v resume %v, in Needs You %v; want its error back", a.Seen, a.GetLimitResume(), attentionFor(d, paneID))
	}
}

// After "continue" met the limit again many times, it still waits at
// least the most, never straight back (tries are capped, so the wait
// doesn't overflow to nothing).
func TestLimitResumeTriesCapped(t *testing.T) {
	d, _, paneID := limitPane(t, "")
	d.mu.Lock()
	p := d.panes[paneID]
	p.mu.Lock()
	p.limit.tries = 1000
	p.mu.Unlock()
	lr := d.limitResumeFor(p, "claude", agentsHook("You've hit your 5-hour limit · resets in 2s"), time.Now())
	d.mu.Unlock()
	if lr == nil || time.Until(time.UnixMilli(lr.AtUnixMs)) < 59*time.Minute {
		t.Fatalf("after many tries: %v, want an hour off", lr)
	}
}

// Typing while "continue" is being sent takes over: it isn't sent, and
// what's typed is left alone.
func TestLimitResumeTypingTakesOver(t *testing.T) {
	d, _, paneID := limitPane(t, "")
	d.mu.Lock()
	p := d.panes[paneID]
	p.mu.Lock()
	p.limit.sending = true
	p.mu.Unlock()
	d.dropLimitResume(p, limitTyped)
	d.mu.Unlock()
	p.mu.Lock()
	taken := p.limit.takenOver
	p.mu.Unlock()
	if !taken {
		t.Fatal("typed while sending: not taken over")
	}
	if err := d.typeContinue(p, func() bool { return !taken }); !errors.Is(err, errTypedOver) {
		t.Errorf("sent anyway: %v", err)
	}
}

// Started again (a restart), an agent isn't sent "continue" until it's said
// something or drawn its prompt.
func TestAgentReady(t *testing.T) {
	p := &pane{}
	p.agentStart, p.hookAt = time.Now(), time.Now().Add(-time.Minute)
	if p.agentReady("Resuming session…\n") {
		t.Error("restarted, nothing drawn: ready")
	}
	if p.agentReady(" Do you trust the files in this folder?\n ❯ 1. Yes, proceed\n   2. No, exit\n") {
		t.Error("a dialog: ready")
	}
	if !p.agentReady("────────\n❯ \n────────\n") {
		t.Error("at its prompt: not ready")
	}
	p.hookAt = time.Now().Add(time.Second)
	if !p.agentReady("") {
		t.Error("it's said something since: not ready")
	}
}

// Claude Code with a wait of its own: Hammock picks it in its limit menu,
// shows its time and types nothing; cancelled there, Hammock's is too, and
// the error's back in Needs You.
func TestLimitResumeClaudeOwnWait(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	t.Cleanup(d.Shutdown)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Hammock"}}}))
	// Its limit menu (2.1.331's words); a key picks; then it's armed, and a
	// moment later cancelled, as Esc there would.
	script := `stty -icanon -echo min 1; printf ' You\047ve hit your 5-hour limit · resets 6:35pm\n\n What do you want to do?\n\n \342\235\257 1. Stop and wait for limit to reset\n   2. Wait here, then continue automatically at 6:35pm\n   3. Upgrade your plan\n\n Enter to confirm · Esc to cancel\n'; ` +
		`k=$(dd bs=1 count=1 2>/dev/null); printf '\033[2J\033[Hpicked %s\n  Usage limit reached · set to continue automatically at 6:35pm\n' "$k"; sleep 3; printf '  Automatic continue cancelled. Your session will wait for you instead\n'; exec sleep 600`
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", script}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitOutput(t, c, paneID, "Esc to cancel")
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"s1","prompt":"go"}`)})
	limitStop(t, d, paneID, "You've hit your 5-hour limit · resets 6:35pm")

	tick := func() {
		d.mu.Lock()
		d.resumeLimitedAgents()
		d.mu.Unlock()
	}
	tick() // picks option 2
	waitOutput(t, c, paneID, "picked 2")
	tick()
	lr := limitAgent(d, paneID).GetLimitResume()
	if lr == nil || !lr.AgentContinues || time.UnixMilli(lr.AtUnixMs).Local().Format("15:04") != "18:35" {
		t.Fatalf("armed: %v, want its own wait at 18:35", lr)
	}
	waitOutput(t, c, paneID, "Automatic continue cancelled")
	tick()
	if a := limitAgent(d, paneID); a.GetLimitResume() != nil || !attentionFor(d, paneID) {
		t.Fatalf("cancelled there: resume %v, in Needs You %v", a.GetLimitResume(), attentionFor(d, paneID))
	}
	tick() // not taken up again
	if limitAgent(d, paneID).GetLimitResume() != nil {
		t.Fatal("cancelled, then waiting again")
	}
}

func agentsHook(msg string) agents.Hook {
	return agents.Hook{Event: "StopFailure", Error: "rate_limit", LastMessage: msg}
}
