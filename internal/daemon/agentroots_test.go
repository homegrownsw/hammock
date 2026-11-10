package daemon

import (
	"testing"

	"github.com/homegrownsw/hammock/internal/agents"
)

// addAgentRoots makes dirs session folders of harness too, for the test.
func addAgentRoots(t *testing.T, harness string, dirs ...string) {
	t.Helper()
	old := agentRoots
	agentRoots = func(h string) []string {
		r := old(h)
		if h == harness {
			r = append(r, dirs...)
		}
		return r
	}
	t.Cleanup(func() { agentRoots = old })
}

// setAgentRoots makes roots each agent's only session folders, for the test.
func setAgentRoots(t *testing.T, roots map[string][]string) {
	t.Helper()
	old := agentRoots
	agentRoots = func(h string) []string { return roots[h] }
	t.Cleanup(func() { agentRoots = old })
}

// mustAgent is the registered agent id.
func mustAgent(t *testing.T, id string) *agents.Agent {
	t.Helper()
	a := agents.Get(id)
	if a == nil {
		t.Fatalf("no agent %q", id)
	}
	return a
}
