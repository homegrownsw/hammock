package daemon

// A first message for an agent that takes none on its command line (Kimi
// Code's, Crush's: agents.Agent.TypePrompt): it's started bare, and the
// message is typed in once it's at its prompt, as the agent view sends one.

import (
	"time"

	"github.com/alexjsp/hammock/internal/agents"
	"github.com/alexjsp/hammock/internal/agentview"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// firstPromptWait is how long an agent has to reach its prompt.
var firstPromptWait = 90 * time.Second

// startCommand is what to type to start ag with prompt, and the message to
// type in afterwards ("" when the command carries it).
func startCommand(ag *agents.Agent, prompt string, scheduled *pb.Schedule) (cmd, later string) {
	if ag.TypePrompt {
		return ag.StartCommand(""), prompt
	}
	if scheduled != nil {
		return ag.ScheduleCommand(scheduled), ""
	}
	return ag.StartCommand(prompt), ""
}

// typeFirstPrompt types text into p once its agent is at its prompt (idle,
// by its hooks or its screen), giving up after firstPromptWait.
func (d *Daemon) typeFirstPrompt(p *pane, text string) {
	if text == "" {
		return
	}
	go func() {
		deadline := time.Now().Add(firstPromptWait)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			a := p.snapshotInfo().Agent
			if p.snapshotInfo().Exited {
				return
			}
			if a.GetState() != pb.AgentState_AGENT_STATE_IDLE {
				continue
			}
			time.Sleep(700 * time.Millisecond) // its input box drawn, not just its process up
			paste := []byte(text)
			if p.bracketedPaste() {
				paste = []byte("\x1b[200~" + text + "\x1b[201~")
			}
			if _, err := p.input(nil, paste); err != nil {
				return
			}
			time.Sleep(150 * time.Millisecond) // a separate burst, or it's part of the paste
			_, _ = p.input(nil, agentview.Enter(p.kittyKeys()))
			return
		}
		d.log.Info("first message not typed: the agent never reached its prompt", "pane", p.id)
	}()
}
