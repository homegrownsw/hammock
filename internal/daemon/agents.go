package daemon

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/agents"
	"github.com/alexjsp/hammock/internal/agentview"
	"github.com/alexjsp/hammock/internal/procinfo"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// HookEvent is what `hmk hook` sends to the daemon's hook socket: one JSON
// object per connection.
type HookEvent struct {
	PaneID  string          `json:"pane_id"`
	Harness string          `json:"harness"` // an agents.Agent's ID: "claude", "codex"…
	Event   string          `json:"event"`   // optional override of the payload's event name
	Payload json.RawMessage `json:"payload"`
}

// agentHub tracks agent state from hook events and maintains the attention queue.
type agentHub struct {
	d  *Daemon
	ln net.Listener
}

func newAgentHub(d *Daemon) *agentHub { return &agentHub{d: d} }

func (h *agentHub) listen(path string) error {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	h.ln = ln
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go h.serve(c)
		}
	}()
	return nil
}

func (h *agentHub) close() {
	if h.ln != nil {
		h.ln.Close()
	}
}

func (h *agentHub) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var ev HookEvent
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&ev); err != nil {
		c.Write([]byte("error: " + err.Error() + "\n"))
		return
	}
	// The event is for the pane the sender runs in, whatever it says.
	pane := hookPane(h.d, c, ev.PaneID)
	if pane == "" {
		c.Write([]byte("error: " + errNoPaneCaller.Error() + "\n"))
		return
	}
	ev.PaneID = pane
	// An agent run by the pane's agent (a headless `claude -p` from a
	// script, say) sends hooks too, but the pane is the outer one's: its
	// session would take over the agent view.
	if nestedAgent(peerPID(c), h.d.panePIDs()) {
		c.Write([]byte("ok\n"))
		return
	}
	learnTranscriptRoots(procinfo.Env(peerPID(c)))
	h.d.mu.Lock()
	h.handle(ev)
	h.d.mu.Unlock()
	c.Write([]byte("ok\n"))
}

// hookPane is the pane a hook event's sender runs in ("" when it isn't in
// one); claimed is the pane it says. A seam for tests, whose hooks come from
// outside any pane.
var hookPane = func(d *Daemon, c net.Conn, claimed string) string {
	pane, _ := d.paneOfProcessClaiming(peerPID(c), claimed)
	return pane
}

// nestedAgent reports whether pid runs under two agent harnesses between it
// and its pane's process (panes maps those processes): an agent started by
// another agent.
func nestedAgent(pid int, panes map[int]string) bool {
	agents := 0
	for i := 0; i < 64 && pid > 1; i++ {
		if isAgentProcess(pid) {
			if agents++; agents > 1 {
				return true
			}
		}
		if _, ok := panes[pid]; ok {
			break
		}
		next, err := procinfo.Parent(pid)
		if err != nil || next == pid {
			break
		}
		pid = next
	}
	return false
}

// classify maps a hook event to an agent state. ok=false means "no change".
func classify(p agents.Hook) (state pb.AgentState, summary string, ok bool) {
	switch event := p.Event; event {
	case "SessionStart":
		// Fired once the agent is at its prompt, ready for input.
		return pb.AgentState_AGENT_STATE_IDLE, "", true
	case "UserPromptSubmit":
		// /rename runs in place: no turn starts, so no Stop would follow.
		if f := strings.Fields(p.Prompt); len(f) > 0 && (f[0] == "/rename" || f[0] == "/name") {
			return 0, "", false
		}
		return pb.AgentState_AGENT_STATE_WORKING, firstLine(p.Prompt), true
	case "PreToolUse", "PostToolUse":
		// Claude's question tool waits for you as soon as it starts.
		if event == "PreToolUse" && p.ToolName == "AskUserQuestion" {
			return pb.AgentState_AGENT_STATE_NEEDS_INPUT, "", true
		}
		return pb.AgentState_AGENT_STATE_WORKING, toolSummary(p), true
	case "PermissionRequest", "approval-requested":
		return pb.AgentState_AGENT_STATE_NEEDS_INPUT, needsSummary(p), true
	case "Notification":
		// Claude sends Notification both for permission prompts and for
		// "waiting for your input" after going idle.
		msg := strings.ToLower(p.Message)
		if p.NotificationType == "permission_prompt" || strings.Contains(msg, "permission") {
			return pb.AgentState_AGENT_STATE_NEEDS_INPUT, needsSummary(p), true
		}
		if p.NotificationType == "idle_prompt" || strings.Contains(msg, "waiting for your input") {
			return pb.AgentState_AGENT_STATE_IDLE, "", true
		}
		return 0, "", false
	case "Stop", "agent-turn-complete":
		return pb.AgentState_AGENT_STATE_IDLE, firstLine(p.LastMessage), true
	case "StopFailure":
		// An API error ended the turn (Claude's "API Error: …" line, which
		// is its last message). The agent is back at its prompt, but the
		// work didn't finish.
		return pb.AgentState_AGENT_STATE_ERRORED, firstLine(cmp.Or(p.LastMessage, p.ErrorDetails, apiErrorSummary(p.Error))), true
	case "SessionEnd":
		return pb.AgentState_AGENT_STATE_EXITED, "", true
	}
	return 0, "", false
}

// apiErrorSummary words a StopFailure's error type, for when it came
// without a message.
func apiErrorSummary(kind string) string {
	switch kind {
	case "rate_limit":
		return "Hit a rate limit"
	case "overloaded":
		return "API overloaded"
	case "authentication_failed", "oauth_org_not_allowed", "cloud_credential_error":
		return "API authentication failed"
	case "billing_error":
		return "API billing error"
	case "max_output_tokens":
		return "Ran out of output tokens"
	}
	return "API error"
}

func toolSummary(p agents.Hook) string {
	var in struct {
		Command     string `json:"command"`
		FilePath    string `json:"file_path"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(p.ToolInput, &in)
	switch {
	case in.Command != "":
		return p.ToolName + ": " + firstLine(in.Command)
	case in.FilePath != "":
		return p.ToolName + ": " + in.FilePath
	}
	return p.ToolName
}

func needsSummary(p agents.Hook) string {
	if p.Message != "" {
		return firstLine(p.Message)
	}
	if p.ToolName != "" {
		return "Allow " + toolSummary(p) + "?"
	}
	return "Waiting for you"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	// Long enough for a two-line preview on the phone; cut on a rune.
	if r := []rune(s); len(r) > 240 {
		s = string(r[:237]) + "…"
	}
	return s
}

// handle applies a hook event. Callers hold d.mu.
func (h *agentHub) handle(ev HookEvent) {
	p, ok := h.d.panes[ev.PaneID]
	if !ok {
		return
	}
	harness := ev.Harness
	if harness == "" {
		harness = p.snapshotInfo().Agent.GetHarness()
	}
	payload := agents.Get(harness).ReadHook(ev.Event, ev.Payload)
	event := payload.Event
	state, summary, ok := classify(payload)
	if !ok {
		return
	}
	sid := payload.SessionID

	p.mu.Lock()
	p.hookAt = time.Now()
	switch {
	case (event == "PreToolUse" || event == "PermissionRequest") && payload.ToolName != "" && payload.ToolName != "AskUserQuestion":
		var in map[string]any
		_ = json.Unmarshal(payload.ToolInput, &in)
		p.pendingTool = toolRule(payload.ToolName, in)
	case event == "PostToolUse" || event == "UserPromptSubmit" || event == "Stop":
		p.pendingTool, p.askedFor, p.askedRule = "", "", "" // done with, or never asked about
	}
	if event == "Notification" && payload.NotificationType == "permission_prompt" || event == "Notification" && strings.Contains(strings.ToLower(payload.Message), "permission") {
		p.askedFor, p.askedRule = payload.Message, p.pendingTool
	}
	// Claude's question tool, in full, for the agent view's prompt (the
	// screen cuts it to the terminal's width).
	switch {
	case event == "PreToolUse" && payload.ToolName == "AskUserQuestion":
		p.asking = &pb.AgentBlock{Id: "asking", Kind: pb.AgentBlock_KIND_QUESTION, Pending: true, Questions: agentview.Questions(payload.ToolInput)}
	case event == "PostToolUse" || event == "UserPromptSubmit" || event == "Stop" || event == "StopFailure":
		p.asking = nil
	}
	if q := p.asking; q != nil && state == pb.AgentState_AGENT_STATE_NEEDS_INPUT && len(q.Questions) > 0 {
		summary = firstLine(q.Questions[0].Question)
	}
	a := p.info.Agent
	if a == nil {
		a = &pb.Agent{}
		p.info.Agent = a
	}
	a.Heuristic = false
	if ev.Harness != "" {
		a.Harness = ev.Harness
	}
	if sid != "" {
		if sid != a.SessionId {
			// A new session (/clear, or another started here): the old one's
			// file isn't its (its own may not be written yet).
			a.TranscriptPath = ""
		}
		a.SessionId = sid
	}
	if t := safeTranscriptOf(a.Harness, payload.TranscriptPath); t != "" {
		a.TranscriptPath = t
	}
	prev := a.State
	if a.State != state {
		a.State = state
		a.StateSinceUnixMs = time.Now().UnixMilli()
		// Only finishing work needs acknowledging; a fresh session at its prompt doesn't.
		a.Seen = state == pb.AgentState_AGENT_STATE_IDLE && (prev != pb.AgentState_AGENT_STATE_WORKING || h.d.watched(p.id))
	}
	seen := a.Seen
	if summary != "" {
		a.Summary = summary
	}
	p.mu.Unlock()

	u := &pb.StateUpdate{}
	switch {
	case state == pb.AgentState_AGENT_STATE_NEEDS_INPUT, state == pb.AgentState_AGENT_STATE_ERRORED,
		state == pb.AgentState_AGENT_STATE_IDLE && prev == pb.AgentState_AGENT_STATE_WORKING && !seen:
		h.d.raiseAttention(p, state, summary, u)
	case state == pb.AgentState_AGENT_STATE_WORKING || state == pb.AgentState_AGENT_STATE_STARTING:
		h.d.clearAttentionForPane(p.id, u)
	}
	info := p.snapshotInfo()
	h.d.persist(h.d.store.PutPane(info))
	u.Panes = append(u.Panes, info)
	h.d.publish(u)
	if event == "Stop" || event == "agent-turn-complete" {
		h.d.scheduleTurnDone(p)
	}
	h.d.scheduleAgentChanged(p) // a scheduled run: started, waiting or done
	// A named tab names its session, again after each turn in case Claude
	// re-wrote an older name, and for a new session (/clear) in the tab.
	if event == "Stop" || event == "StopFailure" || event == "SessionStart" {
		if t := h.d.tabs[p.tabID()]; t != nil {
			h.d.nameSessions(t)
		}
	}
}

// paneExited marks an agent pane's process exit. Callers hold d.mu.
func (h *agentHub) paneExited(p *pane, status int32) {
	p.mu.Lock()
	a := p.info.Agent
	if a == nil {
		p.mu.Unlock()
		return
	}
	state := pb.AgentState_AGENT_STATE_EXITED
	if status != 0 {
		state = pb.AgentState_AGENT_STATE_ERRORED
		a.Summary = fmt.Sprintf("exited with status %d", status)
	}
	a.State, a.StateSinceUnixMs, a.Seen = state, time.Now().UnixMilli(), false
	summary := a.Summary
	p.mu.Unlock()
	u := &pb.StateUpdate{Panes: []*pb.Pane{p.snapshotInfo()}}
	if state == pb.AgentState_AGENT_STATE_ERRORED {
		h.d.raiseAttention(p, state, summary, u)
	}
	h.d.publish(u)
	h.d.scheduleAgentChanged(p)
}

// userInput clears a pane's attention item when someone types into it, and
// takes an agent that was waiting for input back to working: answering a
// question or permission prompt sends no hook of its own (only tool events,
// which a hand-edited settings.json may not route to Hammock), so without this
// the pane would wait for the next prompt. Callers hold d.mu.
func (h *agentHub) userInput(p *pane) {
	u := &pb.StateUpdate{}
	h.d.clearAttentionForPane(p.id, u)
	p.mu.Lock()
	if a := p.info.Agent; a != nil && a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT {
		a.State, a.StateSinceUnixMs, a.Summary = pb.AgentState_AGENT_STATE_WORKING, time.Now().UnixMilli(), ""
		p.mu.Unlock()
		info := p.snapshotInfo()
		h.d.persist(h.d.store.PutPane(info))
		u.Panes = append(u.Panes, info)
	} else {
		p.mu.Unlock()
	}
	if len(u.ClearedAttentionIds) > 0 || len(u.Panes) > 0 {
		h.d.publish(u)
	}
}

// interrupted watches for an agent's "Interrupted" line (Claude's) after
// Esc was pressed in its working pane: an interrupt sends no hook, so
// without this the pane would show working until the next prompt.
func (h *agentHub) interrupted(p *pane) {
	a := p.snapshotInfo().Agent
	ag := agents.Get(a.GetHarness())
	if ag == nil || ag.Screen.Interrupted == nil || a.GetState() != pb.AgentState_AGENT_STATE_WORKING {
		return
	}
	re := ag.Screen.Interrupted
	go func() {
		for range 20 {
			time.Sleep(150 * time.Millisecond)
			lines := strings.Split(strings.TrimRight(p.text(false), "\n "), "\n")
			if !slices.ContainsFunc(lines[max(0, len(lines)-12):], re.MatchString) {
				continue
			}
			h.d.mu.Lock()
			defer h.d.mu.Unlock()
			p.mu.Lock()
			a := p.info.Agent
			if a == nil || a.State != pb.AgentState_AGENT_STATE_WORKING {
				p.mu.Unlock()
				return
			}
			// You stopped it, looking at it: nothing to acknowledge.
			a.State, a.StateSinceUnixMs, a.Summary, a.Seen = pb.AgentState_AGENT_STATE_IDLE, time.Now().UnixMilli(), "Interrupted", true
			p.mu.Unlock()
			u := &pb.StateUpdate{}
			h.d.clearAttentionForPane(p.id, u)
			info := p.snapshotInfo()
			h.d.persist(h.d.store.PutPane(info))
			u.Panes = append(u.Panes, info)
			h.d.publish(u)
			return
		}
	}()
}

// resumeCommand returns the command that resumes the pane's agent session
// after a reboot, or nil to just rerun the pane's command.
func (h *agentHub) resumeCommand(info *pb.Pane) []string {
	a := info.Agent
	if a == nil || a.SessionId == "" || a.State == pb.AgentState_AGENT_STATE_EXITED {
		return nil
	}
	ag := agents.Get(a.Harness)
	if ag == nil || ag.Resume == nil {
		return nil
	}
	resume := ag.Resume(a.SessionId)
	shell := loginShell()
	// Keep a shell in the pane once the agent exits.
	return []string{shell, "-l", "-c", resume + "; exec " + shellQuote(shell) + " -l"}
}

// raiseAttention adds/replaces the pane's attention item. Callers hold d.mu.
func (d *Daemon) raiseAttention(p *pane, state pb.AgentState, summary string, u *pb.StateUpdate) {
	// A scheduled run under way says it's done, or failed, itself (in the
	// schedule's words, as it asks: schedules.go); asking you still raises one.
	if state != pb.AgentState_AGENT_STATE_NEEDS_INPUT && d.scheduleRunActive(p) {
		return
	}
	item := &pb.AttentionItem{
		Id: "at_" + p.id, PaneId: p.id, State: state, Summary: summary,
		CreatedUnixMs: time.Now().UnixMilli(),
	}
	// Claude's question tool is open: it's asking, not after permission
	// (its own notification says "needs your permission to use AskUserQuestion").
	p.mu.Lock()
	if q := p.asking; q != nil && state == pb.AgentState_AGENT_STATE_NEEDS_INPUT {
		item.Question = true
		if len(q.Questions) > 0 && q.Questions[0].Question != "" {
			item.Summary = firstLine(q.Questions[0].Question)
		}
	}
	p.mu.Unlock()
	d.attention[item.Id] = item
	u.Attention = append(u.Attention, proto.CloneOf(item))
	d.pushAttention(item)
}

// clearAttentionForPane removes the pane's attention item, recording it in u.
// Callers hold d.mu.
func (d *Daemon) clearAttentionForPane(paneID string, u *pb.StateUpdate) {
	id := "at_" + paneID
	if _, ok := d.attention[id]; ok {
		delete(d.attention, id)
		u.ClearedAttentionIds = append(u.ClearedAttentionIds, id)
	}
}

// clearAttention removes an attention item by id. Callers hold d.mu.
func (d *Daemon) clearAttention(id string) {
	if _, ok := d.attention[id]; ok {
		delete(d.attention, id)
		d.publish(&pb.StateUpdate{ClearedAttentionIds: []string{id}})
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func hostname() (string, error) { return os.Hostname() }

// watched reports whether a foreground client is showing the pane, so an
// agent finishing in front of the user is already seen. Callers hold d.mu.
func (d *Daemon) watched(paneID string) bool {
	for c := range d.conns {
		// On screen in a client that's in front, or the pane it has focused:
		// a linked computer's presence names only that.
		if c.presence != nil && c.presence.Foreground &&
			(slices.Contains(c.presence.VisiblePaneIds, paneID) || c.presence.FocusedPaneId == paneID) {
			return true
		}
	}
	return false
}
