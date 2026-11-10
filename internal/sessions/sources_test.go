package sessions

import (
	"path/filepath"

	"github.com/homegrownsw/hammock/internal/agents"
)

// claudeAndCodex are the two agents' sources, as the daemon builds them,
// with Claude's config dir and Codex's home given.
func claudeAndCodex(claude, codex string) Sources {
	var out Sources
	for _, a := range []*agents.Agent{agents.Claude, agents.Codex} {
		roots := []string{filepath.Join(claude, "projects")}
		if a == agents.Codex {
			roots = []string{filepath.Join(codex, "sessions")}
		}
		out = append(out, Source{Harness: a.ID, Appends: a.Appends, Read: a.Index,
			Files: func() []string { return a.Sessions(roots) }})
		if a.Names != nil {
			out[len(out)-1].Names = func() map[string]string { return a.Names(roots) }
		}
	}
	return out
}
