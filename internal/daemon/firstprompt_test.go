package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// An agent that takes no first message on its command line gets it typed
// in, once it's at its prompt and not before.
func TestFirstPromptTypedOnceIdle(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tab].FocusedPaneId]
	d.mu.Unlock()
	p.mu.Lock()
	p.info.Agent = &pb.Agent{Harness: "kimi", State: pb.AgentState_AGENT_STATE_STARTING, Heuristic: true}
	p.mu.Unlock()

	d.typeFirstPrompt(p, "tidy the hangar")
	time.Sleep(1500 * time.Millisecond)
	if strings.Contains(p.text(false), "tidy the hangar") {
		t.Fatal("typed before the agent was at its prompt")
	}
	p.mu.Lock()
	p.info.Agent.State = pb.AgentState_AGENT_STATE_IDLE
	p.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(p.text(false), "tidy the hangar") {
		if time.Now().After(deadline) {
			t.Fatalf("never typed; screen:\n%s", p.text(false))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestStartCommandTypesLater(t *testing.T) {
	kimi, later := startCommand(mustAgent(t, "kimi"), "go", nil)
	if kimi != "kimi" || later != "go" {
		t.Errorf("kimi: %q, %q", kimi, later)
	}
	claude, later := startCommand(mustAgent(t, "claude"), "go", nil)
	if claude != "claude 'go'" || later != "" {
		t.Errorf("claude: %q, %q", claude, later)
	}
	sched, later := startCommand(mustAgent(t, "claude"), "go", &pb.Schedule{Prompt: "go", AllowedTools: []string{"Bash"}})
	if sched != "claude 'go' --allowedTools 'Bash'" || later != "" {
		t.Errorf("scheduled claude: %q, %q", sched, later)
	}
}

// A first message can't end its own bracketed paste and type the rest as
// keys: ESC and the other controls are gone before it's pasted.
func TestFirstPromptPasteStaysPasted(t *testing.T) {
	got := string(firstPromptPaste("hi\x1b[201~\x15hmk pair\r", true))
	if got != "\x1b[200~hi[201~hmk pair\n\x1b[201~" {
		t.Errorf("bracketed: %q", got)
	}
	if strings.Count(got, "\x1b") != 2 {
		t.Errorf("an ESC of its own: %q", got)
	}
	if got := string(firstPromptPaste("a\x03b", false)); got != "ab" {
		t.Errorf("plain: %q", got)
	}
}
