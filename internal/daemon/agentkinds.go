package daemon

// The agents this daemon knows (internal/agents), told to clients so their
// pickers and menus offer each one (ADR-0015), with whether it's installed
// here: asked of the login shell off the daemon lock, now and then.

import (
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// agentKinds is the catalogue with which agents are installed.
func agentKinds(installed []string) []*pb.AgentKind {
	out := make([]*pb.AgentKind, 0, len(agents.All))
	for _, a := range agents.All {
		out = append(out, a.Kind(slices.Contains(installed, a.ID)))
	}
	return out
}

// watchAgentKinds keeps d.agentKinds current until the daemon stops,
// telling clients when an agent is installed or removed.
func (d *Daemon) watchAgentKinds() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		kinds := agentKinds(installedAgents())
		d.mu.Lock()
		if !slices.EqualFunc(kinds, d.agentKinds, func(a, b *pb.AgentKind) bool { return proto.Equal(a, b) }) {
			d.agentKinds = kinds
			d.publish(&pb.StateUpdate{AgentKinds: cloneKinds(kinds)})
		}
		d.mu.Unlock()
		select {
		case <-d.done:
			return
		case <-t.C:
		}
	}
}

func cloneKinds(in []*pb.AgentKind) []*pb.AgentKind {
	out := make([]*pb.AgentKind, len(in))
	for i, k := range in {
		out[i] = proto.CloneOf(k)
	}
	return out
}
